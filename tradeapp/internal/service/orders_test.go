package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

func (s *Service) mustQuote(t *testing.T, symbol, side string, qty int64) *domain.Quote {
	t.Helper()
	q, err := s.CreateQuote(ctx, symbol, side, qty)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func (s *Service) mustPlace(t *testing.T, quoteID string) *domain.Order {
	t.Helper()
	o, err := s.PlaceOrder(ctx, user, quoteID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// runJobs claims and runs every due job once; it returns how many ran.
func (s *Service) runJobs(t *testing.T) int {
	t.Helper()
	jobs, err := s.ClaimJobs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		_, _ = s.RunJob(ctx, j)
	}
	return len(jobs)
}

func (s *Service) orderNow(t *testing.T, id string) domain.OrderDetail {
	t.Helper()
	o, err := s.GetOrder(ctx, user, id)
	if err != nil {
		t.Fatal(err)
	}
	return *o
}

func (s *Service) jobsWith(t *testing.T, status string) []domain.Job {
	t.Helper()
	jobs, err := s.ListJobs(ctx, status)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestCreateQuote(t *testing.T) {
	s := setup(t)
	q := s.mustQuote(t, "AAPL", "BUY", 10)
	// ask 190.10 × 10 = 1901.00, fee 50 bps = 9.51 → total 1910.51
	if q.Price != 19_010 || q.Fee != 951 || q.Total != 191_051 || q.ExpiresAt.IsZero() || q.CreatedAt.IsZero() {
		t.Fatalf("quote = %+v", q)
	}

	if _, err := s.CreateQuote(ctx, "aapl", "BUY", 1); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("lower-case symbol: %v", err)
	}
	if _, err := s.CreateQuote(ctx, "NOPE", "BUY", 1); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("unknown symbol (provider 400): %v", err)
	}
}

func TestCreateQuoteProviderDown(t *testing.T) {
	s, broker := setupWithBroker(t)
	broker.SetDown(true)
	if _, err := s.CreateQuote(ctx, "AAPL", "BUY", 1); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestPlaceBuyOrderHoldsCashAndQueuesSubmit(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 500_000)
	q := s.mustQuote(t, "AAPL", "BUY", 10)

	o := s.mustPlace(t, q.ID)
	if o.Status != domain.StatusPendingSubmit || o.HoldAmount != q.Total || o.HoldRemaining != q.Total || o.ProviderOrderID != nil {
		t.Fatalf("order = %+v", o)
	}
	if c := s.cashNow(t); c.Held != q.Total || c.Available != 500_000-q.Total {
		t.Fatalf("cash = %+v", c)
	}
	if jobs := s.jobsWith(t, domain.JobPending); len(jobs) != 1 || *jobs[0].OrderID != o.ID {
		t.Fatalf("pending jobs = %+v", jobs)
	}
	select {
	case <-s.JobsReady():
	default:
		t.Fatal("worker was not nudged")
	}
	if a := s.orderNow(t, o.ID).Activity; len(a) != 1 || a[0].Kind != "order.placed" {
		t.Fatalf("activity = %+v", a)
	}
	s.assertInvariants(t)
}

func TestPlaceOrderRefusals(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 100)

	if _, err := s.PlaceOrder(ctx, user, "qt_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown quote: %v", err)
	}

	big := s.mustQuote(t, "AAPL", "BUY", 1)
	if _, err := s.PlaceOrder(ctx, user, big.ID); !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Errorf("not enough cash: %v", err)
	}

	sell := s.mustQuote(t, "AAPL", "SELL", 1)
	if _, err := s.PlaceOrder(ctx, user, sell.ID); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Errorf("no shares: %v", err)
	}

	broker.SetQuoteTTL(time.Second)
	expiring := s.mustQuote(t, "AAPL", "BUY", 1)
	s.advance(2 * time.Second)
	if _, err := s.PlaceOrder(ctx, user, expiring.ID); !errors.Is(err, domain.ErrQuoteExpired) {
		t.Errorf("expired quote: %v", err)
	}

	// Nothing was held or queued by any of the refusals.
	if c := s.cashNow(t); c.Available != 100 || c.Held != 0 {
		t.Errorf("cash = %+v", c)
	}
	if jobs := s.jobsWith(t, domain.JobPending); len(jobs) != 0 {
		t.Errorf("jobs queued: %+v", jobs)
	}
	s.assertInvariants(t)
}

