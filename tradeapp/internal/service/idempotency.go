package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

// IdempotencyConfig sets how long records live.
type IdempotencyConfig struct {
	TTL         time.Duration // records expire this long after they start
	ZombieAfter time.Duration // a STARTED record older than this is released
}

func DefaultIdempotencyConfig() IdempotencyConfig {
	return IdempotencyConfig{TTL: 24 * time.Hour, ZombieAfter: 5 * time.Minute}
}

// IdempotencyService keeps the Idempotency-Key records behind the HTTP
// middleware and makes every decision about them; the middleware only reads
// the header, captures the response and replays it. The user comes from the
// context (domain.Actor), so a key only ever matches its own user's records.
//
// Flow per request:
//
//	Check  → hit: replay the stored response; reused: reject (422)
//	Begin  → claim the key (STARTED); someone else holds it → 409
//	(the handler runs)
//	Finish → 2xx COMPLETED (replayed), 4xx FAILED with the response
//	         (replayed), 5xx FAILED without one (released, retry may run)
type IdempotencyService struct {
	repo    repository.Repository
	keyRepo repository.IdempotencyRepo
	cfg     IdempotencyConfig
	log     *slog.Logger
	now     func() time.Time
}

func NewIdempotencyService(repo repository.Repository, cfg IdempotencyConfig, log *slog.Logger) *IdempotencyService {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &IdempotencyService{repo: repo, cfg: cfg, log: log, now: time.Now}
}

// Check looks up an existing record for key.
//
//	hit    — same request, finished with a stored response (COMPLETED, or a
//	         FAILED business rejection) → caller replays it
//	reused — same key, different request (body or path) → caller rejects
//	         with 422 idempotency_conflict, whatever the record's status
//
// Both false is a miss (no record, running, or released) → caller calls
// Begin, which decides between "run it" and "already running".
func (s *IdempotencyService) Check(ctx context.Context, key, path string, body []byte) (record *domain.IdempotencyRecord, hit bool, reused bool) {
	actor, ok := domain.ActorFrom(ctx)
	if key == "" || !ok {
		return nil, false, false
	}

	var rec domain.IdempotencyRecord
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		rec, err = s.keyRepo.GetKey(ctx, tx, actor.UserID, key)
		return err
	})
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			s.log.ErrorContext(ctx, "idempotency: lookup failed", "key", key, "err", err)
		}
		return nil, false, false
	}

	// Same key, different request: a client bug, never replay or run it.
	if rec.RequestHash != hashBody(body) || rec.RequestPath != path {
		s.log.WarnContext(ctx, "idempotency key reused with a different request", "key", key, "user_id", actor.UserID,
			"path", path, "first_path", rec.RequestPath)
		return &rec, false, true
	}

	if rec.Replayable() {
		return &rec, true, false
	}
	return nil, false, false
}

// Begin claims key for this request (STARTED). Call it after a Check miss,
// before running the request. Exactly one caller can hold a key:
//
//   - new key: inserted
//   - released key (FAILED, no response): reclaimed for this attempt
//   - anything else (running, or finished meanwhile): ErrRequestInProgress,
//     and the client retries (and then gets the replay)
func (s *IdempotencyService) Begin(ctx context.Context, key, path, action string, body []byte) error {
	actor, ok := domain.ActorFrom(ctx)
	if !ok {
		return domain.ErrUnauthenticated
	}

	now := s.now().UTC()
	rec := domain.IdempotencyRecord{
		UserID:      actor.UserID,
		Key:         key,
		RequestHash: hashBody(body),
		RequestPath: path,
		Action:      action,
		Status:      domain.IdempotencyStarted,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(s.cfg.TTL),
	}

	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		err := s.keyRepo.CreateKey(ctx, tx, rec)
		if !errors.Is(err, domain.ErrDuplicate) {
			return err // claimed, or a real failure
		}
		err = s.keyRepo.ReclaimKey(ctx, tx, rec)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrRequestInProgress
		}
		return err
	})
}

// Finish stores the outcome of the request from the response it produced.
// This is the one "what do we keep?" decision, kept here so the transport
// stays dumb:
//
//	2xx  → COMPLETED, response replayed to retries
//	4xx  → FAILED with the response: a business rejection (insufficient
//	       funds, expired quote, bad input) is replayed, not re-run; the key
//	       is used up, the client makes a new one for a new attempt
//	else → FAILED with no response: released. Our handlers return 5xx only
//	       when their transaction rolled back, so nothing happened and the
//	       same key may safely run again (the browser retries within seconds)
//
// Errors are logged, not returned: the response is going out regardless. If
// this write is lost the record stays STARTED and SweepZombies releases it.
func (s *IdempotencyService) Finish(ctx context.Context, key string, statusCode int, body []byte) {
	actor, ok := domain.ActorFrom(ctx)
	if !ok {
		return
	}

	status := domain.IdempotencyFailed
	code, stored := &statusCode, body
	switch {
	case statusCode >= 200 && statusCode < 300:
		status = domain.IdempotencyCompleted
	case statusCode >= 400 && statusCode < 500:
		// business rejection: keep it for replay
	default:
		code, stored = nil, nil
		s.log.DebugContext(ctx, "idempotency: transient failure, key released", "key", key, "status", statusCode)
	}

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.keyRepo.UpdateKeyFinished(ctx, tx, actor.UserID, key, status, code, stored, s.now().UTC())
	})
	if errors.Is(err, domain.ErrNotFound) {
		s.log.WarnContext(ctx, "idempotency: record was no longer STARTED (reaped as a zombie?)", "key", key, "status", statusCode)
		return
	}
	if err != nil {
		s.log.ErrorContext(ctx, "idempotency: saving the outcome failed", "key", key, "status", statusCode, "err", err)
	}
}

// SweepZombies releases records STARTED longer than ZombieAfter: their
// request crashed or hung between Begin and Finish. Until then a retry gets
// 409; after, it may run. Run it periodically (worker.RunCleanup).
func (s *IdempotencyService) SweepZombies(ctx context.Context) error {
	now := s.now().UTC()
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.keyRepo.UpdateStaleKeysFailed(ctx, tx, now.Add(-s.cfg.ZombieAfter), now)
	})
}

// CleanupExpired deletes records past their expiry.
func (s *IdempotencyService) CleanupExpired(ctx context.Context) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.keyRepo.DeleteKeysExpiredBefore(ctx, tx, s.now().UTC())
	})
}

func hashBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}
