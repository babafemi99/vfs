package engine

import (
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
)

type recorder struct {
	mu     sync.Mutex
	events []domain.Event
}

func (r *recorder) sink(ev domain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) ofType(t domain.EventType) []domain.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Event
	for _, ev := range r.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

type rig struct {
	t     *testing.T
	eng   *Engine
	store *store.Store
	clock *clock.Fake
	rec   *recorder
}

var baseCfg = Config{
	Tick: 500 * time.Millisecond, AutoFill: false, FillProb: 0.35, MaxChunkPct: 60,
	FillDuringCancel: true, OrderTTL: 10 * time.Minute,
}

func newRig(t *testing.T, seed uint64, cfg Config) *rig {
	t.Helper()
	syms, _ := market.ParseSymbols("AAPL:19000,MSFT:41000")
	mkt := market.New(syms, 20, rand.New(rand.NewPCG(seed, 1)))
	clk := clock.NewFake(time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC))
	rec := &recorder{}
	st := store.New(store.Config{
		QuoteTTL: 15 * time.Second, FeeBps: 50,
		CancelDelayMin: 300 * time.Millisecond, CancelDelayMax: 2 * time.Second,
		MarketOpen: true,
	}, clk, rand.New(rand.NewPCG(seed, 2)), mkt, rec.sink)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := New(cfg, st, mkt, clk, rand.New(rand.NewPCG(seed, 4)), log)
	return &rig{t: t, eng: eng, store: st, clock: clk, rec: rec}
}

func (r *rig) order(qty int64, key string) domain.Order {
	r.t.Helper()
	q, err := r.store.CreateQuote("AAPL", domain.Buy, qty)
	if err != nil {
		r.t.Fatal(err)
	}
	o, _, err := r.store.AcceptQuote(q.ID, key)
	if err != nil {
		r.t.Fatal(err)
	}
	return o
}

