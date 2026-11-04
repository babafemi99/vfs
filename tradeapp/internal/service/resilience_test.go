package service

import (
	"errors"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

// The provider is down. The job keeps retrying with backoff until its retry
// budget runs out, never dies early, makes its last try exactly at the
// give-up time, and only then lands in the dead-letter queue.
func TestRetryBudget(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.cfg.JobMaxAttempts = 50
	s.cfg.JobRetryBudget = 10 * time.Minute
	s.mustFund(t, 1_000_000)
	start := s.now()
	s.setNow(start)
	s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID)
	broker.SetDown(true)

	attempts := 0
	var giveUpAt time.Time
	for {
		if n := s.runJobs(t); n != 1 {
			t.Fatalf("attempt %d: ran %d jobs", attempts+1, n)
		}
		attempts++
		if dead := s.jobsWith(t, domain.JobDead); len(dead) == 1 {
			if s.now().Before(giveUpAt) {
				t.Fatalf("dead at %v, before the give-up time %v", s.now(), giveUpAt)
			}
			break
		}
		pending := s.jobsWith(t, domain.JobPending)
		if len(pending) != 1 || pending[0].GiveUpAt == nil {
			t.Fatalf("after attempt %d: %+v", attempts, pending)
		}
		giveUpAt = *pending[0].GiveUpAt
		if pending[0].RunAt.After(giveUpAt) {
			t.Fatalf("retry scheduled after the give-up time: %v > %v", pending[0].RunAt, giveUpAt)
		}
		s.setNow(pending[0].RunAt) // jump to the next retry
		if attempts > 40 {
			t.Fatal("never gave up")
		}
	}

	if !giveUpAt.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("give-up time %v, want start + 10m", giveUpAt)
	}
	if !s.now().Equal(giveUpAt) {
		t.Fatalf("last try at %v, want exactly the give-up time %v", s.now(), giveUpAt)
	}
	if attempts <= 6 {
		t.Fatalf("only %d attempts in a 10 minute budget", attempts)
	}

	// Retrying from the dead-letter queue gives a fresh budget.
	dead := s.jobsWith(t, domain.JobDead)[0]
	retried, err := s.RetryJob(ctx, dead.ID)
	if err != nil || retried.GiveUpAt == nil || !retried.GiveUpAt.Equal(s.now().Add(10*time.Minute)) || retried.Attempts != 0 {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
}

// A service whose provider client has a circuit breaker on the service's clock.
func setupWithBreaker(t *testing.T, threshold int, cooldown time.Duration) (*Service, *providertest.Broker, *provider.Breaker) {
	t.Helper()
	broker := providertest.New(t)
	breaker := provider.NewBreaker(threshold, cooldown)
	s := New(
		repository.New(dbtest.New(t)),
		provider.New(broker.URL, providertest.APIKey, 2*time.Second, breaker),
		DefaultConfig(),
	)
	s.setNow(time.Now())
	breaker.SetClock(func() time.Time { return s.now() })
	return s, broker, breaker
}

