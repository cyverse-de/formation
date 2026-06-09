// Command formation runs the CyVerse Discovery Environment MCP server. It
// exposes Formation's tools over the MCP streamable-HTTP transport, acting as
// an OAuth 2.0 resource server that validates Keycloak bearer tokens, and also
// serves the legacy password-grant login and OAuth resource metadata.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apps"
	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/datastore"
	"github.com/cyverse-de/formation/internal/httpapi"
	"github.com/cyverse-de/formation/internal/mcpserver"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Backend (apps/app-exposer) traffic always verifies TLS. KEYCLOAK_SSL_VERIFY
	// only loosens the Keycloak client, so a Keycloak-scoped flag can never
	// weaken TLS for the DE service calls.
	backendClient := &http.Client{Timeout: cfg.HTTPTimeout}
	keycloakClient := &http.Client{Timeout: cfg.HTTPTimeout}
	if !cfg.KeycloakSSLVerify {
		keycloakClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // explicitly configured for non-prod
		logger.Warn("Keycloak TLS verification disabled (KEYCLOAK_SSL_VERIFY=false); do not use in production")
	}

	deps := buildDeps(cfg, backendClient, logger)
	srv := mcpserver.New(deps)

	handler := buildHandler(cfg, keycloakClient, srv, logger)

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	run(httpSrv, deps, logger)
}

// buildDeps constructs the backend clients and tool dependencies.
func buildDeps(cfg *config.Config, httpClient *http.Client, logger *slog.Logger) *mcpserver.Deps {
	appsClient := apps.NewAppsClient(httpClient, cfg.AppsBaseURL, logger)
	exposerClient := apps.NewAppExposerClient(httpClient, cfg.AppExposerBaseURL, logger)
	vice := apps.NewVICEResolver(exposerClient, apps.VICEConfig{
		ViceDomain:          cfg.ViceDomain,
		MaxSubdomainRetries: cfg.ViceSubdomainRetries,
		SubdomainRetryDelay: cfg.ViceSubdomainRetryDelay,
		URLCheckRetries:     cfg.ViceURLCheckRetries,
		URLCheckTimeout:     cfg.ViceURLCheckTimeout,
		URLCheckCacheTTL:    cfg.ViceURLCheckCacheTTL,
	}, logger)

	// The data store connects lazily via proxy impersonation, pooling one
	// connection per user; nothing is established at startup.
	ds := datastore.New(datastore.Config{
		Host:     cfg.IRODSHost,
		Port:     cfg.IRODSPort,
		User:     cfg.IRODSUser,
		Password: cfg.IRODSPassword,
		Zone:     cfg.IRODSZone,
	}, cfg.IRODSConnIdleTTL, cfg.IRODSMaxConns, logger)

	return &mcpserver.Deps{
		Apps:       appsClient,
		Exposer:    exposerClient,
		Vice:       vice,
		Data:       ds,
		UserSuffix: cfg.UserSuffix,
		OutputZone: cfg.IRODSZone,
		Version:    version,
	}
}

// buildHandler assembles the HTTP mux: the protected MCP endpoint, the public
// OAuth resource metadata, the legacy login, and a health check, honoring the
// configured path prefix.
func buildHandler(cfg *config.Config, httpClient *http.Client, srv *mcp.Server, logger *slog.Logger) http.Handler {
	verifier := authz.NewVerifier(cfg, httpClient, logger)
	kc := authz.NewKeycloak(cfg, httpClient)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	requireAuth := auth.RequireBearerToken(verifier.Verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.ResourceMetadataURL(),
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", requireAuth(mcpHandler))
	if cfg.PublicBaseURL != "" {
		mux.Handle("/.well-known/oauth-protected-resource", auth.ProtectedResourceMetadataHandler(httpapi.ResourceMetadata(cfg)))
	} else {
		logger.Warn("PUBLIC_BASE_URL is not set; OAuth resource metadata is disabled, so MCP clients cannot discover the authorization server")
	}
	mux.HandleFunc("/login", httpapi.Login(kc, logger))
	mux.HandleFunc("/", httpapi.Health)

	if cfg.PathPrefix == "" {
		return mux
	}
	// Keep the health check reachable at the bare root too: k8s probes hit
	// GET / directly, without the ingress path prefix.
	prefixed := http.NewServeMux()
	prefixed.Handle(cfg.PathPrefix+"/", http.StripPrefix(cfg.PathPrefix, mux))
	prefixed.HandleFunc("/", httpapi.Health)
	return prefixed
}

func run(httpSrv *http.Server, deps *mcpserver.Deps, logger *slog.Logger) {
	idleClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		logger.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
		if ds, ok := deps.Data.(*datastore.DataStore); ok {
			ds.Close()
		}
		close(idleClosed)
	}()

	logger.Info("formation listening", "addr", httpSrv.Addr, "version", version)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
	<-idleClosed
}
