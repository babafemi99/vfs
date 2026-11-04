package service

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

// deliver receives a webhook event and runs the jobs it queued.
func (s *Service) deliver(t *testing.T, e provider.Event) {
	t.Helper()
	if _, err := s.ReceiveProviderEvent(ctx, providertest.Body(t, e)); err != nil {
		t.Fatal(err)
	}
	s.runJobs(t)
}

// openBuy funds the account, places a BUY and lets the worker submit it.
// It returns our order id and the provider's.
func (s *Service) openBuy(t *testing.T, broker *providertest.Broker, qty int64) (string, string) {
	t.Helper()
	s.mustFund(t, 10_000_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", qty).ID)
	s.runJobs(t)
	if got := s.orderNow(t, o.ID); got.Status != domain.StatusOpen {
		t.Fatalf("order not open: %s", got.Status)
	}
	return o.ID, broker.OrderIDFor(t, o.ID)
}

func (s *Service) breaksNow(t *testing.T) []domain.Break {
	t.Helper()
	b, err := s.ListBreaks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPartialThenFullFill(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	start := s.cashNow(t)

	s.deliver(t, broker.Fill(t, pid, 4))
	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusPartiallyFilled || got.FilledQty != 4 || len(got.Fills) != 1 {
		t.Fatalf("after partial: %+v", got.Order)
	}
	s.assertInvariants(t)

	s.deliver(t, broker.Fill(t, pid, 0))
	got = s.orderNow(t, orderID)
	if got.Status != domain.StatusFilled || got.FilledQty != 10 || got.HoldRemaining != 0 || len(got.Fills) != 2 {
		t.Fatalf("after full: %+v", got.Order)
	}

	// Paid: each fill's qty × price plus its fee.
	var cost int64
	for _, f := range got.Fills {
		cost += f.Qty*f.Price + f.Fee
	}
	if c := s.cashNow(t); c.Held != 0 || c.Available != start.Total-cost {
		t.Fatalf("cash = %+v, want %d available and nothing held", c, start.Total-cost)
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 10 || p.AvgCost != 19_010 {
		t.Fatalf("position = %+v", p)
	}
	if len(s.breaksNow(t)) != 0 {
		t.Fatalf("unexpected breaks: %+v", s.breaksNow(t))
	}
	s.assertInvariants(t)
}

func TestDuplicateWebhookAppliedOnce(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	e := broker.Fill(t, pid, 4)
	s.deliver(t, e)
	s.deliver(t, e)

	var events int
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		events, err = s.eventRepo.CountEvents(ctx, tx)
		return err
	})
	got := s.orderNow(t, orderID)
	if events != 1 || len(got.Fills) != 1 || got.FilledQty != 4 {
		t.Fatalf("events %d, fills %d, filled %d", events, len(got.Fills), got.FilledQty)
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 4 {
		t.Fatalf("position qty = %d, want 4", p.Qty)
	}
	s.assertInvariants(t)
}

// order.filled (newer version) arrives before order.partially_filled.
func TestOutOfOrderEvents(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	partial := broker.Fill(t, pid, 4) // version 2
	full := broker.Fill(t, pid, 0)    // version 3
	s.deliver(t, full)
	s.deliver(t, partial)

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusFilled || got.FilledQty != 10 || len(got.Fills) != 2 || got.HoldRemaining != 0 {
		t.Fatalf("order = %+v, fills %d", got.Order, len(got.Fills))
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 10 {
		t.Fatalf("position qty = %d", p.Qty)
	}
	if b := s.breaksNow(t); len(b) != 0 {
		t.Fatalf("an in-order fill was flagged: %+v", b)
	}
	s.assertInvariants(t)
}

// A fill lands while the cancel is pending; then the cancel completes.
func TestCancelRaceWithFill(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	held := s.cashNow(t).Held

	order, pending, err := s.CancelOrder(ctx, user, orderID)
	if err != nil || !pending || order.Status != domain.StatusPendingCancel {
		t.Fatalf("cancel: %+v pending=%v err=%v", order, pending, err)
	}
	if c := s.cashNow(t); c.Held != held {
		t.Fatal("cash was released on the cancel request")
	}
	s.runJobs(t) // cancel_order job: DELETE at the broker

	s.deliver(t, broker.Fill(t, pid, 4))
	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusPendingCancel || got.FilledQty != 4 {
		t.Fatalf("fill during cancel: %+v", got.Order)
	}

	s.deliver(t, broker.ConfirmCancel(t, pid))
	got = s.orderNow(t, orderID)
	if got.Status != domain.StatusCancelled || got.FilledQty != 4 || got.HoldRemaining != 0 {
		t.Fatalf("after cancel: %+v", got.Order)
	}
	if line := domain.OutcomeLine(got.Order); line != "4 of 10 bought, 6 cancelled" {
		t.Fatalf("outcome = %q", line)
	}
	if c := s.cashNow(t); c.Held != 0 {
		t.Fatalf("held = %d after the final state", c.Held)
	}
	s.assertInvariants(t)

	if _, _, err := s.CancelOrder(ctx, user, orderID); !errors.Is(err, domain.ErrOrderFinal) {
		t.Fatalf("cancel a final order: %v", err)
	}
}

// The order fills completely before the cancel reaches it; the broker's
// too-late cancel_rejected changes nothing.
func TestCancelTooLateAfterFill(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	if _, _, err := s.CancelOrder(ctx, user, orderID); err != nil {
		t.Fatal(err)
	}
	s.deliver(t, broker.Fill(t, pid, 0)) // fills before the cancel job runs
	s.runJobs(t)                         // cancel job: broker says order_final
	s.deliver(t, broker.RejectCancel(t, pid))

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusFilled || got.FilledQty != 10 {
		t.Fatalf("order = %+v", got.Order)
	}
	if dead := s.jobsWith(t, domain.JobDead); len(dead) != 0 {
		t.Fatalf("cancel job died: %+v", dead)
	}
	s.assertInvariants(t)
}

func TestCancelRejectedOrderKeepsWorking(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	s.deliver(t, broker.Fill(t, pid, 3))

	if _, _, err := s.CancelOrder(ctx, user, orderID); err != nil {
		t.Fatal(err)
	}
	s.runJobs(t)
	s.deliver(t, broker.RejectCancel(t, pid))

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusPartiallyFilled || got.FilledQty != 3 {
		t.Fatalf("order = %+v", got.Order)
	}
	s.assertInvariants(t)
}

func TestCancelBeforeSubmit(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 1_000_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)

	got, pending, err := s.CancelOrder(ctx, user, o.ID)
	if err != nil || pending || got.Status != domain.StatusCancelled || got.HoldRemaining != 0 {
		t.Fatalf("cancel: %+v pending=%v err=%v", got, pending, err)
	}
	if c := s.cashNow(t); c.Held != 0 || c.Available != 1_000_000 {
		t.Fatalf("cash = %+v", c)
	}
	if n := s.runJobs(t); n != 0 || broker.AcceptCalls() != 0 {
		t.Fatalf("submit still ran: jobs %d, accepts %d", n, broker.AcceptCalls())
	}
	s.assertInvariants(t)
}

func TestCancelWhileSubmittingIsRefused(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 1_000_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)

	if jobs, _ := s.ClaimJobs(ctx, 10); len(jobs) != 1 { // the worker has it, mid-flight
		t.Fatal("submit job not claimed")
	}
	if _, _, err := s.CancelOrder(ctx, user, o.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancel while submitting: %v", err)
	}
	if _, _, err := s.CancelOrder(ctx, user, lid.NewOrder()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown order: %v", err)
	}
}

func TestExpireAndRejectReleaseHolds(t *testing.T) {
	s, broker := setupWithBroker(t)
	expiring, pid := s.openBuy(t, broker, 10)
	s.deliver(t, broker.Fill(t, pid, 3))
	s.deliver(t, broker.Expire(t, pid))

	got := s.orderNow(t, expiring)
	if got.Status != domain.StatusExpired || got.HoldRemaining != 0 || got.Reason == nil || *got.Reason != "day_order_expired" {
		t.Fatalf("expired: %+v", got.Order)
	}
	if line := domain.OutcomeLine(got.Order); line != "Expired: 3 of 10 bought" {
		t.Fatalf("outcome = %q", line)
	}

	rejected := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	s.runJobs(t)
	s.deliver(t, broker.Reject(t, broker.OrderIDFor(t, rejected.ID)))
	if got := s.orderNow(t, rejected.ID); got.Status != domain.StatusRejected || got.HoldRemaining != 0 {
		t.Fatalf("rejected: %+v", got.Order)
	}
	if c := s.cashNow(t); c.Held != 0 {
		t.Fatalf("held = %d", c.Held)
	}
	s.assertInvariants(t)
}

// A fill arrives for an order we already consider final: it is still
// recorded (a fill is a fact) and a break is raised.
func TestLateFillAfterFinal(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	s.deliver(t, broker.Fill(t, pid, 4))
	if _, _, err := s.CancelOrder(ctx, user, orderID); err != nil {
		t.Fatal(err)
	}
	s.runJobs(t)
	s.deliver(t, broker.ConfirmCancel(t, pid))

	// The broker later reports another fill on the cancelled order.
	snap := broker.Snapshot(t, pid)
	var e provider.Event
	e.ID, e.Type = "evt_late", "order.partially_filled"
	e.Data.Order = &snap
	e.Data.Fill = &provider.Fill{ID: "fill_late", Qty: 1, Price: snap.Price, Fee: 10}
	s.deliver(t, e)

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusCancelled || len(got.Fills) != 2 {
		t.Fatalf("order = %+v, fills %d", got.Order, len(got.Fills))
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 5 {
		t.Fatalf("position qty = %d, want 5 (the late fill counts)", p.Qty)
	}
	breaks := s.breaksNow(t)
	if len(breaks) != 1 || breaks[0].Kind != domain.BreakFillAfterFinal {
		t.Fatalf("breaks = %+v", breaks)
	}
	s.assertInvariants(t)
}

func TestSellOrderLifecycle(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.buyShares(t, "b1", 5, 18_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "SELL", 3).ID)
	s.runJobs(t)
	pid := broker.OrderIDFor(t, o.ID)

	s.deliver(t, broker.Fill(t, pid, 0))

	got := s.orderNow(t, o.ID)
	if got.Status != domain.StatusFilled || got.HoldRemaining != 0 {
		t.Fatalf("order = %+v", got.Order)
	}
	p := s.positionNow(t, "AAPL")
	// Sold 3 at 189.90 (bid) with fee 2.85: (18990 − 18000) × 3 − 285 = 2685
	if p.Qty != 2 || p.HeldQty != 0 || p.RealizedPnL != 2_685 {
		t.Fatalf("position = %+v", p)
	}
	if c := s.cashNow(t); c.Available != 3*18_990-285 {
		t.Fatalf("available = %d, want %d", c.Available, 3*18_990-285)
	}
	s.assertInvariants(t)
}

func TestUnknownOrderEventIsNoted(t *testing.T) {
	s := setup(t)
	var e provider.Event
	e.ID, e.Type = "evt_x", "order.filled"
	e.Data.Order = &provider.Order{ID: "ord_unknown", ClientOrderID: lid.NewOrder(), Status: "FILLED", Version: 3}
	s.deliver(t, e)

	var ev domain.ProviderEvent
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		ev, err = s.eventRepo.GetEventForUpdate(ctx, tx, "evt_x")
		return err
	})
	if ev.ProcessedAt == nil || ev.ProcessError == nil || *ev.ProcessError != "unknown_order" {
		t.Fatalf("event = %+v", ev)
	}
	if dead := s.jobsWith(t, domain.JobDead); len(dead) != 0 {
		t.Fatalf("an unknown order is not a failure: %+v", dead)
	}
}

