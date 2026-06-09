package clients

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/formation/internal/apierror"
)

// recordedRequest captures what the client sent upstream.
type recordedRequest struct {
	Method string
	Path   string
	Query  map[string]string
	Body   map[string]any
}

// stub serves canned JSON and records the last request.
func stub(t *testing.T, status int, response any) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Query = map[string]string{}
		for k, v := range r.URL.Query() {
			rec.Query[k] = v[0]
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if response != nil {
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	t.Cleanup(server.Close)
	return server, rec
}

func TestAppsClientRequests(t *testing.T) {
	okBody := map[string]any{"ok": true}

	tests := []struct {
		name      string
		call      func(a *Apps) (map[string]any, error)
		wantPath  string
		wantQuery map[string]string
		wantBody  map[string]any
		method    string
	}{
		{
			name: "GetApp",
			call: func(a *Apps) (map[string]any, error) {
				return a.GetApp(context.Background(), "de", "app-uuid", "alice")
			},
			method:    http.MethodGet,
			wantPath:  "/apps/de/app-uuid",
			wantQuery: map[string]string{"user": "alice"},
		},
		{
			name: "SubmitAnalysis",
			call: func(a *Apps) (map[string]any, error) {
				return a.SubmitAnalysis(context.Background(),
					map[string]any{"name": "run-1"}, "alice", "alice@example.org")
			},
			method:    http.MethodPost,
			wantPath:  "/analyses",
			wantQuery: map[string]string{"user": "alice", "email": "alice@example.org"},
			wantBody:  map[string]any{"name": "run-1"},
		},
		{
			name: "ListApps with search",
			call: func(a *Apps) (map[string]any, error) {
				return a.ListApps(context.Background(), "alice", 100, 20, "jupyter")
			},
			method:    http.MethodGet,
			wantPath:  "/apps",
			wantQuery: map[string]string{"user": "alice", "limit": "100", "offset": "20", "search": "jupyter"},
		},
		{
			name: "ListAnalyses with status filter",
			call: func(a *Apps) (map[string]any, error) {
				return a.ListAnalyses(context.Background(), "alice", "Running")
			},
			method:    http.MethodGet,
			wantPath:  "/analyses",
			wantQuery: map[string]string{"user": "alice", "filter": `[{"field":"status","value":"Running"}]`},
		},
		{
			name: "ListAnalyses without status omits filter",
			call: func(a *Apps) (map[string]any, error) {
				return a.ListAnalyses(context.Background(), "alice", "")
			},
			method:    http.MethodGet,
			wantPath:  "/analyses",
			wantQuery: map[string]string{"user": "alice"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := stub(t, http.StatusOK, okBody)
			apps, err := NewApps(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			result, err := tt.call(apps)
			if err != nil {
				t.Fatal(err)
			}
			if result["ok"] != true {
				t.Errorf("result = %v", result)
			}
			if rec.Method != tt.method || rec.Path != tt.wantPath {
				t.Errorf("request = %s %s, want %s %s", rec.Method, rec.Path, tt.method, tt.wantPath)
			}
			for k, want := range tt.wantQuery {
				if got := rec.Query[k]; got != want {
					t.Errorf("query[%s] = %q, want %q", k, got, want)
				}
			}
			if len(rec.Query) != len(tt.wantQuery) {
				t.Errorf("query params = %v, want exactly %v", rec.Query, tt.wantQuery)
			}
			if tt.wantBody != nil {
				want, _ := json.Marshal(tt.wantBody)
				got, _ := json.Marshal(rec.Body)
				if string(want) != string(got) {
					t.Errorf("body = %s, want %s", got, want)
				}
			}
		})
	}
}

func TestGetAnalysis(t *testing.T) {
	t.Run("returns first analysis from filtered listing", func(t *testing.T) {
		server, rec := stub(t, http.StatusOK, map[string]any{
			"analyses": []any{map[string]any{"id": "an-1", "status": "Running"}},
		})
		apps, _ := NewApps(server.URL)

		analysis, err := apps.GetAnalysis(context.Background(), "an-1", "alice")
		if err != nil {
			t.Fatal(err)
		}
		if analysis["id"] != "an-1" {
			t.Errorf("analysis = %v", analysis)
		}
		if rec.Query["filter"] != `[{"field":"id","value":"an-1"}]` {
			t.Errorf("filter = %q", rec.Query["filter"])
		}
	})

	t.Run("empty listing becomes 404 upstream error", func(t *testing.T) {
		server, _ := stub(t, http.StatusOK, map[string]any{"analyses": []any{}})
		apps, _ := NewApps(server.URL)

		_, err := apps.GetAnalysis(context.Background(), "an-1", "alice")
		var upstream *apierror.UpstreamError
		if !errors.As(err, &upstream) || upstream.Status != http.StatusNotFound {
			t.Fatalf("err = %v, want 404 UpstreamError", err)
		}
	})
}

func TestNon2xxBecomesUpstreamError(t *testing.T) {
	server, _ := stub(t, http.StatusBadRequest, map[string]any{"reason": "nope"})
	apps, _ := NewApps(server.URL)

	_, err := apps.GetApp(context.Background(), "de", "app-uuid", "alice")
	var upstream *apierror.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("err = %v, want UpstreamError", err)
	}
	if upstream.Status != http.StatusBadRequest || upstream.Body != "{\"reason\":\"nope\"}\n" {
		t.Errorf("upstream = %+v", upstream)
	}
}

func TestAppExposerRequests(t *testing.T) {
	tests := []struct {
		name      string
		call      func(a *AppExposer) (map[string]any, error)
		method    string
		wantPath  string
		wantQuery map[string]string
		response  any
		want      map[string]any
	}{
		{
			name: "ExtendTimeLimit",
			call: func(a *AppExposer) (map[string]any, error) {
				return a.ExtendTimeLimit(context.Background(), "an-1")
			},
			method:   http.MethodPost,
			wantPath: "/vice/admin/analyses/an-1/time-limit",
			response: map[string]any{"time_limit": "2026-06-10T00:00:00Z"},
			want:     map[string]any{"time_limit": "2026-06-10T00:00:00Z"},
		},
		{
			name: "SaveAndExit synthesizes status",
			call: func(a *AppExposer) (map[string]any, error) {
				return a.SaveAndExit(context.Background(), "an-1")
			},
			method:   http.MethodPost,
			wantPath: "/vice/admin/analyses/an-1/save-and-exit",
			want:     map[string]any{"status": "terminated", "outputs_saved": true},
		},
		{
			name: "ExitWithoutSave synthesizes status",
			call: func(a *AppExposer) (map[string]any, error) {
				return a.ExitWithoutSave(context.Background(), "an-1")
			},
			method:   http.MethodPost,
			wantPath: "/vice/admin/analyses/an-1/exit",
			want:     map[string]any{"status": "terminated", "outputs_saved": false},
		},
		{
			name: "GetExternalID",
			call: func(a *AppExposer) (map[string]any, error) {
				return a.GetExternalID(context.Background(), "an-1")
			},
			method:   http.MethodGet,
			wantPath: "/vice/admin/analyses/an-1/external-id",
			response: map[string]any{"externalID": "ext-1"},
			want:     map[string]any{"externalID": "ext-1"},
		},
		{
			name: "GetAsyncData",
			call: func(a *AppExposer) (map[string]any, error) {
				return a.GetAsyncData(context.Background(), "ext-1")
			},
			method:    http.MethodGet,
			wantPath:  "/vice/async-data",
			wantQuery: map[string]string{"external-id": "ext-1"},
			response:  map[string]any{"subdomain": "a1b2c3"},
			want:      map[string]any{"subdomain": "a1b2c3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := stub(t, http.StatusOK, tt.response)
			exposer, err := NewAppExposer(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			result, err := tt.call(exposer)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Method != tt.method || rec.Path != tt.wantPath {
				t.Errorf("request = %s %s, want %s %s", rec.Method, rec.Path, tt.method, tt.wantPath)
			}
			for k, want := range tt.wantQuery {
				if got := rec.Query[k]; got != want {
					t.Errorf("query[%s] = %q, want %q", k, got, want)
				}
			}
			want, _ := json.Marshal(tt.want)
			got, _ := json.Marshal(result)
			if string(want) != string(got) {
				t.Errorf("result = %s, want %s", got, want)
			}
		})
	}
}
