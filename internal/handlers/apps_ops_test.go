package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/terraintest"
	"github.com/cyverse-de/formation/internal/vice"
)

const (
	opsAppID      = "0123abcd-0000-4000-8000-00000000beef"
	opsAnalysisID = "9876fedc-0000-4000-8000-00000000cafe"
)

// opsCaller is the resolved identity ops tests hand to the ops functions; the
// fake terrain records the forwarded token for assertions.
var opsCaller = &auth.Caller{Token: "test-token", Username: "alice"}

// newAppsOps wires an Apps instance against a fake terrain, bypassing the HTTP
// layer entirely.
func newAppsOps(t *testing.T, respond func(r *http.Request) (int, any)) (*Apps, *terraintest.Server) {
	t.Helper()

	terrain := terraintest.New(t, respond)
	terrainClient, err := clients.NewTerrain(terrain.URL())
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		UserSuffix: "@iplantcollaborative.org",
		ViceDomain: ".vice.invalid",
		OutputZone: "iplant",
	}
	apps := NewApps(
		terrainClient,
		vice.NewURLChecker(200*time.Millisecond, 1, time.Minute),
		vice.NewSubdomainResolverWithRetries(terrainClient, 1, time.Millisecond),
		cfg,
	)
	return apps, terrain
}

func TestListAppsPage(t *testing.T) {
	apps, terrain := newAppsOps(t, func(r *http.Request) (int, any) {
		return 200, map[string]any{
			"total": 2500,
			"apps": []map[string]any{{
				"id": "app-1", "name": "JupyterLab", "description": "notebooks",
				"integrator_name": "bob@iplantcollaborative.org", "extra": "dropped",
			}},
		}
	})

	result, err := apps.ListAppsPage(context.Background(), opsCaller, "jupyter", 50, 10)
	if err != nil {
		t.Fatal(err)
	}

	calls := terrain.Calls()
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	if calls[0].Path != "/apps" {
		t.Errorf("upstream path = %q", calls[0].Path)
	}
	query := calls[0].Query
	if query.Get("limit") != "50" || query.Get("offset") != "10" || query.Get("search") != "jupyter" {
		t.Errorf("upstream query = %v", query)
	}
	// The caller's own bearer token must be forwarded to terrain.
	if calls[0].BearerToken() != opsCaller.Token {
		t.Errorf("upstream Authorization = %q, want the caller's token", calls[0].Authorization)
	}
	if query.Has("user") {
		t.Error("user query parameter should not be sent; terrain derives it from the token")
	}

	if result["total"] != float64(2500) {
		t.Errorf("total = %v, want 2500 (upstream total)", result["total"])
	}
	formatted, _ := result["apps"].([]map[string]any)
	if len(formatted) != 1 {
		t.Fatalf("apps = %v", result["apps"])
	}
	if formatted[0]["integrator_username"] != "bob" {
		t.Errorf("integrator_username = %v, want bob", formatted[0]["integrator_username"])
	}
	if _, ok := formatted[0]["extra"]; ok {
		t.Error("unknown upstream fields should be dropped from formatted apps")
	}
}

func TestAnalysesForUser(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantFilter string
	}{
		{"status filter", "Running", `[{"field":"status","value":"Running"}]`},
		{"explicit status", "Completed", `[{"field":"status","value":"Completed"}]`},
		{"empty status lists all", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps, terrain := newAppsOps(t, func(r *http.Request) (int, any) {
				return 200, map[string]any{"analyses": []map[string]any{{
					"id": "an-1", "name": "run", "app_id": "app-1", "system_id": "de",
					"status": "Running", "startdate": "dropped",
				}}}
			})

			analyses, err := apps.AnalysesForUser(context.Background(), opsCaller, tt.status)
			if err != nil {
				t.Fatal(err)
			}

			calls := terrain.Calls()
			if calls[0].Path != "/analyses" || calls[0].BearerToken() != opsCaller.Token {
				t.Errorf("upstream call = %q with auth %q", calls[0].Path, calls[0].Authorization)
			}
			query := calls[0].Query
			if tt.wantFilter == "" {
				if query.Has("filter") {
					t.Errorf("filter = %q, want absent", query.Get("filter"))
				}
			} else if query.Get("filter") != tt.wantFilter {
				t.Errorf("filter = %q, want %q", query.Get("filter"), tt.wantFilter)
			}

			if len(analyses) != 1 {
				t.Fatalf("analyses = %v", analyses)
			}
			analysis := analyses[0]
			if analysis["analysis_id"] != "an-1" || analysis["status"] != "Running" {
				t.Errorf("analysis = %v", analysis)
			}
			if _, ok := analysis["startdate"]; ok {
				t.Error("extra upstream fields should be dropped")
			}
		})
	}
}

