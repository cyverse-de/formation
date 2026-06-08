package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/config"
)

// keycloakStub serves the password-grant token endpoint for the realm path
// authz.NewKeycloak targets.
func keycloakStub(t *testing.T, status int, body string) (*config.Config, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/cyverse/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "password" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	cfg := &config.Config{
		KeycloakServerURL: srv.URL + "/",
		KeycloakRealm:     "cyverse",
		KeycloakClientID:  "formation",
	}
	return cfg, srv.Close
}

func TestLogin(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		basic      bool
		wantStatus int
	}{
		{"success", http.StatusOK, `{"access_token":"abc"}`, true, http.StatusOK},
		{"invalid credentials", http.StatusUnauthorized, "", true, http.StatusUnauthorized},
		{"upstream error", http.StatusInternalServerError, "", true, http.StatusBadGateway},
		{"missing credentials", http.StatusOK, "", false, http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, closeStub := keycloakStub(t, tc.status, tc.body)
			defer closeStub()
			kc := authz.NewKeycloak(cfg, http.DefaultClient)

			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			if tc.basic {
				req.SetBasicAuth("alice", "secret")
			}
			rec := httptest.NewRecorder()
			Login(kc, slog.Default())(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK {
				var m map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m["access_token"] != "abc" {
					t.Errorf("body = %q", rec.Body.String())
				}
			}
		})
	}
}

func TestLoginRejectsGet(t *testing.T) {
	cfg, closeStub := keycloakStub(t, http.StatusOK, "{}")
	defer closeStub()
	kc := authz.NewKeycloak(cfg, http.DefaultClient)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	Login(kc, slog.Default())(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestResourceMetadata(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:     "https://de.example.org/formation",
		KeycloakServerURL: "https://keycloak.example.org/",
		KeycloakRealm:     "cyverse",
	}
	m := ResourceMetadata(cfg)
	if m.Resource != "https://de.example.org/formation/mcp" {
		t.Errorf("resource = %q", m.Resource)
	}
	if len(m.AuthorizationServers) != 1 || m.AuthorizationServers[0] != "https://keycloak.example.org/realms/cyverse" {
		t.Errorf("authorization servers = %v", m.AuthorizationServers)
	}
	if !strings.Contains(strings.Join(m.ScopesSupported, " "), "openid") {
		t.Errorf("scopes = %v", m.ScopesSupported)
	}
	// Ensure it round-trips as the JSON the metadata handler serves.
	if _, err := json.Marshal(m); err != nil {
		t.Errorf("marshal: %v", err)
	}
}
