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

	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
	if !cfg.KeycloakSSLVerify {
		httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // explicitly configured for non-prod
		logger.Warn("Keycloak TLS verification disabled (KEYCLOAK_SSL_VERIFY=false); do not use in production")
	}

	deps := buildDeps(cfg, httpClient, logger)
	srv := mcpserver.New(deps)

	handler := buildHandler(cfg, httpClient, srv, logger)

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	run(httpSrv, logger)
}

// buildDeps constructs the backend clients and tool dependencies. The data
// store is best-effort: if iRODS is unreachable at startup, data tools report
// the service as unavailable rather than preventing the server from booting.
func buildDeps(cfg *config.Config, httpClient *http.Client, logger *slog.Logger) *mcpserver.Deps {
	appsClient := apps.NewAppsClient(httpClient, cfg.AppsBaseURL, logger)
	exposerClient := apps.NewAppExposerClient(httpClient, cfg.AppExposerBaseURL, logger)
	vice := apps.NewVICEResolver(exposerClient, apps.VICEConfig{
		ViceDomain:       cfg.ViceDomain,
		URLCheckRetries:  cfg.ViceURLCheckRetries,
		URLCheckTimeout:  cfg.ViceURLCheckTimeout,
		URLCheckCacheTTL: cfg.ViceURLCheckCacheTTL,
	}, logger)

	// The data store connects per request using proxy impersonation, so there
	// is no startup connection to establish here.
	ds := datastore.New(datastore.Config{
		Host:     cfg.IRODSHost,
		Port:     cfg.IRODSPort,
		User:     cfg.IRODSUser,
		Password: cfg.IRODSPassword,
		Zone:     cfg.IRODSZone,
	}, logger)

	return &mcpserver.Deps{
		Apps:       appsClient,
		Exposer:    exposerClient,
		Vice:       vice,
		Data:       ds,
		Logger:     logger,
		UserSuffix: cfg.UserSuffix,
		OutputZone: cfg.OutputZone,
		Version:    version,
	}
}

// buildHandler assembles the HTTP mux: the protected MCP endpoint, the public
// OAuth resource metadata, the legacy login, and a health check, honoring the
// configured path prefix.
func buildHandler(cfg *config.Config, httpClient *http.Client, srv *mcp.Server, logger *slog.Logger) http.Handler {
	verifier := authz.NewVerifier(cfg, httpClient)
	kc := authz.NewKeycloak(cfg, httpClient)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	requireAuth := auth.RequireBearerToken(verifier.Verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.ResourceMetadataURL(),
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", requireAuth(mcpHandler))
	mux.Handle("/.well-known/oauth-protected-resource", auth.ProtectedResourceMetadataHandler(httpapi.ResourceMetadata(cfg)))
	mux.HandleFunc("/login", httpapi.Login(kc, logger))
	mux.HandleFunc("/", httpapi.Health)

	if cfg.PathPrefix == "" {
		return mux
	}
	prefixed := http.NewServeMux()
	prefixed.Handle(cfg.PathPrefix+"/", http.StripPrefix(cfg.PathPrefix, mux))
	return prefixed
}

func run(httpSrv *http.Server, logger *slog.Logger) {
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
		close(idleClosed)
	}()

	logger.Info("formation listening", "addr", httpSrv.Addr, "version", version)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
	<-idleClosed
}
