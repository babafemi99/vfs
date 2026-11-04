package provider

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned without calling the provider while the circuit
// breaker is open: the provider has failed repeatedly and we are giving it
// time to recover instead of piling on requests.
var ErrCircuitOpen = errors.New("provider circuit open: calls paused after repeated failures")

// Circuit states.
const (
	CircuitClosed   = "closed"    // normal: calls go through
	CircuitOpen     = "open"      // failing: calls are refused until the cooldown ends
	CircuitHalfOpen = "half_open" // cooldown over: one probe call is in flight
)

// Breaker is a circuit breaker for calls to the provider.
//
//   - closed: every call goes through. threshold failures in a row open it.
//   - open: every call is refused with ErrCircuitOpen until cooldown passes.
//   - half_open: the first call after the cooldown is let through as a probe
//     (others are still refused). Success closes the circuit; failure opens
//     it for another cooldown.
//
// Only signs that the provider itself is in trouble count as failures:
// network errors, timeouts, 5xx and 429. A 4xx answer means the provider is
// up and working, so it counts as a success.
type Breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	onChange  func(from, to string)

	state     string
	failures  int       // consecutive failures while closed
	openUntil time.Time // when open: refuse calls until then
}

func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{threshold: threshold, cooldown: cooldown, now: time.Now, state: CircuitClosed}
}

// OnStateChange registers a callback (for logging) run on every transition.
// It runs while the breaker is locked, so it must not call the breaker.
func (b *Breaker) OnStateChange(fn func(from, to string)) {
	b.mu.Lock()
	b.onChange = fn
	b.mu.Unlock()
}

// SetClock replaces the clock (tests).
func (b *Breaker) SetClock(now func() time.Time) { b.mu.Lock(); b.now = now; b.mu.Unlock() }

// Allow reports whether a call may go to the provider now. In half_open it
// returns true once (the probe) and false until that probe is recorded.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if b.now().Before(b.openUntil) {
			return false
		}
		b.setState(CircuitHalfOpen) // this caller is the probe
		return true
	default: // half_open: a probe is already in flight
		return false
	}
}

// Rejecting reports whether calls would be refused right now, without
// taking the probe slot. The worker uses it to stop claiming provider jobs.
func (b *Breaker) Rejecting() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case CircuitOpen:
		return b.now().Before(b.openUntil)
	case CircuitHalfOpen:
		return true
	}
	return false
}

// Success records a call the provider answered.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.setState(CircuitClosed)
}

// Failure records a call that showed the provider in trouble.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case CircuitHalfOpen:
		b.open()
	case CircuitClosed:
		b.failures++
		if b.failures >= b.threshold {
			b.open()
		}
	}
}

// Abandon records a call that ended without an answer for reasons on our
// side (the caller's context was cancelled). It frees the probe slot without
// judging the provider.
func (b *Breaker) Abandon() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == CircuitHalfOpen {
		b.openUntil = b.now() // let the next call probe straight away
		b.setState(CircuitOpen)
	}
}

// State returns the current state.
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) open() {
	b.failures = 0
	b.openUntil = b.now().Add(b.cooldown)
	b.setState(CircuitOpen)
}

func (b *Breaker) setState(to string) {
	from := b.state
	b.state = to
	if from != to && b.onChange != nil {
		b.onChange(from, to)
	}
}
