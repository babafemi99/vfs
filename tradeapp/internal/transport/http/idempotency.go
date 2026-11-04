package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

const maxIdempotencyKeyLen = 255

// idempotent makes a POST handler run at most once per Idempotency-Key.
// action names the operation in the stored record ("account.fund").
//
// The HTTP side lives here: read the header and body, capture the response,
// replay a stored one. IdempoSrv decides everything else (Check, Begin,
// Finish), including which outcomes are kept.
func (s *API) idempotent(action string, next http.HandlerFunc) http.HandlerFunc {
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

		idempo, count := s.Dependency.IdempoSrv, s.Dependency.Metrics.Idempotency
		record, hit, reused := idempo.Check(r.Context(), key, r.URL.Path, body)
		if reused {
			count.Inc("conflict")
			s.writeErr(w, r, domain.ErrIdempotencyConflict)
			return
		}
		if hit {
			count.Inc("replayed")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(*record.ResponseCode)
			_, _ = w.Write(record.ResponseBody)
			return
		}

		if err := idempo.Begin(r.Context(), key, r.URL.Path, action, body); err != nil {
			if errors.Is(err, domain.ErrRequestInProgress) {
				count.Inc("in_progress")
			}
			s.writeErr(w, r, err)
			return
		}
		count.Inc("new")

		rec := &captureWriter{header: http.Header{}, status: http.StatusOK}
		next(rec, r)

		// Use a context that survives the client hanging up, so the outcome
		// is stored even if nobody is waiting for it.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		idempo.Finish(saveCtx, key, rec.status, rec.body.Bytes())

		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
	}
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
