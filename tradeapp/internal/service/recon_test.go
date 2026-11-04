package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
)

func (s *Service) mustRecon(t *testing.T) *domain.ReconReport {
	t.Helper()
	r, err := s.RunReconciliation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func kinds(ms []domain.Mismatch) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Kind)
	}
	return out
}

func TestReconCleanBooks(t *testing.T) {
	s, broker := setupWithBroker(t)
	_, pid := s.openBuy(t, broker, 10)
	s.deliver(t, broker.Fill(t, pid, 0)) // FILLED
	s.openBuy(t, broker, 5)              // still OPEN

	if _, err := s.LatestReconciliation(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("latest before any run: %v", err)
	}

	// Everything just changed: too fresh to judge.
	fresh := s.mustRecon(t)
	if fresh.OrdersChecked != 0 || fresh.OrdersSkipped != 2 || !fresh.OK {
		t.Fatalf("fresh run = %+v", fresh)
	}

	s.advance(time.Minute)
	r := s.mustRecon(t)
	if !r.OK || r.OrdersChecked != 2 || len(r.Mismatches) != 0 || !r.InvariantsOK || r.OpenBreaks != 0 {
		t.Fatalf("report = %+v", r)
	}
	if !strings.HasPrefix(r.RunID, "recon-") {
		t.Fatalf("run id = %q", r.RunID)
	}
	latest, err := s.LatestReconciliation(ctx)
	if err != nil || latest.RunID != r.RunID || latest.OrdersChecked != 2 {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
}

// Webhooks were dropped and the sweeper hasn't run yet: recon sees the gap.
func TestReconFindsDroppedFills(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	lost := broker.Fill(t, pid, 0)
	s.advance(time.Minute)

	r := s.mustRecon(t)
	got := kinds(r.Mismatches)
	want := []string{domain.MismatchStatus, domain.MismatchFilledQty, domain.MismatchFillMissingLocal}
	if r.OK || len(got) != len(want) {
		t.Fatalf("mismatches = %+v", r.Mismatches)
	}
	for i := range want {
		if got[i] != want[i] || r.Mismatches[i].OrderID != orderID {
			t.Fatalf("mismatch %d = %+v, want %s", i, r.Mismatches[i], want[i])
		}
	}
	if r.Mismatches[0].Local != "OPEN" || r.Mismatches[0].Provider != "FILLED" || r.Mismatches[2].Provider != lost.Data.Fill.ID {
		t.Fatalf("details = %+v", r.Mismatches)
	}

	// The sweeper catches up; the next run is clean.
	if _, err := s.SweepStuckOrders(ctx); err != nil {
		t.Fatal(err)
	}
	s.advance(2 * time.Minute)
	if r := s.mustRecon(t); !r.OK {
		t.Fatalf("after sweep: %+v", r.Mismatches)
	}
}

func TestReconFindsOrdersOnOneSideOnly(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 3)
	broker.Forget(t, pid) // the broker lost our order

	// Someone else's order at the broker.
	c := provider.New(broker.URL, providertest.APIKey, 2*time.Second, nil)
	q, err := c.CreateQuote(ctx, "MSFT", "BUY", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcceptQuote(ctx, q.ID, "foreign-client-1"); err != nil {
		t.Fatal(err)
	}

	s.advance(time.Minute)
	r := s.mustRecon(t)
	if r.OK || len(r.Mismatches) != 2 {
		t.Fatalf("mismatches = %+v", r.Mismatches)
	}
	byKind := map[string]domain.Mismatch{}
	for _, m := range r.Mismatches {
		byKind[m.Kind] = m
	}
	if m := byKind[domain.MismatchMissingAtProvider]; m.OrderID != orderID || m.ProviderOrderID != pid {
		t.Errorf("missing at provider = %+v", m)
	}
	if m := byKind[domain.MismatchUnknownToUs]; !strings.Contains(m.Provider, "foreign-client-1") {
		t.Errorf("unknown to us = %+v", m)
	}
}

// Never-sent orders (refused at submit, or cancelled before sending) are
// not expected at the provider.
func TestReconIgnoresNeverSentOrders(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 1_000_000)
	cancelled := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	if _, _, err := s.CancelOrder(ctx, user, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	broker.SetAcceptMode(providertest.AcceptMarketClosed)
	s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	s.runJobs(t)

	s.advance(time.Minute)
	if r := s.mustRecon(t); !r.OK || len(r.Mismatches) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestCompareOrder(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	cmp, err := s.CompareOrder(ctx, user, orderID)
	if err != nil || !cmp.Agree || cmp.Provider == nil || cmp.Provider.ID != pid {
		t.Fatalf("in sync: %+v, %v", cmp, err)
	}

	broker.Fill(t, pid, 4) // dropped webhook; no settling window here
	cmp, err = s.CompareOrder(ctx, user, strings.ToLower(orderID))
	if err != nil || cmp.Agree || len(cmp.Mismatches) != 3 || cmp.Provider.Status != "PARTIALLY_FILLED" {
		t.Fatalf("after dropped fill: %+v, %v", cmp, err)
	}

	// Not yet sent: the provider not knowing it is expected.
	pendingOrder := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	cmp, err = s.CompareOrder(ctx, user, pendingOrder.ID)
	if err != nil || cmp.Provider != nil || !cmp.Agree {
		t.Fatalf("unsent: %+v, %v", cmp, err)
	}

	if _, err := s.CompareOrder(ctx, user, "order-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	broker.SetDown(true)
	if _, err := s.CompareOrder(ctx, user, orderID); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Fatalf("broker down: %v", err)
	}
}

func TestOrderIDsAreNormalized(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, _ := s.openBuy(t, broker, 1)
	for _, variant := range []string{strings.ToLower(orderID), strings.ToUpper(orderID)} {
		got, err := s.GetOrder(ctx, user, variant)
		if err != nil || got.ID != orderID {
			t.Fatalf("GetOrder(%q) = %v, %v", variant, got, err)
		}
	}
	if _, pending, err := s.CancelOrder(ctx, user, strings.ToUpper(orderID)); err != nil || !pending {
		t.Fatalf("cancel by upper-case id: pending=%v err=%v", pending, err)
	}
}

// The broker is down for every cancel attempt; once it is back, the sweeper
// re-queues the dead cancel and the order gets cancelled.
func TestSweeperRequeuesDeadCancel(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	if _, _, err := s.CancelOrder(ctx, user, orderID); err != nil {
		t.Fatal(err)
	}

	broker.SetDown(true)
	for range s.cfg.JobMaxAttempts {
		s.runJobs(t)
		s.advance(10 * time.Minute)
	}
	if dead := s.jobsWith(t, domain.JobDead); len(dead) != 1 || dead[0].Kind != domain.JobCancelOrder {
		t.Fatalf("dead = %+v", dead)
	}

	broker.SetDown(false)
	if _, err := s.SweepStuckOrders(ctx); err != nil {
		t.Fatal(err)
	}
	if pending := s.jobsWith(t, domain.JobPending); len(pending) != 1 || pending[0].Kind != domain.JobCancelOrder {
		t.Fatalf("not re-queued: %+v", pending)
	}
	s.runJobs(t) // the cancel reaches the broker
	s.deliver(t, broker.ConfirmCancel(t, pid))

	if got := s.orderNow(t, orderID); got.Status != domain.StatusCancelled || got.HoldRemaining != 0 {
		t.Fatalf("order = %+v", got.Order)
	}
	s.assertInvariants(t)
}
