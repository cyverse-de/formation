package mcp

import (
	"net/http"
	"strings"

	"github.com/cyverse-de/go-mod/logging"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"

	"github.com/cyverse-de/formation/internal/config"
)

var log = logging.Log.WithFields(logrus.Fields{"package": "mcp"})

// RegisterWellKnown adds the OAuth discovery endpoints MCP clients use to
// find and authenticate against the server: RFC 9728 protected resource
// metadata, an RFC 8414/OIDC authorization-server metadata facade pointing at
// Keycloak's real endpoints, and a client registration shim that hands every
// client the shared public client_id.
//
// Each document is served at both the prefix-stripped path (covering
// {prefix}/.well-known/... and the bare form) and the RFC root form with the
// path prefix inserted after /.well-known/{kind} (which the gateway must
// route to formation).
func RegisterWellKnown(e *echo.Echo, cfg *config.Config) {
	prefix := cfg.PathPrefix

	prm := protectedResourceMetadata(cfg)
	asMeta := authServerMetadata(cfg)

	serveJSON := func(doc map[string]any) echo.HandlerFunc {
		return func(c echo.Context) error {
			// OAuth metadata is public discovery data (RFC 9728 §3.1).
			c.Response().Header().Set("Access-Control-Allow-Origin", "*")
			return c.JSON(http.StatusOK, doc)
		}
	}

	e.GET("/.well-known/oauth-protected-resource/mcp", serveJSON(prm))
	e.GET("/.well-known/oauth-protected-resource", serveJSON(prm))
	e.GET("/.well-known/oauth-authorization-server", serveJSON(asMeta))
	e.GET("/.well-known/openid-configuration", serveJSON(asMeta))

	// RFC root forms, e.g. /.well-known/oauth-protected-resource/formation/mcp;
	// these paths don't start with the prefix, so StripPathPrefix leaves them
	// alone and they need their own literal routes.
	if prefix != "" {
		e.GET("/.well-known/oauth-protected-resource"+prefix+"/mcp", serveJSON(prm))
		e.GET("/.well-known/oauth-authorization-server"+prefix, serveJSON(asMeta))
		e.GET("/.well-known/openid-configuration"+prefix, serveJSON(asMeta))
	}

	e.POST("/oauth/register", registerClient(cfg))
}

func scopes(cfg *config.Config) []string {
	return strings.Fields(cfg.MCPScopes)
}

// protectedResourceMetadata builds the RFC 9728 document identifying the MCP
// endpoint and pointing clients at the authorization-server metadata facade.
func protectedResourceMetadata(cfg *config.Config) map[string]any {
	return map[string]any{
		"resource":                 cfg.PublicBaseURL + "/mcp",
		"authorization_servers":    []string{cfg.PublicBaseURL},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         scopes(cfg),
		"resource_name":            "CyVerse Formation",
	}
}

// authServerMetadata builds the RFC 8414 document: authorization and token
// endpoints point at the Keycloak realm, while the registration endpoint is
// formation's shim. The issuer is formation's public base URL because the
// document lives under it, even though tokens carry the Keycloak realm issuer.
func authServerMetadata(cfg *config.Config) map[string]any {
	realm := strings.TrimSuffix(cfg.KeycloakServerURL, "/") + "/realms/" + cfg.KeycloakRealm
	return map[string]any{
		"issuer":                                cfg.PublicBaseURL,
		"authorization_endpoint":                realm + "/protocol/openid-connect/auth",
		"token_endpoint":                        realm + "/protocol/openid-connect/token",
		"jwks_uri":                              realm + "/protocol/openid-connect/certs",
		"registration_endpoint":                 cfg.PublicBaseURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      scopes(cfg),
	}
}

// registerClient is an RFC 7591 dynamic-registration shim: it accepts any
// registration request and returns the shared public Keycloak client, storing
// nothing. Keycloak itself validates redirect URIs at authorization time
// against the client's configured allowlist.
func registerClient(cfg *config.Config) echo.HandlerFunc {
	return func(c echo.Context) error {
		var request struct {
			RedirectURIs []string `json:"redirect_uris"`
			ClientName   string   `json:"client_name"`
		}
		if err := c.Bind(&request); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":             "invalid_client_metadata",
				"error_description": "request body must be a JSON client metadata document",
			})
		}

		// Logged so Keycloak admins can discover callback URLs that still
		// need adding to the public client's redirect URI allowlist.
		log.Infof("MCP client registration from %q with redirect URIs %v mapped to shared client %s",
			request.ClientName, request.RedirectURIs, cfg.MCPClientID)

		response := map[string]any{
			"client_id":                  cfg.MCPClientID,
			"redirect_uris":              request.RedirectURIs,
			"token_endpoint_auth_method": "none",
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
		}
		if request.ClientName != "" {
			response["client_name"] = request.ClientName
		}
		return c.JSON(http.StatusCreated, response)
	}
}