func TestQuoteCanBeUsedOnce(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 1_000_000)
	q := s.mustQuote(t, "AAPL", "BUY", 1)
	s.mustPlace(t, q.ID)
	if _, err := s.PlaceOrder(ctx, user, q.ID); !errors.Is(err, domain.ErrQuoteAlreadyUsed) {
		t.Fatalf("second order: %v", err)
	}
	if c := s.cashNow(t); c.Held != q.Total {
		t.Fatalf("held = %d, want one hold of %d", c.Held, q.Total)
	}
	s.assertInvariants(t)
}

// With enough cash for 10 orders, 20 concurrent orders from different
// quotes: exactly 10 are placed and available cash never goes negative.
func TestNoDoubleSpendConcurrentOrders(t *testing.T) {
	s := setup(t)
	var quotes []*domain.Quote
	for range 20 {
		quotes = append(quotes, s.mustQuote(t, "AAPL", "BUY", 1))
	}
	s.mustFund(t, quotes[0].Total*10)

	var placed, short atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for _, q := range quotes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.PlaceOrder(ctx, user, q.ID)
			switch {
			case err == nil:
				placed.Add(1)
			case errors.Is(err, domain.ErrInsufficientFunds):
				short.Add(1)
			default:
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if placed.Load() != 10 || short.Load() != 10 {
		t.Fatalf("placed %d, refused %d; want 10 and 10", placed.Load(), short.Load())
	}
	if c := s.cashNow(t); c.Available != 0 {
		t.Fatalf("available = %d, want 0", c.Available)
	}
	s.assertInvariants(t)
}

func TestSubmitOpensOrder(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 500_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 10).ID)

	if n := s.runJobs(t); n != 1 {
		t.Fatalf("ran %d jobs", n)
	}
	got := s.orderNow(t, o.ID)
	if got.Status != domain.StatusOpen || got.ProviderOrderID == nil || *got.ProviderOrderID == "" {
		t.Fatalf("order = %+v", got.Order)
	}
	if len(s.jobsWith(t, domain.JobDone)) != 1 || broker.AcceptCalls() != 1 {
		t.Fatal("job not done, or accept not called exactly once")
	}
	// The hold stays until the order reaches a final state.
	if c := s.cashNow(t); c.Held != o.HoldAmount {
		t.Fatalf("held = %d, want %d", c.Held, o.HoldAmount)
	}
	if n := s.runJobs(t); n != 0 {
		t.Fatalf("done job ran again (%d)", n)
	}
	s.assertInvariants(t)
}

func TestSubmitRejectedReleasesHold(t *testing.T) {
	for _, mode := range []string{providertest.AcceptQuoteExpired, providertest.AcceptMarketClosed} {
		t.Run(mode, func(t *testing.T) {
			s, broker := setupWithBroker(t)
			s.mustFund(t, 500_000)
			o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 10).ID)

			broker.SetAcceptMode(mode)
			s.runJobs(t)

			got := s.orderNow(t, o.ID)
			if got.Status != domain.StatusRejected || got.Reason == nil || *got.Reason != mode || got.HoldRemaining != 0 {
				t.Fatalf("order = %+v", got.Order)
			}
			if c := s.cashNow(t); c.Available != 500_000 || c.Held != 0 {
				t.Fatalf("cash = %+v, want everything back", c)
			}
			if len(s.jobsWith(t, domain.JobDone)) != 1 {
				t.Fatal("a rejection is a final answer: the job should be done")
			}
			s.assertInvariants(t)
		})
	}
}

func TestSubmitRejectedSellReleasesShares(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.buyShares(t, "b1", 5, 18_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "SELL", 3).ID)
	if p := s.positionNow(t, "AAPL"); p.HeldQty != 3 {
		t.Fatalf("held_qty = %d, want 3", p.HeldQty)
	}
	s.assertInvariants(t)

	broker.SetAcceptMode(providertest.AcceptMarketClosed)
	s.runJobs(t)

	if got := s.orderNow(t, o.ID); got.Status != domain.StatusRejected {
		t.Fatalf("status = %s", got.Status)
	}
	if p := s.positionNow(t, "AAPL"); p.HeldQty != 0 || p.Qty != 5 {
		t.Fatalf("position = %+v", p)
	}
	s.assertInvariants(t)
}

