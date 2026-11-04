// Command brokersim runs the fake stock broker.
package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/api"
	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/config"
	"github.com/babafem99/vfs/lab/brokersim/internal/engine"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
	"github.com/babafem99/vfs/lab/brokersim/internal/webhook"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("brokersim exited", "err", err)
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

	// One seeded stream per component, so they don't shift each other.
	mkt := market.New(cfg.Symbols, cfg.SpreadBps, rand.New(rand.NewPCG(cfg.Seed, 1)))
	disp := webhook.New(webhook.Options{
		URL:    cfg.WebhookURL,
		Secret: cfg.WebhookSecret,
		Rand:   rand.New(rand.NewPCG(cfg.Seed, 3)),
		Logger: log,
	})
	st := store.New(store.Config{
		QuoteTTL:       cfg.QuoteTTL,
		FeeBps:         cfg.FeeBps,
		CancelDelayMin: cfg.CancelDelayMin,
		CancelDelayMax: cfg.CancelDelayMax,
		MarketOpen:     cfg.MarketOpen,
	}, clock.Real{}, rand.New(rand.NewPCG(cfg.Seed, 2)), mkt, disp.Enqueue)

	eng := engine.New(engine.Config{
		Tick:             cfg.Tick,
		AutoFill:         cfg.AutoFill,
		FillProb:         cfg.FillProb,
		MaxChunkPct:      cfg.MaxChunkPct,
		FillDuringCancel: cfg.FillDuringCancel,
		OrderTTL:         cfg.OrderTTL,
	}, st, mkt, clock.Real{}, rand.New(rand.NewPCG(cfg.Seed, 4)), log)

	go disp.Run(ctx)
	go eng.Run(ctx)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: api.NewRouter(api.Deps{
			Store: st, Market: mkt, Engine: eng, Dispatcher: disp, APIKey: cfg.APIKey, Log: log,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("brokersim listening", "addr", cfg.Addr, "webhook_url", cfg.WebhookURL, "seed", cfg.Seed)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
