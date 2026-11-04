// Package config reads simulator settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/market"
)

type Config struct {
	Addr          string
	APIKey        string
	WebhookURL    string
	WebhookSecret string
	Seed          uint64
	Symbols       []market.Symbol
	QuoteTTL      time.Duration
	OrderTTL      time.Duration
	FeeBps        int64
	SpreadBps     int64

	// Engine (stage 3).
	Tick             time.Duration
	AutoFill         bool
	FillProb         float64
	MaxChunkPct      int64
	CancelDelayMin   time.Duration
	CancelDelayMax   time.Duration
	FillDuringCancel bool
	MarketOpen       bool
}

// Load reads the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads settings through getenv, applying defaults for unset values.
func LoadFrom(getenv func(string) string) (Config, error) {
	p := parser{getenv: getenv}
	c := Config{
		Addr:             p.str("SIM_ADDR", ":8090"),
		APIKey:           p.str("SIM_API_KEY", "sim_test_key"),
		WebhookURL:       p.str("SIM_WEBHOOK_URL", "http://localhost:8091/webhooks/provider"),
		WebhookSecret:    p.str("SIM_WEBHOOK_SECRET", "whsec_test"),
		Seed:             uint64(p.int("SIM_SEED", 42)),
		QuoteTTL:         p.dur("SIM_QUOTE_TTL", 15*time.Second),
		OrderTTL:         p.dur("SIM_ORDER_TTL", 10*time.Minute),
		FeeBps:           p.int("SIM_FEE_BPS", 50),
		SpreadBps:        p.int("SIM_SPREAD_BPS", 20),
		Tick:             p.dur("SIM_TICK", 500*time.Millisecond),
		AutoFill:         p.bool("SIM_AUTO_FILL", true),
		FillProb:         p.float("SIM_FILL_PROB", 0.35),
		MaxChunkPct:      p.int("SIM_MAX_CHUNK_PCT", 60),
		CancelDelayMin:   p.dur("SIM_CANCEL_DELAY_MIN", 300*time.Millisecond),
		CancelDelayMax:   p.dur("SIM_CANCEL_DELAY_MAX", 2*time.Second),
		FillDuringCancel: p.bool("SIM_FILL_DURING_CANCEL", true),
		MarketOpen:       p.bool("SIM_MARKET_OPEN", true),
	}
	syms, err := market.ParseSymbols(p.str("SIM_SYMBOLS", "AAPL:19000,MSFT:41000,TSLA:25000,DANGCEM:48000,MTNN:22000"))
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("SIM_SYMBOLS: %w", err))
	}
	c.Symbols = syms

	check := func(ok bool, msg string) {
		if !ok {
			p.errs = append(p.errs, errors.New(msg))
		}
	}
	check(c.APIKey != "", "SIM_API_KEY must not be empty")
	check(c.QuoteTTL > 0 && c.OrderTTL > 0 && c.Tick > 0, "SIM_QUOTE_TTL, SIM_ORDER_TTL and SIM_TICK must be > 0")
	check(c.FeeBps >= 0 && c.SpreadBps >= 0, "SIM_FEE_BPS and SIM_SPREAD_BPS must be >= 0")
	check(c.FillProb >= 0 && c.FillProb <= 1, "SIM_FILL_PROB must be in [0, 1]")
	check(c.MaxChunkPct >= 1 && c.MaxChunkPct <= 100, "SIM_MAX_CHUNK_PCT must be in [1, 100]")
	check(c.CancelDelayMin >= 0 && c.CancelDelayMin <= c.CancelDelayMax, "need 0 <= SIM_CANCEL_DELAY_MIN <= SIM_CANCEL_DELAY_MAX")
	return c, errors.Join(p.errs...)
}

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) str(key, def string) string {
	if v := p.getenv(key); v != "" {
		return v
	}
	return def
}

func parse[T any](p *parser, key string, def T, fn func(string) (T, error)) T {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	out, err := fn(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s=%q: %w", key, v, err))
		return def
	}
	return out
}

func (p *parser) int(key string, def int64) int64 {
	return parse(p, key, def, func(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) })
}
func (p *parser) float(key string, def float64) float64 {
	return parse(p, key, def, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) })
}
func (p *parser) bool(key string, def bool) bool { return parse(p, key, def, strconv.ParseBool) }
func (p *parser) dur(key string, def time.Duration) time.Duration {
	return parse(p, key, def, time.ParseDuration)
}
