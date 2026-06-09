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
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/handlers"
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

	// PATH_PREFIX matches FastAPI's root_path: proxy metadata only, never routing.
	log.Infof("configured path prefix (informational, routes serve at /): %s", cfg.PathPrefix)

	keycloak, err := auth.NewKeycloak(
		cfg.KeycloakServerURL, cfg.KeycloakRealm,
		cfg.KeycloakClientID, cfg.KeycloakClientSecret, cfg.KeycloakSSLVerify,
	)
	if err != nil {
		return nil, err
	}
	verifier := auth.NewVerifier(cfg.KeycloakServerURL, cfg.KeycloakRealm, cfg.KeycloakSSLVerify)
	requireUser := auth.RequireUser(verifier)

	e.GET("/", handlers.Health)
	e.POST("/login", handlers.Login(keycloak))
	e.GET("/user", handlers.UserInfo, requireUser)

	return e, nil
}
