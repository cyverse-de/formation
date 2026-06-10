package vice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cyverse-de/formation/internal/clients"
)

func TestURLCheckerReady(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantReady  bool
		wantStatus int
		wantGET    bool
	}{
		{
			name:       "200 via HEAD is ready",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) },
			wantReady:  true,
			wantStatus: 200,
		},
		{
			name: "405 on HEAD falls back to GET",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(405)
					return
				}
				w.WriteHeader(200)
			},
			wantReady:  true,
			wantStatus: 200,
			wantGET:    true,
		},
		{
			name: "404 on HEAD falls back to GET",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(404)
					return
				}
				w.WriteHeader(204)
			},
			wantReady:  true,
			wantStatus: 204,
			wantGET:    true,
		},
		{
			name:       "302 counts as ready without following redirects",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", "/x"); w.WriteHeader(302) },
			wantReady:  true,
			wantStatus: 302,
		},
		{
			name:       "500 is not ready",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
			wantReady:  false,
			wantStatus: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawGET atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					sawGET.Store(true)
				}
				tt.handler(w, r)
			}))
			defer server.Close()

			checker := NewURLChecker(2*time.Second, 1, time.Second)
			ready, details := checker.Check(context.Background(), server.URL)

			if ready != tt.wantReady {
				t.Errorf("ready = %v, want %v (details %v)", ready, tt.wantReady, details)
			}
			if details["status_code"] != tt.wantStatus {
				t.Errorf("status_code = %v, want %d", details["status_code"], tt.wantStatus)
			}
			if details["attempt"] != 1 {
				t.Errorf("attempt = %v, want 1", details["attempt"])
			}
			if tt.wantGET && !sawGET.Load() {
				t.Error("expected GET fallback")
			}
		})
	}
}

func TestURLCheckerCache(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	checker := NewURLChecker(2*time.Second, 1, time.Minute)
	checker.Check(context.Background(), server.URL)
	checker.Check(context.Background(), server.URL)

	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1 (second check should be cached)", hits.Load())
	}
}

func TestURLCheckerTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	checker := NewURLChecker(50*time.Millisecond, 1, time.Second)
	ready, details := checker.Check(context.Background(), server.URL)

	if ready {
		t.Error("ready = true, want false on timeout")
	}
	if details["error"] != "timeout" {
		t.Errorf("error = %v, want timeout (details %v)", details["error"], details)
	}
	if details["timeout_seconds"] != 0.05 {
		t.Errorf("timeout_seconds = %v, want 0.05", details["timeout_seconds"])
	}
}

func TestURLCheckerHEADTimeoutFallsBackToGETSameAttempt(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond) // HEAD times out, GET succeeds
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	checker := NewURLChecker(100*time.Millisecond, 2, time.Second)
	ready, details := checker.Check(context.Background(), server.URL)

	if !ready {
		t.Fatalf("ready = false, details %v", details)
	}
	if details["attempt"] != 1 {
		t.Errorf("attempt = %v, want 1 (GET fallback happens within the attempt)", details["attempt"])
	}
}

func TestURLCheckerRetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Attempt 1: HEAD and GET both time out. Attempt 2: HEAD succeeds.
		if calls.Add(1) <= 2 {
			time.Sleep(300 * time.Millisecond)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	checker := NewURLChecker(100*time.Millisecond, 2, time.Second)
	ready, details := checker.Check(context.Background(), server.URL)

	if !ready {
		t.Fatalf("ready = false, details %v", details)
	}
	if details["attempt"] != 2 {
		t.Errorf("attempt = %v, want 2", details["attempt"])
	}
}

func exposerStub(t *testing.T, handler http.HandlerFunc) *clients.AppExposer {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	exposer, err := clients.NewAppExposer(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return exposer
}

func TestSubdomainResolver(t *testing.T) {
	t.Run("resolves after async-data 404 retries", func(t *testing.T) {
		var asyncCalls atomic.Int32
		exposer := exposerStub(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/vice/admin/analyses/an-1/external-id":
				_ = json.NewEncoder(w).Encode(map[string]any{"external_id": "ext-1"})
			case "/vice/async-data":
				if asyncCalls.Add(1) < 3 {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"subdomain": "a1b2c3"})
			default:
				w.WriteHeader(500)
			}
		})

		resolver := NewSubdomainResolverWithRetries(exposer, 5, 10*time.Millisecond)
		if got := resolver.Resolve(context.Background(), "an-1"); got != "a1b2c3" {
			t.Errorf("Resolve() = %q, want a1b2c3", got)
		}
	})

	t.Run("missing external_id returns empty", func(t *testing.T) {
		exposer := exposerStub(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{})
		})
		resolver := NewSubdomainResolverWithRetries(exposer, 2, time.Millisecond)
		if got := resolver.Resolve(context.Background(), "an-1"); got != "" {
			t.Errorf("Resolve() = %q, want empty", got)
		}
	})

	t.Run("non-404 async-data error gives up", func(t *testing.T) {
		var asyncCalls atomic.Int32
		exposer := exposerStub(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/vice/async-data" {
				asyncCalls.Add(1)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"external_id": "ext-1"})
		})
		resolver := NewSubdomainResolverWithRetries(exposer, 5, time.Millisecond)
		if got := resolver.Resolve(context.Background(), "an-1"); got != "" {
			t.Errorf("Resolve() = %q, want empty", got)
		}
		if asyncCalls.Load() != 1 {
			t.Errorf("async-data calls = %d, want 1 (no retry on non-404)", asyncCalls.Load())
		}
	})

	t.Run("404s exhaust retries", func(t *testing.T) {
		var asyncCalls atomic.Int32
		exposer := exposerStub(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/vice/async-data" {
				asyncCalls.Add(1)
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"external_id": "ext-1"})
		})
		resolver := NewSubdomainResolverWithRetries(exposer, 3, time.Millisecond)
		if got := resolver.Resolve(context.Background(), "an-1"); got != "" {
			t.Errorf("Resolve() = %q, want empty", got)
		}
		if asyncCalls.Load() != 3 {
			t.Errorf("async-data calls = %d, want 3", asyncCalls.Load())
		}
	})
}