func TestCircuitBreakerPausesProviderJobs(t *testing.T) {
	s, broker, breaker := setupWithBreaker(t, 2, time.Minute)
	s.mustFund(t, 1_000_000)
	var orders []string
	for range 3 {
		orders = append(orders, s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID).ID)
	}

	// Two failures open the circuit; the third job never reaches the broker.
	broker.SetDown(true)
	if n := s.runJobs(t); n != 3 {
		t.Fatalf("ran %d jobs", n)
	}
	if breaker.State() != provider.CircuitOpen {
		t.Fatalf("circuit = %s", breaker.State())
	}
	accepts := broker.AcceptCalls()
	pending := s.jobsWith(t, domain.JobPending)
	if len(pending) != 3 {
		t.Fatalf("pending = %+v", pending)
	}
	used := 0
	for _, j := range pending {
		used += j.Attempts
	}
	if used != 2 {
		t.Fatalf("attempts used = %d, want 2 (the deferred job must not use one)", used)
	}

	// While open: provider calls fail fast, provider jobs are not claimed,
	// but webhook processing (database only) carries on.
	if _, err := s.CreateQuote(ctx, "AAPL", "BUY", 1); !errors.Is(err, domain.ErrProviderUnavailable) {
		t.Fatalf("quote while open: %v", err)
	}
	var e provider.Event
	e.ID, e.Type = "evt_while_open", "order.filled"
	e.Data.Order = &provider.Order{ID: "ord_x", ClientOrderID: "someone-else", Status: "FILLED", Version: 2}
	if _, err := s.ReceiveProviderEvent(ctx, providertest.Body(t, e)); err != nil {
		t.Fatal(err)
	}
	s.setNow(s.now().Add(30 * time.Second)) // retries due, circuit still open
	jobs, err := s.ClaimJobs(ctx, 50)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != domain.JobProcessEvent {
		t.Fatalf("claimed while open = %+v, %v", jobs, err)
	}
	_, _ = s.RunJob(ctx, jobs[0])
	if broker.AcceptCalls() != accepts {
		t.Fatal("the broker was called while the circuit was open")
	}

	// The broker recovers; after the cooldown a probe closes the circuit and
	// every order goes through.
	broker.SetDown(false)
	s.setNow(s.now().Add(time.Minute))
	s.runJobs(t)
	if breaker.State() != provider.CircuitClosed {
		t.Fatalf("circuit after recovery = %s", breaker.State())
	}
	for _, id := range orders {
		if got := s.orderNow(t, id); got.Status != domain.StatusOpen {
			t.Fatalf("order %s = %s", id, got.Status)
		}
	}
	if dead := s.jobsWith(t, domain.JobDead); len(dead) != 0 {
		t.Fatalf("dead jobs: %+v", dead)
	}
	s.assertInvariants(t)
}

func hasAlert(alerts []string, want string) bool {
	for _, a := range alerts {
		if a == want {
			return true
		}
	}
	return false
}

func TestJobStatsAndBulkRetry(t *testing.T) {
	s, broker := setupWithBroker(t)
	s.cfg.JobMaxAttempts = 1 // dead after one failure
	s.mustFund(t, 1_000_000)
	var orders []string
	for range 3 {
		orders = append(orders, s.mustPlace(t, s.mustQuote(t, "AAPL", "BUY", 1).ID).ID)
	}

	stats, err := s.JobStats(ctx)
	if err != nil || stats.Pending != 3 || stats.Dead != 0 || !stats.OK || stats.Circuit != provider.CircuitClosed {
		t.Fatalf("fresh stats = %+v, %v", stats, err)
	}

	// Jobs left waiting too long raise the lag alert.
	s.advance(6 * time.Minute)
	if stats, _ = s.JobStats(ctx); stats.OK || !hasAlert(stats.Alerts, domain.AlertQueueLag) || stats.OldestWaitingSeconds < 360 {
		t.Fatalf("lagging stats = %+v", stats)
	}

	broker.SetDown(true)
	s.runJobs(t)
	stats, _ = s.JobStats(ctx)
	if stats.Dead != 3 || stats.Pending != 0 || !hasAlert(stats.Alerts, domain.AlertDeadLetters) {
		t.Fatalf("after failures = %+v", stats)
	}

	// Bulk retry validates its filters.
	if _, err := s.RetryDeadJobs(ctx, "nope", nil, nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("unknown kind: %v", err)
	}
	later, earlier := s.now(), s.now().Add(-time.Hour)
	if _, err := s.RetryDeadJobs(ctx, "", &later, &earlier); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("from after to: %v", err)
	}
	// A window before the failures matches nothing.
	if n, err := s.RetryDeadJobs(ctx, "", nil, &earlier); err != nil || n != 0 {
		t.Fatalf("empty window: %d, %v", n, err)
	}

	broker.SetDown(false)
	s.cfg.JobMaxAttempts = 50
	n, err := s.RetryDeadJobs(ctx, domain.JobSubmitOrder, nil, nil)
	if err != nil || n != 3 {
		t.Fatalf("bulk retry: %d, %v", n, err)
	}
	if n, _ := s.RetryDeadJobs(ctx, "", nil, nil); n != 0 {
		t.Fatalf("second bulk retry replayed %d", n)
	}

	s.runJobs(t)
	for _, id := range orders {
		if got := s.orderNow(t, id); got.Status != domain.StatusOpen {
			t.Fatalf("order %s = %s", id, got.Status)
		}
	}
	if stats, _ = s.JobStats(ctx); !stats.OK || stats.Dead != 0 {
		t.Fatalf("after recovery = %+v", stats)
	}
	s.assertInvariants(t)
}
