package service

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

func setupIdempotency(t *testing.T) (*IdempotencyService, *time.Time) {
	t.Helper()
	s := NewIdempotencyService(repository.New(dbtest.New(t)), DefaultIdempotencyConfig(), nil)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

var actorCtx = domain.WithActor(ctx, domain.Actor{UserID: user})

const path = "/api/account/fund"

var body = []byte(`{"amount":100}`)

func mustBegin(t *testing.T, s *IdempotencyService, key string) {
	t.Helper()
	if err := s.Begin(actorCtx, key, path, "account.fund", body); err != nil {
		t.Fatalf("begin %s: %v", key, err)
	}
}

func TestIdempotencyLifecycle(t *testing.T) {
	s, _ := setupIdempotency(t)

	if _, hit, reused := s.Check(actorCtx, "k", path, body); hit || reused {
		t.Fatal("new key: want a miss")
	}
	mustBegin(t, s, "k")

	// Running: a miss for Check, and Begin refuses a second holder.
	if _, hit, reused := s.Check(actorCtx, "k", path, body); hit || reused {
		t.Fatal("running: want a miss")
	}
	if err := s.Begin(actorCtx, "k", path, "account.fund", body); !errors.Is(err, domain.ErrRequestInProgress) {
		t.Fatalf("second begin: %v, want ErrRequestInProgress", err)
	}

	s.Finish(actorCtx, "k", 200, []byte(`{"ok":true}`))
	rec, hit, _ := s.Check(actorCtx, "k", path, body)
	if !hit || *rec.ResponseCode != 200 || string(rec.ResponseBody) != `{"ok":true}` || rec.Status != domain.IdempotencyCompleted {
		t.Fatalf("completed: hit=%v rec=%+v", hit, rec)
	}
	if _, _, reused := s.Check(actorCtx, "k", path, []byte(`{"amount":5}`)); !reused {
		t.Fatal("different body: want reused")
	}
}

func TestIdempotencyBusinessFailureIsKept(t *testing.T) {
	s, _ := setupIdempotency(t)
	mustBegin(t, s, "k")
	s.Finish(actorCtx, "k", 422, []byte(`{"error":"insufficient_funds"}`))

	rec, hit, _ := s.Check(actorCtx, "k", path, body)
	if !hit || rec.Status != domain.IdempotencyFailed || *rec.ResponseCode != 422 {
		t.Fatalf("4xx: hit=%v rec=%+v", hit, rec)
	}
	if err := s.Begin(actorCtx, "k", path, "account.fund", body); !errors.Is(err, domain.ErrRequestInProgress) {
		t.Fatalf("a stored rejection must not be reclaimed: %v", err)
	}
}

func TestIdempotencyTransientFailureReleasesKey(t *testing.T) {
	s, _ := setupIdempotency(t)
	mustBegin(t, s, "k")
	s.Finish(actorCtx, "k", 500, []byte(`{"error":"internal_error"}`))

	if _, hit, reused := s.Check(actorCtx, "k", path, body); hit || reused {
		t.Fatal("5xx: want a miss (released)")
	}
	mustBegin(t, s, "k") // the retry runs
	s.Finish(actorCtx, "k", 200, []byte(`{"ok":true}`))
	if _, hit, _ := s.Check(actorCtx, "k", path, body); !hit {
		t.Fatal("retry after 5xx: want its success stored")
	}
}

// Many requests reclaim one released key at once: exactly one runs.
func TestIdempotencyConcurrentBegin(t *testing.T) {
	s, _ := setupIdempotency(t)
	mustBegin(t, s, "k")
	s.Finish(actorCtx, "k", 503, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Begin(actorCtx, "k", path, "account.fund", body)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case !errors.Is(err, domain.ErrRequestInProgress):
				t.Errorf("begin: %v", err)
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d requests claimed the key, want 1", won)
	}
}

func TestIdempotencyZombiesAndExpiry(t *testing.T) {
	s, now := setupIdempotency(t)
	mustBegin(t, s, "crashed")

	*now = now.Add(4 * time.Minute)
	mustBegin(t, s, "recent")
	if err := s.SweepZombies(actorCtx); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(actorCtx, "crashed", path, "account.fund", body); !errors.Is(err, domain.ErrRequestInProgress) {
		t.Fatalf("4m old: still running, got %v", err)
	}

	*now = now.Add(2 * time.Minute) // crashed is 6m old, recent 2m
	if err := s.SweepZombies(actorCtx); err != nil {
		t.Fatal(err)
	}
	mustBegin(t, s, "crashed") // released: a retry may run
	if err := s.Begin(actorCtx, "recent", path, "account.fund", body); !errors.Is(err, domain.ErrRequestInProgress) {
		t.Fatalf("recent: must not be reaped, got %v", err)
	}

	s.Finish(actorCtx, "recent", 200, []byte(`{}`)) // finishes normally

	*now = now.Add(25 * time.Hour)
	if err := s.CleanupExpired(actorCtx); err != nil {
		t.Fatal(err)
	}
	if _, hit, reused := s.Check(actorCtx, "recent", path, body); hit || reused {
		t.Fatal("expired record should be gone")
	}
}

func TestIdempotencyNeedsActor(t *testing.T) {
	s, _ := setupIdempotency(t)
	if err := s.Begin(ctx, "k", path, "account.fund", body); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("begin without actor: %v", err)
	}
	// Keys are per user: another user's key with the same name is a miss.
	mustBegin(t, s, "k")
	s.Finish(actorCtx, "k", 200, []byte(`{}`))
	other := domain.WithActor(ctx, domain.Actor{UserID: user + 1})
	if _, hit, reused := s.Check(other, "k", path, body); hit || reused {
		t.Fatal("another user's key must not match")
	}
}
