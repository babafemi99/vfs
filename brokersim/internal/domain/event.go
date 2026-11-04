package domain

import "time"

// Event is a state change the outside world should hear about.
// Order and Quote are snapshots taken after the change.
// The webhook layer gives it an id and turns it into a payload.
type Event struct {
	Type   EventType
	At     time.Time
	Quote  *Quote // quote events only
	Order  *Order // order events only
	Fill   *Fill  // fill events only
	Reason string // cancel_rejected, rejected, expired
}
