// Package service is the business layer: validation, ids, and orchestration
// of repository and provider calls. Each exported method is one use case and
// opens its own transaction with repo.RunInTx.
//
// Unexported helpers that take a pgx.Tx (postEntry, holdCash, applyBuyFill,
// ...) are building blocks: they run inside the caller's transaction so a use
// case can combine them atomically.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

// Config holds the service's tunables.
type Config struct {
	JobRetryBudget time.Duration // how long a failing job keeps retrying before the dead-letter queue
	JobMaxAttempts int           // hard cap on attempts, whatever the budget
	JobBackoffBase time.Duration // first retry delay; the ceiling doubles each attempt
	JobBackoffMax  time.Duration // highest retry delay ceiling
	JobLease       time.Duration // how long a claimed job stays locked to its worker
	JobLagAlert    time.Duration // alert when a job has waited longer than this
	PriceCacheTTL  time.Duration // how long provider prices are reused
	StuckAfter     time.Duration // an active order quiet for this long is polled by the sweeper; also recon's settling window
	ReconLookback  time.Duration // reconciliation checks orders changed within this window
	Logger         *slog.Logger  // structured log of important business events (nil: discard)
}

func DefaultConfig() Config {
	return Config{
		JobRetryBudget: 15 * time.Minute,
		JobMaxAttempts: 50,
		JobBackoffBase: time.Second,
		JobBackoffMax:  2 * time.Minute,
		JobLease:       30 * time.Second,
		JobLagAlert:    5 * time.Minute,
		PriceCacheTTL:  time.Second,
		StuckAfter:     30 * time.Second,
		ReconLookback:  24 * time.Hour,
	}
}

type Service struct {
	repo          repository.Repository
	ledgerRepo    repository.LedgerRepo
	positionRepo  repository.PositionRepo
	orderRepo     repository.OrderRepo
	quoteRepo     repository.QuoteRepo
	activityRepo  repository.ActivityRepo
	invariantRepo repository.InvariantRepo
	jobRepo       repository.JobRepo
	eventRepo     repository.EventRepo
	breakRepo     repository.BreakRepo
	reconRepo     repository.ReconRepo

	broker *provider.Client
	cfg    Config
	log    *slog.Logger
	now    func() time.Time // the clock; tests replace it

	jobsReady chan struct{} // nudges the worker when new jobs are queued

	pricesMu      sync.Mutex
	prices        []domain.Price
	pricesFetched time.Time
}

func New(repo repository.Repository, broker *provider.Client, cfg Config) *Service {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		repo:      repo,
		broker:    broker,
		cfg:       cfg,
		log:       log,
		now:       time.Now,
		jobsReady: make(chan struct{}, 1),
	}
}

// Ping checks the database is reachable.
func (s *Service) Ping(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

// JobsReady receives a value whenever new jobs were queued.
func (s *Service) JobsReady() <-chan struct{} {
	return s.jobsReady
}

// notifyJobsReady nudges the worker without ever blocking.
func (s *Service) notifyJobsReady() {
	select {
	case s.jobsReady <- struct{}{}:
	default:
	}
}
