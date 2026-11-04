package httpapi

import (
	"net/http"
	"strconv"

	"github.com/babafem99/vfs/tradeapp/internal/service"
)

func (s *API) getAccount(w http.ResponseWriter, r *http.Request) {
	summary, err := s.Dependency.TradeSrv.GetAccount(r.Context(), DemoUserID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *API) fundAccount(w http.ResponseWriter, r *http.Request) {
	var req fundRequest
	if !decode(w, r, &req) {
		return
	}

	result, err := s.Dependency.TradeSrv.FundAccount(r.Context(), DemoUserID, req.Amount)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *API) listActivity(w http.ResponseWriter, r *http.Request) {
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

	items, err := s.Dependency.TradeSrv.ListActivity(r.Context(), DemoUserID, afterID, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
