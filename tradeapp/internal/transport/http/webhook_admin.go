package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

// listWebhooks is GET /api/admin/webhooks?limit=n: the newest requests,
// oldest first (ready to print top to bottom like a terminal).
func (s *API) listWebhooks(w http.ResponseWriter, r *http.Request) {
	limit := service.DefaultWebhookLogLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer")
			return
		}
		limit = n
	}

	logs, err := s.Dependency.WebhookSrv.ListLatestLogs(r.Context(), limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": logs})
}

// getWebhook is GET /api/admin/webhooks/{id}: one request with its body and
// processing state.
func (s *API) getWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such webhook")
		return
	}

	log, err := s.Dependency.WebhookSrv.GetLog(r.Context(), id)
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