func TestAppParameters(t *testing.T) {
	t.Run("groups passthrough", func(t *testing.T) {
		apps, terrain := newAppsOps(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{
				"groups":           []map[string]any{{"id": "g1"}},
				"overall_job_type": "Interactive",
				"name":             "dropped",
			}
		})

		result, err := apps.AppParameters(context.Background(), opsCaller, "de", opsAppID)
		if err != nil {
			t.Fatal(err)
		}
		if calls := terrain.Calls(); calls[0].Path != "/apps/de/"+opsAppID {
			t.Errorf("upstream path = %q", calls[0].Path)
		}

		if result["overall_job_type"] != "Interactive" {
			t.Errorf("overall_job_type = %v", result["overall_job_type"])
		}
		if groups, _ := result["groups"].([]any); len(groups) != 1 {
			t.Errorf("groups = %v", result["groups"])
		}
		if _, ok := result["name"]; ok {
			t.Error("only groups and overall_job_type should be returned")
		}
	})

	t.Run("missing groups becomes empty list", func(t *testing.T) {
		apps, _ := newAppsOps(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{"overall_job_type": "DE"}
		})
		result, err := apps.AppParameters(context.Background(), opsCaller, "de", opsAppID)
		if err != nil {
			t.Fatal(err)
		}
		groups, ok := result["groups"].([]any)
		if !ok || len(groups) != 0 {
			t.Errorf("groups = %v (%T), want []", result["groups"], result["groups"])
		}
	})

	t.Run("bad app UUID", func(t *testing.T) {
		apps, _ := newAppsOps(t, nil)
		_, err := apps.AppParameters(context.Background(), opsCaller, "de", "not-a-uuid")
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) || apiErr.Message != "Invalid app ID format" {
			t.Fatalf("error = %v, want Invalid app ID format", err)
		}
		if apiErr.Details["field"] != "app_id" {
			t.Errorf("details.field = %v, want app_id", apiErr.Details["field"])
		}
	})
}

