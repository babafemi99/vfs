package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

func (s *API) listPrices(w http.ResponseWriter, r *http.Request) {
	prices, err := s.Dependency.TradeSrv.ListPrices(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

func (s *API) createQuote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if !decode(w, r, &req) {
		return
	}

	quote, err := s.Dependency.TradeSrv.CreateQuote(r.Context(), req.Symbol, req.Side, req.Qty)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, quote)
}

func (s *API) placeOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if !decode(w, r, &req) {
		return
	}

	order, err := s.Dependency.TradeSrv.PlaceOrder(r.Context(), DemoUserID, req.QuoteID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *API) listOrders(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "all"
	}

	orders, err := s.Dependency.TradeSrv.ListOrders(r.Context(), DemoUserID, status)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *API) getOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.Dependency.TradeSrv.GetOrder(r.Context(), DemoUserID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *API) listJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "dead"
	}

	jobs, err := s.Dependency.TradeSrv.ListJobs(r.Context(), status)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *API) retryJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}

	job, err := s.Dependency.TradeSrv.RetryJob(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// cancelOrder: 200 when the order was cancelled right away (it had not been
// sent yet), 202 when the cancel is in progress at the broker.
func (s *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	order, pending, err := s.Dependency.TradeSrv.CancelOrder(r.Context(), DemoUserID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if pending {
		status = http.StatusAccepted
	}
	writeJSON(w, status, order)
}

func (s *API) listBreaks(w http.ResponseWriter, r *http.Request) {
	breaks, err := s.Dependency.TradeSrv.ListBreaks(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"breaks": breaks})
}

func (s *API) checkInvariants(w http.ResponseWriter, r *http.Request) {
	results, err := s.Dependency.TradeSrv.CheckInvariants(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	ok := true
	for _, res := range results {
		ok = ok && res.OK
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "invariants": results})
}

func (s *API) jobStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Dependency.TradeSrv.JobStats(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

type retryDeadJobsRequest struct {
	Kind string     `json:"kind"` // optional: only this kind
	From *time.Time `json:"from"` // optional: died at or after (RFC 3339)
	To   *time.Time `json:"to"`   // optional: died before
}

// retryDeadJobs replays dead jobs in bulk. An empty body replays them all.
func (s *API) retryDeadJobs(w http.ResponseWriter, r *http.Request) {
	var req retryDeadJobsRequest
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	retried, err := s.Dependency.TradeSrv.RetryDeadJobs(r.Context(), req.Kind, req.From, req.To)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"retried": retried})
}
