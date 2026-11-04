package store

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
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

func (r *recorder) count(t domain.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ev := range r.events {
		if ev.Type == t {
			n++
		}
	}
	return n
}

var testCfg = Config{
	QuoteTTL:       15 * time.Second,
	FeeBps:         50,
	CancelDelayMin: 300 * time.Millisecond,
	CancelDelayMax: 2 * time.Second,
	MarketOpen:     true,
}

func newTestStore(t *testing.T) (*Store, *clock.Fake, *recorder) {
	t.Helper()
	syms, err := market.ParseSymbols("AAPL:19000,MSFT:41000")
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC))
	mkt := market.New(syms, 20, rand.New(rand.NewPCG(42, 1)))
	rec := &recorder{}
	s := New(testCfg, clk, rand.New(rand.NewPCG(42, 2)), mkt, rec.sink)
	return s, clk, rec
}

func mustQuote(t *testing.T, s *Store) domain.Quote {
	t.Helper()
	q, err := s.CreateQuote("AAPL", domain.Buy, 10)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCreateQuote(t *testing.T) {
	s, _, _ := newTestStore(t)
	buy, _ := s.CreateQuote("AAPL", domain.Buy, 10)
	sell, _ := s.CreateQuote("AAPL", domain.Sell, 10)
	if buy.Price != 19019 || sell.Price != 18981 {
		t.Errorf("buy at %d (want ask 19019), sell at %d (want bid 18981)", buy.Price, sell.Price)
	}
	if buy.ID == sell.ID {
		t.Error("duplicate ids")
	}
	for _, tc := range []struct {
		sym  string
		side domain.Side
		qty  int64
	}{{"NOPE", domain.Buy, 1}, {"AAPL", "HOLD", 1}, {"AAPL", domain.Buy, 0}} {
		if _, err := s.CreateQuote(tc.sym, tc.side, tc.qty); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%+v: %v, want validation_error", tc, err)
		}
	}
}

func TestAcceptIdempotency(t *testing.T) {
	s, _, rec := newTestStore(t)
	q1, q2 := mustQuote(t, s), mustQuote(t, s)

	o1, created, err := s.AcceptQuote(q1.ID, "key-1")
	if err != nil || !created || o1.Status != domain.OrderOpen {
		t.Fatalf("first accept: %+v created=%v err=%v", o1, created, err)
	}
	o2, created, err := s.AcceptQuote(q1.ID, "key-1")
	if err != nil || created || o2.ID != o1.ID {
		t.Fatalf("replay: id %s created=%v err=%v, want same order", o2.ID, created, err)
	}
	if n := rec.count(domain.EventOrderAccepted); n != 1 {
		t.Errorf("order.accepted emitted %d times, want 1", n)
	}

	if _, _, err := s.AcceptQuote(q2.ID, "key-1"); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Errorf("same key, other quote: %v", err)
	}
	if _, _, err := s.AcceptQuote(q1.ID, "key-2"); !errors.Is(err, domain.ErrQuoteAlreadyAccepted) {
		t.Errorf("other key, same quote: %v", err)
	}
	if _, _, err := s.AcceptQuote("qt_nope", "key-3"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown quote: %v", err)
	}
	if _, _, err := s.AcceptQuote(q2.ID, ""); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty key: %v", err)
	}

	byKey, err := s.GetOrderByClientOrderID("key-1")
	if err != nil || byKey.ID != o1.ID {
		t.Errorf("lookup by client_order_id: %v %v", byKey.ID, err)
	}
	gotQ, _ := s.GetQuote(q1.ID)
	if gotQ.Status != domain.QuoteAccepted || gotQ.OrderID != o1.ID {
		t.Errorf("quote after accept: %+v", gotQ)
	}
}

func TestAcceptExpiredQuote(t *testing.T) {
	s, clk, rec := newTestStore(t)
	q := mustQuote(t, s)
	clk.Advance(testCfg.QuoteTTL)

	if _, _, err := s.AcceptQuote(q.ID, "k"); !errors.Is(err, domain.ErrQuoteExpired) {
		t.Fatalf("accept after TTL: %v", err)
	}
	if got, _ := s.GetQuote(q.ID); got.Status != domain.QuoteExpired {
		t.Errorf("GetQuote status = %s, want EXPIRED", got.Status)
	}
	if err := s.ExpireQuote(q.ID); err != nil {
		t.Fatal(err)
	}
	_ = s.ExpireQuote(q.ID)
	if n := rec.count(domain.EventQuoteExpired); n != 1 {
		t.Errorf("quote.expired emitted %d times, want 1", n)
	}
	// The key was not consumed by the failed accept.
	if _, err := s.GetOrderByClientOrderID("k"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("failed accept left an order behind: %v", err)
	}
}

func TestAcceptMarketClosed(t *testing.T) {
	s, _, _ := newTestStore(t)
	q := mustQuote(t, s)
	s.SetMarketOpen(false)
	if _, _, err := s.AcceptQuote(q.ID, "k"); !errors.Is(err, domain.ErrMarketClosed) {
		t.Errorf("got %v, want market_closed", err)
	}
}

