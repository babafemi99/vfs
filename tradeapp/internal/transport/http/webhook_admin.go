package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// listWebhooks is GET /api/admin/webhooks?limit=n: the newest requests,
// oldest first (ready to print top to bottom like a terminal).
func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be between 1 and 1000")
			return
		}
		limit = n
	}

	var logs []domain.WebhookLog
	err := s.repo.RunInTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		logs, err = s.webhookRepo.ListLatestLogs(r.Context(), tx, limit)
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": logs})
}

// getWebhook is GET /api/admin/webhooks/{id}: one request with its body and
// processing state.
func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such webhook")
		return
	}

	var log domain.WebhookLog
	err = s.repo.RunInTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		log, err = s.webhookRepo.GetLog(r.Context(), tx, id)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such webhook")
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, log)
}
