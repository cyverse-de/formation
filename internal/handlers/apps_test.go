package handlers

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/terraintest"
	"github.com/cyverse-de/formation/internal/vice"
)

const (
	testAppID      = "0123abcd-0000-4000-8000-00000000beef"
	testAnalysisID = "9876fedc-0000-4000-8000-00000000cafe"
)

// testEnv runs the apps routes behind the real auth middleware (fake
// Keycloak) with a fake terrain standing in for the backend.
type testEnv struct {
	echo    *echo.Echo
	kc      *authtest.Keycloak
	terrain *terraintest.Server
}

func newTestEnv(t *testing.T, respond func(r *http.Request) (int, any)) *testEnv {
	t.Helper()

	terrain := terraintest.New(t, respond)
	terrainClient, err := clients.NewTerrain(terrain.URL())
	if err != nil {
		t.Fatal(err)
	}

	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), kc.Realm, true)
	requireUserOrSA := auth.RequireUserOrServiceAccount(verifier, false)

	keycloak, err := auth.NewKeycloak(kc.ServerURL(), kc.Realm, "formation", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	callers := auth.NewCallerResolver(
		auth.NewImpersonator(keycloak),
		map[string]string{auth.AppRunnerRole: "Svc-Account-1"},
	)

	cfg := &config.Config{
		UserSuffix: "@iplantcollaborative.org",
		ViceDomain: ".vice.invalid",
		OutputZone: "iplant",
	}
	apps := NewApps(
		terrainClient,
		vice.NewURLChecker(200*time.Millisecond, 1, time.Minute),
		vice.NewSubdomainResolverWithRetries(terrainClient, 1, time.Millisecond),
		callers,
		cfg,
	)

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.GET("/apps/job-types", apps.JobTypes, requireUserOrSA)
	e.GET("/apps", apps.List, requireUserOrSA)
	e.GET("/apps/analyses", apps.ListAnalyses, requireUserOrSA)
	e.GET("/apps/analyses/", apps.ListAnalyses, requireUserOrSA)
	e.GET("/apps/analyses/:analysis_id/status", apps.Status, requireUserOrSA)
	e.POST("/apps/analyses/:analysis_id/control", apps.Control, requireUserOrSA)
	e.GET("/apps/analyses/:analysis_id/details", apps.Details, requireUserOrSA)
	e.GET("/apps/:system_id/:app_id/parameters", apps.Parameters, requireUserOrSA)
	e.POST("/app/launch/:system_id/:app_id", apps.Launch, requireUserOrSA)

	return &testEnv{echo: e, kc: kc, terrain: terrain}
}

func (env *testEnv) userToken(t *testing.T, extra map[string]any) string {
	claims := map[string]any{"preferred_username": "alice"}
	maps.Copy(claims, extra)
	return env.kc.Token(t, claims)
}

