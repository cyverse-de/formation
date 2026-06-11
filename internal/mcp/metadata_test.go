package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/handlers"
)

func newMetadataEnv(t *testing.T) (*echo.Echo, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		KeycloakServerURL: "https://kc.example.org/auth/",
		KeycloakRealm:     "de",
		PathPrefix:        "/formation",
		MCPClientID:       "formation-mcp",
		PublicBaseURL:     "https://de.example.org/formation",
		MCPScopes:         "openid profile email",
	}

	e := echo.New()
	e.Pre(handlers.StripPathPrefix(cfg.PathPrefix))
	RegisterWellKnown(e, cfg)
	return e, cfg
}

func getJSON(t *testing.T, e *echo.Echo, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %s response %q: %v", path, rec.Body.String(), err)
		}
	}
	return rec.Code, body
}

func TestProtectedResourceMetadata(t *testing.T) {
	e, _ := newMetadataEnv(t)

	// Every discovery form an MCP client might construct must serve the doc.
	paths := []string{
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-protected-resource",
		"/formation/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-protected-resource/formation/mcp",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			code, body := getJSON(t, e, path)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if body["resource"] != "https://de.example.org/formation/mcp" {
				t.Errorf("resource = %v", body["resource"])
			}
			servers, _ := body["authorization_servers"].([]any)
			if len(servers) != 1 || servers[0] != "https://de.example.org/formation" {
				t.Errorf("authorization_servers = %v", body["authorization_servers"])
			}
			scopes, _ := body["scopes_supported"].([]any)
			if len(scopes) != 3 || scopes[0] != "openid" {
				t.Errorf("scopes_supported = %v", body["scopes_supported"])
			}
		})
	}
}

func TestAuthServerMetadataFacade(t *testing.T) {
	e, _ := newMetadataEnv(t)

	paths := []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-authorization-server/formation",
		"/.well-known/openid-configuration",
		"/.well-known/openid-configuration/formation",
		"/formation/.well-known/openid-configuration",
		"/formation/.well-known/oauth-authorization-server",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			code, body := getJSON(t, e, path)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}

			realm := "https://kc.example.org/auth/realms/de"
			want := map[string]string{
				"issuer":                 "https://de.example.org/formation",
				"authorization_endpoint": realm + "/protocol/openid-connect/auth",
				"token_endpoint":         realm + "/protocol/openid-connect/token",
				"jwks_uri":               realm + "/protocol/openid-connect/certs",
				"registration_endpoint":  "https://de.example.org/formation/oauth/register",
			}
			for field, value := range want {
				if body[field] != value {
					t.Errorf("%s = %v, want %q", field, body[field], value)
				}
			}
			challenges, _ := body["code_challenge_methods_supported"].([]any)
			if len(challenges) != 1 || challenges[0] != "S256" {
				t.Errorf("code_challenge_methods_supported = %v", body["code_challenge_methods_supported"])
			}
			authMethods, _ := body["token_endpoint_auth_methods_supported"].([]any)
			if len(authMethods) != 1 || authMethods[0] != "none" {
				t.Errorf("token_endpoint_auth_methods_supported = %v", body["token_endpoint_auth_methods_supported"])
			}
		})
	}
}

func TestRegistrationShim(t *testing.T) {
	e, cfg := newMetadataEnv(t)

	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
		check    func(t *testing.T, body map[string]any)
	}{
		{
			name:     "returns the shared client id and echoes redirect URIs",
			path:     "/oauth/register",
			body:     `{"client_name": "Claude", "redirect_uris": ["https://claude.ai/api/mcp/auth_callback"]}`,
			wantCode: http.StatusCreated,
			check: func(t *testing.T, body map[string]any) {
				if body["client_id"] != cfg.MCPClientID {
					t.Errorf("client_id = %v, want %q", body["client_id"], cfg.MCPClientID)
				}
				uris, _ := body["redirect_uris"].([]any)
				if len(uris) != 1 || uris[0] != "https://claude.ai/api/mcp/auth_callback" {
					t.Errorf("redirect_uris = %v", body["redirect_uris"])
				}
				if body["token_endpoint_auth_method"] != "none" {
					t.Errorf("token_endpoint_auth_method = %v", body["token_endpoint_auth_method"])
				}
				if body["client_name"] != "Claude" {
					t.Errorf("client_name = %v", body["client_name"])
				}
				if _, ok := body["client_secret"]; ok {
					t.Error("client_secret must not be issued for a public client")
				}
			},
		},
		{
			name:     "prefixed path works",
			path:     "/formation/oauth/register",
			body:     `{"redirect_uris": ["http://localhost:33418/callback"]}`,
			wantCode: http.StatusCreated,
			check: func(t *testing.T, body map[string]any) {
				if body["client_id"] != cfg.MCPClientID {
					t.Errorf("client_id = %v", body["client_id"])
				}
				if _, ok := body["client_name"]; ok {
					t.Error("client_name should be omitted when not sent")
				}
			},
		},
		{
			name:     "malformed body is invalid_client_metadata",
			path:     "/oauth/register",
			body:     "{not json",
			wantCode: http.StatusBadRequest,
			check: func(t *testing.T, body map[string]any) {
				if body["error"] != "invalid_client_metadata" {
					t.Errorf("error = %v", body["error"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
			}
			tt.check(t, body)
		})
	}
}
