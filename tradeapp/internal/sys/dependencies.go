// Package sys wires the application together: it opens the database, builds
// the repository, the provider client and every service, and hands them out
// as one Dependencies value. main and the transport layer take it from here;
// nothing else constructs services.
package sys

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/config"
	"github.com/babafem99/vfs/tradeapp/internal/db"
	"github.com/babafem99/vfs/tradeapp/internal/metrics"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

type Dependencies struct {
	Config  *config.Config
	Logger  *slog.Logger
	Metrics *metrics.App

	TradeSrv   *service.Service            // accounts, quotes, orders, jobs, recon
	IdempoSrv  *service.IdempotencyService // Idempotency-Key records
	WebhookSrv *service.WebhookService     // webhook replay cache and log
}

// NewDependencies connects to the database (running migrations) and builds
// every service. The returned func releases what it opened.
func NewDependencies(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Dependencies, func(), error) {
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
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

	tradeService := service.New(repo, broker, service.Config{
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
	idempotencyService := service.NewIdempotencyService(repo, service.IdempotencyConfig{
		TTL:         cfg.IdempotencyTTL,
		ZombieAfter: cfg.IdempotencyZombieAfter,
	}, log)
	webhookService := service.NewWebhookService(repo)

	appMetrics := metrics.NewApp()
	RegisterCollectors(appMetrics, tradeService)

	return &Dependencies{
			Config:     cfg,
			Logger:     log,
			Metrics:    appMetrics,
			TradeSrv:   tradeService,
			IdempoSrv:  idempotencyService,
			WebhookSrv: webhookService,
		}, func() {
			pool.Close()
		}, nil
}
