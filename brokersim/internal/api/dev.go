package api

import (
	"net/http"
	"strconv"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
)

// Every dev action goes through the same store/domain methods as the public
// API and the engine, so the state machine still applies and the normal
// webhooks are sent.

func (s *Server) devListOrders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"orders": s.Store.ListOrders(store.ListFilter{})})
}

// devFill: {"qty": 3} fills exactly 3; no body / no qty fills all remaining.
func (s *Server) devFill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Qty *int64 `json:"qty"`
	}
	if err := decode(w, r, &req, true); err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	id := r.PathValue("id")
	if req.Qty == nil {
		s.orderResult(w)(s.Store.FillRemaining(id))
		return
	}
	s.orderResult(w)(s.Store.FillOrder(id, *req.Qty))
}

func (s *Server) devReject(w http.ResponseWriter, r *http.Request) {
	reason, ok := s.reason(w, r, "upstream_rejected")
	if ok {
		s.orderResult(w)(s.Store.RejectOrder(r.PathValue("id"), reason))
	}
}

func (s *Server) devExpire(w http.ResponseWriter, r *http.Request) {
	reason, ok := s.reason(w, r, "day_order_expired")
	if ok {
		s.orderResult(w)(s.Store.ExpireOrder(r.PathValue("id"), reason))
	}
}

// devCancel requests a cancel, exactly like DELETE /v1/orders/{id}.
func (s *Server) devCancel(w http.ResponseWriter, r *http.Request) {
	s.orderResult(w)(s.Store.RequestCancel(r.PathValue("id")))
}

// devConfirmCancel resolves a pending cancel now: CANCELLED, or
// cancel_rejected (too_late_to_cancel) if a fill already completed the order.
func (s *Server) devConfirmCancel(w http.ResponseWriter, r *http.Request) {
	s.orderResult(w)(s.Store.ResolveCancel(r.PathValue("id")))
}

// devRejectCancel refuses a pending cancel; the order goes back to working.
func (s *Server) devRejectCancel(w http.ResponseWriter, r *http.Request) {
	reason, ok := s.reason(w, r, "cancel_rejected")
	if ok {
		s.orderResult(w)(s.Store.RejectCancel(r.PathValue("id"), reason))
	}
}

type engineSettings struct {
	AutoFill           bool `json:"auto_fill"`
	AutoResolveCancels bool `json:"auto_resolve_cancels"`
	MarketOpen         bool `json:"market_open"`
}

func (s *Server) engineSettings() engineSettings {
	return engineSettings{
		AutoFill:           s.Engine.AutoFill(),
		AutoResolveCancels: s.Engine.AutoResolveCancels(),
		MarketOpen:         s.Store.MarketOpen(),
	}
}

func (s *Server) devGetEngine(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engineSettings())
}

// devPutEngine updates only the fields present in the body.
func (s *Server) devPutEngine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoFill           *bool `json:"auto_fill"`
		AutoResolveCancels *bool `json:"auto_resolve_cancels"`
		MarketOpen         *bool `json:"market_open"`
	}
	if err := decode(w, r, &req, false); err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	if req.AutoFill != nil {
		s.Engine.SetAutoFill(*req.AutoFill)
	}
	if req.AutoResolveCancels != nil {
		s.Engine.SetAutoResolveCancels(*req.AutoResolveCancels)
	}
	if req.MarketOpen != nil {
		s.Store.SetMarketOpen(*req.MarketOpen)
	}
	writeJSON(w, http.StatusOK, s.engineSettings())
}

// devDeliveries returns the webhook delivery log, newest first. ?limit=N (default 50).
func (s *Server) devDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, s.Log, domain.Errorf(domain.CodeValidation, "limit must be a positive integer"), nil)
			return
		}
		limit = n
	}
	ds := s.Dispatcher.Deliveries()
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": ds[:min(limit, len(ds))]})
}

func (s *Server) devRedeliver(w http.ResponseWriter, r *http.Request) {
	d, err := s.Dispatcher.Redeliver(r.PathValue("event_id"))
	if err != nil {
		writeError(w, s.Log, err, nil)
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

// devReset clears all orders, quotes and deliveries. Config and engine settings stay.
func (s *Server) devReset(w http.ResponseWriter, r *http.Request) {
	s.Store.Reset()
	s.Dispatcher.Reset()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// orderResult writes the order (200) or the error. Errors include the
// current order when there is one, so you can see why the action was refused.
func (s *Server) orderResult(w http.ResponseWriter) func(domain.Order, error) {
	return func(o domain.Order, err error) {
		if err != nil {
			var cur *domain.Order
			if o.ID != "" {
				cur = &o
			}
			writeError(w, s.Log, err, cur)
			return
		}
		writeJSON(w, http.StatusOK, o)
	}
}

// reason reads an optional {"reason": "..."} body.
func (s *Server) reason(w http.ResponseWriter, r *http.Request, def string) (string, bool) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(w, r, &req, true); err != nil {
		writeError(w, s.Log, err, nil)
		return "", false
	}
	if req.Reason == "" {
		req.Reason = def
	}
	return req.Reason, true
}