func TestControlAnalysis(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		wantPath   string
		wantResult map[string]any
	}{
		{
			name: "extend_time", operation: "extend_time",
			wantPath:   "/analyses/" + opsAnalysisID + "/time-limit",
			wantResult: map[string]any{"time_limit": "1749600000", "operation": "extend_time"},
		},
		{
			name: "save_and_exit", operation: "save_and_exit",
			wantPath:   "/analyses/" + opsAnalysisID + "/stop",
			wantResult: map[string]any{"status": "terminated", "outputs_saved": true, "operation": "save_and_exit"},
		},
		{
			name: "exit", operation: "exit",
			wantPath:   "/vice/analyses/" + opsAnalysisID + "/exit",
			wantResult: map[string]any{"status": "terminated", "outputs_saved": false, "operation": "exit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps, terrain := newAppsOps(t, func(r *http.Request) (int, any) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/time-limit"):
					return 200, map[string]any{"time_limit": "1749600000"}
				case strings.HasSuffix(r.URL.Path, "/stop"):
					return 200, map[string]any{"id": opsAnalysisID}
				default:
					return 200, nil
				}
			})

			result, err := apps.ControlAnalysis(context.Background(), opsCaller, opsAnalysisID, tt.operation)
			if err != nil {
				t.Fatal(err)
			}
			calls := terrain.Calls()
			if calls[0].Method != http.MethodPost || calls[0].Path != tt.wantPath {
				t.Errorf("upstream call = %s %s, want POST %s", calls[0].Method, calls[0].Path, tt.wantPath)
			}

			for key, want := range tt.wantResult {
				if result[key] != want {
					t.Errorf("result[%q] = %v, want %v", key, result[key], want)
				}
			}
		})
	}

	t.Run("invalid operation checked before UUID", func(t *testing.T) {
		apps, _ := newAppsOps(t, nil)
		_, err := apps.ControlAnalysis(context.Background(), opsCaller, "not-a-uuid", "bogus")
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) || apiErr.Message != "Invalid operation. Must be one of: extend_time, save_and_exit, exit" {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("valid operation with bad UUID", func(t *testing.T) {
		apps, _ := newAppsOps(t, nil)
		_, err := apps.ControlAnalysis(context.Background(), opsCaller, "not-a-uuid", "exit")
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) || apiErr.Message != "Invalid analysis ID format" {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestAnalysisStatus(t *testing.T) {
	t.Run("with subdomain", func(t *testing.T) {
		apps, _ := newAppsOps(t, func(r *http.Request) (int, any) {
			switch {
			case r.URL.Path == "/analyses":
				return 200, map[string]any{"analyses": []map[string]any{{"id": opsAnalysisID, "status": "Running"}}}
			case strings.HasSuffix(r.URL.Path, "/external-id"):
				return 200, map[string]any{"externalID": "ext-1"}
			case r.URL.Path == "/vice/async-data":
				return 200, map[string]any{"subdomain": "a1b2c3"}
			default:
				return 500, nil
			}
		})

		// Uppercase UUID: the result echoes the raw ID as given.
		rawID := strings.ToUpper(opsAnalysisID)
		result, err := apps.AnalysisStatus(context.Background(), opsCaller, rawID)
		if err != nil {
			t.Fatal(err)
		}

		if result["analysis_id"] != rawID {
			t.Errorf("analysis_id = %v, want raw %q", result["analysis_id"], rawID)
		}
		if result["status"] != "Running" {
			t.Errorf("status = %v", result["status"])
		}
		if result["url"] != "https://a1b2c3.vice.invalid" {
			t.Errorf("url = %v", result["url"])
		}
		// The VICE domain is unreachable in tests, so the probe reports not ready.
		if result["url_ready"] != false {
			t.Errorf("url_ready = %v, want false", result["url_ready"])
		}
		if _, ok := result["url_check_details"]; !ok {
			t.Error("url_check_details missing")
		}
	})

	t.Run("no subdomain and missing status", func(t *testing.T) {
		apps, _ := newAppsOps(t, func(r *http.Request) (int, any) {
			if r.URL.Path == "/analyses" {
				return 200, map[string]any{"analyses": []map[string]any{{"id": opsAnalysisID}}}
			}
			return 404, nil
		})

		result, err := apps.AnalysisStatus(context.Background(), opsCaller, opsAnalysisID)
		if err != nil {
			t.Fatal(err)
		}

		if result["status"] != "Unknown" {
			t.Errorf("status = %v, want Unknown", result["status"])
		}
		if result["url_ready"] != false {
			t.Errorf("url_ready = %v, want false", result["url_ready"])
		}
		if _, ok := result["url"]; ok {
			t.Error("url should be absent without a subdomain")
		}
		if _, ok := result["url_check_details"]; ok {
			t.Error("url_check_details should be absent without a subdomain")
		}
	})

	t.Run("unknown analysis is an upstream 404", func(t *testing.T) {
		apps, _ := newAppsOps(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{"analyses": []any{}}
		})

		_, err := apps.AnalysisStatus(context.Background(), opsCaller, opsAnalysisID)
		var upstream *apierror.UpstreamError
		if !errors.As(err, &upstream) || upstream.Status != 404 {
			t.Fatalf("error = %v, want UpstreamError with status 404", err)
		}
	})
}

var generatedNamePattern = regexp.MustCompile(`^jupyterlab-4-3-\d{4}-\d{2}-\d{2}-\d{6}$`)

func TestLaunchAnalysis(t *testing.T) {
	// terrainResponder serves the launch flow: app details for name
	// generation, the submission response, and 404s for the VICE lookups.
	terrainResponder := func(submitResponse map[string]any) func(r *http.Request) (int, any) {
		return func(r *http.Request) (int, any) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/analyses":
				return 200, submitResponse
			case strings.HasPrefix(r.URL.Path, "/apps/"):
				return 200, map[string]any{"name": "JupyterLab 4.3"}
			default:
				return 404, nil
			}
		}
	}

	// submits picks the analysis submissions out of the recorded calls, which
	// also include the GetApp and VICE subdomain lookups.
	submits := func(terrain *terraintest.Server) []terraintest.Call {
		var posts []terraintest.Call
		for _, call := range terrain.Calls() {
			if call.Method == http.MethodPost && call.Path == "/analyses" {
				posts = append(posts, call)
			}
		}
		return posts
	}

	t.Run("empty submission gets defaults", func(t *testing.T) {
		apps, terrain := newAppsOps(t, terrainResponder(map[string]any{
			"id": opsAnalysisID, "name": "run", "status": "Submitted",
		}))

		result, err := apps.LaunchAnalysis(context.Background(), opsCaller, "de", opsAppID, "iplant", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}

		posts := submits(terrain)
		if len(posts) != 1 {
			t.Fatalf("submissions = %d, want 1", len(posts))
		}
		submit := posts[0]
		if submit.Query.Has("email") || submit.Query.Has("user") {
			t.Errorf("user/email query parameters should not be sent: %v", submit.Query)
		}

		submission := submit.Body
		if submission["app_id"] != opsAppID || submission["system_id"] != "de" {
			t.Errorf("app_id/system_id = %v/%v", submission["app_id"], submission["system_id"])
		}
		if submission["debug"] != false || submission["notify"] != true {
			t.Errorf("debug/notify = %v/%v", submission["debug"], submission["notify"])
		}
		if cfg, ok := submission["config"].(map[string]any); !ok || len(cfg) != 0 {
			t.Errorf("config = %v", submission["config"])
		}

		name, _ := submission["name"].(string)
		if !generatedNamePattern.MatchString(name) {
			t.Errorf("generated name = %q", name)
		}
		if submission["output_dir"] != "/iplant/home/alice/analyses/"+name {
			t.Errorf("output_dir = %v", submission["output_dir"])
		}

		if result["analysis_id"] != opsAnalysisID || result["name"] != "run" || result["status"] != "Submitted" {
			t.Errorf("result = %v", result)
		}
		if _, ok := result["url"]; ok {
			t.Error("url should be absent when no subdomain resolves")
		}
	})

	t.Run("email stripped; terrain derives it from the token", func(t *testing.T) {
		apps, terrain := newAppsOps(t, terrainResponder(map[string]any{"id": opsAnalysisID}))

		_, err := apps.LaunchAnalysis(context.Background(), opsCaller, "de", opsAppID, "iplant",
			map[string]any{"name": "my-run", "email": "body@example.org"})
		if err != nil {
			t.Fatal(err)
		}

		// No GetApp call: the explicit name skips name generation.
		calls := terrain.Calls()
		if calls[0].Method != http.MethodPost || calls[0].Path != "/analyses" {
			t.Fatalf("first call = %s %s, want the submit", calls[0].Method, calls[0].Path)
		}
		if _, ok := calls[0].Body["email"]; ok {
			t.Error("email must be stripped from the submission body")
		}
		if calls[0].Body["name"] != "my-run" {
			t.Errorf("name = %v, want my-run preserved", calls[0].Body["name"])
		}
	})

	t.Run("placeholder requirements stripped, real ones kept", func(t *testing.T) {
		apps, terrain := newAppsOps(t, terrainResponder(map[string]any{"id": opsAnalysisID}))

		_, err := apps.LaunchAnalysis(context.Background(), opsCaller, "de", opsAppID, "iplant", map[string]any{
			"name": "r1",
			"requirements": []any{map[string]any{
				"step_number": float64(0), "min_cpu_cores": float64(0),
				"max_cpu_cores": float64(0), "min_memory_limit": float64(0),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := submits(terrain)[0].Body["requirements"]; ok {
			t.Error("all-zero placeholder requirements should be removed")
		}

		_, err = apps.LaunchAnalysis(context.Background(), opsCaller, "de", opsAppID, "iplant", map[string]any{
			"name": "r2",
			"requirements": []any{map[string]any{
				"step_number": float64(0), "min_cpu_cores": float64(4),
				"max_cpu_cores": float64(0), "min_memory_limit": float64(0),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := submits(terrain)[1].Body["requirements"]; !ok {
			t.Error("real requirements should be preserved")
		}
	})

	t.Run("response fallbacks and url from subdomain", func(t *testing.T) {
		apps, _ := newAppsOps(t, func(r *http.Request) (int, any) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/analyses":
				return 200, map[string]any{"id": opsAnalysisID}
			case strings.HasSuffix(r.URL.Path, "/external-id"):
				return 200, map[string]any{"externalID": "ext-1"}
			case r.URL.Path == "/vice/async-data":
				return 200, map[string]any{"subdomain": "d4e5f6"}
			default:
				return 500, nil
			}
		})

		result, err := apps.LaunchAnalysis(context.Background(), opsCaller, "de", opsAppID, "iplant",
			map[string]any{"name": "my-run"})
		if err != nil {
			t.Fatal(err)
		}

		if result["name"] != "my-run" {
			t.Errorf("name = %v, want submission fallback when upstream omits it", result["name"])
		}
		if result["status"] != "Submitted" {
			t.Errorf("status = %v, want Submitted fallback", result["status"])
		}
		if result["url"] != "https://d4e5f6.vice.invalid" {
			t.Errorf("url = %v", result["url"])
		}
	})

	t.Run("bad app UUID", func(t *testing.T) {
		apps, _ := newAppsOps(t, nil)
		_, err := apps.LaunchAnalysis(context.Background(), opsCaller, "de", "not-a-uuid", "iplant", map[string]any{})
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) || apiErr.Message != "Invalid app ID format" {
			t.Fatalf("error = %v", err)
		}
	})
}
