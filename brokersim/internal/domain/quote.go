package domain

import "time"

type Quote struct {
	ID        string      `json:"id"`
	Symbol    string      `json:"symbol"`
	Side      Side        `json:"side"`
	Qty       int64       `json:"qty"`
	Price     int64       `json:"price"` // per share, minor units: ask for BUY, bid for SELL
	Fee       int64       `json:"fee"`
	Total     int64       `json:"total"` // BUY: notional+fee, SELL: notional-fee
	Status    QuoteStatus `json:"status"`
	OrderID   string      `json:"order_id,omitempty"`
	ExpiresAt time.Time   `json:"expires_at"`
	CreatedAt time.Time   `json:"created_at"`
}

// FeeFor returns notional*bps/10000, rounded half-up. Amounts are non-negative.
func FeeFor(notional, bps int64) int64 {
	return (notional*bps + 5000) / 10000
}

func NewQuote(id, symbol string, side Side, qty, price, feeBps int64, now time.Time, ttl time.Duration) (*Quote, error) {
	switch {
	case symbol == "":
		return nil, Errorf(CodeValidation, "symbol is required")
	case !side.Valid():
		return nil, Errorf(CodeValidation, "side must be BUY or SELL, got %q", side)
	case qty <= 0:
		return nil, Errorf(CodeValidation, "qty must be > 0, got %d", qty)
	case price <= 0:
		return nil, Errorf(CodeValidation, "price must be > 0, got %d", price)
	}
	notional := price * qty
	fee := FeeFor(notional, feeBps)
	total := notional + fee
	if side == Sell {
		total = notional - fee
	}
	return &Quote{
		ID: id, Symbol: symbol, Side: side, Qty: qty,
		Price: price, Fee: fee, Total: total,
		Status:    QuoteActive,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}, nil
}

// PastExpiry reports whether the quote's lifetime is over at now.
// A quote is valid strictly before ExpiresAt.
func (q *Quote) PastExpiry(now time.Time) bool { return !now.Before(q.ExpiresAt) }

// Accept moves ACTIVE -> ACCEPTED and links the order.
func (q *Quote) Accept(orderID string, now time.Time) error {
	switch {
	case q.Status == QuoteAccepted:
		return Errorf(CodeQuoteAlreadyAccepted, "quote %s already accepted", q.ID)
	case q.Status == QuoteExpired || q.PastExpiry(now):
		return Errorf(CodeQuoteExpired, "quote %s expired at %s", q.ID, q.ExpiresAt.Format(time.RFC3339))
	}
	q.Status = QuoteAccepted
	q.OrderID = orderID
	return nil
}

// Expire moves ACTIVE -> EXPIRED once the quote is past its expiry.
func (q *Quote) Expire(now time.Time) (*Event, error) {
	if q.Status != QuoteActive {
		return nil, Errorf(CodeInvalidTransition, "quote %s is %s", q.ID, q.Status)
	}
	if !q.PastExpiry(now) {
		return nil, Errorf(CodeInvalidTransition, "quote %s has not expired yet", q.ID)
	}
	q.Status = QuoteExpired
	snap := *q
	return &Event{Type: EventQuoteExpired, At: now, Quote: &snap}, nil
}

// View returns a copy with the status the client should see at now:
// an ACTIVE quote past expiry reads as EXPIRED even before the engine marks it.
func (q *Quote) View(now time.Time) Quote {
	v := *q
	if v.Status == QuoteActive && v.PastExpiry(now) {
		v.Status = QuoteExpired
	}
	return v
}
