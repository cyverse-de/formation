package mcp

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/datastore"
	"github.com/cyverse-de/formation/internal/datastore/datastoretest"
	"github.com/cyverse-de/formation/internal/handlers"
	"github.com/cyverse-de/formation/internal/vice"
)

const (
	testAppID      = "0123abcd-0000-4000-8000-00000000beef"
	testAnalysisID = "9876fedc-0000-4000-8000-00000000cafe"
)

// upstreamCall records one request received by a service stub.
type upstreamCall struct {
	method string
	path   string
	body   map[string]any
}

// recordingStub captures every request and serves responses from respond.
func recordingStub(calls *[]upstreamCall, respond func(r *http.Request) (int, any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := upstreamCall{method: r.Method, path: r.URL.Path}
		_ = json.NewDecoder(r.Body).Decode(&call.body)
		*calls = append(*calls, call)

		status, payload := respond(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if payload != nil {
			_ = json.NewEncoder(w).Encode(payload)
		}
	})
}

// env runs the full /mcp stack: fake Keycloak, stubbed apps and app-exposer
// services, an in-memory data store, and the prefix-stripping middleware,
// mirroring buildServer.
type env struct {
	serverURL string
	kc        *authtest.Keycloak
	store     *datastoretest.Store
	cfg       *config.Config
}

func newEnv(t *testing.T, appsHandler, exposerHandler http.Handler) *env {
	t.Helper()

	if appsHandler == nil {
		appsHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	}
	if exposerHandler == nil {
		exposerHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	}
	appsServer := httptest.NewServer(appsHandler)
	t.Cleanup(appsServer.Close)
	exposerServer := httptest.NewServer(exposerHandler)
	t.Cleanup(exposerServer.Close)

	appsClient, err := clients.NewApps(appsServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	exposerClient, err := clients.NewAppExposer(exposerServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), kc.Realm, true)

	cfg := &config.Config{
		KeycloakServerURL:       kc.ServerURL() + "/",
		KeycloakRealm:           kc.Realm,
		UserSuffix:              "@iplantcollaborative.org",
		ViceDomain:              ".vice.invalid",
		OutputZone:              "iplant",
		PathPrefix:              "/formation",
		ServiceAccountUsernames: map[string]string{auth.AppRunnerRole: "svc"},
		MCPEnabled:              true,
		MCPClientID:             "formation-mcp",
		PublicBaseURL:           "https://de.example.org/formation",
		MCPScopes:               config.DefaultMCPScopes,
		MCPLaunchMaxWait:        config.DefaultMCPLaunchMaxWait,
		MCPBrowseByteLimit:      config.DefaultMCPBrowseByteLimit,
	}

	apps := handlers.NewApps(
		appsClient, exposerClient,
		vice.NewURLChecker(200*time.Millisecond, 1, time.Minute),
		vice.NewSubdomainResolverWithRetries(exposerClient, 1, time.Millisecond),
		cfg,
	)
	store := datastoretest.New("alice")
	data := handlers.NewData(store)

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.Pre(handlers.StripPathPrefix(cfg.PathPrefix))
	e.Any("/mcp", echo.WrapHandler(Handler(Deps{Apps: apps, Data: data, Cfg: cfg}, verifier)))
	RegisterWellKnown(e, cfg)

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	return &env{serverURL: srv.URL, kc: kc, store: store, cfg: cfg}
}

// authTransport adds the bearer token to every outgoing request.
type authTransport struct {
	token string
}

func (a *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(cloned)
}

