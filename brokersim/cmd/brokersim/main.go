// Command brokersim runs the fake stock broker.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "brokersim")
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

	if cfg.Seed == 0 {
		// Random ids per run: a restarted brokersim must not reuse ids that a
		// persisted trade app database already has.
		cfg.Seed = uint64(time.Now().UnixNano())
	}
	log.Info("brokersim config", "seed", cfg.Seed, "auto_fill", cfg.AutoFill,
		"auto_resolve_cancels", cfg.AutoResolveCancels, "dev_trusted", len(cfg.DevTrustedNets))

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

	eng.SetAutoResolveCancels(cfg.AutoResolveCancels)

	go disp.Run(ctx)
	go eng.Run(ctx)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: api.NewRouter(api.Deps{
			Store: st, Market: mkt, Engine: eng, Dispatcher: disp, APIKey: cfg.APIKey, Log: log,
			DevTrusted: cfg.DevTrustedNets,
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

// healthcheck is `brokersim healthcheck`, for the container's HEALTHCHECK
// (the image is distroless: no shell, no curl). /dev answers loopback.
func healthcheck() {
	addr := os.Getenv("SIM_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + addr + "/dev/engine")
	if err == nil {
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			os.Exit(0)
		}
		err = fmt.Errorf("status %s", res.Status)
	}
	fmt.Fprintln(os.Stderr, "unhealthy:", err)
	os.Exit(1)
}