func TestReceiveRejectsBadBodies(t *testing.T) {
	s := setup(t)
	for _, body := range []string{`nope`, `{}`, `{"id":"evt_1"}`} {
		if _, err := s.ReceiveProviderEvent(ctx, []byte(body)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s: %v", body, err)
		}
	}
}

// Every webhook is lost; the sweeper finds the quiet order and catches up.
func TestSweeperRecoversDroppedWebhooks(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)
	dropped1 := broker.Fill(t, pid, 4)
	dropped2 := broker.Fill(t, pid, 0)

	if n, err := s.SweepStuckOrders(ctx); err != nil || n != 0 {
		t.Fatalf("swept a fresh order: %d, %v", n, err)
	}
	s.advance(time.Minute)
	if n, err := s.SweepStuckOrders(ctx); err != nil || n != 1 {
		t.Fatalf("sweep: %d, %v", n, err)
	}

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusFilled || got.FilledQty != 10 || len(got.Fills) != 2 || got.HoldRemaining != 0 {
		t.Fatalf("after sweep: %+v, fills %d", got.Order, len(got.Fills))
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 10 {
		t.Fatalf("position qty = %d", p.Qty)
	}
	s.assertInvariants(t)

	// The dropped webhooks finally arrive: nothing changes.
	s.deliver(t, dropped2)
	s.deliver(t, dropped1)
	if again := s.orderNow(t, orderID); again.FilledQty != 10 || len(again.Fills) != 2 {
		t.Fatalf("late webhooks changed the order: %+v", again.Order)
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 10 {
		t.Fatalf("late webhooks changed the position: %d", p.Qty)
	}
	s.assertInvariants(t)
}

// The submit job gave up, the broker never had the order and the quote has
// expired: the sweeper rejects it and gives the cash back.
func TestSweeperResolvesNeverSubmittedOrder(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.mustFund(t, 1_000_000)
	o := s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	broker.SetDown(true)
	for range s.cfg.JobMaxAttempts {
		s.runJobs(t)
		s.advance(10 * time.Minute)
	}
	if len(s.jobsWith(t, domain.JobDead)) != 1 {
		t.Fatal("submit job not dead")
	}
	broker.SetDown(false)

	if _, err := s.SweepStuckOrders(ctx); err != nil {
		t.Fatal(err)
	}
	got := s.orderNow(t, o.ID)
	if got.Status != domain.StatusRejected || got.Reason == nil || *got.Reason != "submit_failed" || got.HoldRemaining != 0 {
		t.Fatalf("order = %+v", got.Order)
	}
	if c := s.cashNow(t); c.Available != 1_000_000 {
		t.Fatalf("available = %d", c.Available)
	}
	if b := s.breaksNow(t); len(b) != 1 || b[0].Kind != domain.BreakSubmitNeverAccepted {
		t.Fatalf("breaks = %+v", b)
	}
	s.assertInvariants(t)
}

// Two partial fills arrive newest first. The older snapshot must not move
// filled_qty or the status backwards.
func TestOlderSnapshotDoesNotRewind(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	first := broker.Fill(t, pid, 4)  // version 2, filled 4
	second := broker.Fill(t, pid, 2) // version 3, filled 6
	s.deliver(t, second)
	s.deliver(t, first)

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusPartiallyFilled || got.FilledQty != 6 || got.ProviderVersion != 3 || len(got.Fills) != 2 {
		t.Fatalf("order = %+v, fills %d", got.Order, len(got.Fills))
	}
	s.assertInvariants(t)
}

// The broker fills part of the order before it sees our cancel; the fill's
// webhook arrives after we marked the order PENDING_CANCEL. We stay
// PENDING_CANCEL ("Cancelling… 4 of 10 filled") until the broker decides.
func TestFillFromBeforeTheCancelKeepsPendingCancel(t *testing.T) {
	s, broker := setupWithBroker(t)
	orderID, pid := s.openBuy(t, broker, 10)

	fill := broker.Fill(t, pid, 4) // the broker's snapshot says PARTIALLY_FILLED
	if _, _, err := s.CancelOrder(ctx, user, orderID); err != nil {
		t.Fatal(err)
	}
	// Keep the cancel job from reaching the broker, so the only news is the
	// fill webhook (otherwise the job's newer PENDING_CANCEL answer wins).
	broker.SetDown(true)
	s.deliver(t, fill)

	got := s.orderNow(t, orderID)
	if got.Status != domain.StatusPendingCancel || got.FilledQty != 4 {
		t.Fatalf("order = %+v", got.Order)
	}
	if msg := statusMessage(got.Order); msg != "Cancelling… 4 of 10 filled" {
		t.Fatalf("message = %q", msg)
	}
	s.assertInvariants(t)
}
