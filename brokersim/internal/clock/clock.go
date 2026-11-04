// Package clock lets the simulator run on real time or on a fake clock in tests.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

// Real is the wall clock, in UTC.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake is a manually advanced clock. Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
