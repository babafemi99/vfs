package domain

import (
	"slices"
	"time"
)

type Order struct {
	ID            string      `json:"id"`
	ClientOrderID string      `json:"client_order_id"`
	QuoteID       string      `json:"quote_id"`
	Symbol        string      `json:"symbol"`
	Side          Side        `json:"side"`
	Qty           int64       `json:"qty"`
	FilledQty     int64       `json:"filled_qty"`
	RemainingQty  int64       `json:"remaining_qty"`
	Price         int64       `json:"price"`          // guaranteed quote price; all fills at this price
	AvgFillPrice  int64       `json:"avg_fill_price"` // 0 until the first fill
	Status        OrderStatus `json:"status"`
	Reason        string      `json:"reason,omitempty"`
	Version       int64       `json:"version"`
	Fills         []Fill      `json:"fills"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`

	// CancelDueAt is set while a cancel is outstanding. It is tracked apart
	// from Status because a fill can complete the order (FILLED) while the
	// cancel is still pending, and the cancel must then be rejected.
	CancelDueAt time.Time `json:"-"`
}

type Fill struct {
	ID    string    `json:"id"`
	Qty   int64     `json:"qty"`
	Price int64     `json:"price"`
	Fee   int64     `json:"fee"`
	At    time.Time `json:"at"`
}

// NewOrder creates an OPEN order from an accepted quote.
func NewOrder(id, clientOrderID string, q *Quote, now time.Time) (*Order, *Event) {
	o := &Order{
		ID: id, ClientOrderID: clientOrderID, QuoteID: q.ID,
		Symbol: q.Symbol, Side: q.Side,
		Qty: q.Qty, RemainingQty: q.Qty, Price: q.Price,
		Status: OrderOpen, Version: 1,
		Fills:     []Fill{},
		CreatedAt: now, UpdatedAt: now,
	}
	return o, o.event(EventOrderAccepted, now, nil, "")
}

// Snapshot returns a deep copy.
func (o *Order) Snapshot() Order {
	c := *o
	c.Fills = slices.Clone(o.Fills)
	return c
}

// HasPendingCancel reports whether a cancel is waiting to be resolved.
func (o *Order) HasPendingCancel() bool { return !o.CancelDueAt.IsZero() }

// Fill executes qty shares at the order price.
func (o *Order) Fill(fillID string, qty, feeBps int64, now time.Time) (*Event, error) {
	if o.Status.Final() {
		return nil, o.finalErr()
	}
	if qty <= 0 || qty > o.RemainingQty {
		return nil, Errorf(CodeValidation, "fill qty must be in [1, %d], got %d", o.RemainingQty, qty)
	}
	remaining := o.RemainingQty - qty
	to, typ := OrderPartiallyFilled, EventOrderPartiallyFilled
	switch {
	case remaining == 0:
		to, typ = OrderFilled, EventOrderFilled
	case o.Status == OrderPendingCancel:
		to = OrderPendingCancel
	}
	if err := o.transition(to, now); err != nil {
		return nil, err
	}
	f := Fill{ID: fillID, Qty: qty, Price: o.Price, Fee: FeeFor(o.Price*qty, feeBps), At: now}
	o.AvgFillPrice = (o.AvgFillPrice*o.FilledQty + f.Price*qty) / (o.FilledQty + qty)
	o.FilledQty += qty
	o.RemainingQty = remaining
	o.Fills = append(o.Fills, f)
	return o.event(typ, now, &f, ""), nil
}

// RequestCancel moves a working order to PENDING_CANCEL, to be resolved at dueAt.
// Calling it again while pending is a no-op and returns a nil event.
func (o *Order) RequestCancel(now, dueAt time.Time) (*Event, error) {
	if o.Status == OrderPendingCancel {
		return nil, nil
	}
	if err := o.transition(OrderPendingCancel, now); err != nil {
		return nil, err
	}
	o.CancelDueAt = dueAt
	return o.event(EventOrderPendingCancel, now, nil, ""), nil
}

// ResolveCancel settles a pending cancel. If a fill already completed the
// order, the cancel is rejected as too late and the order is unchanged.
func (o *Order) ResolveCancel(now time.Time) (*Event, error) {
	if !o.HasPendingCancel() {
		return nil, Errorf(CodeInvalidTransition, "order %s has no pending cancel", o.ID)
	}
	if o.Status == OrderFilled {
		o.CancelDueAt = time.Time{}
		return o.event(EventOrderCancelRejected, now, nil, "too_late_to_cancel"), nil
	}
	if err := o.transition(OrderCancelled, now); err != nil {
		return nil, err
	}
	o.CancelDueAt = time.Time{}
	return o.event(EventOrderCancelled, now, nil, ""), nil
}

// RejectCancel refuses a pending cancel; the order keeps working.
func (o *Order) RejectCancel(reason string, now time.Time) (*Event, error) {
	if o.Status.Final() {
		return nil, o.finalErr()
	}
	if o.Status != OrderPendingCancel {
		return nil, Errorf(CodeInvalidTransition, "order %s is %s, not PENDING_CANCEL", o.ID, o.Status)
	}
	to := OrderOpen
	if o.FilledQty > 0 {
		to = OrderPartiallyFilled
	}
	if err := o.transition(to, now); err != nil {
		return nil, err
	}
	if reason == "" {
		reason = "cancel_rejected"
	}
	o.CancelDueAt = time.Time{}
	o.Reason = reason
	return o.event(EventOrderCancelRejected, now, nil, reason), nil
}

// Reject moves an OPEN order to REJECTED.
func (o *Order) Reject(reason string, now time.Time) (*Event, error) {
	if err := o.transition(OrderRejected, now); err != nil {
		return nil, err
	}
	o.Reason = reason
	return o.event(EventOrderRejected, now, nil, reason), nil
}

// Expire moves an OPEN or PARTIALLY_FILLED order to EXPIRED. Fills are kept.
func (o *Order) Expire(reason string, now time.Time) (*Event, error) {
	if err := o.transition(OrderExpired, now); err != nil {
		return nil, err
	}
	o.Reason = reason
	return o.event(EventOrderExpired, now, nil, reason), nil
}

// transition is the only place Status changes. On error nothing is modified.
func (o *Order) transition(to OrderStatus, now time.Time) error {
	if o.Status.Final() {
		return o.finalErr()
	}
	if !CanTransition(o.Status, to) {
		return Errorf(CodeInvalidTransition, "order %s: %s -> %s not allowed", o.ID, o.Status, to)
	}
	o.Status = to
	o.Version++
	o.UpdatedAt = now
	return nil
}

func (o *Order) finalErr() error {
	return Errorf(CodeOrderFinal, "order %s is %s", o.ID, o.Status)
}

func (o *Order) event(t EventType, now time.Time, f *Fill, reason string) *Event {
	snap := o.Snapshot()
	ev := &Event{Type: t, At: now, Order: &snap, Reason: reason}
	if f != nil {
		fc := *f
		ev.Fill = &fc
	}
	return ev
}
