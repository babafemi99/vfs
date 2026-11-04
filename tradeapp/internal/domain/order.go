package domain

import (
	"fmt"
	"time"
)

const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

// Local order statuses. PENDING_SUBMIT is ours (not yet at the provider);
// the rest mirror the provider's statuses.
const (
	StatusPendingSubmit   = "PENDING_SUBMIT"
	StatusOpen            = "OPEN"
	StatusPartiallyFilled = "PARTIALLY_FILLED"
	StatusPendingCancel   = "PENDING_CANCEL"
	StatusFilled          = "FILLED"
	StatusCancelled       = "CANCELLED"
	StatusRejected        = "REJECTED"
	StatusExpired         = "EXPIRED"
)

// FinalStatuses never change once reached (first final state wins).
var FinalStatuses = []string{StatusFilled, StatusCancelled, StatusRejected, StatusExpired}

// IsFinal reports whether s is a final status.
func IsFinal(s string) bool {
	for _, f := range FinalStatuses {
		if s == f {
			return true
		}
	}
	return false
}

// providerStatuses are the statuses a provider snapshot can carry.
var providerStatuses = map[string]bool{
	StatusOpen: true, StatusPartiallyFilled: true, StatusPendingCancel: true,
	StatusFilled: true, StatusCancelled: true, StatusRejected: true, StatusExpired: true,
}

// NextStatus decides the local status after a newer provider snapshot.
// It returns the status to keep or move to, and whether it changed.
//
//   - A final local status never changes (first final state wins).
//   - While we are cancelling (PENDING_CANCEL), a snapshot that still says
//     OPEN or PARTIALLY_FILLED keeps us in PENDING_CANCEL (a fill arrived
//     during the cancel), unless the provider rejected the cancel.
//   - Otherwise the provider's status is taken as it is.
func NextStatus(local, provider string, cancelRejected bool) (string, bool) {
	if IsFinal(local) || !providerStatuses[provider] {
		return local, false
	}
	if local == StatusPendingCancel && !cancelRejected &&
		(provider == StatusOpen || provider == StatusPartiallyFilled) {
		return local, false
	}
	return provider, provider != local
}

// Quote is a provider quote cached locally.
type Quote struct {
	ID        string    `json:"id"` // provider quote id
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Qty       int64     `json:"qty"`
	Price     int64     `json:"price"`
	Fee       int64     `json:"fee"`
	Total     int64     `json:"total"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Order is a local order. ID is also the provider client_order_id.
type Order struct {
	ID              string    `json:"id"`
	UserID          int64     `json:"-"`
	QuoteID         string    `json:"quote_id"`
	ProviderOrderID *string   `json:"provider_order_id,omitempty"`
	Symbol          string    `json:"symbol"`
	Side            string    `json:"side"`
	Qty             int64     `json:"qty"`
	Price           int64     `json:"price"`
	FilledQty       int64     `json:"filled_qty"`
	AvgFillPrice    int64     `json:"avg_fill_price"`
	HoldAmount      int64     `json:"hold_amount"`    // cash (BUY) or shares (SELL)
	HoldRemaining   int64     `json:"hold_remaining"` // still held right now
	Status          string    `json:"status"`
	Reason          *string   `json:"reason,omitempty"`
	ProviderVersion int64     `json:"provider_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Fill is one provider fill of an order. ID is the provider fill id.
type Fill struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Qty       int64     `json:"qty"`
	Price     int64     `json:"price"`
	Fee       int64     `json:"fee"`
	CreatedAt time.Time `json:"created_at"`
}

// OrderDetail is one order with its fills and activity.
type OrderDetail struct {
	Order
	Fills    []Fill     `json:"fills"`
	Activity []Activity `json:"activity"`
}

// Price is a provider market price for one symbol.
type Price struct {
	Symbol string `json:"symbol"`
	Bid    int64  `json:"bid"`
	Mid    int64  `json:"mid"`
	Ask    int64  `json:"ask"`
}

// Quote limits.
const (
	MaxOrderQty     = 1_000_000
	MaxSymbolLength = 16
)

// ValidateQuoteRequest checks a quote request before it goes to the provider.
func ValidateQuoteRequest(symbol, side string, qty int64) error {
	if symbol == "" || len(symbol) > MaxSymbolLength {
		return fmt.Errorf("%w: symbol must be 1-%d characters", ErrInvalidArgument, MaxSymbolLength)
	}
	for _, r := range symbol {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("%w: symbol must be upper-case letters and digits", ErrInvalidArgument)
		}
	}
	if side != SideBuy && side != SideSell {
		return fmt.Errorf("%w: side must be BUY or SELL", ErrInvalidArgument)
	}
	if qty < 1 || qty > MaxOrderQty {
		return fmt.Errorf("%w: qty must be between 1 and %d", ErrInvalidArgument, MaxOrderQty)
	}
	return nil
}

// OutcomeLine is the plain-English result of a final order, e.g.
// "6 of 10 bought, 4 cancelled". It is empty for orders still working.
func OutcomeLine(o Order) string {
	verb := "bought"
	if o.Side == SideSell {
		verb = "sold"
	}
	reason := ""
	if o.Reason != nil {
		reason = *o.Reason
	}

	switch o.Status {
	case StatusFilled:
		return "Filled"
	case StatusCancelled:
		if o.FilledQty == 0 {
			return "Cancelled"
		}
		return fmt.Sprintf("%d of %d %s, %d cancelled", o.FilledQty, o.Qty, verb, o.Qty-o.FilledQty)
	case StatusExpired:
		return fmt.Sprintf("Expired: %d of %d %s", o.FilledQty, o.Qty, verb)
	case StatusRejected:
		if reason == "" {
			return "Rejected"
		}
		return "Rejected: " + reason
	}
	return ""
}