func (env *testEnv) request(t *testing.T, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	env.echo.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestJobTypesEndpoint(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.request(t, http.MethodGet, "/apps/job-types", env.userToken(t, nil), "")

	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	types, _ := body["job_types"].([]any)
	if len(types) != 4 {
		t.Fatalf("job_types count = %d, want 4", len(types))
	}
	first, _ := types[0].(map[string]any)
	if first["name"] != "VICE" || first["internal_name"] != "Interactive" {
		t.Errorf("first job type = %v", first)
	}
}

func TestListAppsUnfilteredPassesThrough(t *testing.T) {
	env := newTestEnv(t, func(r *http.Request) (int, any) {
		return 200, map[string]any{
			"total": 2500,
			"apps": []map[string]any{{
				"id": "app-1", "name": "JupyterLab", "description": "notebooks",
				"integrator_name": "bob@iplantcollaborative.org", "extra": "dropped",
			}},
		}
	})
	token := env.userToken(t, nil)
	rec := env.request(t, http.MethodGet, "/apps?limit=50&offset=10&name=jupyter", token, "")

	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	calls := env.terrain.Calls()
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
	if calls[0].BearerToken() != token {
		t.Errorf("upstream Authorization = %q, want the caller's token", calls[0].Authorization)
	}
	if query.Has("user") {
		t.Error("user query parameter should not be sent; terrain derives it from the token")
	}

	body := decodeBody(t, rec)
	if body["total"] != float64(2500) {
		t.Errorf("total = %v, want 2500 (upstream total)", body["total"])
	}
	apps, _ := body["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("apps = %v", body["apps"])
	}
	app, _ := apps[0].(map[string]any)
	if app["integrator_username"] != "bob" {
		t.Errorf("integrator_username = %v, want bob", app["integrator_username"])
	}
	if _, ok := app["extra"]; ok {
		t.Error("unknown upstream fields should be dropped from formatted apps")
	}
}

func TestListAppsFilteredPagesThroughCorpus(t *testing.T) {
	// Page 1 holds 500 apps (3 Interactive); page 2 holds 3 (1 Interactive).
	makeApp := func(id string, jobType string) map[string]any {
		return map[string]any{"id": id, "name": id, "overall_job_type": jobType}
	}
	page1 := make([]map[string]any, 0, listAppsPageSize)
	for i := range listAppsPageSize {
		jobType := "DE"
		if i == 0 || i == 250 || i == 499 {
			jobType = "Interactive"
		}
		page1 = append(page1, makeApp("p1-"+string(rune('a'+i%26))+"-"+time.Duration(i).String(), jobType))
	}
	page2 := []map[string]any{
		makeApp("p2-0", "DE"), makeApp("p2-1", "Interactive"), makeApp("p2-2", "OSG"),
	}

	env := newTestEnv(t, func(r *http.Request) (int, any) {
		apps := page1
		if r.URL.Query().Get("offset") != "0" {
			apps = page2
		}
		return 200, map[string]any{"total": len(page1) + len(page2), "apps": apps}
	})
	token := env.userToken(t, nil)

	rec := env.request(t, http.MethodGet, "/apps?job_type=vice&limit=3", token, "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	calls := env.terrain.Calls()
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2 (paged through corpus)", len(calls))
	}
	if calls[0].Query.Get("limit") != "500" || calls[1].Query.Get("offset") != "500" {
		t.Errorf("upstream paging queries = %v, %v", calls[0].Query, calls[1].Query)
	}

	body := decodeBody(t, rec)
	if body["total"] != float64(4) {
		t.Errorf("total = %v, want 4 (filtered count)", body["total"])
	}
	if apps, _ := body["apps"].([]any); len(apps) != 3 {
		t.Errorf("apps page length = %d, want 3", len(apps))
	}

	// Offset slices into the filtered result set.
	rec = env.request(t, http.MethodGet, "/apps?job_type=vice&limit=3&offset=3", token, "")
	body = decodeBody(t, rec)
	if apps, _ := body["apps"].([]any); len(apps) != 1 {
		t.Errorf("apps with offset=3 length = %d, want 1", len(apps))
	}
}

func TestListAppsValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	token := env.userToken(t, nil)

	tests := []struct {
		name       string
		target     string
		wantDetail string
		wantField  string
	}{
		{"limit too small", "/apps?limit=0", "Limit must be between 1 and 1000", "limit"},
		{"limit too large", "/apps?limit=1001", "Limit must be between 1 and 1000", "limit"},
		{"limit not an int", "/apps?limit=abc", "Invalid integer value for limit", "limit"},
		{"negative offset", "/apps?offset=-1", "Offset must be non-negative", "offset"},
		{
			"bad integration_date", "/apps?integration_date=2025-09-29",
			"Invalid date filter format: '2025-09-29'. Expected format: <operator><date> (e.g., '>2025-09-29', '<=2024-12-31T23:59:59')",
			"integration_date",
		},
		{
			"bad edited_date value", "/apps?edited_date=%3Enot-a-date",
			"Invalid date format: 'not-a-date'. Expected ISO 8601 format (e.g., '2025-09-29', '2025-09-29T14:30:00', '2025-09-29T14:30:00Z')",
			"edited_date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.request(t, http.MethodGet, tt.target, token, "")
			if rec.Code != 400 {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			body := decodeBody(t, rec)
			if body["detail"] != tt.wantDetail {
				t.Errorf("detail = %q, want %q", body["detail"], tt.wantDetail)
			}
			details, _ := body["details"].(map[string]any)
			if details["field"] != tt.wantField {
				t.Errorf("details.field = %v, want %q", details["field"], tt.wantField)
			}
		})
	}
}

