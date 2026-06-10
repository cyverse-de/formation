//go:build parity

// Package parity runs the original Python service and the Go rewrite side by
// side against shared mock backends (fake Keycloak, apps, app-exposer) and
// diffs their responses over a request corpus.
//
// Run with: go test -tags parity -v -timeout 10m ./internal/parity/
// Requires uv and the Python sources still present in the repo root.
// The /data endpoints need a real iRODS and are not covered here.
package parity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cyverse-de/formation/internal/authtest"
)

const (
	appID      = "0aaaaaaa-2222-4222-8222-222222222222"
	analysisID = "aaaaaaaa-1111-4111-8111-111111111111" // Running, has a VICE subdomain
	launchID   = "11111111-2222-4333-8444-555555555555" // returned by POST /analyses
)

// mockApps serves the subset of the apps service the corpus exercises.
func mockApps(t *testing.T) *httptest.Server {
	apps := makeAppsCorpus()

	appDetails := map[string]any{
		"id": appID, "name": "JupyterLab 4.3", "description": "interactive notebooks",
		"overall_job_type": "Interactive",
		"groups":           []any{map[string]any{"id": "group-1", "label": "Parameters", "parameters": []any{}}},
	}

	analyses := []map[string]any{
		{
			"id": analysisID, "name": "vice-run", "app_id": appID, "system_id": "de",
			"status": "Running", "startdate": "1748000000000", "username": "alice@iplantcollaborative.org",
		},
		{
			"id": "bbbbbbbb-1111-4111-8111-111111111111", "name": "done-run", "app_id": appID,
			"system_id": "de", "status": "Completed", "startdate": "1747000000000",
		},
		{
			"id": "cccccccc-1111-4111-8111-111111111111", "name": "batch-run", "app_id": appID,
			"system_id": "de", "status": "Running", "startdate": "1746000000000",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /apps", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		limit, _ := strconv.Atoi(query.Get("limit"))
		offset, _ := strconv.Atoi(query.Get("offset"))
		search := strings.ToLower(query.Get("search"))

		matched := make([]map[string]any, 0, len(apps))
		for _, app := range apps {
			name, _ := app["name"].(string)
			if search == "" || strings.Contains(strings.ToLower(name), search) {
				matched = append(matched, app)
			}
		}
		total := len(matched)
		if offset > len(matched) {
			offset = len(matched)
		}
		end := min(offset+limit, len(matched))
		writeJSON(w, map[string]any{"total": total, "apps": matched[offset:end]})
	})
	mux.HandleFunc("GET /apps/de/"+appID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, appDetails)
	})
	mux.HandleFunc("GET /apps/de/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"reason":"app not found"}`))
	})
	mux.HandleFunc("GET /analyses", func(w http.ResponseWriter, r *http.Request) {
		var filters []map[string]string
		if raw := r.URL.Query().Get("filter"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &filters); err != nil {
				w.WriteHeader(400)
				return
			}
		}
		matched := make([]map[string]any, 0, len(analyses))
		for _, analysis := range analyses {
			ok := true
			for _, filter := range filters {
				if analysis[filter["field"]] != filter["value"] {
					ok = false
				}
			}
			if ok {
				matched = append(matched, analysis)
			}
		}
		writeJSON(w, map[string]any{"analyses": matched, "total": len(matched)})
	})
	mux.HandleFunc("POST /analyses", func(w http.ResponseWriter, r *http.Request) {
		var submission map[string]any
		_ = json.NewDecoder(r.Body).Decode(&submission)
		writeJSON(w, map[string]any{"id": launchID, "name": submission["name"], "status": "Submitted"})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func makeAppsCorpus() []map[string]any {
	jobTypes := []string{"Interactive", "DE", "OSG", "Tapis"}
	apps := make([]map[string]any, 0, 12)
	for i := range 12 {
		name := fmt.Sprintf("App %02d", i)
		description := "batch tool"
		if i%4 == 0 {
			name = fmt.Sprintf("JupyterLab %02d", i)
			description = "interactive notebook environment"
		}
		integrator := "bob@iplantcollaborative.org"
		if i%3 == 0 {
			integrator = "alice@iplantcollaborative.org"
		}
		apps = append(apps, map[string]any{
			"id":               fmt.Sprintf("a%07d-1111-4111-8111-111111111111", i),
			"name":             name,
			"description":      description,
			"version":          "1.0",
			"integrator_name":  integrator,
			"integration_date": fmt.Sprintf("2025-0%d-15T00:00:00Z", i%9+1),
			"edited_date":      fmt.Sprintf("2025-0%d-20T12:30:00Z", i%9+1),
			"system_id":        "de",
			"overall_job_type": jobTypes[i%4],
			"wiki_url":         "https://wiki.example/app", // dropped by formatting
		})
	}
	return apps
}

// mockExposer serves the app-exposer VICE admin endpoints.
func mockExposer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vice/admin/analyses/{id}/time-limit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"time_limit": "2026-06-10T20:00:00Z"})
	})
	mux.HandleFunc("POST /vice/admin/analyses/{id}/save-and-exit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /vice/admin/analyses/{id}/exit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("GET /vice/admin/analyses/{id}/external-id", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == analysisID {
			writeJSON(w, map[string]any{"external_id": "ext-1"})
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"reason":"no external id"}`))
	})
	mux.HandleFunc("GET /vice/async-data", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("external-id") == "ext-1" {
			writeJSON(w, map[string]any{"subdomain": "a1b2c3"})
			return
		}
		w.WriteHeader(404)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// loginTokenHandler dispatches the fake Keycloak password grant on username.
func loginTokenHandler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	switch r.PostFormValue("username") {
	case "good":
		writeJSON(w, map[string]any{
			"access_token": "tok-abc", "refresh_token": "ref-xyz",
			"expires_in": 300, "token_type": "Bearer",
		})
	case "badpass":
		w.WriteHeader(401)
		writeJSON(w, map[string]any{"error": "invalid_grant"})
	default:
		w.WriteHeader(500)
		writeJSON(w, map[string]any{"error": "server_error"})
	}
}

func writeConfig(t *testing.T, dir, keycloakURL, appsURL, exposerURL string) string {
	config := map[string]any{
		"irods": map[string]any{
			"host": "127.0.0.1", "port": "1", "user": "rods", "password": "rods", "zone": "iplant",
		},
		"keycloak": map[string]any{
			"server_url": keycloakURL, "realm": "de",
			"client_id": "formation", "client_secret": "secret", "ssl_verify": false,
		},
		"services": map[string]any{
			"apps_base_url": appsURL, "app_exposer_base_url": exposerURL,
		},
		"application": map[string]any{
			"user_suffix": "@iplantcollaborative.org", "vice_domain": ".parity.invalid",
			"vice_url_check_timeout": 1.0, "vice_url_check_retries": 1, "vice_url_check_cache_ttl": 5.0,
			"service_account_usernames": map[string]any{"app-runner": "svcacct1"},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func freePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func repoRoot(t *testing.T) string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// scrubbedEnviron strips formation's config environment variables so a
// developer's direnv/.env settings (real Keycloak, real iRODS, ...) can't
// override the harness config file: env takes precedence over JSON in both
// implementations.
func scrubbedEnviron() []string {
	prefixes := []string{
		"IRODS_", "KEYCLOAK_", "VICE_", "APPS_BASE_URL=", "APP_EXPOSER_BASE_URL=",
		"PERMISSIONS_BASE_URL=", "USER_SUFFIX=", "PATH_PREFIX=", "OUTPUT_ZONE=",
		"SERVICE_ACCOUNTS_ONLY=", "SERVICE_ACCOUNT_USERNAMES=", "CONFIG_FILE=",
	}
	environ := os.Environ()
	scrubbed := make([]string, 0, len(environ))
	for _, entry := range environ {
		drop := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			scrubbed = append(scrubbed, entry)
		}
	}
	return scrubbed
}

// startServer launches a server process and waits until GET / responds.
func startServer(t *testing.T, name string, port int, configPath string, command string, args ...string) string {
	t.Helper()

	cmd := exec.Command(command, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(scrubbedEnviron(), "CONFIG_FILE="+configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logFile, err := os.Create(filepath.Join(t.TempDir(), name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	logData, _ := os.ReadFile(logFile.Name())
	t.Fatalf("%s did not become ready; log:\n%s", name, logData)
	return ""
}

var (
	timestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}-\d{6}`)
	normalizedDetail = []string{"Token validation failed:", "Authentication error:"}
)

