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
	Method        string
	Path          string
	Query         map[string]string
	Body          map[string]any
	Authorization string
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
		rec.Authorization = r.Header.Get("Authorization")
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

const testToken = "test-token"

func TestTerrainClientRequests(t *testing.T) {
	okBody := map[string]any{"ok": true}

	tests := []struct {
		name      string
		call      func(c *Terrain) (map[string]any, error)
		method    string
		wantPath  string
		wantQuery map[string]string
		wantBody  map[string]any
		response  any
		want      map[string]any
	}{
		{
			name: "GetApp",
			call: func(c *Terrain) (map[string]any, error) {
				return c.GetApp(context.Background(), testToken, "de", "app-uuid")
			},
			method:    http.MethodGet,
			wantPath:  "/apps/de/app-uuid",
			wantQuery: map[string]string{},
		},
		{
			name: "ListApps with search",
			call: func(c *Terrain) (map[string]any, error) {
				return c.ListApps(context.Background(), testToken, 100, 20, "jupyter")
			},
			method:    http.MethodGet,
			wantPath:  "/apps",
			wantQuery: map[string]string{"limit": "100", "offset": "20", "search": "jupyter"},
		},
		{
			name: "SubmitAnalysis",
			call: func(c *Terrain) (map[string]any, error) {
				return c.SubmitAnalysis(context.Background(), testToken, map[string]any{"name": "run-1"})
			},
			method:    http.MethodPost,
			wantPath:  "/analyses",
			wantQuery: map[string]string{},
			wantBody:  map[string]any{"name": "run-1"},
		},
		{
			name: "ListAnalyses with status filter",
			call: func(c *Terrain) (map[string]any, error) {
				return c.ListAnalyses(context.Background(), testToken, "Running")
			},
			method:    http.MethodGet,
			wantPath:  "/analyses",
			wantQuery: map[string]string{"filter": `[{"field":"status","value":"Running"}]`},
		},
		{
			name: "ListAnalyses without status omits filter",
			call: func(c *Terrain) (map[string]any, error) {
				return c.ListAnalyses(context.Background(), testToken, "")
			},
			method:    http.MethodGet,
			wantPath:  "/analyses",
			wantQuery: map[string]string{},
		},
		{
			name: "ExtendTimeLimit",
			call: func(c *Terrain) (map[string]any, error) {
				return c.ExtendTimeLimit(context.Background(), testToken, "an-1")
			},
			method:    http.MethodPost,
			wantPath:  "/analyses/an-1/time-limit",
			wantQuery: map[string]string{},
			response:  map[string]any{"time_limit": "1749600000"},
			want:      map[string]any{"time_limit": "1749600000"},
		},
		{
			name: "SaveAndExit stops via terrain and synthesizes status",
			call: func(c *Terrain) (map[string]any, error) {
				return c.SaveAndExit(context.Background(), testToken, "an-1")
			},
			method:    http.MethodPost,
			wantPath:  "/analyses/an-1/stop",
			wantQuery: map[string]string{},
			response:  map[string]any{"id": "an-1"},
			want:      map[string]any{"status": "terminated", "outputs_saved": true},
		},
		{
			name: "ExitWithoutSave synthesizes status",
			call: func(c *Terrain) (map[string]any, error) {
				return c.ExitWithoutSave(context.Background(), testToken, "an-1")
			},
			method:    http.MethodPost,
			wantPath:  "/vice/analyses/an-1/exit",
			wantQuery: map[string]string{},
			want:      map[string]any{"status": "terminated", "outputs_saved": false},
		},
		{
			name: "GetExternalID",
			call: func(c *Terrain) (map[string]any, error) {
				return c.GetExternalID(context.Background(), testToken, "an-1")
			},
			method:    http.MethodGet,
			wantPath:  "/vice/analyses/an-1/external-id",
			wantQuery: map[string]string{},
			response:  map[string]any{"externalID": "ext-1"},
			want:      map[string]any{"externalID": "ext-1"},
		},
		{
			name: "GetAsyncData",
			call: func(c *Terrain) (map[string]any, error) {
				return c.GetAsyncData(context.Background(), testToken, "ext-1")
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
			response := tt.response
			if response == nil && tt.want == nil {
				response = okBody
			}
			server, rec := stub(t, http.StatusOK, response)
			terrain, err := NewTerrain(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			result, err := tt.call(terrain)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Method != tt.method || rec.Path != tt.wantPath {
				t.Errorf("request = %s %s, want %s %s", rec.Method, rec.Path, tt.method, tt.wantPath)
			}
			if rec.Authorization != "Bearer "+testToken {
				t.Errorf("Authorization = %q, want the bearer token", rec.Authorization)
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
			if tt.want != nil {
				want, _ := json.Marshal(tt.want)
				got, _ := json.Marshal(result)
				if string(want) != string(got) {
					t.Errorf("result = %s, want %s", got, want)
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
		terrain, _ := NewTerrain(server.URL)

		analysis, err := terrain.GetAnalysis(context.Background(), testToken, "an-1")
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
		terrain, _ := NewTerrain(server.URL)

		_, err := terrain.GetAnalysis(context.Background(), testToken, "an-1")
		var upstream *apierror.UpstreamError
		if !errors.As(err, &upstream) || upstream.Status != http.StatusNotFound {
			t.Fatalf("err = %v, want 404 UpstreamError", err)
		}
	})
}

func TestNon2xxBecomesUpstreamError(t *testing.T) {
	server, _ := stub(t, http.StatusBadRequest, map[string]any{"reason": "nope"})
	terrain, _ := NewTerrain(server.URL)

	_, err := terrain.GetApp(context.Background(), testToken, "de", "app-uuid")
	var upstream *apierror.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("err = %v, want UpstreamError", err)
	}
	if upstream.Status != http.StatusBadRequest || upstream.Body != "{\"reason\":\"nope\"}\n" {
		t.Errorf("upstream = %+v", upstream)
	}
}
