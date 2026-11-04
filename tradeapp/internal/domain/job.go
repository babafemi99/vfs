package domain

import "time"

// Job kinds.
const (
	JobSubmitOrder  = "submit_order"  // send a placed order to the provider
	JobCancelOrder  = "cancel_order"  // ask the provider to cancel an order
	JobProcessEvent = "process_event" // apply a received provider webhook
)

// Job statuses. A job moves pending → running → done. On failure it goes back
// to pending with a later RunAt, or to dead (the dead-letter queue) once its
// retry budget (GiveUpAt) or attempt cap (MaxAttempts) is used up.
const (
	JobPending = "pending"
	JobRunning = "running"
	JobDone    = "done"
	JobDead    = "dead"
)

// Job is one unit of background work (the outbox).
type Job struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	OrderID     *string    `json:"order_id,omitempty"`
	EventID     *string    `json:"event_id,omitempty"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"` // hard safety cap
	RunAt       time.Time  `json:"run_at"`
	GiveUpAt    *time.Time `json:"give_up_at,omitempty"` // retry budget: dead-lettered after this
	LockedUntil *time.Time `json:"locked_until,omitempty"`
	LastError   *string    `json:"last_error,omitempty"`
	RequestID   *string    `json:"request_id,omitempty"` // the request that queued it, for tracing
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// OutOfRetries reports whether a failed job has used up its retry budget or
// its attempt cap at time now.
func (j Job) OutOfRetries(now time.Time) bool {
	if j.Attempts >= j.MaxAttempts {
		return true
	}
	return j.GiveUpAt != nil && !now.Before(*j.GiveUpAt)
}

// Backoff is the wait before retry number attempt (1-based): base, 2×base,
// 4×base, ... capped at max.
func Backoff(attempt int, base, max time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return min(d, max)
}

// JobCount is how many jobs have one kind and status.
type JobCount struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// Alert names raised by the job queue's health check.
const (
	AlertDeadLetters = "dead_letter_queue_not_empty" // jobs gave up and need a human
	AlertQueueLag    = "queue_lag"                   // a job has waited longer than the lag threshold
	AlertCircuitOpen = "provider_circuit_open"       // provider calls are paused
)

// JobStats is the health of the job queue.
type JobStats struct {
	Counts               []JobCount `json:"counts"`
	Pending              int        `json:"pending"`
	Running              int        `json:"running"`
	Dead                 int        `json:"dead"`
	OldestWaitingSeconds int64      `json:"oldest_waiting_seconds"` // age of the oldest pending/running job
	Circuit              string     `json:"provider_circuit"`
	Alerts               []string   `json:"alerts"`
	OK                   bool       `json:"ok"`
}

// JobAlerts lists the alerts that apply to these numbers.
func JobAlerts(dead int, oldestWaiting, lagThreshold time.Duration, circuitOpen bool) []string {
	alerts := []string{}
	if dead > 0 {
		alerts = append(alerts, AlertDeadLetters)
	}
	if oldestWaiting > lagThreshold {
		alerts = append(alerts, AlertQueueLag)
	}
	if circuitOpen {
		alerts = append(alerts, AlertCircuitOpen)
	}
	return alerts
}
