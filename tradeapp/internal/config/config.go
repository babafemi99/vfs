// Package config reads the trade app's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr           string        // API_ADDR
	DatabaseURL    string        // DATABASE_URL
	IdempotencyTTL time.Duration // IDEMPOTENCY_TTL: keys older than this are deleted
	// IDEMPOTENCY_ZOMBIE_AFTER: a key still running after this is released
	IdempotencyZombieAfter time.Duration

	ProviderURL     string        // PROVIDER_URL: brokersim
	ProviderAPIKey  string        // PROVIDER_API_KEY: sent as X-Api-Key
	ProviderTimeout time.Duration // PROVIDER_TIMEOUT: per HTTP call

	WorkerPoll     time.Duration // WORKER_POLL: how often the worker looks for due jobs
	JobRetryBudget time.Duration // JOB_RETRY_BUDGET: how long a failing job retries before the dead-letter queue
	JobMaxAttempts int           // JOB_MAX_ATTEMPTS: hard cap on attempts, whatever the budget
	JobBackoffBase time.Duration // JOB_BACKOFF_BASE: first retry delay; the ceiling doubles each attempt
	JobBackoffMax  time.Duration // JOB_BACKOFF_MAX: highest retry delay ceiling

	BreakerThreshold int           // BREAKER_THRESHOLD: provider failures in a row that open the circuit
	BreakerCooldown  time.Duration // BREAKER_COOLDOWN: how long the circuit stays open before a probe
	JobLease         time.Duration // JOB_LEASE: how long a running job stays locked to its worker
	JobLagAlert      time.Duration // JOB_LAG_ALERT: alert when a job has waited longer than this

	WebhookSecret    string        // PROVIDER_WEBHOOK_SECRET: must match brokersim
	WebhookTolerance time.Duration // WEBHOOK_TOLERANCE: max signature age
	SweepInterval    time.Duration // SWEEP_INTERVAL: how often to look for stuck orders
	StuckAfter       time.Duration // STUCK_AFTER: an active order quiet this long is polled
	ReconInterval    time.Duration // RECON_INTERVAL: how often to reconcile with the provider
	ReconLookback    time.Duration // RECON_LOOKBACK: reconcile orders changed within this window

	RateLimitPerMinute int // RATE_LIMIT_PER_MINUTE: per user, per route group (orders, quotes, fund); 0 = off
	RateLimitBurst     int // RATE_LIMIT_BURST: requests allowed at once before the per-minute rate applies
}

// Load reads the config from the environment, using defaults for anything unset.
func Load() (Config, error) {
	var errs []error
	duration := func(key, def string) time.Duration {
		d, err := time.ParseDuration(env(key, def))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: must be a positive duration", key))
		}
		return d
	}
	nonNegative := func(key, def string) int {
		n, err := strconv.Atoi(env(key, def))
		if err != nil || n < 0 {
			errs = append(errs, fmt.Errorf("%s: must be a whole number, 0 or more", key))
		}
		return n
	}
	integer := func(key, def string) int {
		n, err := strconv.Atoi(env(key, def))
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s: must be a positive integer", key))
		}
		return n
	}

	c := Config{
		Addr:                   env("API_ADDR", ":8091"),
		DatabaseURL:            env("DATABASE_URL", "postgres://localhost:5432/tradeapp?sslmode=disable"),
		IdempotencyTTL:         duration("IDEMPOTENCY_TTL", "24h"),
		IdempotencyZombieAfter: duration("IDEMPOTENCY_ZOMBIE_AFTER", "5m"),

		ProviderURL:     env("PROVIDER_URL", "http://localhost:8090"),
		ProviderAPIKey:  env("PROVIDER_API_KEY", "sim_test_key"),
		ProviderTimeout: duration("PROVIDER_TIMEOUT", "5s"),

		WorkerPoll:     duration("WORKER_POLL", "1s"),
		JobRetryBudget: duration("JOB_RETRY_BUDGET", "15m"),
		JobMaxAttempts: integer("JOB_MAX_ATTEMPTS", "50"),
		JobBackoffBase: duration("JOB_BACKOFF_BASE", "1s"),
		JobBackoffMax:  duration("JOB_BACKOFF_MAX", "2m"),

		BreakerThreshold: integer("BREAKER_THRESHOLD", "5"),
		BreakerCooldown:  duration("BREAKER_COOLDOWN", "15s"),
		JobLease:         duration("JOB_LEASE", "30s"),
		JobLagAlert:      duration("JOB_LAG_ALERT", "5m"),

		WebhookSecret:    env("PROVIDER_WEBHOOK_SECRET", "whsec_test"),
		WebhookTolerance: duration("WEBHOOK_TOLERANCE", "5m"),
		SweepInterval:    duration("SWEEP_INTERVAL", "15s"),
		StuckAfter:       duration("STUCK_AFTER", "30s"),
		ReconInterval:    duration("RECON_INTERVAL", "5m"),
		ReconLookback:    duration("RECON_LOOKBACK", "24h"),

		RateLimitPerMinute: nonNegative("RATE_LIMIT_PER_MINUTE", "60"),
		RateLimitBurst:     nonNegative("RATE_LIMIT_BURST", "10"),
	}
	if c.WebhookSecret == "" {
		errs = append(errs, errors.New("PROVIDER_WEBHOOK_SECRET must not be empty"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL must not be empty"))
	}
	if c.ProviderURL == "" {
		errs = append(errs, errors.New("PROVIDER_URL must not be empty"))
	}
	if c.JobLease <= c.ProviderTimeout {
		errs = append(errs, errors.New("JOB_LEASE must be longer than PROVIDER_TIMEOUT"))
	}
	return c, errors.Join(errs...)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