// connect opens an MCP session as a user with the given extra JWT claims.
func (e *env) connect(t *testing.T, extraClaims map[string]any) *sdk.ClientSession {
	t.Helper()

	claims := map[string]any{"preferred_username": "alice"}
	maps.Copy(claims, extraClaims)

	client := sdk.NewClient(&sdk.Implementation{Name: "formation-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint:             e.serverURL + "/mcp",
		HTTPClient:           &http.Client{Transport: &authTransport{token: e.kc.Token(t, claims)}, Timeout: 30 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	return result
}

func textContent(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *TextContent", result.Content[0])
	}
	return text.Text
}

func TestMCPRequiresBearerToken(t *testing.T) {
	env := newEnv(t, nil, nil)

	tests := []struct {
		name string
		path string
	}{
		{"bare path", "/mcp"},
		{"prefixed path", "/formation/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, env.serverURL+tt.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			authenticate := resp.Header.Get("WWW-Authenticate")
			wantMetadata := `resource_metadata="https://de.example.org/formation/.well-known/oauth-protected-resource/mcp"`
			if !strings.Contains(authenticate, wantMetadata) {
				t.Errorf("WWW-Authenticate = %q, want containing %q", authenticate, wantMetadata)
			}
		})
	}
}

func TestMCPListTools(t *testing.T) {
	env := newEnv(t, nil, nil)
	session := env.connect(t, nil)

	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)

	want := slices.Clone(ToolNames)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

func TestMCPListApps(t *testing.T) {
	appsStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user") != "alice" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 2,
			"apps": []map[string]any{
				{"id": testAppID, "name": "JupyterLab", "system_id": "de", "description": "notebooks",
					"integrator_name": "bob@iplantcollaborative.org"},
				{"id": testAppID, "name": "RStudio", "system_id": "de"},
			},
		})
	})

	env := newEnv(t, appsStub, nil)
	session := env.connect(t, nil)

	result := callTool(t, session, "list_apps", map[string]any{"name": "lab"})
	if result.IsError {
		t.Fatalf("IsError = true: %s", textContent(t, result))
	}

	text := textContent(t, result)
	for _, want := range []string{"Found 2 apps:", "**JupyterLab**", "ID: `" + testAppID + "`", "Integrator: bob", "Description: notebooks"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

func TestMCPServiceAccountPolicy(t *testing.T) {
	env := newEnv(t, nil, nil)

	t.Run("service account without app-runner role is rejected on apps tools", func(t *testing.T) {
		session := env.connect(t, map[string]any{"preferred_username": "service-account-ci"})
		result := callTool(t, session, "list_running_analyses", nil)
		if !result.IsError {
			t.Fatal("IsError = false, want rejection")
		}
		text := textContent(t, result)
		want := `Error executing list_running_analyses: service account missing required role: "app-runner"`
		if text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})

	t.Run("service account with role uses the mapped backend username", func(t *testing.T) {
		var calls []upstreamCall
		appsStub := recordingStub(&calls, func(r *http.Request) (int, any) {
			if r.URL.Query().Get("user") != "svc" {
				return 500, nil
			}
			return 200, map[string]any{"analyses": []map[string]any{}}
		})
		saEnv := newEnv(t, appsStub, nil)

		session := saEnv.connect(t, map[string]any{
			"preferred_username": "service-account-ci",
			"realm_access":       map[string]any{"roles": []string{auth.AppRunnerRole}},
		})
		result := callTool(t, session, "list_running_analyses", nil)
		if result.IsError {
			t.Fatalf("IsError = true: %s", textContent(t, result))
		}
		if text := textContent(t, result); text != "No running analyses found" {
			t.Errorf("text = %q", text)
		}
	})
}

func TestMCPLaunchAppAndWait(t *testing.T) {
	appResponse := map[string]any{
		"overall_job_type": "Interactive",
		"groups": []map[string]any{{
			"parameters": []map[string]any{
				{"id": "param-1", "name": "Input file", "required": true, "type": "FileInput", "description": "data to process"},
				{"id": "param-2", "name": "Hidden", "required": true, "isVisible": false},
			},
		}},
	}

	t.Run("missing required parameters returns the template without launching", func(t *testing.T) {
		var calls []upstreamCall
		appsStub := recordingStub(&calls, func(r *http.Request) (int, any) {
			if strings.HasPrefix(r.URL.Path, "/apps/") {
				return 200, appResponse
			}
			return 500, nil
		})

		env := newEnv(t, appsStub, nil)
		session := env.connect(t, nil)

		result := callTool(t, session, "launch_app_and_wait", map[string]any{"app_id": testAppID})
		if result.IsError {
			t.Fatalf("IsError = true: %s", textContent(t, result))
		}

		text := textContent(t, result)
		for _, want := range []string{"requires additional parameters", "**Input file** (`param-1`)", `"param-1": "value",`} {
			if !strings.Contains(text, want) {
				t.Errorf("text missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "param-2") {
			t.Errorf("invisible parameter should not be reported:\n%s", text)
		}
		for _, call := range calls {
			if call.method == http.MethodPost {
				t.Errorf("unexpected launch POST to %s", call.path)
			}
		}
	})

	t.Run("batch job returns immediately after submission", func(t *testing.T) {
		var calls []upstreamCall
		appsStub := recordingStub(&calls, func(r *http.Request) (int, any) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/apps/"):
				return 200, map[string]any{"overall_job_type": "DE", "groups": []any{}}
			case r.URL.Path == "/analyses" && r.Method == http.MethodPost:
				return 200, map[string]any{"id": testAnalysisID, "name": "run", "status": "Submitted"}
			default:
				return 500, nil
			}
		})

		env := newEnv(t, appsStub, nil)
		session := env.connect(t, nil)

		result := callTool(t, session, "launch_app_and_wait", map[string]any{"app_id": testAppID, "name": "run"})
		if result.IsError {
			t.Fatalf("IsError = true: %s", textContent(t, result))
		}

		text := textContent(t, result)
		for _, want := range []string{"Analysis launched successfully!", "`" + testAnalysisID + "`", "**Job Type:** DE", "Batch job submitted"} {
			if !strings.Contains(text, want) {
				t.Errorf("text missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("launch with a VICE URL polls even without an overall_job_type", func(t *testing.T) {
		restore := launchPollInterval
		launchPollInterval = 10 * time.Millisecond
		defer func() { launchPollInterval = restore }()

		appsStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasPrefix(r.URL.Path, "/apps/"):
				// App metadata without overall_job_type, like some VICE apps.
				_ = json.NewEncoder(w).Encode(map[string]any{"groups": []any{}})
			case r.URL.Path == "/analyses" && r.Method == http.MethodPost:
				_ = json.NewEncoder(w).Encode(map[string]any{"id": testAnalysisID, "status": "Submitted"})
			case r.URL.Path == "/analyses":
				_ = json.NewEncoder(w).Encode(map[string]any{"analyses": []map[string]any{{"id": testAnalysisID, "status": "Running"}}})
			default:
				w.WriteHeader(500)
			}
		})
		exposerStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/external-id"):
				_ = json.NewEncoder(w).Encode(map[string]any{"external_id": "ext-1"})
			case r.URL.Path == "/vice/async-data":
				_ = json.NewEncoder(w).Encode(map[string]any{"subdomain": "a1b2c3"})
			default:
				w.WriteHeader(500)
			}
		})

		env := newEnv(t, appsStub, exposerStub)
		session := env.connect(t, nil)

		result := callTool(t, session, "launch_app_and_wait", map[string]any{"app_id": testAppID, "max_wait": 1})
		if !result.IsError {
			t.Fatalf("IsError = false, want poll timeout (batch path taken?): %s", textContent(t, result))
		}
		text := textContent(t, result)
		if strings.Contains(text, "Batch job submitted") {
			t.Fatalf("took the batch path despite a VICE URL:\n%s", text)
		}
		if !strings.Contains(text, "not ready after") {
			t.Errorf("text = %q, want poll timeout", text)
		}
	})

	t.Run("interactive launch times out when the URL never becomes ready", func(t *testing.T) {
		restore := launchPollInterval
		launchPollInterval = 10 * time.Millisecond
		defer func() { launchPollInterval = restore }()

		appsStub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/analyses" && r.Method == http.MethodPost:
				_ = json.NewEncoder(w).Encode(map[string]any{"id": testAnalysisID, "status": "Submitted"})
			case r.URL.Path == "/analyses":
				_ = json.NewEncoder(w).Encode(map[string]any{"analyses": []map[string]any{{"id": testAnalysisID, "status": "Running"}}})
			default:
				w.WriteHeader(500)
			}
		})

		env := newEnv(t, appsStub, nil)
		session := env.connect(t, nil)

		// overall_job_type skips the parameter fetch, like the Python client.
		result := callTool(t, session, "launch_app_and_wait", map[string]any{
			"app_id": testAppID, "overall_job_type": "Interactive", "max_wait": 1,
		})
		if !result.IsError {
			t.Fatalf("IsError = false, want timeout error: %s", textContent(t, result))
		}
		text := textContent(t, result)
		want := "Error executing launch_app_and_wait: Analysis " + testAnalysisID + " not ready after"
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want containing %q", text, want)
		}
	})
}

