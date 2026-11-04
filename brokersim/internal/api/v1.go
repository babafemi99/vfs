package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
)

func (s *Server) createQuote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol string      `json:"symbol"`
		Side   domain.Side `json:"side"`
		Qty    int64       `json:"qty"`
	}
	if err := decode(w, r, &req, false); err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	q, err := s.Store.CreateQuote(req.Symbol, req.Side, req.Qty)
	if err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) getQuote(w http.ResponseWriter, r *http.Request) {
	q, err := s.Store.GetQuote(r.PathValue("id"))
	if err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) acceptQuote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientOrderID string `json:"client_order_id"`
	}
	if err := decode(w, r, &req, true); err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	switch {
	case key != "" && req.ClientOrderID != "" && key != req.ClientOrderID:
		writeError(w, s.Log, domain.Errorf(domain.CodeValidation, "Idempotency-Key and client_order_id differ"), nil)
		return
	case req.ClientOrderID == "":
		req.ClientOrderID = key
	}
	o, created, err := s.Store.AcceptQuote(r.PathValue("id"), req.ClientOrderID)
	if err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, o)
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.GetOrder(r.PathValue("id"))
	if err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// listOrders: with client_order_id it returns that single order (or 404);
// otherwise {"orders": [...]} filtered by status and since, newest first.
func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if coid := q.Get("client_order_id"); coid != "" {
		o, err := s.Store.GetOrderByClientOrderID(coid)
		if err != nil {
			writeError(w, s.Log, err, nil)
			return
		}
		writeJSON(w, http.StatusOK, o)
		return
	}

	var f store.ListFilter
	if raw := q.Get("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			st := domain.OrderStatus(strings.TrimSpace(part))
			if !validStatus(st) {
				writeError(w, s.Log, domain.Errorf(domain.CodeValidation, "unknown status %q", st), nil)
				return
			}
			f.Statuses = append(f.Statuses, st)
		}
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, s.Log, domain.Errorf(domain.CodeValidation, "since must be RFC3339"), nil)
			return
		}
		f.Since = t
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": s.Store.ListOrders(f)})
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.RequestCancel(r.PathValue("id"))
	switch {
	case errors.Is(err, domain.ErrOrderFinal):
		writeError(w, s.Log, err, &o)
	case err != nil:
		writeError(w, s.Log, err, nil)
	default:
		writeJSON(w, http.StatusAccepted, o)
	}
}

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prices": s.Market.Prices()})
}

func (s *Server) getPrice(w http.ResponseWriter, r *http.Request) {
	sym := r.PathValue("symbol")
	p, ok := s.Market.Price(sym)
	if !ok {
		writeError(w, s.Log, domain.Errorf(domain.CodeNotFound, "unknown symbol %q", sym), nil)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// decode reads a JSON body (max 1 MiB). allowEmpty lets an empty body through.
func decode(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) error {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
	if err == nil || (allowEmpty && errors.Is(err, io.EOF)) {
		return nil
	}
	return domain.Errorf(domain.CodeValidation, "invalid JSON body: %v", err)
}

func validStatus(s domain.OrderStatus) bool {
	switch s {
	case domain.OrderOpen, domain.OrderPartiallyFilled, domain.OrderPendingCancel,
		domain.OrderFilled, domain.OrderCancelled, domain.OrderRejected, domain.OrderExpired:
		return true
	}
	return false
}