func TestListAppsServiceAccountImpersonation(t *testing.T) {
	env := newTestEnv(t, func(r *http.Request) (int, any) {
		return 200, map[string]any{"total": 0, "apps": []any{}}
	})
	exchange := env.kc.ServeTokenExchange(t)

	token := env.kc.Token(t, map[string]any{
		"preferred_username": "service-account-de",
		"realm_access":       map[string]any{"roles": []string{auth.AppRunnerRole}},
	})

	rec := env.request(t, http.MethodGet, "/apps", token, "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	// Configured mapping "Svc-Account-1" sanitized to lowercase alphanumerics,
	// then exchanged for an impersonation token that goes to terrain.
	impersonated := exchange.IssuedFor("svcaccount1")
	if impersonated == "" {
		t.Fatal("no token exchange happened for svcaccount1")
	}
	calls := env.terrain.Calls()
	if got := calls[0].BearerToken(); got != impersonated {
		t.Errorf("terrain received token %q, want the impersonation token", got)
	}
	if calls[0].BearerToken() == token {
		t.Error("terrain must not receive the raw service-account token")
	}
}

func TestListAnalysesStatusFilter(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantFilter string
	}{
		{"default is Running", "/apps/analyses", `[{"field":"status","value":"Running"}]`},
		{"trailing slash works", "/apps/analyses/", `[{"field":"status","value":"Running"}]`},
		{"explicit status", "/apps/analyses?status=Completed", `[{"field":"status","value":"Completed"}]`},
		{"explicit empty status lists all", "/apps/analyses?status=", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, func(r *http.Request) (int, any) {
				return 200, map[string]any{"analyses": []map[string]any{{
					"id": "an-1", "name": "run", "app_id": "app-1", "system_id": "de",
					"status": "Running", "startdate": "dropped",
				}}}
			})
			token := env.userToken(t, nil)
			rec := env.request(t, http.MethodGet, tt.target, token, "")
			if rec.Code != 200 {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}

			calls := env.terrain.Calls()
			if calls[0].Path != "/analyses" || calls[0].BearerToken() != token {
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

			body := decodeBody(t, rec)
			analyses, _ := body["analyses"].([]any)
			if len(analyses) != 1 {
				t.Fatalf("analyses = %v", body["analyses"])
			}
			analysis, _ := analyses[0].(map[string]any)
			if analysis["analysis_id"] != "an-1" || analysis["status"] != "Running" {
				t.Errorf("analysis = %v", analysis)
			}
			if _, ok := analysis["startdate"]; ok {
				t.Error("extra upstream fields should be dropped")
			}
		})
	}
}

func TestParametersEndpoint(t *testing.T) {
	t.Run("groups passthrough", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{
				"groups":           []map[string]any{{"id": "g1"}},
				"overall_job_type": "Interactive",
				"name":             "dropped",
			}
		})
		rec := env.request(t, http.MethodGet, "/apps/de/"+testAppID+"/parameters", env.userToken(t, nil), "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if calls := env.terrain.Calls(); calls[0].Path != "/apps/de/"+testAppID {
			t.Errorf("upstream path = %q", calls[0].Path)
		}

		body := decodeBody(t, rec)
		if body["overall_job_type"] != "Interactive" {
			t.Errorf("overall_job_type = %v", body["overall_job_type"])
		}
		if groups, _ := body["groups"].([]any); len(groups) != 1 {
			t.Errorf("groups = %v", body["groups"])
		}
		if _, ok := body["name"]; ok {
			t.Error("only groups and overall_job_type should be returned")
		}
	})

	t.Run("missing groups becomes empty list", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{"overall_job_type": "DE"}
		})
		rec := env.request(t, http.MethodGet, "/apps/de/"+testAppID+"/parameters", env.userToken(t, nil), "")
		body := decodeBody(t, rec)
		groups, ok := body["groups"].([]any)
		if !ok || len(groups) != 0 {
			t.Errorf("groups = %v (%T), want []", body["groups"], body["groups"])
		}
	})

	t.Run("bad app UUID", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rec := env.request(t, http.MethodGet, "/apps/de/not-a-uuid/parameters", env.userToken(t, nil), "")
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Invalid app ID format" {
			t.Errorf("detail = %v", body["detail"])
		}
	})
}

func TestControlEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		wantPath  string
		wantBody  map[string]any
	}{
		{
			name: "extend_time", operation: "extend_time",
			wantPath: "/analyses/" + testAnalysisID + "/time-limit",
			wantBody: map[string]any{"time_limit": "1749600000", "operation": "extend_time"},
		},
		{
			name: "save_and_exit", operation: "save_and_exit",
			wantPath: "/analyses/" + testAnalysisID + "/stop",
			wantBody: map[string]any{"status": "terminated", "outputs_saved": true, "operation": "save_and_exit"},
		},
		{
			name: "exit", operation: "exit",
			wantPath: "/vice/analyses/" + testAnalysisID + "/exit",
			wantBody: map[string]any{"status": "terminated", "outputs_saved": false, "operation": "exit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, func(r *http.Request) (int, any) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/time-limit"):
					return 200, map[string]any{"time_limit": "1749600000"}
				case strings.HasSuffix(r.URL.Path, "/stop"):
					return 200, map[string]any{"id": testAnalysisID}
				default:
					return 200, nil
				}
			})
			rec := env.request(t, http.MethodPost,
				"/apps/analyses/"+testAnalysisID+"/control?operation="+tt.operation, env.userToken(t, nil), "")
			if rec.Code != 200 {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			calls := env.terrain.Calls()
			if calls[0].Method != http.MethodPost || calls[0].Path != tt.wantPath {
				t.Errorf("upstream call = %s %s, want POST %s", calls[0].Method, calls[0].Path, tt.wantPath)
			}

			body := decodeBody(t, rec)
			for key, want := range tt.wantBody {
				if body[key] != want {
					t.Errorf("body[%q] = %v, want %v", key, body[key], want)
				}
			}
		})
	}

	t.Run("invalid operation checked before UUID", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rec := env.request(t, http.MethodPost, "/apps/analyses/not-a-uuid/control?operation=bogus", env.userToken(t, nil), "")
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["detail"] != "Invalid operation. Must be one of: extend_time, save_and_exit, exit" {
			t.Errorf("detail = %v", body["detail"])
		}
	})

	t.Run("valid operation with bad UUID", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rec := env.request(t, http.MethodPost, "/apps/analyses/not-a-uuid/control?operation=exit", env.userToken(t, nil), "")
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Invalid analysis ID format" {
			t.Errorf("detail = %v", body["detail"])
		}
	})
}

func TestDetailsEndpoint(t *testing.T) {
	t.Run("passthrough", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{"analyses": []map[string]any{{
				"id": testAnalysisID, "status": "Running", "interactive_urls": []string{"https://x"},
			}}}
		})
		rec := env.request(t, http.MethodGet, "/apps/analyses/"+testAnalysisID+"/details", env.userToken(t, nil), "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		calls := env.terrain.Calls()
		if got := calls[0].Query.Get("filter"); got != `[{"field":"id","value":"`+testAnalysisID+`"}]` {
			t.Errorf("upstream filter = %q", got)
		}
		body := decodeBody(t, rec)
		if body["id"] != testAnalysisID || body["status"] != "Running" {
			t.Errorf("body = %v", body)
		}
	})

	t.Run("not found surfaces as 502 with status_code 404", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			return 200, map[string]any{"analyses": []any{}}
		})
		rec := env.request(t, http.MethodGet, "/apps/analyses/"+testAnalysisID+"/details", env.userToken(t, nil), "")
		if rec.Code != 502 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["detail"] != "External service error: " || body["status_code"] != float64(404) {
			t.Errorf("body = %v", body)
		}
	})
}

