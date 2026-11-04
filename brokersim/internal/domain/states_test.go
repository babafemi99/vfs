package domain

import "testing"

var allStatuses = []OrderStatus{
	OrderOpen, OrderPartiallyFilled, OrderPendingCancel,
	OrderFilled, OrderCancelled, OrderRejected, OrderExpired,
}

// The spec's transition table, written out independently of states.go.
var specAllowed = map[[2]OrderStatus]bool{
	{OrderOpen, OrderPartiallyFilled}:            true,
	{OrderOpen, OrderFilled}:                     true,
	{OrderOpen, OrderPendingCancel}:              true,
	{OrderOpen, OrderRejected}:                   true,
	{OrderOpen, OrderExpired}:                    true,
	{OrderPartiallyFilled, OrderPartiallyFilled}: true,
	{OrderPartiallyFilled, OrderFilled}:          true,
	{OrderPartiallyFilled, OrderPendingCancel}:   true,
	{OrderPartiallyFilled, OrderExpired}:         true,
	{OrderPendingCancel, OrderPendingCancel}:     true,
	{OrderPendingCancel, OrderFilled}:            true,
	{OrderPendingCancel, OrderCancelled}:         true,
	{OrderPendingCancel, OrderOpen}:              true,
	{OrderPendingCancel, OrderPartiallyFilled}:   true,
}

func TestTransitionTableMatchesSpec(t *testing.T) {
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := specAllowed[[2]OrderStatus{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestForbiddenTransitions(t *testing.T) {
	for _, tc := range [][2]OrderStatus{
		{OrderFilled, OrderCancelled},
		{OrderCancelled, OrderPartiallyFilled},
		{OrderPartiallyFilled, OrderRejected},
		{OrderPendingCancel, OrderExpired},
		{OrderPendingCancel, OrderRejected},
		{OrderOpen, OrderOpen},
		{OrderOpen, OrderCancelled},
		{OrderExpired, OrderOpen},
		{OrderRejected, OrderFilled},
	} {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("%s -> %s should be forbidden", tc[0], tc[1])
		}
	}
}

func TestFinalStates(t *testing.T) {
	final := map[OrderStatus]bool{OrderFilled: true, OrderCancelled: true, OrderRejected: true, OrderExpired: true}
	for _, s := range allStatuses {
		if s.Final() != final[s] {
			t.Errorf("%s.Final() = %v", s, s.Final())
		}
		if final[s] && len(orderTransitions[s]) > 0 {
			t.Errorf("final state %s has outgoing transitions", s)
		}
	}
}
