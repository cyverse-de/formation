package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func newExposer(t *testing.T, handler http.HandlerFunc) (*AppExposerClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	base, _ := url.Parse(srv.URL)
	return NewAppExposerClient(srv.Client(), base, nil), srv
}

func TestResolveSubdomainRetriesOn404(t *testing.T) {
	var asyncCalls atomic.Int32
	exposer, srv := newExposer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vice/admin/analyses/an-1/external-id":
			_, _ = w.Write([]byte(`{"external_id":"ext-1"}`))
		case "/vice/async-data":
			// First two attempts: 404. Third: subdomain.
			if asyncCalls.Add(1) < 3 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"subdomain":"abc123"}`))
		}
	})
	defer srv.Close()

	r := NewVICEResolver(exposer, VICEConfig{ViceDomain: ".cyverse.run"}, nil)
	r.sleep = noSleep

	sub := r.ResolveSubdomain(context.Background(), "an-1")
	if sub != "abc123" {
		t.Fatalf("subdomain = %q, want abc123", sub)
	}
	if got := asyncCalls.Load(); got != 3 {
		t.Errorf("async-data called %d times, want 3", got)
	}
	if url := r.URLFor(sub); url != "https://abc123.cyverse.run" {
		t.Errorf("URLFor = %q", url)
	}

	// A second resolution is served from the subdomain cache.
	if sub := r.ResolveSubdomain(context.Background(), "an-1"); sub != "abc123" {
		t.Errorf("cached subdomain = %q, want abc123", sub)
	}
	if got := asyncCalls.Load(); got != 3 {
		t.Errorf("cache miss: async-data called %d times, want 3", got)
	}
}

// noSleep skips retry delays in tests.
func noSleep(context.Context, time.Duration) bool { return true }

func TestResolveSubdomainGivesUp(t *testing.T) {
	exposer, srv := newExposer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vice/admin/analyses/an-1/external-id" {
			_, _ = w.Write([]byte(`{"external_id":"ext-1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	r := NewVICEResolver(exposer, VICEConfig{MaxSubdomainRetries: 2}, nil)
	r.sleep = noSleep
	if sub := r.ResolveSubdomain(context.Background(), "an-1"); sub != "" {
		t.Errorf("expected empty subdomain, got %q", sub)
	}
}

func TestCheckURLReady(t *testing.T) {
	var headCount, getCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headCount.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed) // force GET fallback
			return
		}
		getCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewVICEResolver(nil, VICEConfig{URLCheckCacheTTL: time.Minute}, nil)
	r.probeClient = srv.Client()
	r.sleep = noSleep

	ready, details := r.CheckURLReady(context.Background(), srv.URL)
	if !ready {
		t.Fatalf("expected ready, details=%+v", details)
	}
	if headCount.Load() != 1 || getCount.Load() != 1 {
		t.Errorf("expected HEAD then GET fallback, head=%d get=%d", headCount.Load(), getCount.Load())
	}

	// Second call should be served from cache (no new requests).
	r.CheckURLReady(context.Background(), srv.URL)
	if getCount.Load() != 1 {
		t.Errorf("cache miss: get called %d times", getCount.Load())
	}
}

func TestCheckURLReadyCacheExpiry(t *testing.T) {
	var getCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		getCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewVICEResolver(nil, VICEConfig{URLCheckCacheTTL: time.Second}, nil)
	r.probeClient = srv.Client()
	r.sleep = noSleep
	current := time.Unix(1000, 0)
	r.now = func() time.Time { return current }

	r.CheckURLReady(context.Background(), srv.URL)
	current = current.Add(2 * time.Second) // past TTL
	r.CheckURLReady(context.Background(), srv.URL)
	if getCount.Load() != 2 {
		t.Errorf("expected 2 probes after cache expiry, got %d", getCount.Load())
	}
}
