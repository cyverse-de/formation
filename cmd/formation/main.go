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
	"github.com/cyverse-de/formation/internal/datastore"
	"github.com/cyverse-de/formation/internal/handlers"
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

	// PATH_PREFIX matches FastAPI's root_path: proxy metadata only, never routing.
	log.Infof("configured path prefix (informational, routes serve at /): %s", cfg.PathPrefix)

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

	store, err := datastore.NewIRODS(cfg.IRODSHost, cfg.IRODSPort, cfg.IRODSUser, cfg.IRODSPassword, cfg.IRODSZone)
	if err != nil {
		return nil, nil, err
	}
	data := handlers.NewData(store)

	e.GET("/", handlers.Health)
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

	return e, store.Release, nil
}