// normalize makes both servers' JSON comparable: generated analysis-name
// timestamps, library-specific auth error text, and VICE probe details vary
// run to run and implementation to implementation.
func normalize(v any) any {
	switch value := v.(type) {
	case map[string]any:
		for key, item := range value {
			if key == "url_check_details" {
				value[key] = "<url_check_details>"
				continue
			}
			value[key] = normalize(item)
		}
		if detail, ok := value["detail"].(string); ok {
			for _, prefix := range normalizedDetail {
				if strings.HasPrefix(detail, prefix) {
					value["detail"] = prefix + " <normalized>"
				}
			}
		}
		return value
	case []any:
		for i, item := range value {
			value[i] = normalize(item)
		}
		return value
	case string:
		return timestampPattern.ReplaceAllString(value, "<timestamp>")
	default:
		return v
	}
}

type request struct {
	name   string
	method string
	target string // path + query
	auth   string // Authorization header value ("" = none)
	body   string

	// When set, statuses are asserted per server and bodies are not diffed —
	// used for the documented acceptable deltas.
	wantPythonStatus int
	wantGoStatus     int
}

func send(t *testing.T, baseURL string, req request) (int, []byte) {
	t.Helper()
	httpReq, err := http.NewRequest(req.method, baseURL+req.target, strings.NewReader(req.body))
	if err != nil {
		t.Fatal(err)
	}
	if req.auth != "" {
		httpReq.Header.Set("Authorization", req.auth)
	}
	if req.body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func normalizedJSON(t *testing.T, data []byte) (any, bool) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, false
	}
	return normalize(decoded), true
}