func TestStatusEndpoint(t *testing.T) {
	t.Run("with subdomain", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			switch {
			case r.URL.Path == "/analyses":
				return 200, map[string]any{"analyses": []map[string]any{{"id": testAnalysisID, "status": "Running"}}}
			case strings.HasSuffix(r.URL.Path, "/external-id"):
				return 200, map[string]any{"externalID": "ext-1"}
			case r.URL.Path == "/vice/async-data":
				return 200, map[string]any{"subdomain": "a1b2c3"}
			default:
				return 500, nil
			}
		})
		// Uppercase UUID: the response echoes the raw path parameter.
		rawID := strings.ToUpper(testAnalysisID)
		rec := env.request(t, http.MethodGet, "/apps/analyses/"+rawID+"/status", env.userToken(t, nil), "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}

		body := decodeBody(t, rec)
		if body["analysis_id"] != rawID {
			t.Errorf("analysis_id = %v, want raw %q", body["analysis_id"], rawID)
		}
		if body["status"] != "Running" {
			t.Errorf("status = %v", body["status"])
		}
		if body["url"] != "https://a1b2c3.vice.invalid" {
			t.Errorf("url = %v", body["url"])
		}
		// The VICE domain is unreachable in tests, so the probe reports not ready.
		if body["url_ready"] != false {
			t.Errorf("url_ready = %v, want false", body["url_ready"])
		}
		if _, ok := body["url_check_details"]; !ok {
			t.Error("url_check_details missing")
		}
	})

	t.Run("no subdomain and missing status", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			if r.URL.Path == "/analyses" {
				return 200, map[string]any{"analyses": []map[string]any{{"id": testAnalysisID}}}
			}
			return 404, nil
		})
		rec := env.request(t, http.MethodGet, "/apps/analyses/"+testAnalysisID+"/status", env.userToken(t, nil), "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}

		body := decodeBody(t, rec)
		if body["status"] != "Unknown" {
			t.Errorf("status = %v, want Unknown", body["status"])
		}
		if body["url_ready"] != false {
			t.Errorf("url_ready = %v, want false", body["url_ready"])
		}
		if _, ok := body["url"]; ok {
			t.Error("url should be absent without a subdomain")
		}
		if _, ok := body["url_check_details"]; ok {
			t.Error("url_check_details should be absent without a subdomain")
		}
	})
}

var analysisNamePattern = regexp.MustCompile(`^jupyterlab-4-3-\d{4}-\d{2}-\d{2}-\d{6}$`)