// The provider creates the order but its answer is lost (500). The retry
// finds the order by client_order_id and adopts it: no second accept.
func TestAmbiguousAcceptIsAdopted(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 500_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 10).ID)

	broker.SetAcceptMode(providertest.AcceptFail500AfterOpen)
	s.runJobs(t)
	if got := s.orderNow(t, o.ID); got.Status != domain.StatusPendingSubmit {
		t.Fatalf("after the 500: %s", got.Status)
	}
	pending := s.jobsWith(t, domain.JobPending)
	if len(pending) != 1 || pending[0].Attempts != 1 || pending[0].LastError == nil {
		t.Fatalf("job after the 500: %+v", pending)
	}

	if n := s.runJobs(t); n != 0 {
		t.Fatal("the retry ran before its backoff")
	}
	s.advance(2 * time.Second)
	s.runJobs(t)

	got := s.orderNow(t, o.ID)
	if got.Status != domain.StatusOpen || got.ProviderOrderID == nil {
		t.Fatalf("after the retry: %+v", got.Order)
	}
	if broker.AcceptCalls() != 1 || broker.OrderCount() != 1 {
		t.Fatalf("accept calls %d, broker orders %d; want 1 and 1", broker.AcceptCalls(), broker.OrderCount())
	}
	s.assertInvariants(t)
}

// The provider is down for every attempt: the job ends in the dead-letter
// queue, the order waits in PENDING_SUBMIT with its hold, and a manual retry
// once the provider is back submits it.
func TestProviderDownEndsInDeadLetterQueue(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 500_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 10).ID)
	broker.SetDown(true)

	for attempt := 1; attempt <= s.cfg.JobMaxAttempts; attempt++ {
		if n := s.runJobs(t); n != 1 {
			t.Fatalf("attempt %d: ran %d jobs", attempt, n)
		}
		s.advance(time.Duration(attempt) * time.Minute)
	}

	dead := s.jobsWith(t, domain.JobDead)
	if len(dead) != 1 || dead[0].Attempts != s.cfg.JobMaxAttempts || dead[0].LastError == nil {
		t.Fatalf("dead jobs = %+v", dead)
	}
	if n := s.runJobs(t); n != 0 {
		t.Fatal("a dead job ran")
	}
	if got := s.orderNow(t, o.ID); got.Status != domain.StatusPendingSubmit {
		t.Fatalf("order = %s", got.Status)
	}
	s.assertInvariants(t)

	if _, err := s.RetryJob(ctx, 999_999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("retry unknown job: %v", err)
	}
	broker.SetDown(false)
	retried, err := s.RetryJob(ctx, dead[0].ID)
	if err != nil || retried.Status != domain.JobPending || retried.Attempts != 0 {
		t.Fatalf("retry: %+v, %v", retried, err)
	}
	if _, err := s.RetryJob(ctx, dead[0].ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("retrying a pending job: %v", err)
	}

	s.runJobs(t)
	if got := s.orderNow(t, o.ID); got.Status != domain.StatusOpen {
		t.Fatalf("after retry: %s", got.Status)
	}
	s.assertInvariants(t)
}

func TestListOrders(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 1_000_000)
	open := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	s.runJobs(t)
	broker.SetAcceptMode(providertest.AcceptMarketClosed)
	rejected := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	s.runJobs(t)

	all, err := s.ListOrders(ctx, user, "all")
	if err != nil || len(all) != 2 || all[0].ID != rejected.ID {
		t.Fatalf("all = %+v, %v (want newest first)", all, err)
	}
	openOnly, err := s.ListOrders(ctx, user, "open")
	if err != nil || len(openOnly) != 1 || openOnly[0].ID != open.ID {
		t.Fatalf("open = %+v, %v", openOnly, err)
	}
	if _, err := s.ListOrders(ctx, user, "nope"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("bad status: %v", err)
	}
	if _, err := s.GetOrder(ctx, user, "not-an-order-id"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := s.GetOrder(ctx, user, lid.NewOrder()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestAccountValuesPositionsAtBid(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.buyShares(t, "b1", 10, 18_000) // AAPL bid in the fake broker is 189.90

	summary, err := s.GetAccount(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	p := summary.Positions[0]
	if *p.LastPrice != 18_990 || *p.MarketValue != 189_900 || *p.UnrealizedPnL != 9_900 {
		t.Fatalf("position = %+v", p)
	}
	if *summary.PositionsValue != 189_900 || *summary.UnrealizedPnL != 9_900 {
		t.Fatalf("summary = %+v", summary)
	}

	// Without prices the account still loads, just without market values.
	broker.SetDown(true)
	s.advance(time.Minute) // past the price cache
	summary, err = s.GetAccount(ctx, user)
	if err != nil || summary.PositionsValue != nil || summary.Positions[0].MarketValue != nil {
		t.Fatalf("broker down: %+v, %v", summary, err)
	}
}