func (r *rig) get(id string) domain.Order {
	r.t.Helper()
	o, err := r.store.GetOrder(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return o
}

// tick advances the clock by d and runs one engine step.
func (r *rig) tick(d time.Duration) {
	r.clock.Advance(d)
	r.eng.Tick()
}

func TestQuoteExpiryEmittedOnce(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	q, _ := r.store.CreateQuote("AAPL", domain.Buy, 1)
	r.tick(14 * time.Second)
	if n := len(r.rec.ofType(domain.EventQuoteExpired)); n != 0 {
		t.Fatalf("expired too early (%d events)", n)
	}
	r.tick(time.Second)
	r.tick(time.Second)
	if n := len(r.rec.ofType(domain.EventQuoteExpired)); n != 1 {
		t.Errorf("quote.expired emitted %d times, want 1", n)
	}
	if got, _ := r.store.GetQuote(q.ID); got.Status != domain.QuoteExpired {
		t.Errorf("status = %s", got.Status)
	}
}

func TestAutoFillPartialChunksUntilFilled(t *testing.T) {
	cfg := baseCfg
	cfg.AutoFill, cfg.FillProb = true, 1
	r := newRig(t, 7, cfg)
	o := r.order(10, "k")

	var lastVersion int64
	for i := 0; i < 100 && r.get(o.ID).Status != domain.OrderFilled; i++ {
		r.tick(500 * time.Millisecond)
		cur := r.get(o.ID)
		if cur.FilledQty > cur.Qty || cur.RemainingQty != cur.Qty-cur.FilledQty || cur.Version < lastVersion {
			t.Fatalf("invariant broken: %+v", cur)
		}
		lastVersion = cur.Version
	}
	final := r.get(o.ID)
	if final.Status != domain.OrderFilled {
		t.Fatalf("not filled after 100 ticks: %+v", final)
	}
	// With MaxChunkPct=60 the first chunk is at most 6 of 10, so >= 2 fills.
	if len(final.Fills) < 2 || len(r.rec.ofType(domain.EventOrderPartiallyFilled)) < 1 {
		t.Errorf("expected partial fills, got %d fills", len(final.Fills))
	}
	for _, f := range final.Fills {
		if f.Price != final.Price {
			t.Errorf("fill at %d, want quote price %d", f.Price, final.Price)
		}
	}
}

func TestAutoFillOff(t *testing.T) {
	r := newRig(t, 1, baseCfg) // AutoFill false
	o := r.order(10, "k")
	for range 20 {
		r.tick(500 * time.Millisecond)
	}
	if got := r.get(o.ID); got.FilledQty != 0 || got.Status != domain.OrderOpen {
		t.Errorf("order changed with auto-fill off: %+v", got)
	}
}

func TestCancelResolvesAfterDelay(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	o := r.order(10, "k")
	if _, err := r.store.RequestCancel(o.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(200 * time.Millisecond) // below CancelDelayMin
	if got := r.get(o.ID); got.Status != domain.OrderPendingCancel {
		t.Fatalf("resolved too early: %s", got.Status)
	}
	r.tick(2 * time.Second) // past CancelDelayMax
	if got := r.get(o.ID); got.Status != domain.OrderCancelled {
		t.Errorf("status = %s, want CANCELLED", got.Status)
	}
	if n := len(r.rec.ofType(domain.EventOrderCancelled)); n != 1 {
		t.Errorf("order.cancelled emitted %d times", n)
	}
}

func TestPartialFillDuringPendingCancelThenConfirm(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	o := r.order(10, "k")
	_, _ = r.store.RequestCancel(o.ID)
	if _, err := r.store.FillOrder(o.ID, 6); err != nil {
		t.Fatal(err)
	}
	r.tick(3 * time.Second)
	got := r.get(o.ID)
	if got.Status != domain.OrderCancelled || got.FilledQty != 6 {
		t.Errorf("got %s filled %d, want CANCELLED filled 6", got.Status, got.FilledQty)
	}
}

func TestFillWinsCancelRace(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	o := r.order(10, "k")
	_, _ = r.store.RequestCancel(o.ID)
	_, _ = r.store.FillOrder(o.ID, 10)
	r.tick(3 * time.Second)
	r.tick(3 * time.Second)

	if got := r.get(o.ID); got.Status != domain.OrderFilled {
		t.Errorf("status = %s, want FILLED", got.Status)
	}
	rej := r.rec.ofType(domain.EventOrderCancelRejected)
	if len(rej) != 1 || rej[0].Reason != "too_late_to_cancel" {
		t.Errorf("cancel_rejected events = %+v", rej)
	}
}

func TestNoFillDuringCancelWhenDisabled(t *testing.T) {
	cfg := baseCfg
	cfg.AutoFill, cfg.FillProb, cfg.FillDuringCancel = true, 1, false
	r := newRig(t, 1, cfg)
	o := r.order(10, "k")
	_, _ = r.store.RequestCancel(o.ID)
	r.tick(100 * time.Millisecond) // before the cancel is due
	if got := r.get(o.ID); got.FilledQty != 0 {
		t.Errorf("PENDING_CANCEL order was filled: %+v", got)
	}
}

func TestOrderExpiry(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	open := r.order(10, "a")
	partial := r.order(10, "b")
	_, _ = r.store.FillOrder(partial.ID, 3)
	pending := r.order(10, "c")
	_, _ = r.store.RequestCancel(pending.ID)
	_, _ = r.store.RejectCancel(pending.ID, "") // back to OPEN, should expire too

	r.tick(10*time.Minute - time.Second)
	if got := r.get(open.ID); got.Status != domain.OrderOpen {
		t.Fatalf("expired too early: %s", got.Status)
	}
	r.tick(time.Second)
	for _, id := range []string{open.ID, partial.ID, pending.ID} {
		got := r.get(id)
		if got.Status != domain.OrderExpired || got.Reason != "day_order_expired" {
			t.Errorf("%s: %s %q, want EXPIRED day_order_expired", id, got.Status, got.Reason)
		}
	}
	if got := r.get(partial.ID); got.FilledQty != 3 {
		t.Errorf("expired order lost fills: %d", got.FilledQty)
	}
}

// script runs a fixed scenario and returns a transcript of every event.
func script(t *testing.T, seed uint64) []string {
	cfg := baseCfg
	cfg.AutoFill = true
	r := newRig(t, seed, cfg)
	a := r.order(10, "a")
	r.order(25, "b")
	r.tick(500 * time.Millisecond)
	_, _ = r.store.RequestCancel(a.ID)
	r.order(7, "c")
	_, _ = r.store.CreateQuote("MSFT", domain.Sell, 3) // left to expire
	for range 60 {
		r.tick(500 * time.Millisecond)
	}
	var out []string
	for _, ev := range r.rec.events {
		line := string(ev.Type)
		if ev.Order != nil {
			line += fmt.Sprintf(" %s %s %d/%d v%d", ev.Order.ID, ev.Order.Status, ev.Order.FilledQty, ev.Order.Qty, ev.Order.Version)
		}
		if ev.Quote != nil {
			line += " " + ev.Quote.ID
		}
		out = append(out, line)
	}
	return out
}

func TestDeterminism(t *testing.T) {
	a, b := script(t, 42), script(t, 42)
	if len(a) < 5 {
		t.Fatalf("scenario too quiet: %v", a)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same seed, different runs:\n%v\n%v", a, b)
	}
	if reflect.DeepEqual(a, script(t, 43)) {
		t.Error("different seed produced an identical run (suspicious)")
	}
}

func TestAutoResolveCancelsOff(t *testing.T) {
	r := newRig(t, 1, baseCfg)
	r.eng.SetAutoResolveCancels(false)
	o := r.order(10, "k")
	_, _ = r.store.RequestCancel(o.ID)
	r.tick(time.Minute)
	if got := r.get(o.ID); got.Status != domain.OrderPendingCancel {
		t.Fatalf("status = %s, want PENDING_CANCEL to wait for a manual decision", got.Status)
	}
	r.eng.SetAutoResolveCancels(true)
	r.tick(time.Millisecond)
	if got := r.get(o.ID); got.Status != domain.OrderCancelled {
		t.Errorf("status = %s after turning auto-resolve back on", got.Status)
	}
}