func TestLaunchEndpoint(t *testing.T) {
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
	launchPath := "/app/launch/de/" + testAppID

	t.Run("empty body gets defaults", func(t *testing.T) {
		env := newTestEnv(t, terrainResponder(map[string]any{
			"id": testAnalysisID, "name": "run", "status": "Submitted",
		}))
		rec := env.request(t, http.MethodPost, launchPath, env.userToken(t, nil), "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}

		// calls[0] is the GetApp lookup for name generation, calls[1] the submit.
		calls := env.terrain.Calls()
		if len(calls) < 2 {
			t.Fatalf("upstream calls = %d, want at least 2", len(calls))
		}
		submit := calls[1]
		if submit.Path != "/analyses" || submit.Method != http.MethodPost {
			t.Errorf("submit call = %s %s", submit.Method, submit.Path)
		}
		if submit.Query.Has("email") || submit.Query.Has("user") {
			t.Errorf("user/email query parameters should not be sent: %v", submit.Query)
		}

		submission := submit.Body
		if submission["app_id"] != testAppID || submission["system_id"] != "de" {
			t.Errorf("app_id/system_id = %v/%v", submission["app_id"], submission["system_id"])
		}
		if submission["debug"] != false || submission["notify"] != true {
			t.Errorf("debug/notify = %v/%v", submission["debug"], submission["notify"])
		}
		if cfg, ok := submission["config"].(map[string]any); !ok || len(cfg) != 0 {
			t.Errorf("config = %v", submission["config"])
		}

		name, _ := submission["name"].(string)
		if !analysisNamePattern.MatchString(name) {
			t.Errorf("generated name = %q", name)
		}
		if submission["output_dir"] != "/iplant/home/alice/analyses/"+name {
			t.Errorf("output_dir = %v", submission["output_dir"])
		}

		body := decodeBody(t, rec)
		if body["analysis_id"] != testAnalysisID || body["name"] != "run" || body["status"] != "Submitted" {
			t.Errorf("response = %v", body)
		}
		if _, ok := body["url"]; ok {
			t.Error("url should be absent when no subdomain resolves")
		}
	})

	t.Run("email stripped from body; terrain derives it from the token", func(t *testing.T) {
		env := newTestEnv(t, terrainResponder(map[string]any{"id": testAnalysisID}))
		token := env.userToken(t, map[string]any{"email": "alice@university.edu"})
		rec := env.request(t, http.MethodPost, launchPath, token,
			`{"name":"my-run","email":"body@example.org"}`)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		// No GetApp call: the explicit name skips name generation.
		calls := env.terrain.Calls()
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
		env := newTestEnv(t, terrainResponder(map[string]any{"id": testAnalysisID}))
		token := env.userToken(t, nil)

		// The recorded calls include the VICE subdomain lookups, so pick out
		// only the analysis submissions.
		submits := func() []terraintest.Call {
			var posts []terraintest.Call
			for _, call := range env.terrain.Calls() {
				if call.Method == http.MethodPost && call.Path == "/analyses" {
					posts = append(posts, call)
				}
			}
			return posts
		}

		rec := env.request(t, http.MethodPost, launchPath, token,
			`{"name":"r1","requirements":[{"step_number":0,"min_cpu_cores":0,"max_cpu_cores":0,"min_memory_limit":0}]}`)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if _, ok := submits()[0].Body["requirements"]; ok {
			t.Error("all-zero placeholder requirements should be removed")
		}

		rec = env.request(t, http.MethodPost, launchPath, token,
			`{"name":"r2","requirements":[{"step_number":0,"min_cpu_cores":4,"max_cpu_cores":0,"min_memory_limit":0}]}`)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if _, ok := submits()[1].Body["requirements"]; !ok {
			t.Error("real requirements should be preserved")
		}
	})

	t.Run("response fallbacks and url from subdomain", func(t *testing.T) {
		env := newTestEnv(t, func(r *http.Request) (int, any) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/analyses":
				return 200, map[string]any{"id": testAnalysisID}
			case strings.HasSuffix(r.URL.Path, "/external-id"):
				return 200, map[string]any{"externalID": "ext-1"}
			case r.URL.Path == "/vice/async-data":
				return 200, map[string]any{"subdomain": "d4e5f6"}
			default:
				return 500, nil
			}
		})
		rec := env.request(t, http.MethodPost, launchPath, env.userToken(t, nil), `{"name":"my-run"}`)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}

		body := decodeBody(t, rec)
		if body["name"] != "my-run" {
			t.Errorf("name = %v, want submission fallback when upstream omits it", body["name"])
		}
		if body["status"] != "Submitted" {
			t.Errorf("status = %v, want Submitted fallback", body["status"])
		}
		if body["url"] != "https://d4e5f6.vice.invalid" {
			t.Errorf("url = %v", body["url"])
		}
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rec := env.request(t, http.MethodPost, launchPath, env.userToken(t, nil), `{not json`)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Invalid JSON in request body" {
			t.Errorf("detail = %v", body["detail"])
		}
	})

	t.Run("bad app UUID", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rec := env.request(t, http.MethodPost, "/app/launch/de/not-a-uuid", env.userToken(t, nil), "")
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Invalid app ID format" {
			t.Errorf("detail = %v", body["detail"])
		}
	})
}
