// Package httpapi provides the non-MCP HTTP endpoints served on the same mux:
// the legacy password-grant login, a health check, and the OAuth
// protected-resource metadata document.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/config"
)

// Login returns a handler for the legacy password-grant login. It accepts HTTP
// Basic credentials and returns Keycloak's raw token JSON.
func Login(kc *authz.Keycloak, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing credentials")
			return
		}
		token, err := kc.GetAccessToken(r.Context(), username, password)
		if err != nil {
			if errors.Is(err, authz.ErrInvalidCredentials) {
				writeError(w, http.StatusUnauthorized, "Invalid credentials")
				return
			}
			logger.Error("login failed at Keycloak token endpoint; this usually means Keycloak is unreachable or misconfigured", "error", err)
			writeError(w, http.StatusBadGateway, "Authentication service error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(token)
	}
}

// Health returns an unauthenticated health-check handler for the root path.
func Health(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Hello from formation."))
}

// ResourceMetadata builds the OAuth 2.0 protected-resource metadata advertising
// Keycloak as the authorization server for this MCP resource.
func ResourceMetadata(cfg *config.Config) *oauthex.ProtectedResourceMetadata {
	resource := cfg.PublicBaseURL + "/mcp"
	return &oauthex.ProtectedResourceMetadata{
		Resource:               resource,
		AuthorizationServers:   []string{cfg.KeycloakIssuer()},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        []string{"openid", "profile", "email"},
		ResourceName:           "CyVerse Formation MCP",
	}
}

func writeError(w http.ResponseWriter, code int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
