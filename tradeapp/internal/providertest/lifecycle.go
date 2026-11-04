package providertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// The methods below change an order the way brokersim's engine or /dev API
// would, and return the webhook event brokersim would send. Tests decide
// what to do with it: deliver it, deliver it twice, out of order, or drop it.

// OrderIDFor returns the provider order id for one of our order ids.
func (b *Broker) OrderIDFor(t testing.TB, clientOrderID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, ok := b.byClientID[clientOrderID]
	if !ok {
		t.Fatalf("broker has no order for client id %s", clientOrderID)
	}
	return id
}

// Fill fills qty of an order (all remaining if qty is 0). Fee is 50 bps of
// the fill, rounded half up.
func (b *Broker) Fill(t testing.TB, providerOrderID string, qty int64) provider.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.mustOrder(t, providerOrderID)
	if o.Status != "OPEN" && o.Status != "PARTIALLY_FILLED" && o.Status != "PENDING_CANCEL" {
		t.Fatalf("cannot fill a %s order", o.Status)
	}
	if qty == 0 || qty > o.RemainingQty {
		qty = o.RemainingQty
	}

	b.nextID++
	fill := provider.Fill{
		ID:    fmt.Sprintf("fill_%d", b.nextID),
		Qty:   qty,
		Price: o.Price,
		Fee:   (qty*o.Price*50 + 5_000) / 10_000,
		At:    time.Now().UTC(),
	}
	o.Fills = append(o.Fills, fill)
	o.FilledQty += qty
	o.RemainingQty -= qty
	o.AvgFillPrice = o.Price

	eventType := "order.partially_filled"
	if o.RemainingQty == 0 {
		o.Status, eventType = "FILLED", "order.filled"
	} else if o.Status != "PENDING_CANCEL" {
		o.Status = "PARTIALLY_FILLED"
	}
	b.bump(o)
	return b.event(eventType, o, &fill, "")
}

// ConfirmCancel resolves a pending cancel: CANCELLED, keeping filled_qty.
func (b *Broker) ConfirmCancel(t testing.TB, providerOrderID string) provider.Event {
	return b.finish(t, providerOrderID, "PENDING_CANCEL", "CANCELLED", "order.cancelled", "")
}

// RejectCancel rejects a pending cancel; the order keeps working.
func (b *Broker) RejectCancel(t testing.TB, providerOrderID string) provider.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.mustOrder(t, providerOrderID)
	if o.Status == "FILLED" {
		// Too late: nothing changes, not even the version.
		return b.event("order.cancel_rejected", o, nil, "too_late_to_cancel")
	}
	if o.Status != "PENDING_CANCEL" {
		t.Fatalf("no pending cancel on a %s order", o.Status)
	}
	o.Status = "OPEN"
	if o.FilledQty > 0 {
		o.Status = "PARTIALLY_FILLED"
	}
	o.Reason = "cancel_rejected"
	b.bump(o)
	return b.event("order.cancel_rejected", o, nil, "cancel_rejected")
}

// Expire expires a working order.
func (b *Broker) Expire(t testing.TB, providerOrderID string) provider.Event {
	return b.finish(t, providerOrderID, "", "EXPIRED", "order.expired", "day_order_expired")
}

// Reject rejects an OPEN order.
func (b *Broker) Reject(t testing.TB, providerOrderID string) provider.Event {
	return b.finish(t, providerOrderID, "OPEN", "REJECTED", "order.rejected", "upstream_rejected")
}

// Snapshot returns a copy of the provider's current view of an order.
func (b *Broker) Snapshot(t testing.TB, providerOrderID string) provider.Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	return copyOrder(b.mustOrder(t, providerOrderID))
}

// Body encodes an event as the webhook body.
func Body(t testing.TB, e provider.Event) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *Broker) cancel(w http.ResponseWriter, id string) {
	o, ok := b.orders[id]
	if !ok {
		writeErr(w, 404, "not_found", "no such order")
		return
	}
	switch o.Status {
	case "OPEN", "PARTIALLY_FILLED":
		o.Status = "PENDING_CANCEL"
		b.bump(o)
	case "PENDING_CANCEL":
	default:
		writeJSON(w, 409, map[string]any{"error": map[string]string{"code": "order_final", "message": "order is final"}, "order": o})
		return
	}
	writeJSON(w, 202, o)
}

func (b *Broker) finish(t testing.TB, id, from, to, eventType, reason string) provider.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.mustOrder(t, id)
	working := o.Status == "OPEN" || o.Status == "PARTIALLY_FILLED" || o.Status == "PENDING_CANCEL"
	if (from != "" && o.Status != from) || !working {
		t.Fatalf("cannot move a %s order to %s", o.Status, to)
	}
	o.Status = to
	if reason != "" {
		o.Reason = reason
	}
	b.bump(o)
	return b.event(eventType, o, nil, reason)
}

func (b *Broker) mustOrder(t testing.TB, id string) *provider.Order {
	o, ok := b.orders[id]
	if !ok {
		t.Fatalf("broker has no order %s", id)
	}
	return o
}

func (b *Broker) bump(o *provider.Order) {
	o.Version++
	o.UpdatedAt = time.Now().UTC()
}

func (b *Broker) event(eventType string, o *provider.Order, fill *provider.Fill, reason string) provider.Event {
	b.nextEvent++
	var e provider.Event
	e.ID = fmt.Sprintf("evt_%d", b.nextEvent)
	e.Type = eventType
	e.CreatedAt = time.Now().UTC()
	snap := copyOrder(o)
	e.Data.Order = &snap
	e.Data.Fill = fill
	e.Data.Reason = reason
	return e
}

func copyOrder(o *provider.Order) provider.Order {
	c := *o
	c.Fills = append([]provider.Fill{}, o.Fills...)
	return c
}

// Forget drops an order from the broker, as if it had lost it.
func (b *Broker) Forget(t testing.TB, providerOrderID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.mustOrder(t, providerOrderID)
	delete(b.byClientID, o.ClientOrderID)
	delete(b.orders, providerOrderID)
}