func TestConcurrentAcceptSameKey(t *testing.T) {
	s, _, rec := newTestStore(t)
	q := mustQuote(t, s)

	var wg sync.WaitGroup
	ids := make([]string, 50)
	created := make([]bool, 50)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, c, err := s.AcceptQuote(q.ID, "same-key")
			if err != nil {
				t.Error(err)
			}
			ids[i], created[i] = o.ID, c
		}()
	}
	wg.Wait()

	nCreated := 0
	for i := range 50 {
		if ids[i] != ids[0] {
			t.Fatalf("got different order ids %s and %s", ids[0], ids[i])
		}
		if created[i] {
			nCreated++
		}
	}
	if nCreated != 1 || rec.count(domain.EventOrderAccepted) != 1 || len(s.ListOrders(ListFilter{})) != 1 {
		t.Errorf("created=%d events=%d orders=%d, want 1/1/1",
			nCreated, rec.count(domain.EventOrderAccepted), len(s.ListOrders(ListFilter{})))
	}
}

func TestConcurrentAcceptDifferentKeys(t *testing.T) {
	s, _, rec := newTestStore(t)
	q := mustQuote(t, s)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, conflicts := 0, 0
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.AcceptQuote(q.ID, fmt.Sprintf("key-%d", i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrQuoteAlreadyAccepted):
				conflicts++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || conflicts != 49 || rec.count(domain.EventOrderAccepted) != 1 {
		t.Errorf("ok=%d conflicts=%d events=%d, want 1/49/1", ok, conflicts, rec.count(domain.EventOrderAccepted))
	}
}

func TestCancelIdempotencyAndFinal(t *testing.T) {
	s, clk, rec := newTestStore(t)
	o, _, _ := s.AcceptQuote(mustQuote(t, s).ID, "k")

	for range 2 {
		got, err := s.RequestCancel(o.ID)
		if err != nil || got.Status != domain.OrderPendingCancel {
			t.Fatalf("cancel: %s %v", got.Status, err)
		}
	}
	if n := rec.count(domain.EventOrderPendingCancel); n != 1 {
		t.Errorf("order.pending_cancel emitted %d times, want 1", n)
	}

	s.mu.Lock()
	delay := s.orders[o.ID].CancelDueAt.Sub(clk.Now())
	s.mu.Unlock()
	if delay < testCfg.CancelDelayMin || delay > testCfg.CancelDelayMax {
		t.Errorf("cancel delay %s outside [%s, %s]", delay, testCfg.CancelDelayMin, testCfg.CancelDelayMax)
	}

	if _, err := s.ResolveCancel(o.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.RequestCancel(o.ID)
	if !errors.Is(err, domain.ErrOrderFinal) || got.ID != o.ID || got.Status != domain.OrderCancelled {
		t.Errorf("cancel on final: %+v %v, want order_final with order", got, err)
	}
	if _, err := s.RequestCancel("ord_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown order: %v", err)
	}
}

func TestStoreFillAndEventOrder(t *testing.T) {
	s, _, rec := newTestStore(t)
	o, _, _ := s.AcceptQuote(mustQuote(t, s).ID, "k")
	_, _ = s.FillOrder(o.ID, 4)
	_, _ = s.RequestCancel(o.ID)
	_, _ = s.FillOrder(o.ID, 6)
	_, _ = s.ResolveCancel(o.ID)

	want := []domain.EventType{
		domain.EventOrderAccepted, domain.EventOrderPartiallyFilled, domain.EventOrderPendingCancel,
		domain.EventOrderFilled, domain.EventOrderCancelRejected,
	}
	if len(rec.events) != len(want) {
		t.Fatalf("got %d events, want %d", len(rec.events), len(want))
	}
	var lastVersion int64
	for i, ev := range rec.events {
		if ev.Type != want[i] {
			t.Errorf("event %d = %s, want %s", i, ev.Type, want[i])
		}
		if ev.Order.Version < lastVersion {
			t.Errorf("event %d version went down", i)
		}
		lastVersion = ev.Order.Version
	}
	got, _ := s.GetOrder(o.ID)
	if got.Status != domain.OrderFilled || got.FilledQty != 10 || len(got.Fills) != 2 {
		t.Errorf("final order: %+v", got)
	}
}

func TestListOrders(t *testing.T) {
	s, clk, _ := newTestStore(t)
	var ids []string
	for i := range 3 {
		o, _, err := s.AcceptQuote(mustQuote(t, s).ID, fmt.Sprintf("k%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
		clk.Advance(time.Second)
	}
	_, _ = s.FillOrder(ids[0], 1) // ids[0] -> PARTIALLY_FILLED, updated at t0+3s
	_, _ = s.RejectOrder(ids[1], "x")

	all := s.ListOrders(ListFilter{})
	if len(all) != 3 || all[0].ID != ids[2] || all[2].ID != ids[0] {
		t.Errorf("not newest first: %v", all)
	}
	working := s.ListOrders(ListFilter{Statuses: []domain.OrderStatus{domain.OrderOpen, domain.OrderPartiallyFilled}})
	if len(working) != 2 || working[0].ID != ids[2] || working[1].ID != ids[0] {
		t.Errorf("status filter: %v", working)
	}
	since := s.ListOrders(ListFilter{Since: clk.Now()})
	if len(since) != 2 { // ids[0] (filled) and ids[1] (rejected) were updated at now
		t.Errorf("since filter returned %d orders, want 2", len(since))
	}
}