func TestParity(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed; skipping Python/Go parity run")
	}

	keycloak := authtest.New(t, "de")
	keycloak.TokenHandler = loginTokenHandler
	apps := mockApps(t)
	exposer := mockExposer(t)

	configPath := writeConfig(t, t.TempDir(), keycloak.ServerURL(), apps.URL, exposer.URL)

	binary := filepath.Join(t.TempDir(), "formation")
	build := exec.Command("go", "build", "-o", binary, "./cmd/formation")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go server: %v\n%s", err, out)
	}

	pythonPort := freePort(t)
	goPort := freePort(t)
	pythonURL := startServer(t, "python", pythonPort, configPath,
		"uv", "run", "fastapi", "run", "main.py", "--port", strconv.Itoa(pythonPort))
	goURL := startServer(t, "go", goPort, configPath,
		binary, "--listen-port", strconv.Itoa(goPort))

	bearer := func(claims map[string]any) string {
		return "Bearer " + keycloak.Token(t, claims)
	}
	userAuth := bearer(map[string]any{
		"sub": "sub-alice", "preferred_username": "alice",
		"email": "alice@university.edu", "name": "Alice Smith",
	})
	subOnlyAuth := bearer(map[string]any{"sub": "sub-only-user"})
	saAuth := bearer(map[string]any{
		"sub": "sub-sa", "preferred_username": "service-account-de",
		"realm_access": map[string]any{"roles": []string{"app-runner"}},
	})
	saNoRoleAuth := bearer(map[string]any{
		"sub": "sub-sa2", "preferred_username": "service-account-other",
		"realm_access": map[string]any{"roles": []string{}},
	})
	expiredAuth := bearer(map[string]any{
		"sub": "sub-alice", "preferred_username": "alice",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	badIssuerAuth := bearer(map[string]any{
		"sub": "sub-alice", "preferred_username": "alice", "iss": "https://evil.example/realms/de",
	})
	basic := func(credentials string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
	}

	statusPath := "/apps/analyses/" + analysisID + "/status"
	controlPath := "/apps/analyses/" + analysisID + "/control"
	detailsPath := "/apps/analyses/" + analysisID + "/details"
	missingAnalysis := "dddddddd-1111-4111-8111-111111111111"

	corpus := []request{
		{name: "root", method: "GET", target: "/"},

		// Auth edges
		{name: "user info", method: "GET", target: "/user", auth: userAuth},
		{name: "user info sub only", method: "GET", target: "/user", auth: subOnlyAuth},
		{name: "user missing auth", method: "GET", target: "/user"},
		{name: "user wrong scheme", method: "GET", target: "/user", auth: "Digest abc"},
		{name: "user empty bearer", method: "GET", target: "/user", auth: "Bearer"},
		{name: "user garbage token", method: "GET", target: "/user", auth: "Bearer not.a.jwt"},
		{name: "user expired token", method: "GET", target: "/user", auth: expiredAuth},
		{name: "user bad issuer", method: "GET", target: "/user", auth: badIssuerAuth},

		// Login
		{name: "login success", method: "POST", target: "/login", auth: basic("good:pw")},
		{name: "login bad credentials", method: "POST", target: "/login", auth: basic("badpass:pw")},
		{name: "login keycloak error", method: "POST", target: "/login", auth: basic("boom:pw")},
		{name: "login missing auth", method: "POST", target: "/login"},
		{name: "login non-basic", method: "POST", target: "/login", auth: "Bearer xyz"},
		{name: "login bad base64", method: "POST", target: "/login", auth: "Basic !!!not-base64!!!"},
		{name: "login no colon", method: "POST", target: "/login", auth: basic("nocolon")},

		// Apps listing
		{name: "job types", method: "GET", target: "/apps/job-types", auth: userAuth},
		{name: "apps default", method: "GET", target: "/apps", auth: userAuth},
		{name: "apps paged", method: "GET", target: "/apps?limit=5&offset=2", auth: userAuth},
		{name: "apps name search", method: "GET", target: "/apps?name=jupyter", auth: userAuth},
		{name: "apps job type alias", method: "GET", target: "/apps?job_type=vice", auth: userAuth},
		{name: "apps job type paged", method: "GET", target: "/apps?job_type=VICE&limit=2&offset=1", auth: userAuth},
		{name: "apps description filter", method: "GET", target: "/apps?description=notebook", auth: userAuth},
		{name: "apps integrator filter", method: "GET", target: "/apps?integrator=alice", auth: userAuth},
		{name: "apps date filter", method: "GET", target: "/apps?integration_date=%3E2025-03-01", auth: userAuth},
		{name: "apps combined date filters", method: "GET",
			target: "/apps?integration_date=%3E%3D2025-01-01&edited_date=%3C2025-06-01", auth: userAuth},
		{name: "apps service account", method: "GET", target: "/apps?limit=3", auth: saAuth},
		{name: "apps sa missing role", method: "GET", target: "/apps", auth: saNoRoleAuth},
		{name: "apps limit too small", method: "GET", target: "/apps?limit=0", auth: userAuth},
		{name: "apps limit too large", method: "GET", target: "/apps?limit=1001", auth: userAuth},
		{name: "apps negative offset", method: "GET", target: "/apps?offset=-1", auth: userAuth},

		// Documented deltas: FastAPI 422 pydantic arrays vs Go 400; Python 500
		// on invalid date filter vs Go 400; FastAPI 307 redirect on the bare
		// analyses path vs Go serving it directly.
		{name: "apps non-integer limit (delta)", method: "GET", target: "/apps?limit=abc", auth: userAuth,
			wantPythonStatus: 422, wantGoStatus: 400},
		{name: "apps invalid date filter (delta)", method: "GET", target: "/apps?integration_date=2025-01-01",
			auth: userAuth, wantPythonStatus: 500, wantGoStatus: 400},
		{name: "analyses bare path (delta)", method: "GET", target: "/apps/analyses", auth: userAuth,
			wantPythonStatus: 307, wantGoStatus: 200},

		// Analyses
		{name: "analyses default", method: "GET", target: "/apps/analyses/", auth: userAuth},
		{name: "analyses all statuses", method: "GET", target: "/apps/analyses/?status=", auth: userAuth},
		{name: "analyses completed", method: "GET", target: "/apps/analyses/?status=Completed", auth: userAuth},
		{name: "analysis status", method: "GET", target: statusPath, auth: userAuth},
		{name: "analysis status no subdomain", method: "GET",
			target: "/apps/analyses/cccccccc-1111-4111-8111-111111111111/status", auth: userAuth},
		{name: "analysis status bad uuid", method: "GET", target: "/apps/analyses/nope/status", auth: userAuth},
		{name: "analysis details", method: "GET", target: detailsPath, auth: userAuth},
		{name: "analysis details missing", method: "GET",
			target: "/apps/analyses/" + missingAnalysis + "/details", auth: userAuth},
		{name: "control extend", method: "POST", target: controlPath + "?operation=extend_time", auth: userAuth},
		{name: "control save and exit", method: "POST", target: controlPath + "?operation=save_and_exit", auth: userAuth},
		{name: "control exit", method: "POST", target: controlPath + "?operation=exit", auth: userAuth},
		{name: "control invalid operation", method: "POST", target: controlPath + "?operation=bogus", auth: userAuth},
		{name: "control invalid op and uuid", method: "POST",
			target: "/apps/analyses/nope/control?operation=bogus", auth: userAuth},
		{name: "control bad uuid", method: "POST", target: "/apps/analyses/nope/control?operation=exit", auth: userAuth},

		// Parameters
		{name: "app parameters", method: "GET", target: "/apps/de/" + appID + "/parameters", auth: userAuth},
		{name: "app parameters missing app", method: "GET",
			target: "/apps/de/" + missingAnalysis + "/parameters", auth: userAuth},
		{name: "app parameters bad uuid", method: "GET", target: "/apps/de/nope/parameters", auth: userAuth},

		// Launch
		{name: "launch empty body", method: "POST", target: "/app/launch/de/" + appID, auth: userAuth},
		{name: "launch with body", method: "POST", target: "/app/launch/de/" + appID, auth: userAuth,
			body: `{"name":"my-analysis","email":"body@example.org","debug":true,"config":{"param":"x"}}`},
		{name: "launch placeholder requirements", method: "POST", target: "/app/launch/de/" + appID, auth: userAuth,
			body: `{"name":"req-test","requirements":[{"step_number":0,"min_cpu_cores":0,"max_cpu_cores":0,"min_memory_limit":0}]}`},
		{name: "launch real requirements", method: "POST", target: "/app/launch/de/" + appID, auth: userAuth,
			body: `{"name":"req-real","requirements":[{"step_number":0,"min_cpu_cores":4,"max_cpu_cores":8,"min_memory_limit":2147483648}]}`},
		{name: "launch service account", method: "POST", target: "/app/launch/de/" + appID, auth: saAuth,
			body: `{"name":"sa-run"}`},
		{name: "launch bad uuid", method: "POST", target: "/app/launch/de/nope", auth: userAuth},
	}

	for _, testCase := range corpus {
		t.Run(testCase.name, func(t *testing.T) {
			pythonStatus, pythonBody := send(t, pythonURL, testCase)
			goStatus, goBody := send(t, goURL, testCase)

			if testCase.wantPythonStatus != 0 {
				if pythonStatus != testCase.wantPythonStatus {
					t.Errorf("python status = %d, want %d (body %s)", pythonStatus, testCase.wantPythonStatus, pythonBody)
				}
				if goStatus != testCase.wantGoStatus {
					t.Errorf("go status = %d, want %d (body %s)", goStatus, testCase.wantGoStatus, goBody)
				}
				return
			}

			if pythonStatus != goStatus {
				t.Fatalf("status mismatch: python %d vs go %d\npython: %s\ngo: %s",
					pythonStatus, goStatus, pythonBody, goBody)
			}

			pythonJSON, pythonOK := normalizedJSON(t, pythonBody)
			goJSON, goOK := normalizedJSON(t, goBody)
			if pythonOK != goOK {
				t.Fatalf("one body is not JSON\npython: %s\ngo: %s", pythonBody, goBody)
			}
			if !pythonOK {
				if string(pythonBody) != string(goBody) {
					t.Fatalf("raw body mismatch\npython: %s\ngo: %s", pythonBody, goBody)
				}
				return
			}
			if !reflect.DeepEqual(pythonJSON, goJSON) {
				pretty := func(v any) string {
					out, _ := json.MarshalIndent(v, "", "  ")
					return string(out)
				}
				t.Errorf("body mismatch (status %d)\npython: %s\ngo: %s\nraw python: %s\nraw go: %s",
					pythonStatus, pretty(pythonJSON), pretty(goJSON), pythonBody, goBody)
			}
		})
	}
}
