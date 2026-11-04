package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

const (
	streamPoll      = 500 * time.Millisecond // how often to look for new rows
	streamHeartbeat = 15 * time.Second       // keeps proxies from closing an idle stream
	streamBatch     = 500
)

// streamItem is one SSE event: its id is the row id, so a reconnecting
// client resumes after the last one it saw.
type streamItem struct {
	ID   int64
	Data any
}

// sse serves Server-Sent Events from a table that only grows. It replays
// every row after the cursor (?after=<id>, or the Last-Event-ID header a
// browser sends when it reconnects), then follows new rows by polling. It
// reads the database, so nothing is missed and it works with several API
// instances.
func (s *Server) sse(w http.ResponseWriter, r *http.Request, event string,
	next func(ctx context.Context, after int64) ([]streamItem, error)) {

	after := int64(0)
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("after")
	}
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "after must be a non-negative integer")
			return
		}
		after = n
	}

	// A stream lives longer than the server's write timeout.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 2000\n\n")
	_ = rc.Flush()

	poll := time.NewTicker(streamPoll)
	defer poll.Stop()
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()

	for {
		items, err := next(r.Context(), after)
		if err != nil {
			if r.Context().Err() == nil {
				s.log.Warn("stream read failed", "event", event, "err", err)
			}
			return
		}
		for _, item := range items {
			data, err := json.Marshal(item.Data)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", item.ID, event, data); err != nil {
				return
			}
			after = item.ID
		}
		if len(items) > 0 {
			if err := rc.Flush(); err != nil {
				return
			}
		}
		if len(items) == streamBatch {
			continue // more waiting: send it straight away
		}

		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case <-poll.C:
		}
	}
}

// stream is GET /api/stream: the activity feed, live.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	s.sse(w, r, "activity", func(ctx context.Context, after int64) ([]streamItem, error) {
		items, err := s.svc.ListActivity(ctx, DemoUserID, after, service.MaxActivityLimit)
		out := make([]streamItem, len(items))
		for i, a := range items {
			out[i] = streamItem{ID: a.ID, Data: a}
		}
		return out, err
	})
}

// webhookStream is GET /api/admin/webhooks/stream: every webhook request, live.
func (s *Server) webhookStream(w http.ResponseWriter, r *http.Request) {
	s.sse(w, r, "webhook", func(ctx context.Context, after int64) ([]streamItem, error) {
		var logs []domain.WebhookLog
		err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
			var err error
			logs, err = s.webhookRepo.ListLogsAfter(ctx, tx, after, streamBatch)
			return err
		})
		out := make([]streamItem, len(logs))
		for i, l := range logs {
			out[i] = streamItem{ID: l.ID, Data: l}
		}
		return out, err
	})
}
