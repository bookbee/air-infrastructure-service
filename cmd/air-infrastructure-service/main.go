// Command air-infrastructure-service is the AIR platform's local control
// plane: it brokers scoped, policy-checked credentials for Redis, Postgres and
// Mongo so no sibling service hardcodes an endpoint or a password.
//
// It is never on the data path — it hands out an endpoint plus credentials and
// steps aside.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/bookbee/air-infrastructure-service/internal/app"
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/observability"
)

// shutdownGrace bounds how long in-flight requests have to finish after a
// signal. Credential issuance is a sub-second call, so this is generous.
const shutdownGrace = 10 * time.Second

func main() {
	var (
		envFile     = flag.String("env-file", ".env", "path to a .env file; absent is not an error")
		healthcheck = flag.Bool("healthcheck", false, "probe the local /v1/health and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(constants.Version)
		return
	}

	if err := run(*envFile, *healthcheck); err != nil {
		// Before the logger exists, and for a failed healthcheck, stderr is the
		// only channel a container runtime will show.
		fmt.Fprintln(os.Stderr, "air-infrastructure-service:", err)
		os.Exit(1)
	}
}

func run(envFile string, healthcheck bool) error {
	settings, err := config.Load(envFile)
	if err != nil {
		return err
	}

	if healthcheck {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		url := fmt.Sprintf("http://127.0.0.1:%d/v1/health", settings.API.Port)
		return app.Probe(ctx, url)
	}

	log := observability.NewLogger(settings.Log.Level, settings.Log.Render, os.Stdout)

	handler, _, err := app.New(settings, log)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(settings.API.Host, strconv.Itoa(settings.API.Port))
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// A control plane on a laptop still gets pointed at by scanners and stray
		// clients; these keep a stalled peer from holding a connection open
		// forever.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		log.Info("air-infrastructure-service.start",
			"environment", string(settings.Environment),
			"version", constants.Version,
			"addr", addr,
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case err := <-errs:
		if err != nil {
			return fmt.Errorf("serving on %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
	}

	// Drain in flight requests before exiting, so a rolling restart does not
	// fail a consumer that is mid-request for a credential.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("air-infrastructure-service.stop", "error", err.Error())
		return err
	}
	log.Info("air-infrastructure-service.stop")
	return nil
}
