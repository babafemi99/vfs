package domain

type Side string

const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

func (s Side) Valid() bool { return s == Buy || s == Sell }

type QuoteStatus string

const (
	QuoteActive   QuoteStatus = "ACTIVE"
	QuoteAccepted QuoteStatus = "ACCEPTED"
	QuoteExpired  QuoteStatus = "EXPIRED"
)

type OrderStatus string

const (
	OrderOpen            OrderStatus = "OPEN"
	OrderPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderPendingCancel   OrderStatus = "PENDING_CANCEL"
	OrderFilled          OrderStatus = "FILLED"
	OrderCancelled       OrderStatus = "CANCELLED"
	OrderRejected        OrderStatus = "REJECTED"
	OrderExpired         OrderStatus = "EXPIRED"
)

// Final reports whether no further change is allowed. First final state wins.
func (s OrderStatus) Final() bool {
	switch s {
	case OrderFilled, OrderCancelled, OrderRejected, OrderExpired:
		return true
	}
	return false
}

// orderTransitions is the complete set of allowed order transitions.
// Anything not listed here is refused with invalid_transition.
var orderTransitions = map[OrderStatus][]OrderStatus{
	OrderOpen: {
		OrderPartiallyFilled, OrderFilled, OrderPendingCancel, OrderRejected, OrderExpired,
	},
	OrderPartiallyFilled: {
		OrderPartiallyFilled, OrderFilled, OrderPendingCancel, OrderExpired,
	},
	OrderPendingCancel: {
		OrderPendingCancel, // partial fill while cancel is pending
		OrderFilled,        // fill wins the race
		OrderCancelled,     // cancel resolves
		OrderOpen,          // cancel rejected, nothing filled yet
		OrderPartiallyFilled,
	},
}

// CanTransition reports whether an order may move from one status to another.
func CanTransition(from, to OrderStatus) bool {
	for _, s := range orderTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

type EventType string

const (
	EventQuoteExpired         EventType = "quote.expired"
	EventOrderAccepted        EventType = "order.accepted"
	EventOrderPartiallyFilled EventType = "order.partially_filled"
	EventOrderFilled          EventType = "order.filled"
	EventOrderPendingCancel   EventType = "order.pending_cancel"
	EventOrderCancelled       EventType = "order.cancelled"
	EventOrderCancelRejected  EventType = "order.cancel_rejected"
	EventOrderRejected        EventType = "order.rejected"
	EventOrderExpired         EventType = "order.expired"
)
