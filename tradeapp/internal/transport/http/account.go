package httpapi

import (
	"net/http"
	"strconv"

	"github.com/babafem99/vfs/tradeapp/internal/service"
)

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	summary, err := s.svc.GetAccount(r.Context(), DemoUserID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

type fundRequest struct {
	Amount int64 `json:"amount"`
}

func (s *Server) fundAccount(w http.ResponseWriter, r *http.Request) {
	var req fundRequest
	if !decode(w, r, &req) {
		return
	}

	result, err := s.svc.FundAccount(r.Context(), DemoUserID, req.Amount)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	afterID, limit := int64(0), service.DefaultActivityLimit

	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "after must be an integer")
			return
		}
		afterID = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer")
			return
		}
		limit = n
	}

	items, err := s.svc.ListActivity(r.Context(), DemoUserID, afterID, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
