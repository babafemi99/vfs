package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestBreakerOpensProbesAndCloses(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	b := provider.NewBreaker(3, 10*time.Second)
	b.SetClock(c.now)
	var changes []string
	b.OnStateChange(func(from, to string) { changes = append(changes, from+"→"+to) })

	// Two failures: still closed. A success resets the count.
	b.Failure()
	b.Failure()
	b.Success()
	b.Failure()
	b.Failure()
	if b.State() != provider.CircuitClosed || !b.Allow() {
		t.Fatal("opened before 3 failures in a row")
	}

	b.Failure() // third in a row
	if b.State() != provider.CircuitOpen || b.Allow() || !b.Rejecting() {
		t.Fatalf("not open after 3 failures: %s", b.State())
	}

	// Cooldown over: exactly one probe gets through.
	c.advance(10 * time.Second)
	if b.Rejecting() {
		t.Fatal("still rejecting after the cooldown")
	}
	if !b.Allow() || b.State() != provider.CircuitHalfOpen {
		t.Fatal("no probe after the cooldown")
	}
	if b.Allow() || !b.Rejecting() {
		t.Fatal("a second call got through while probing")
	}

	// The probe fails: open again for another cooldown.
	b.Failure()
	if b.State() != provider.CircuitOpen || b.Allow() {
		t.Fatal("failed probe did not re-open")
	}
	c.advance(10 * time.Second)
	b.Allow()   // probe
	b.Success() // provider is back
	if b.State() != provider.CircuitClosed || !b.Allow() {
		t.Fatal("successful probe did not close")
	}

	want := []string{"closed→open", "open→half_open", "half_open→open", "open→half_open", "half_open→closed"}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v", changes)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("changes = %v, want %v", changes, want)
		}
	}
}

func TestBreakerAbandonedProbeFreesTheSlot(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	b := provider.NewBreaker(1, time.Second)
	b.SetClock(c.now)
	b.Failure()
	c.advance(time.Second)
	b.Allow() // probe
	b.Abandon()
	if !b.Allow() {
		t.Fatal("abandoned probe blocked the next probe")
	}
}

// Through the client: 5xx and network errors open the circuit, then calls
// stop reaching the provider; 4xx answers never open it.
func TestClientCircuitBreaking(t *testing.T) {
	var hits atomic.Int32
	status := atomic.Int32{}
	status.Store(503)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"error":{"code":"x","message":"y"}}`))
	}))
	defer srv.Close()

	b := provider.NewBreaker(3, time.Hour)
	c := provider.New(srv.URL, "k", time.Second, b)
	ctx := context.Background()

	for range 3 {
		_, _ = c.GetOrder(ctx, "ord_1")
	}
	if b.State() != provider.CircuitOpen || !c.CircuitRejecting() {
		t.Fatalf("state = %s after three 503s", b.State())
	}
	if _, err := c.GetOrder(ctx, "ord_1"); !errors.Is(err, provider.ErrCircuitOpen) {
		t.Fatalf("open circuit: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("provider hit %d times, want 3 (the 4th call must not reach it)", hits.Load())
	}

	// 4xx answers are the provider working: they never open the circuit.
	status.Store(409)
	b2 := provider.NewBreaker(2, time.Hour)
	c2 := provider.New(srv.URL, "k", time.Second, b2)
	for range 5 {
		_, _ = c2.GetOrder(ctx, "ord_1")
	}
	if b2.State() != provider.CircuitClosed {
		t.Fatalf("4xx opened the circuit: %s", b2.State())
	}

	// Network errors count.
	b3 := provider.NewBreaker(2, time.Hour)
	c3 := provider.New("http://127.0.0.1:1", "k", 200*time.Millisecond, b3)
	_, _ = c3.ListPrices(ctx)
	_, _ = c3.ListPrices(ctx)
	if b3.State() != provider.CircuitOpen {
		t.Fatalf("network errors did not open the circuit: %s", b3.State())
	}
}
