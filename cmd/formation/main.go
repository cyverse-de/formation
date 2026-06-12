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
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/handlers"
	mcpserver "github.com/cyverse-de/formation/internal/mcp"
	"github.com/cyverse-de/formation/internal/vice"
)

const serviceName = "formation"

var log = logging.Log.WithFields(logrus.Fields{"service": serviceName})

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

	e, err := buildServer(cfg)
	if err != nil {
		log.Fatal(err)
	}

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
func buildServer(cfg *config.Config) (*echo.Echo, error) {
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.Use(otelecho.Middleware(serviceName))

	// The gateway forwards requests with the path prefix intact, so strip it
	// before routing like FastAPI's root_path did; bare paths also work.
	e.Pre(handlers.StripPathPrefix(cfg.PathPrefix))
	log.Infof("stripping configured path prefix before routing: %s", cfg.PathPrefix)

	verifier := auth.NewVerifier(cfg.KeycloakServerURL, cfg.KeycloakRealm, cfg.KeycloakSSLVerify)

	terrainClient, err := clients.NewTerrain(cfg.TerrainBaseURL)
	if err != nil {
		return nil, err
	}
	urlChecker := vice.NewURLChecker(cfg.ViceURLCheckTimeout, cfg.ViceURLCheckRetries, cfg.ViceURLCheckCacheTTL)
	subdomains := vice.NewSubdomainResolver(terrainClient)
	apps := handlers.NewApps(terrainClient, urlChecker, subdomains, cfg)
	data := handlers.NewData(terrainClient)
	user := handlers.NewUser(terrainClient)

	landing, err := handlers.Landing(cfg, mcpserver.ToolNames)
	if err != nil {
		return nil, err
	}
	e.GET("/", landing)

	mcpHandler := mcpserver.Handler(mcpserver.Deps{Apps: apps, Data: data, User: user, Cfg: cfg}, verifier)
	// POST carries JSON-RPC; GET and DELETE are part of the streamable
	// HTTP transport. The SDK answers its own errors, so the JSON error
	// handler stays out of the MCP path.
	e.Any("/mcp", echo.WrapHandler(mcpHandler))
	mcpserver.RegisterWellKnown(e, cfg)
	log.Infof("MCP server mounted at /mcp (resource %s/mcp, shared client %s)", cfg.PublicBaseURL, cfg.MCPClientID)

	return e, nil
}
