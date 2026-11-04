// Command api runs the trade app backend.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/config"
	"github.com/babafem99/vfs/tradeapp/internal/db"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/internal/service"
	httpapi "github.com/babafem99/vfs/tradeapp/internal/transport/http"
	"github.com/babafem99/vfs/tradeapp/internal/worker"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	log.Info("database ready")

	repo := repository.New(pool)
	breaker := provider.NewBreaker(cfg.BreakerThreshold, cfg.BreakerCooldown)
	breaker.OnStateChange(func(from, to string) {
		if to == provider.CircuitOpen {
			log.Warn("provider circuit open: pausing provider calls", "from", from, "cooldown", cfg.BreakerCooldown)
			return
		}
		log.Info("provider circuit "+to, "from", from)
	})
	broker := provider.New(cfg.ProviderURL, cfg.ProviderAPIKey, cfg.ProviderTimeout, breaker)
	svc := service.New(repo, broker, service.Config{
		JobRetryBudget: cfg.JobRetryBudget,
		JobMaxAttempts: cfg.JobMaxAttempts,
		JobBackoffBase: cfg.JobBackoffBase,
		JobBackoffMax:  cfg.JobBackoffMax,
		JobLease:       cfg.JobLease,
		JobLagAlert:    cfg.JobLagAlert,
		PriceCacheTTL:  time.Second,
		StuckAfter:     cfg.StuckAfter,
		ReconLookback:  cfg.ReconLookback,
		Logger:         log,
	})
	api := httpapi.New(svc, repo, httpapi.Config{
		WebhookSecret:    cfg.WebhookSecret,
		WebhookTolerance: cfg.WebhookTolerance,
	}, log)

	go worker.New(svc, log, cfg.WorkerPoll, cfg.SweepInterval, cfg.ReconInterval).Run(ctx)
	go api.RunKeyCleanup(ctx, cfg.IdempotencyTTL, time.Hour)
	log.Info("worker started", "provider", cfg.ProviderURL)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
