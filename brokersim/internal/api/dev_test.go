package api

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/webhook"
)

// dev posts to a /dev route and decodes the order (or error) it returns.
func (e *env) dev(path, body string, wantStatus int) domain.Order {
	e.t.Helper()
	var o domain.Order
	if got := e.do("POST", path, body, nil, &o); got != wantStatus {
		e.t.Fatalf("POST %s %s: status %d, want %d", path, body, got, wantStatus)
	}
	return o
}

// expect checks status, filled/qty, and the event the last action emitted.
func (e *env) expect(o domain.Order, status domain.OrderStatus, filled int64, event domain.EventType) {
	e.t.Helper()
	if o.Status != status || o.FilledQty != filled {
		e.t.Errorf("order = %s %d/%d, want %s %d", o.Status, o.FilledQty, o.Qty, status, filled)
	}
	if ev := e.lastEvent(); ev.Type != event {
		e.t.Errorf("last event = %s, want %s", ev.Type, event)
	}
}

func (e *env) newOrder(key string) domain.Order {
	e.t.Helper()
	return e.accept(e.quote().ID, key) // qty 10
}

func TestDevOnlyFromLoopback(t *testing.T) {
	e := newEnv(t)
	for _, addr := range []string{"192.168.1.5:4000", "[2001:db8::1]:4000"} {
		req := httptest.NewRequest("GET", "/dev/orders", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"forbidden"`) {
			t.Errorf("%s: %d %s", addr, rec.Code, rec.Body)
		}
	}
	for _, addr := range []string{"127.0.0.1:4000", "[::1]:4000"} {
		req := httptest.NewRequest("GET", "/dev/orders", nil) // no API key needed
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s: %d %s", addr, rec.Code, rec.Body)
		}
	}
}

func TestDevPartialFillThenFill(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.expect(e.dev("/dev/orders/"+o.ID+"/fill", `{"qty":3}`, 200), domain.OrderPartiallyFilled, 3, domain.EventOrderPartiallyFilled)
	e.expect(e.dev("/dev/orders/"+o.ID+"/fill", `{"qty":2}`, 200), domain.OrderPartiallyFilled, 5, domain.EventOrderPartiallyFilled)
	e.expect(e.dev("/dev/orders/"+o.ID+"/fill", ``, 200), domain.OrderFilled, 10, domain.EventOrderFilled)

	// Final: everything is refused with the order attached.
	ae := e.expectError("POST", "/dev/orders/"+o.ID+"/fill", `{"qty":1}`, nil, 409, "order_final")
	if ae.Order == nil || ae.Order.Status != domain.OrderFilled {
		t.Errorf("error body should carry the order: %+v", ae.Order)
	}
	e.expectError("POST", "/dev/orders/"+o.ID+"/cancel", ``, nil, 409, "order_final")
	e.expectError("POST", "/dev/orders/"+o.ID+"/expire", ``, nil, 409, "order_final")
}

func TestDevFillValidation(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.expectError("POST", "/dev/orders/"+o.ID+"/fill", `{"qty":11}`, nil, 400, "validation_error")
	e.expectError("POST", "/dev/orders/"+o.ID+"/fill", `{"qty":0}`, nil, 400, "validation_error")
	e.expectError("POST", "/dev/orders/ord_nope/fill", ``, nil, 404, "not_found")
}

func TestDevCancelConfirm(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.expect(e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200), domain.OrderPendingCancel, 0, domain.EventOrderPendingCancel)
	e.expect(e.dev("/dev/orders/"+o.ID+"/cancel/confirm", ``, 200), domain.OrderCancelled, 0, domain.EventOrderCancelled)
}

func TestDevPartialFillThenCancel(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/fill", `{"qty":4}`, 200)
	e.expect(e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200), domain.OrderPendingCancel, 4, domain.EventOrderPendingCancel)
	e.expect(e.dev("/dev/orders/"+o.ID+"/cancel/confirm", ``, 200), domain.OrderCancelled, 4, domain.EventOrderCancelled)
}

func TestDevPartialFillDuringPendingCancel(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200)
	e.expect(e.dev("/dev/orders/"+o.ID+"/fill", `{"qty":3}`, 200), domain.OrderPendingCancel, 3, domain.EventOrderPartiallyFilled)
	e.expect(e.dev("/dev/orders/"+o.ID+"/cancel/confirm", ``, 200), domain.OrderCancelled, 3, domain.EventOrderCancelled)
}

func TestDevFillWinsCancelRace(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200)
	e.expect(e.dev("/dev/orders/"+o.ID+"/fill", ``, 200), domain.OrderFilled, 10, domain.EventOrderFilled)
	got := e.dev("/dev/orders/"+o.ID+"/cancel/confirm", ``, 200)
	e.expect(got, domain.OrderFilled, 10, domain.EventOrderCancelRejected)
	if r := e.lastEvent().Reason; r != "too_late_to_cancel" {
		t.Errorf("reason = %q", r)
	}
	// The cancel is settled now; confirming again has nothing to resolve.
	e.expectError("POST", "/dev/orders/"+o.ID+"/cancel/confirm", ``, nil, 409, "invalid_transition")
}

func TestDevRejectCancel(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200)
	got := e.dev("/dev/orders/"+o.ID+"/cancel/reject", ``, 200)
	e.expect(got, domain.OrderOpen, 0, domain.EventOrderCancelRejected)
	if got.Reason != "cancel_rejected" {
		t.Errorf("reason = %q", got.Reason)
	}

	// With fills it goes back to PARTIALLY_FILLED, and keeps working.
	p := e.newOrder("b")
	e.dev("/dev/orders/"+p.ID+"/fill", `{"qty":2}`, 200)
	e.dev("/dev/orders/"+p.ID+"/cancel", ``, 200)
	e.expect(e.dev("/dev/orders/"+p.ID+"/cancel/reject", `{"reason":"exchange_busy"}`, 200),
		domain.OrderPartiallyFilled, 2, domain.EventOrderCancelRejected)
	e.expect(e.dev("/dev/orders/"+p.ID+"/fill", ``, 200), domain.OrderFilled, 10, domain.EventOrderFilled)

	e.expectError("POST", "/dev/orders/"+o.ID+"/cancel/reject", ``, nil, 409, "invalid_transition")
}

func TestDevReject(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	got := e.dev("/dev/orders/"+o.ID+"/reject", `{"reason":"insufficient_liquidity"}`, 200)
	e.expect(got, domain.OrderRejected, 0, domain.EventOrderRejected)
	if got.Reason != "insufficient_liquidity" {
		t.Errorf("reason = %q", got.Reason)
	}

	// Only OPEN orders can be rejected.
	p := e.newOrder("b")
	e.dev("/dev/orders/"+p.ID+"/fill", `{"qty":1}`, 200)
	e.expectError("POST", "/dev/orders/"+p.ID+"/reject", ``, nil, 409, "invalid_transition")
}

func TestDevExpire(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/fill", `{"qty":6}`, 200)
	got := e.dev("/dev/orders/"+o.ID+"/expire", ``, 200)
	e.expect(got, domain.OrderExpired, 6, domain.EventOrderExpired)
	if got.Reason != "day_order_expired" {
		t.Errorf("reason = %q", got.Reason)
	}

	// PENDING_CANCEL can't expire; the cancel has to resolve first.
	p := e.newOrder("b")
	e.dev("/dev/orders/"+p.ID+"/cancel", ``, 200)
	e.expectError("POST", "/dev/orders/"+p.ID+"/expire", ``, nil, 409, "invalid_transition")
}

func TestDevEngineSettings(t *testing.T) {
	e := newEnv(t)
	var s engineSettings
	e.do("GET", "/dev/engine", "", nil, &s)
	if s != (engineSettings{AutoFill: false, AutoResolveCancels: true, MarketOpen: true}) {
		t.Errorf("initial = %+v", s)
	}
	// Partial update: only market_open changes.
	e.do("PUT", "/dev/engine", `{"market_open":false}`, nil, &s)
	if s != (engineSettings{AutoFill: false, AutoResolveCancels: true, MarketOpen: false}) {
		t.Errorf("after market_open=false: %+v", s)
	}
	q := e.quote()
	e.expectError("POST", "/v1/quotes/"+q.ID+"/accept", `{"client_order_id":"x"}`, nil, 409, "market_closed")

	e.do("PUT", "/dev/engine", `{"market_open":true,"auto_resolve_cancels":false}`, nil, &s)
	o := e.accept(q.ID, "x")
	e.dev("/dev/orders/"+o.ID+"/cancel", ``, 200)
	e.clock.Advance(time.Minute)
	e.eng.Tick()
	var cur domain.Order
	e.do("GET", "/v1/orders/"+o.ID, "", nil, &cur)
	if cur.Status != domain.OrderPendingCancel {
		t.Errorf("with auto_resolve_cancels off the cancel should wait, got %s", cur.Status)
	}

	e.do("PUT", "/dev/engine", `{"auto_fill":true}`, nil, &s)
	if !s.AutoFill || !e.eng.AutoFill() {
		t.Error("auto_fill not applied")
	}
	e.expectError("PUT", "/dev/engine", `nope`, nil, 400, "validation_error")
}

func TestDevDeliveriesRedeliverReset(t *testing.T) {
	e := newEnv(t)
	o := e.newOrder("a")
	e.dev("/dev/orders/"+o.ID+"/fill", ``, 200)
	e.waitReceived(2)

	var log struct{ Deliveries []webhook.Delivery }
	e.do("GET", "/dev/deliveries", "", nil, &log)
	if len(log.Deliveries) != 2 || log.Deliveries[0].Type != domain.EventOrderFilled {
		t.Fatalf("deliveries = %+v", log.Deliveries)
	}
	e.do("GET", "/dev/deliveries?limit=1", "", nil, &log)
	if len(log.Deliveries) != 1 {
		t.Errorf("limit ignored: %d rows", len(log.Deliveries))
	}

	evID := log.Deliveries[0].EventID
	var d webhook.Delivery
	if code := e.do("POST", "/dev/deliveries/"+evID+"/redeliver", "", nil, &d); code != 202 || d.State != webhook.StatePending {
		t.Errorf("redeliver: %d %+v", code, d)
	}
	got := e.waitReceived(3)
	if got[2].ID != evID || got[2].Type != domain.EventOrderFilled {
		t.Errorf("redelivered %s %s, want same event %s", got[2].ID, got[2].Type, evID)
	}
	e.expectError("POST", "/dev/deliveries/evt_nope/redeliver", "", nil, 404, "not_found")

	var ok map[string]bool
	e.do("POST", "/dev/reset", "", nil, &ok)
	var orders struct{ Orders []domain.Order }
	e.do("GET", "/dev/orders", "", nil, &orders)
	e.do("GET", "/dev/deliveries", "", nil, &log)
	if len(orders.Orders) != 0 || len(log.Deliveries) != 0 {
		t.Errorf("after reset: %d orders, %d deliveries", len(orders.Orders), len(log.Deliveries))
	}
	e.expectError("GET", "/v1/orders/"+o.ID, "", nil, 404, "not_found")
}

// Every order state is reachable through the dev API, and the webhooks the
// receiver got match the events in order.
func TestDevAllStatesReachable(t *testing.T) {
	e := newEnv(t)
	seen := map[domain.OrderStatus]bool{}
	note := func(o domain.Order) { seen[o.Status] = true }

	a, b, c, d := e.newOrder("a"), e.newOrder("b"), e.newOrder("c"), e.newOrder("d")
	note(a)
	note(e.dev("/dev/orders/"+a.ID+"/fill", `{"qty":1}`, 200))
	note(e.dev("/dev/orders/"+a.ID+"/cancel", ``, 200))
	note(e.dev("/dev/orders/"+a.ID+"/cancel/confirm", ``, 200))
	note(e.dev("/dev/orders/"+b.ID+"/fill", ``, 200))
	note(e.dev("/dev/orders/"+c.ID+"/reject", ``, 200))
	note(e.dev("/dev/orders/"+d.ID+"/expire", ``, 200))

	for _, s := range []domain.OrderStatus{
		domain.OrderOpen, domain.OrderPartiallyFilled, domain.OrderPendingCancel,
		domain.OrderFilled, domain.OrderCancelled, domain.OrderRejected, domain.OrderExpired,
	} {
		if !seen[s] {
			t.Errorf("state %s not reached", s)
		}
	}

	types := e.eventTypes()
	got := e.waitReceived(len(types))
	var gotTypes []domain.EventType
	for _, p := range got {
		gotTypes = append(gotTypes, p.Type)
	}
	if !reflect.DeepEqual(types, gotTypes) {
		t.Errorf("webhooks out of step with events:\n%v\n%v", types, gotTypes)
	}
}

func TestDevPrices(t *testing.T) {
	h := newEnv(t).h
	req := httptest.NewRequest("GET", "/dev/prices", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"prices"`) {
		t.Fatalf("prices: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/dev/prices", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("non-loopback: %d", rec.Code)
	}
}
