package domain

import "time"

// Provider webhook event types we act on specially.
const (
	EventOrderCancelRejected = "order.cancel_rejected"
)

// ProviderEvent is a webhook from the provider, stored in the inbox before
// any processing so nothing received is ever lost.
type ProviderEvent struct {
	EventID         string     `json:"event_id"`
	Type            string     `json:"type"`
	ProviderOrderID *string    `json:"provider_order_id,omitempty"`
	ClientOrderID   *string    `json:"client_order_id,omitempty"`
	Payload         []byte     `json:"-"`
	ReceivedAt      time.Time  `json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	ProcessError    *string    `json:"process_error,omitempty"`
}

// Break kinds: something that contradicts what we expected. Breaks never
// block processing; they are recorded for reconciliation.
const (
	BreakFillAfterFinal      = "fill_after_final"      // a fill beyond what the final order allowed
	BreakFillUnpaid          = "fill_unpaid"           // a BUY fill the user's cash could not cover
	BreakFillSharesMissing   = "fill_shares_missing"   // a SELL fill for shares the user does not hold
	BreakSubmitNeverAccepted = "submit_never_accepted" // gave up submitting; the provider never had it
)

// Break is a recorded contradiction between us and the provider.
type Break struct {
	ID        int64     `json:"id"`
	OrderID   *string   `json:"order_id,omitempty"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}
