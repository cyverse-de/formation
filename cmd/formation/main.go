package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cyverse-de/go-mod/logging"
	"github.com/cyverse-de/go-mod/otelutils"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	echoSwagger "github.com/swaggo/echo-swagger"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"github.com/cyverse-de/formation/apidocs"
	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/datastore"
	"github.com/cyverse-de/formation/internal/handlers"
	mcpserver "github.com/cyverse-de/formation/internal/mcp"
	"github.com/cyverse-de/formation/internal/vice"
)

const serviceName = "formation"

var log = logging.Log.WithFields(logrus.Fields{"service": serviceName})

// @title formation
// @version 1.0
// @description REST API for the CyVerse Discovery Environment: app discovery and launching, analysis management, and iRODS data access.
// @description
// @description To authenticate: open the Authorize dialog and enter your username and password under BasicAuth,
// @description run POST /login, then copy the access_token from the response into the BearerAuth value
// @description (prefixed with "Bearer ") to call the other endpoints.
//
// @securityDefinitions.basic BasicAuth
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description A Keycloak JWT prefixed with "Bearer ". Obtain one via POST /login.
func main() {
	var (
		listenPort = flag.Int("listen-port", 8000, "The port to listen on for HTTP requests.")
		logLevel   = flag.String("log-level", "info", "One of trace, debug, info, warn, error, fatal, or panic.")
	)
	flag.Parse()
	logging.SetupLogging(*logLevel)

	tracerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := otelutils.TracerProviderFromEnv(tracerCtx, serviceName, func(e error) { log.Fatal(e) })
	defer shutdown()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	e, cleanup, err := buildServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	go func() {
		if err := e.Start(fmt.Sprintf(":%d", *listenPort)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Error(err)
	}
}

// buildServer wires up the Echo instance and routes; split from main for tests.
// The returned cleanup function releases the iRODS connection pool and must be
// called when the server shuts down.
func buildServer(cfg *config.Config) (*echo.Echo, func(), error) {
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.Use(otelecho.Middleware(serviceName))

	// The gateway forwards requests with the path prefix intact, so strip it
	// before routing like FastAPI's root_path did; bare paths also work.
	e.Pre(handlers.StripPathPrefix(cfg.PathPrefix))
	log.Infof("stripping configured path prefix before routing: %s", cfg.PathPrefix)

	keycloak, err := auth.NewKeycloak(
		cfg.KeycloakServerURL, cfg.KeycloakRealm,
		cfg.KeycloakClientID, cfg.KeycloakClientSecret, cfg.KeycloakSSLVerify,
	)
	if err != nil {
		return nil, nil, err
	}
	verifier := auth.NewVerifier(cfg.KeycloakServerURL, cfg.KeycloakRealm, cfg.KeycloakSSLVerify)
	requireUser := auth.RequireUser(verifier)
	requireUserOrSA := auth.RequireUserOrServiceAccount(verifier, cfg.ServiceAccountsOnly)

	appsClient, err := clients.NewApps(cfg.AppsBaseURL)
	if err != nil {
		return nil, nil, err
	}
	exposerClient, err := clients.NewAppExposer(cfg.AppExposerBaseURL)
	if err != nil {
		return nil, nil, err
	}
	urlChecker := vice.NewURLChecker(cfg.ViceURLCheckTimeout, cfg.ViceURLCheckRetries, cfg.ViceURLCheckCacheTTL)
	subdomains := vice.NewSubdomainResolver(exposerClient)
	apps := handlers.NewApps(appsClient, exposerClient, urlChecker, subdomains, cfg)

	store, err := datastore.NewIRODS(cfg.IRODSHost, cfg.IRODSPort, cfg.IRODSUser, cfg.IRODSPassword, cfg.IRODSZone, cfg.IRODSCacheTTL)
	if err != nil {
		return nil, nil, err
	}
	data := handlers.NewData(store)

	// The spec's basePath makes Swagger UI's try-it-out requests include the
	// gateway prefix; direct (unprefixed) access works via StripPathPrefix.
	apidocs.SwaggerInfo.BasePath = cfg.PathPrefix
	e.GET("/docs", func(c echo.Context) error {
		return c.Redirect(http.StatusMovedPermanently, "docs/index.html")
	})
	e.GET("/docs/*", echoSwagger.WrapHandler)

	landing, err := handlers.Landing(cfg, mcpserver.ToolNames)
	if err != nil {
		return nil, nil, err
	}
	e.GET("/", landing)
	e.POST("/login", handlers.Login(keycloak))
	e.GET("/user", handlers.UserInfo, requireUser)

	e.GET("/apps/job-types", apps.JobTypes, requireUserOrSA)
	e.GET("/apps", apps.List, requireUserOrSA)
	// FastAPI registered /apps/analyses/ with a trailing slash and redirected
	// the bare path; serving both directly is strictly more compatible.
	e.GET("/apps/analyses", apps.ListAnalyses, requireUserOrSA)
	e.GET("/apps/analyses/", apps.ListAnalyses, requireUserOrSA)
	e.GET("/apps/analyses/:analysis_id/status", apps.Status, requireUserOrSA)
	e.POST("/apps/analyses/:analysis_id/control", apps.Control, requireUserOrSA)
	e.GET("/apps/analyses/:analysis_id/details", apps.Details, requireUserOrSA)
	e.GET("/apps/:system_id/:app_id/parameters", apps.Parameters, requireUserOrSA)
	e.POST("/app/launch/:system_id/:app_id", apps.Launch, requireUserOrSA)

	e.GET("/data/*", data.Get, requireUser)
	e.PUT("/data/*", data.Put, requireUser)
	e.DELETE("/data/*", data.Delete, requireUser)

	if cfg.MCPEnabled {
		mcpHandler := mcpserver.Handler(mcpserver.Deps{Apps: apps, Data: data, Cfg: cfg}, verifier)
		// POST carries JSON-RPC; GET and DELETE are part of the streamable
		// HTTP transport. The SDK answers its own errors, so the FastAPI-style
		// error handler stays out of the MCP path.
		e.Any("/mcp", echo.WrapHandler(mcpHandler))
		mcpserver.RegisterWellKnown(e, cfg)
		log.Infof("MCP server mounted at /mcp (resource %s/mcp, shared client %s)", cfg.PublicBaseURL, cfg.MCPClientID)
	}

	return e, store.Release, nil
}
