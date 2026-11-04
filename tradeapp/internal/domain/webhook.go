package domain

import "time"

// What happened to one webhook request.
const (
	WebhookAccepted          = "accepted"           // verified, new, stored for processing
	WebhookDuplicate         = "duplicate"          // verified, but this event id was already received
	WebhookRejectedSignature = "rejected_signature" // signature did not match
	WebhookRejectedTimestamp = "rejected_timestamp" // timestamp missing or outside the tolerance
	WebhookRejectedNonce     = "rejected_nonce"     // nonce missing or malformed
	WebhookRejectedReplay    = "rejected_replay"    // nonce already seen: the same request sent again
	WebhookInvalidBody       = "invalid_body"       // verified but not a usable event
	WebhookError             = "error"              // our side failed; the provider will retry
)

// MaxLoggedBody caps how much of a webhook body the log keeps.
const MaxLoggedBody = 64 << 10

// WebhookLog is one webhook request as received, with what we did with it
// and, for accepted events, how processing went.
type WebhookLog struct {
	ID              int64     `json:"id"`
	ReceivedAt      time.Time `json:"received_at"`
	Outcome         string    `json:"outcome"`
	Detail          *string   `json:"detail,omitempty"`
	Verified        bool      `json:"verified"`
	EventID         *string   `json:"event_id,omitempty"`
	EventType       *string   `json:"event_type,omitempty"`
	ProviderOrderID *string   `json:"provider_order_id,omitempty"`
	Timestamp       *string   `json:"timestamp,omitempty"`
	Nonce           *string   `json:"nonce,omitempty"`
	SignaturePrefix *string   `json:"signature_prefix,omitempty"`
	RemoteAddr      *string   `json:"remote_addr,omitempty"`
	BodySize        int       `json:"body_size"`
	Body            *string   `json:"body,omitempty"` // only on the detail view

	// From the inbox, for accepted events.
	OrderID      *string    `json:"order_id,omitempty"` // our order (the event's client_order_id)
	ProcessedAt  *time.Time `json:"processed_at,omitempty"`
	ProcessError *string    `json:"process_error,omitempty"`
}
