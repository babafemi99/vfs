package httpapi

import "net/http"

// runRecon reconciles now and returns the report. It is a check, not a
// money movement, so it needs no idempotency key; running it twice is fine.
func (s *API) runRecon(w http.ResponseWriter, r *http.Request) {
	report, err := s.Dependency.TradeSrv.RunReconciliation(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *API) latestRecon(w http.ResponseWriter, r *http.Request) {
	report, err := s.Dependency.TradeSrv.LatestReconciliation(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// compareOrder shows our order next to the provider's live view of it.
func (s *API) compareOrder(w http.ResponseWriter, r *http.Request) {
	cmp, err := s.Dependency.TradeSrv.CompareOrder(r.Context(), DemoUserID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cmp)
}
