package domain

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

const feeBps = 50

func newTestOrder(t *testing.T, qty int64) *Order {
	t.Helper()
	q, err := NewQuote("qt_1", "AAPL", Buy, qty, 1000, feeBps, t0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Accept("ord_1", t0); err != nil {
		t.Fatal(err)
	}
	o, ev := NewOrder("ord_1", "c-1", q, t0)
	if ev.Type != EventOrderAccepted || o.Status != OrderOpen || o.Version != 1 {
		t.Fatalf("new order: %+v event %s", o, ev.Type)
	}
	return o
}

// checkInvariants is run after every operation.
func checkInvariants(t *testing.T, o *Order) {
	t.Helper()
	if o.FilledQty > o.Qty {
		t.Fatalf("FilledQty %d > Qty %d", o.FilledQty, o.Qty)
	}
	if o.RemainingQty != o.Qty-o.FilledQty {
		t.Fatalf("RemainingQty %d != Qty-FilledQty %d", o.RemainingQty, o.Qty-o.FilledQty)
	}
	var sum int64
	for _, f := range o.Fills {
		sum += f.Qty
	}
	if sum != o.FilledQty {
		t.Fatalf("sum of fills %d != FilledQty %d", sum, o.FilledQty)
	}
}

func mustEvent(t *testing.T, ev *Event, err error, want EventType) *Event {
	t.Helper()
	if err != nil {
		t.Fatalf("want %s, got error %v", want, err)
	}
	if ev == nil || ev.Type != want {
		t.Fatalf("want event %s, got %+v", want, ev)
	}
	return ev
}

func TestFillPartialThenFull(t *testing.T) {
	o := newTestOrder(t, 10)
	now := t0
	lastVersion := o.Version

	for i, step := range []struct {
		qty    int64
		status OrderStatus
		event  EventType
	}{
		{4, OrderPartiallyFilled, EventOrderPartiallyFilled},
		{3, OrderPartiallyFilled, EventOrderPartiallyFilled},
		{3, OrderFilled, EventOrderFilled},
	} {
		now = now.Add(time.Second)
		ev, err := o.Fill(fmt.Sprintf("fill_%d", i), step.qty, feeBps, now)
		mustEvent(t, ev, err, step.event)
		checkInvariants(t, o)
		if o.Status != step.status {
			t.Fatalf("step %d: status %s, want %s", i, o.Status, step.status)
		}
		if o.Version <= lastVersion {
			t.Fatalf("version did not increase: %d -> %d", lastVersion, o.Version)
		}
		lastVersion = o.Version
		if !o.UpdatedAt.Equal(now) {
			t.Fatalf("UpdatedAt not set")
		}
		if ev.Fill == nil || ev.Fill.Qty != step.qty || ev.Order.Version != o.Version || ev.Order.FilledQty != o.FilledQty {
			t.Fatalf("event snapshot wrong: %+v", ev)
		}
	}
	if o.AvgFillPrice != o.Price {
		t.Errorf("AvgFillPrice %d != Price %d", o.AvgFillPrice, o.Price)
	}
	if o.Fills[0].Fee != FeeFor(4*1000, feeBps) {
		t.Errorf("fill fee = %d", o.Fills[0].Fee)
	}
}

func TestFillNeverExceedsQty(t *testing.T) {
	o := newTestOrder(t, 5)
	for _, qty := range []int64{0, -1, 6} {
		before := o.Snapshot()
		if _, err := o.Fill("f", qty, feeBps, t0); !errors.Is(err, ErrValidation) {
			t.Errorf("Fill(%d): %v, want validation_error", qty, err)
		}
		if !reflect.DeepEqual(before, o.Snapshot()) {
			t.Errorf("failed Fill(%d) changed the order", qty)
		}
	}
	if _, err := o.Fill("f1", 3, feeBps, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Fill("f2", 3, feeBps, t0); !errors.Is(err, ErrValidation) {
		t.Errorf("overfill: %v", err)
	}
	checkInvariants(t, o)
}

func TestEventSnapshotIsIndependent(t *testing.T) {
	o := newTestOrder(t, 10)
	ev, _ := o.Fill("f1", 1, feeBps, t0)
	_, _ = o.Fill("f2", 1, feeBps, t0)
	if len(ev.Order.Fills) != 1 || ev.Order.FilledQty != 1 {
		t.Errorf("earlier event snapshot was mutated: %+v", ev.Order)
	}
}

// finalOrders builds one order in each final state.
func finalOrders(t *testing.T) map[OrderStatus]*Order {
	t.Helper()
	filled := newTestOrder(t, 10)
	_, _ = filled.Fill("f", 10, feeBps, t0)

	cancelled := newTestOrder(t, 10)
	_, _ = cancelled.Fill("f", 6, feeBps, t0)
	_, _ = cancelled.RequestCancel(t0, t0.Add(time.Second))
	_, _ = cancelled.ResolveCancel(t0.Add(time.Second))

	rejected := newTestOrder(t, 10)
	_, _ = rejected.Reject("upstream_rejected", t0)

	expired := newTestOrder(t, 10)
	_, _ = expired.Fill("f", 2, feeBps, t0)
	_, _ = expired.Expire("day_order_expired", t0)

	out := map[OrderStatus]*Order{
		OrderFilled: filled, OrderCancelled: cancelled, OrderRejected: rejected, OrderExpired: expired,
	}
	for want, o := range out {
		if o.Status != want {
			t.Fatalf("setup: got %s, want %s", o.Status, want)
		}
	}
	return out
}

func TestFinalStatesAreImmutable(t *testing.T) {
	ops := map[string]func(o *Order) (*Event, error){
		"fill":          func(o *Order) (*Event, error) { return o.Fill("f", 1, feeBps, t0) },
		"cancel":        func(o *Order) (*Event, error) { return o.RequestCancel(t0, t0) },
		"reject":        func(o *Order) (*Event, error) { return o.Reject("x", t0) },
		"expire":        func(o *Order) (*Event, error) { return o.Expire("x", t0) },
		"reject_cancel": func(o *Order) (*Event, error) { return o.RejectCancel("x", t0) },
		"resolve":       func(o *Order) (*Event, error) { return o.ResolveCancel(t0) },
	}
	for status, o := range finalOrders(t) {
		for name, op := range ops {
			before := o.Snapshot()
			ev, err := op(o)
			if err == nil || ev != nil {
				t.Errorf("%s on %s: ev=%v err=%v, want error", name, status, ev, err)
			}
			if !reflect.DeepEqual(before, o.Snapshot()) {
				t.Errorf("%s on %s changed the order", name, status)
			}
		}
	}
	// Retained fills on non-filled finals.
	fo := finalOrders(t)
	if fo[OrderCancelled].FilledQty != 6 || fo[OrderExpired].FilledQty != 2 {
		t.Error("CANCELLED/EXPIRED must keep FilledQty")
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	o := newTestOrder(t, 10)
	due := t0.Add(time.Second)
	ev, err := o.RequestCancel(t0, due)
	mustEvent(t, ev, err, EventOrderPendingCancel)
	v := o.Version
	ev, err = o.RequestCancel(t0, due.Add(time.Hour))
	if ev != nil || err != nil {
		t.Fatalf("second cancel: ev=%v err=%v, want no-op", ev, err)
	}
	if o.Version != v || !o.CancelDueAt.Equal(due) {
		t.Error("second cancel changed the order")
	}
	ev, err = o.ResolveCancel(due)
	mustEvent(t, ev, err, EventOrderCancelled)
	if o.HasPendingCancel() {
		t.Error("pending cancel not cleared")
	}
}

func TestPartialFillDuringPendingCancelThenConfirm(t *testing.T) {
	o := newTestOrder(t, 10)
	_, _ = o.Fill("f1", 4, feeBps, t0)
	ev, err := o.RequestCancel(t0, t0.Add(time.Second))
	mustEvent(t, ev, err, EventOrderPendingCancel)

	ev, err = o.Fill("f2", 2, feeBps, t0)
	mustEvent(t, ev, err, EventOrderPartiallyFilled)
	if o.Status != OrderPendingCancel {
		t.Fatalf("status after fill during cancel = %s", o.Status)
	}
	checkInvariants(t, o)

	ev, err = o.ResolveCancel(t0.Add(time.Second))
	mustEvent(t, ev, err, EventOrderCancelled)
	if o.Status != OrderCancelled || o.FilledQty != 6 {
		t.Errorf("got %s filled %d, want CANCELLED filled 6", o.Status, o.FilledQty)
	}
}

func TestFillWinsCancelRace(t *testing.T) {
	o := newTestOrder(t, 10)
	_, _ = o.RequestCancel(t0, t0.Add(time.Second))
	ev, err := o.Fill("f1", 10, feeBps, t0)
	mustEvent(t, ev, err, EventOrderFilled)

	before := o.Snapshot()
	ev, err = o.ResolveCancel(t0.Add(time.Second))
	mustEvent(t, ev, err, EventOrderCancelRejected)
	if ev.Reason != "too_late_to_cancel" {
		t.Errorf("reason = %q", ev.Reason)
	}
	after := o.Snapshot()
	before.CancelDueAt, after.CancelDueAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(before, after) {
		t.Error("too-late cancel must not change the order")
	}
	if _, err := o.ResolveCancel(t0); err == nil {
		t.Error("cancel_rejected must be emitted only once")
	}
}

func TestRejectCancelDerivesStatus(t *testing.T) {
	o := newTestOrder(t, 10)
	_, _ = o.RequestCancel(t0, t0)
	ev, err := o.RejectCancel("", t0)
	mustEvent(t, ev, err, EventOrderCancelRejected)
	if o.Status != OrderOpen || o.HasPendingCancel() {
		t.Errorf("no fills: got %s, want OPEN", o.Status)
	}

	o = newTestOrder(t, 10)
	_, _ = o.Fill("f", 3, feeBps, t0)
	_, _ = o.RequestCancel(t0, t0)
	_, _ = o.RejectCancel("", t0)
	if o.Status != OrderPartiallyFilled {
		t.Errorf("with fills: got %s, want PARTIALLY_FILLED", o.Status)
	}

	if _, err := o.RejectCancel("", t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("reject cancel without pending cancel: %v", err)
	}
}

func TestRejectOnlyFromOpen(t *testing.T) {
	o := newTestOrder(t, 10)
	_, _ = o.Fill("f", 1, feeBps, t0)
	if _, err := o.Reject("x", t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("reject from PARTIALLY_FILLED: %v", err)
	}
	o = newTestOrder(t, 10)
	_, _ = o.RequestCancel(t0, t0)
	if _, err := o.Reject("x", t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("reject from PENDING_CANCEL: %v", err)
	}
	if _, err := o.Expire("x", t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expire from PENDING_CANCEL: %v", err)
	}
}
