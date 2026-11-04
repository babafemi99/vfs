package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request) {
	prices, err := s.svc.ListPrices(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

type quoteRequest struct {
	Symbol string `json:"symbol"`
	Side   string `json:"side"`
	Qty    int64  `json:"qty"`
}

func (s *Server) createQuote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if !decode(w, r, &req) {
		return
	}

	quote, err := s.svc.CreateQuote(r.Context(), req.Symbol, req.Side, req.Qty)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, quote)
}

type placeOrderRequest struct {
	QuoteID string `json:"quote_id"`
}

func (s *Server) placeOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if !decode(w, r, &req) {
		return
	}

	order, err := s.svc.PlaceOrder(r.Context(), DemoUserID, req.QuoteID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "all"
	}

	orders, err := s.svc.ListOrders(r.Context(), DemoUserID, status)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.svc.GetOrder(r.Context(), DemoUserID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "dead"
	}

	jobs, err := s.svc.ListJobs(r.Context(), status)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}

	job, err := s.svc.RetryJob(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// cancelOrder: 200 when the order was cancelled right away (it had not been
// sent yet), 202 when the cancel is in progress at the broker.
func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	order, pending, err := s.svc.CancelOrder(r.Context(), DemoUserID, r.PathValue("id"))
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

func (s *Server) listBreaks(w http.ResponseWriter, r *http.Request) {
	breaks, err := s.svc.ListBreaks(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"breaks": breaks})
}

func (s *Server) checkInvariants(w http.ResponseWriter, r *http.Request) {
	results, err := s.svc.CheckInvariants(r.Context())
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

func (s *Server) jobStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.svc.JobStats(r.Context())
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
func (s *Server) retryDeadJobs(w http.ResponseWriter, r *http.Request) {
	var req retryDeadJobsRequest
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	retried, err := s.svc.RetryDeadJobs(r.Context(), req.Kind, req.From, req.To)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"retried": retried})
}
