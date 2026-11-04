package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

const maxIdempotencyKeyLen = 255

// idempotent makes a POST handler run at most once per Idempotency-Key.
//
//  1. Reserve the key (its own short transaction). If it already exists:
//     different request → 422, still running → 409, finished → replay the
//     stored response byte for byte.
//  2. Run the handler, capturing its response.
//  3. Success (2xx): store the response for replays. Failure: delete the key,
//     so the client can fix the cause and retry with the same key.
//
// If the process dies between 2 and 3 the key stays "in progress" (409) until
// it expires, so the work is never repeated.
func (s *Server) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
			return
		}
		if len(key) > maxIdempotencyKeyLen {
			writeError(w, http.StatusBadRequest, "validation_error", "Idempotency-Key is too long")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "request body too large or unreadable")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		sum := sha256.Sum256([]byte(r.Method + " " + r.URL.Path + "\n" + string(body)))
		hash := hex.EncodeToString(sum[:])

		// 1. Reserve the key, or read the existing one.
		reserved := false
		var existing domain.IdempotencyRecord
		err = s.repo.RunInTx(r.Context(), func(tx pgx.Tx) error {
			err := s.keyRepo.CreateKey(r.Context(), tx, domain.IdempotencyRecord{
				UserID:      DemoUserID,
				Key:         key,
				RequestHash: hash,
			})
			if err == nil {
				reserved = true
				return nil
			}
			if !errors.Is(err, domain.ErrDuplicate) {
				return err
			}
			existing, err = s.keyRepo.GetKey(r.Context(), tx, DemoUserID, key)
			return err
		})
		if err != nil {
			s.writeErr(w, r, err)
			return
		}

		if !reserved {
			switch {
			case existing.RequestHash != hash:
				writeError(w, http.StatusUnprocessableEntity, "idempotency_conflict", domain.ErrIdempotencyConflict.Error())
			case existing.ResponseCode == nil:
				writeError(w, http.StatusConflict, "request_in_progress", domain.ErrRequestInProgress.Error())
			default:
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(*existing.ResponseCode)
				_, _ = w.Write(existing.ResponseBody)
			}
			return
		}

		// 2. Run the handler.
		rec := &captureWriter{header: http.Header{}, status: http.StatusOK}
		next(rec, r)

		// 3. Store the response, or free the key. Use a context that survives
		//    the client disconnecting so the key is never left half done.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		err = s.repo.RunInTx(saveCtx, func(tx pgx.Tx) error {
			if rec.status >= 200 && rec.status < 300 {
				return s.keyRepo.UpdateKeyResponse(saveCtx, tx, DemoUserID, key, rec.status, rec.body.Bytes())
			}
			return s.keyRepo.DeleteKey(saveCtx, tx, DemoUserID, key)
		})
		if err != nil {
			s.log.Error("idempotency: saving result failed", "key", key, "err", err)
		}

		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
	}
}

// webhookLogRetention is how long the webhook log keeps requests.
const webhookLogRetention = 7 * 24 * time.Hour

// RunKeyCleanup deletes, every interval until ctx is cancelled: expired
// idempotency keys, webhook nonces older than twice the timestamp tolerance
// (the timestamp check already refuses anything that old), and webhook log
// rows past their retention.
func (s *Server) RunKeyCleanup(ctx context.Context, ttl, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := s.DeleteExpiredIdempotencyKeys(ctx, ttl); err != nil && ctx.Err() == nil {
			s.log.Warn("idempotency cleanup failed", "err", err)
		}
		err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
			if err := s.webhookRepo.DeleteNoncesSeenBefore(ctx, tx, time.Now().Add(-2*s.cfg.WebhookTolerance)); err != nil {
				return err
			}
			return s.webhookRepo.DeleteLogsReceivedBefore(ctx, tx, time.Now().Add(-webhookLogRetention))
		})
		if err != nil && ctx.Err() == nil {
			s.log.Warn("webhook cleanup failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// DeleteExpiredIdempotencyKeys removes keys older than ttl.
func (s *Server) DeleteExpiredIdempotencyKeys(ctx context.Context, ttl time.Duration) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.keyRepo.DeleteKeysCreatedBefore(ctx, tx, time.Now().Add(-ttl))
	})
}

// captureWriter buffers a handler's response so it can be stored before it
// is sent.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header         { return c.header }
func (c *captureWriter) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *captureWriter) WriteHeader(code int)        { c.status = code }
