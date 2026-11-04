// Command api runs the trade app backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/config"
	"github.com/babafem99/vfs/tradeapp/internal/health"
	"github.com/babafem99/vfs/tradeapp/internal/reqid"
	"github.com/babafem99/vfs/tradeapp/internal/sys"
	httpapi "github.com/babafem99/vfs/tradeapp/internal/transport/http"
	"github.com/babafem99/vfs/tradeapp/internal/worker"
)

// drainTimeout is how long shutdown waits for in-flight requests.
const drainTimeout = 10 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
	}
	log := newLogger(os.Getenv("LOG_FORMAT"))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	deps, cleanup, err := sys.NewDependencies(ctx, &cfg, log)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	defer cleanup()

	// Background work: the outbox worker (jobs, sweeper, recon) and cleanup.
	go worker.New(deps.TradeSrv, log, deps.Metrics, cfg.WorkerPoll, cfg.SweepInterval, cfg.ReconInterval).Run(ctx)
	go worker.RunCleanup(ctx, log, deps.IdempoSrv, deps.WebhookSrv, cfg.WebhookTolerance, time.Minute)
	log.Info("worker started", "provider", cfg.ProviderURL)

	api := &httpapi.API{
		Dependency: deps,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- api.Serve()
	}()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			stopBackground()
			cleanup()
			os.Exit(1)
		}
	case <-stopChan:
		log.Info("shutting down: waiting for connections to drain")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		if err := api.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
	}

	stopBackground() // stop the worker and cleanup, then close the pool (deferred)
	log.Info("server stopped")
}

// newLogger writes text (readable in a terminal) or, with LOG_FORMAT=json,
// one JSON object per line for a log pipeline (Vector → VictoriaLogs), where
// request_id, order_id and level become searchable fields. Either way, every
// line written with a request's context carries its request_id.
func newLogger(format string) *slog.Logger {
	var h slog.Handler = slog.NewTextHandler(os.Stdout, nil)
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, nil)
	}
	return slog.New(reqid.NewLogHandler(h)).With("service", "tradeapp-api")
}

// healthcheck is `api healthcheck`, for the container's HEALTHCHECK.
func healthcheck() {
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8091"
	}
	if err := health.Probe(addr, "/healthz"); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
