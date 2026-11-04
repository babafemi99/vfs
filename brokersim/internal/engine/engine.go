// Package engine is the simulator's clock-driven "market": on every tick it
// moves prices, expires quotes, fills orders, resolves cancels and expires
// stale orders. It only uses public store methods, so every change goes
// through the domain rules and emits the normal events.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
)

type Config struct {
	Tick             time.Duration
	AutoFill         bool    // initial value; change at runtime with SetAutoFill
	FillProb         float64 // chance a working order gets a fill on a tick
	MaxChunkPct      int64   // max % of remaining filled in one chunk
	FillDuringCancel bool    // PENDING_CANCEL orders can still fill
	OrderTTL         time.Duration
}

type Engine struct {
	cfg    Config
	store  *store.Store
	market *market.Market
	clock  clock.Clock
	rng    *rand.Rand // only used from Tick, which is never concurrent
	log    *slog.Logger

	autoFill           atomic.Bool
	autoResolveCancels atomic.Bool
}

func New(cfg Config, st *store.Store, mkt *market.Market, clk clock.Clock, rng *rand.Rand, log *slog.Logger) *Engine {
	e := &Engine{cfg: cfg, store: st, market: mkt, clock: clk, rng: rng, log: log}
	e.autoFill.Store(cfg.AutoFill)
	e.autoResolveCancels.Store(true)
	return e
}

// SetAutoFill turns random fills on or off.
func (e *Engine) SetAutoFill(on bool) { e.autoFill.Store(on) }
func (e *Engine) AutoFill() bool      { return e.autoFill.Load() }

// SetAutoResolveCancels turns automatic cancel resolution on or off. When
// off, PENDING_CANCEL orders wait for a manual confirm or reject.
func (e *Engine) SetAutoResolveCancels(on bool) { e.autoResolveCancels.Store(on) }
func (e *Engine) AutoResolveCancels() bool      { return e.autoResolveCancels.Load() }

// Run calls Tick every cfg.Tick until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(e.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Tick()
		}
	}
}

// Tick runs one engine step. Tests call it directly with a fake clock.
// Orders are visited oldest first so a seed reproduces the same run.
func (e *Engine) Tick() {
	// 1. Prices.
	e.market.Step()

	// 2. Quote expiry.
	for _, id := range e.store.ExpiredQuoteIDs() {
		e.check("expire quote", id, e.store.ExpireQuote(id))
	}

	// 3. Auto-fill.
	if e.autoFill.Load() {
		statuses := []domain.OrderStatus{domain.OrderOpen, domain.OrderPartiallyFilled}
		if e.cfg.FillDuringCancel {
			statuses = append(statuses, domain.OrderPendingCancel)
		}
		for _, o := range e.store.ListOrders(store.ListFilter{Statuses: statuses, OldestFirst: true}) {
			if e.rng.Float64() >= e.cfg.FillProb {
				continue
			}
			maxChunk := max(1, o.RemainingQty*e.cfg.MaxChunkPct/100)
			qty := 1 + e.rng.Int64N(maxChunk)
			_, err := e.store.FillOrder(o.ID, qty)
			e.check("fill", o.ID, err)
		}
	}

	now := e.clock.Now()

	// 4. Due cancels, including ones a fill already completed (those get
	// cancel_rejected), so the filter is on CancelDueAt, not on status.
	if e.autoResolveCancels.Load() {
		for _, o := range e.store.ListOrders(store.ListFilter{OldestFirst: true}) {
			if o.HasPendingCancel() && !now.Before(o.CancelDueAt) {
				_, err := e.store.ResolveCancel(o.ID)
				e.check("resolve cancel", o.ID, err)
			}
		}
	}

	// 5. Day-order expiry.
	working := []domain.OrderStatus{domain.OrderOpen, domain.OrderPartiallyFilled}
	for _, o := range e.store.ListOrders(store.ListFilter{Statuses: working, OldestFirst: true}) {
		if now.Sub(o.CreatedAt) >= e.cfg.OrderTTL {
			_, err := e.store.ExpireOrder(o.ID, "day_order_expired")
			e.check("expire order", o.ID, err)
		}
	}
}

// check logs unexpected errors. Losing a race against another change
// (order already final, transition no longer allowed) is normal.
func (e *Engine) check(op, id string, err error) {
	if err == nil || errors.Is(err, domain.ErrOrderFinal) || errors.Is(err, domain.ErrInvalidTransition) {
		return
	}
	e.log.Warn("engine step failed", "op", op, "id", id, "err", err)
}
