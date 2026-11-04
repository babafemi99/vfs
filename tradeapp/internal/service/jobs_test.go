package service

import (
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// queueJobs inserts n pending process_event jobs with no event, due now.
// Running one fails permanently (there is no event to process).
func (s *Service) queueJobs(t *testing.T, n int) {
	t.Helper()
	s.inTx(t, func(tx pgx.Tx) error {
		for range n {
			if err := s.jobRepo.CreateJob(ctx, tx, domain.Job{
				Kind: domain.JobProcessEvent, Status: domain.JobPending, MaxAttempts: 3, RunAt: s.now(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Two workers claiming at the same time never get the same job.
func TestClaimJobsSkipsLockedRows(t *testing.T) {
	s := setup(t)
	s.queueJobs(t, 20)

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := s.ClaimJobs(ctx, 5)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, j := range jobs {
				seen[j.ID]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct jobs, want 20", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d claimed %d times", id, n)
		}
	}
}

// A worker that dies keeps its lease until it expires; then the job is
// claimed again, and the dead worker's late result is ignored.
func TestExpiredLeaseIsReclaimed(t *testing.T) {
	s := setup(t)
	s.queueJobs(t, 1)

	first, _ := s.ClaimJobs(ctx, 10)
	if len(first) != 1 || first[0].Attempts != 1 {
		t.Fatalf("first claim = %+v", first)
	}
	if again, _ := s.ClaimJobs(ctx, 10); len(again) != 0 {
		t.Fatal("claimed while leased")
	}

	s.advance(s.cfg.JobLease + time.Second)
	second, _ := s.ClaimJobs(ctx, 10)
	if len(second) != 1 || second[0].Attempts != 2 {
		t.Fatalf("reclaim = %+v", second)
	}

	// The first worker finally reports back: its outcome must not count.
	_, _ = s.RunJob(ctx, first[0])
	if running := s.jobsWith(t, domain.JobRunning); len(running) != 1 || running[0].Attempts != 2 {
		t.Fatalf("stale result changed the job: %+v", running)
	}
}

// A job that can never succeed (no event to process) is dead on the first
// attempt, whatever its budget.
func TestPermanentFailureGoesStraightToDead(t *testing.T) {
	s := setup(t)
	s.queueJobs(t, 1)
	s.runJobs(t)

	dead := s.jobsWith(t, domain.JobDead)
	if len(dead) != 1 || dead[0].Attempts != 1 || dead[0].LastError == nil {
		t.Fatalf("dead = %+v", dead)
	}
}

// Full jitter: each delay is somewhere between the base and this
// attempt's exponential ceiling (1s, 2s, 4s, … capped at JobBackoffMax).
func TestRetryDelayFullJitter(t *testing.T) {
	s := setup(t)
	base := s.cfg.JobBackoffBase
	for attempt, ceiling := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		lowest, highest := time.Hour, time.Duration(0)
		for range 200 {
			d := s.retryDelay(attempt + 1)
			if d < base || d > ceiling {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt+1, d, base, ceiling)
			}
			lowest, highest = min(lowest, d), max(highest, d)
		}
		// Spread across the window, not bunched at one end.
		if ceiling > base && highest-lowest < (ceiling-base)/2 {
			t.Errorf("attempt %d: delays only span %v..%v", attempt+1, lowest, highest)
		}
	}
	if d := s.retryDelay(40); d < base || d > s.cfg.JobBackoffMax {
		t.Errorf("capped delay = %v", d)
	}
}