func TestMCPDataTools(t *testing.T) {
	t.Run("browse directory", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Dirs["/iplant/home/alice"] = []datastore.Entry{
			{Name: "subdir", Type: datastore.TypeCollection},
			{Name: "notes.txt", Type: datastore.TypeDataObject},
		}

		session := env.connect(t, nil)
		result := callTool(t, session, "browse_data", map[string]any{"path": "/iplant/home/alice"})
		if result.IsError {
			t.Fatalf("IsError = true: %s", textContent(t, result))
		}
		text := textContent(t, result)
		for _, want := range []string{"**Directory:** `/iplant/home/alice`", "**Directories:**", "- subdir", "**Files:**", "- notes.txt"} {
			if !strings.Contains(text, want) {
				t.Errorf("text missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("browse file with metadata and truncation", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Files["/iplant/file.txt"] = []byte("0123456789")
		env.store.Meta["/iplant/file.txt"] = []datastore.AVU{{Attribute: "weight", Value: "12", Units: "kg"}}

		session := env.connect(t, nil)
		result := callTool(t, session, "browse_data", map[string]any{
			"path": "/iplant/file.txt", "limit": 4, "include_metadata": true,
		})
		text := textContent(t, result)
		for _, want := range []string{"**File Content:**", "```\n0123\n```", "*(content truncated", "- weight: 12,kg"} {
			if !strings.Contains(text, want) {
				t.Errorf("text missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("upload create and metadata conversion", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Dirs["/iplant/home/alice"] = nil

		session := env.connect(t, nil)
		result := callTool(t, session, "upload_file", map[string]any{
			"path":     "/iplant/home/alice/new.txt",
			"content":  "hello world",
			"metadata": map[string]any{"Author": "alice", "Weight": "12,kg"},
		})
		if text := textContent(t, result); text != "File created: `/iplant/home/alice/new.txt`" {
			t.Errorf("text = %q", text)
		}
		if got := string(env.store.Uploads["/iplant/home/alice/new.txt"]); got != "hello world" {
			t.Errorf("uploaded content = %q", got)
		}

		// Attributes lowercased and units split on comma, like the REST headers.
		wantAVUs := []datastore.AVU{{Attribute: "author", Value: "alice"}, {Attribute: "weight", Value: "12", Units: "kg"}}
		if len(env.store.SetMetaCalls) != 1 || !slices.Equal(env.store.SetMetaCalls[0].AVUs, wantAVUs) {
			t.Errorf("SetMetaCalls = %+v", env.store.SetMetaCalls)
		}
	})

	t.Run("create directory", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Dirs["/iplant/home/alice"] = nil

		session := env.connect(t, nil)
		result := callTool(t, session, "create_directory", map[string]any{"path": "/iplant/home/alice/newdir"})
		if text := textContent(t, result); text != "Directory created: `/iplant/home/alice/newdir`" {
			t.Errorf("text = %q", text)
		}
		if !slices.Contains(env.store.CreatedDirs, "/iplant/home/alice/newdir") {
			t.Errorf("CreatedDirs = %v", env.store.CreatedDirs)
		}
	})

	t.Run("set metadata replace", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Files["/iplant/file.txt"] = []byte("x")

		session := env.connect(t, nil)
		result := callTool(t, session, "set_metadata", map[string]any{
			"path": "/iplant/file.txt", "metadata": map[string]any{"author": "bob"}, "replace": true,
		})
		if text := textContent(t, result); text != "Metadata replaced for: `/iplant/file.txt`" {
			t.Errorf("text = %q", text)
		}
		if len(env.store.SetMetaCalls) != 1 || !env.store.SetMetaCalls[0].Replace {
			t.Errorf("SetMetaCalls = %+v", env.store.SetMetaCalls)
		}
	})

	t.Run("delete dry run and real delete", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Dirs["/iplant/full"] = []datastore.Entry{{Name: "child.txt", Type: datastore.TypeDataObject}}

		session := env.connect(t, nil)
		result := callTool(t, session, "delete_data", map[string]any{
			"path": "/iplant/full", "recurse": true, "dry_run": true,
		})
		if text := textContent(t, result); text != "Dry-run: Would delete `/iplant/full` (1 items)" {
			t.Errorf("text = %q", text)
		}
		if len(env.store.DeletedDirs) != 0 {
			t.Errorf("DeletedDirs = %v, want none after dry run", env.store.DeletedDirs)
		}

		result = callTool(t, session, "delete_data", map[string]any{"path": "/iplant/full", "recurse": true})
		if text := textContent(t, result); text != "Deleted (recursive): `/iplant/full` (1 items)" {
			t.Errorf("text = %q", text)
		}
		if len(env.store.DeletedDirs) != 1 {
			t.Errorf("DeletedDirs = %v", env.store.DeletedDirs)
		}
	})

	t.Run("permission errors surface as tool errors", func(t *testing.T) {
		env := newEnv(t, nil, nil)
		env.store.Files["/iplant/secret.txt"] = []byte("x")
		env.store.DenyRead = []string{"/iplant/secret.txt"}

		session := env.connect(t, nil)
		result := callTool(t, session, "browse_data", map[string]any{"path": "/iplant/secret.txt"})
		if !result.IsError {
			t.Fatal("IsError = false, want permission error")
		}
		if text := textContent(t, result); text != "Error executing browse_data: Access denied" {
			t.Errorf("text = %q", text)
		}
	})
}

func TestMCPStopAnalysis(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		wantPath string
		wantText string
	}{
		{
			name:     "default saves outputs",
			args:     map[string]any{"analysis_id": testAnalysisID},
			wantPath: "/vice/admin/analyses/" + testAnalysisID + "/save-and-exit",
			wantText: "Analysis stopped with saving outputs",
		},
		{
			name:     "save_outputs false exits without saving",
			args:     map[string]any{"analysis_id": testAnalysisID, "save_outputs": false},
			wantPath: "/vice/admin/analyses/" + testAnalysisID + "/exit",
			wantText: "Analysis stopped without saving outputs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []upstreamCall
			exposerStub := recordingStub(&calls, func(r *http.Request) (int, any) {
				return 200, map[string]any{"result": "ok"}
			})

			env := newEnv(t, nil, exposerStub)
			session := env.connect(t, nil)

			result := callTool(t, session, "stop_analysis", tt.args)
			if result.IsError {
				t.Fatalf("IsError = true: %s", textContent(t, result))
			}
			if text := textContent(t, result); text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if len(calls) != 1 || calls[0].path != tt.wantPath {
				t.Errorf("exposer calls = %+v, want one POST to %s", calls, tt.wantPath)
			}
		})
	}
}
