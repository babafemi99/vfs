package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/engine"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
	"github.com/babafem99/vfs/lab/brokersim/internal/webhook"
)

const apiKey = "sim_test_key"

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// env is a full simulator: store, engine (ticked manually), dispatcher, and a
// signature-checking webhook receiver.
type env struct {
	t     *testing.T
	h     http.Handler
	store *store.Store
	clock *clock.Fake
	eng   *engine.Engine
	disp  *webhook.Dispatcher

	mu       sync.Mutex
	events   []domain.Event    // every event, in order, recorded synchronously
	received []webhook.Payload // what the receiver got over HTTP
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t}

	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get(webhook.HeaderTimestamp), 10, 64)
		if err := webhook.Verify("whsec_test", ts, r.Header.Get(webhook.HeaderNonce), body, r.Header.Get(webhook.HeaderSignature), time.Minute); err != nil {
			t.Errorf("webhook signature: %v", err)
			w.WriteHeader(401)
			return
		}
		var p webhook.Payload
		_ = json.Unmarshal(body, &p)
		e.mu.Lock()
		e.received = append(e.received, p)
		e.mu.Unlock()
	}))
	t.Cleanup(recv.Close)

	e.disp = webhook.New(webhook.Options{
		URL: recv.URL, Secret: "whsec_test", Backoff: []time.Duration{time.Millisecond},
		Rand: rand.New(rand.NewPCG(1, 3)), Logger: quietLog,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.disp.Run(ctx)

	syms, _ := market.ParseSymbols("AAPL:19000,MSFT:41000")
	mkt := market.New(syms, 20, rand.New(rand.NewPCG(42, 1)))
	e.clock = clock.NewFake(time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC))
	e.store = store.New(store.Config{
		QuoteTTL: 15 * time.Second, FeeBps: 50,
		CancelDelayMin: 300 * time.Millisecond, CancelDelayMax: 2 * time.Second,
		MarketOpen: true,
	}, e.clock, rand.New(rand.NewPCG(42, 2)), mkt, func(ev domain.Event) {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
		e.disp.Enqueue(ev)
	})
	e.eng = engine.New(engine.Config{Tick: time.Second, MaxChunkPct: 60, OrderTTL: 10 * time.Minute},
		e.store, mkt, e.clock, rand.New(rand.NewPCG(42, 4)), quietLog)
	e.h = NewRouter(Deps{Store: e.store, Market: mkt, Engine: e.eng, Dispatcher: e.disp, APIKey: apiKey, Log: quietLog})
	return e
}

// eventTypes returns the types of all events so far.
func (e *env) eventTypes() []domain.EventType {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.EventType
	for _, ev := range e.events {
		out = append(out, ev.Type)
	}
	return out
}

// lastEvent returns the most recent event.
func (e *env) lastEvent() domain.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		e.t.Fatal("no events")
	}
	return e.events[len(e.events)-1]
}

// waitReceived waits until the receiver has n payloads and returns them.
func (e *env) waitReceived(n int) []webhook.Payload {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		got := append([]webhook.Payload(nil), e.received...)
		e.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("receiver did not get %d webhooks", n)
	return nil
}

func (e *env) do(method, path, body string, hdr map[string]string, out any) int {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Api-Key", apiKey)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		e.t.Errorf("%s %s: content-type %q", method, path, ct)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

type apiErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Order *domain.Order `json:"order"`
}

func (e *env) expectError(method, path, body string, hdr map[string]string, status int, code string) apiErr {
	e.t.Helper()
	var ae apiErr
	if got := e.do(method, path, body, hdr, &ae); got != status || ae.Error.Code != code {
		e.t.Errorf("%s %s: got %d %q, want %d %q", method, path, got, ae.Error.Code, status, code)
	}
	return ae
}

func (e *env) quote() domain.Quote {
	e.t.Helper()
	var q domain.Quote
	if code := e.do("POST", "/v1/quotes", `{"symbol":"AAPL","side":"BUY","qty":10}`, nil, &q); code != 201 {
		e.t.Fatalf("create quote: %d", code)
	}
	return q
}

func (e *env) accept(quoteID, key string) domain.Order {
	e.t.Helper()
	var o domain.Order
	if code := e.do("POST", "/v1/quotes/"+quoteID+"/accept", `{"client_order_id":"`+key+`"}`, nil, &o); code != 201 {
		e.t.Fatalf("accept: %d", code)
	}
	return o
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	for _, key := range []string{"", "wrong"} {
		req := httptest.NewRequest("GET", "/v1/prices", nil)
		if key != "" {
			req.Header.Set("X-Api-Key", key)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"unauthorized"`) {
			t.Errorf("key %q: %d %s", key, rec.Code, rec.Body)
		}
	}
}

func TestQuotes(t *testing.T) {
	e := newEnv(t)
	q := e.quote()
	if q.Status != domain.QuoteActive || q.Price != 19019 || q.Fee == 0 || q.Total != q.Price*10+q.Fee {
		t.Errorf("quote = %+v", q)
	}
	var got domain.Quote
	if code := e.do("GET", "/v1/quotes/"+q.ID, "", nil, &got); code != 200 || got.ID != q.ID {
		t.Errorf("get quote: %d %+v", code, got)
	}
	e.clock.Advance(15 * time.Second)
	e.do("GET", "/v1/quotes/"+q.ID, "", nil, &got)
	if got.Status != domain.QuoteExpired {
		t.Errorf("status after TTL = %s", got.Status)
	}

	e.expectError("GET", "/v1/quotes/qt_nope", "", nil, 404, "not_found")
	e.expectError("POST", "/v1/quotes", `{"symbol":"NOPE","side":"BUY","qty":1}`, nil, 400, "validation_error")
	e.expectError("POST", "/v1/quotes", `{"symbol":"AAPL","side":"BUY","qty":0}`, nil, 400, "validation_error")
	e.expectError("POST", "/v1/quotes", `not json`, nil, 400, "validation_error")
}

func TestAccept(t *testing.T) {
	e := newEnv(t)
	q1, q2 := e.quote(), e.quote()
	o := e.accept(q1.ID, "c-1")
	if o.Status != domain.OrderOpen || o.ClientOrderID != "c-1" || o.Version != 1 {
		t.Errorf("order = %+v", o)
	}

	var replay domain.Order
	if code := e.do("POST", "/v1/quotes/"+q1.ID+"/accept", `{"client_order_id":"c-1"}`, nil, &replay); code != 200 || replay.ID != o.ID {
		t.Errorf("replay: %d %s", code, replay.ID)
	}
	// Header only, same key: also a replay.
	if code := e.do("POST", "/v1/quotes/"+q1.ID+"/accept", "", map[string]string{"Idempotency-Key": "c-1"}, &replay); code != 200 || replay.ID != o.ID {
		t.Errorf("header replay: %d %s", code, replay.ID)
	}

	e.expectError("POST", "/v1/quotes/"+q2.ID+"/accept", `{"client_order_id":"c-1"}`, nil, 422, "idempotency_conflict")
	e.expectError("POST", "/v1/quotes/"+q1.ID+"/accept", `{"client_order_id":"c-2"}`, nil, 409, "quote_already_accepted")
	e.expectError("POST", "/v1/quotes/"+q2.ID+"/accept", `{"client_order_id":"a"}`, map[string]string{"Idempotency-Key": "b"}, 400, "validation_error")
	e.expectError("POST", "/v1/quotes/"+q2.ID+"/accept", ``, nil, 400, "validation_error")

	e.store.SetMarketOpen(false)
	e.expectError("POST", "/v1/quotes/"+q2.ID+"/accept", `{"client_order_id":"c-3"}`, nil, 409, "market_closed")
	e.store.SetMarketOpen(true)

	e.clock.Advance(15 * time.Second)
	e.expectError("POST", "/v1/quotes/"+q2.ID+"/accept", `{"client_order_id":"c-3"}`, nil, 409, "quote_expired")
}

func TestOrdersAndCancel(t *testing.T) {
	e := newEnv(t)
	o1 := e.accept(e.quote().ID, "c-1")
	e.clock.Advance(time.Second)
	o2 := e.accept(e.quote().ID, "c-2")
	if _, err := e.store.FillOrder(o1.ID, 4); err != nil {
		t.Fatal(err)
	}

	var got domain.Order
	if code := e.do("GET", "/v1/orders/"+o1.ID, "", nil, &got); code != 200 || got.FilledQty != 4 || len(got.Fills) != 1 {
		t.Errorf("get order: %d %+v", code, got)
	}
	if code := e.do("GET", "/v1/orders?client_order_id=c-2", "", nil, &got); code != 200 || got.ID != o2.ID {
		t.Errorf("by client_order_id: %d %s", code, got.ID)
	}
	e.expectError("GET", "/v1/orders?client_order_id=nope", "", nil, 404, "not_found")
	e.expectError("GET", "/v1/orders/ord_nope", "", nil, 404, "not_found")

	var list struct{ Orders []domain.Order }
	e.do("GET", "/v1/orders", "", nil, &list)
	if len(list.Orders) != 2 || list.Orders[0].ID != o2.ID {
		t.Errorf("list not newest first: %+v", list.Orders)
	}
	e.do("GET", "/v1/orders?status=PARTIALLY_FILLED", "", nil, &list)
	if len(list.Orders) != 1 || list.Orders[0].ID != o1.ID {
		t.Errorf("status filter: %+v", list.Orders)
	}
	e.expectError("GET", "/v1/orders?status=BOGUS", "", nil, 400, "validation_error")
	e.expectError("GET", "/v1/orders?since=yesterday", "", nil, 400, "validation_error")

	// Cancel twice: both 202, still PENDING_CANCEL.
	for range 2 {
		if code := e.do("DELETE", "/v1/orders/"+o1.ID, "", nil, &got); code != 202 || got.Status != domain.OrderPendingCancel {
			t.Errorf("cancel: %d %s", code, got.Status)
		}
	}
	if _, err := e.store.ResolveCancel(o1.ID); err != nil {
		t.Fatal(err)
	}
	ae := e.expectError("DELETE", "/v1/orders/"+o1.ID, "", nil, 409, "order_final")
	if ae.Order == nil || ae.Order.Status != domain.OrderCancelled || ae.Order.FilledQty != 4 {
		t.Errorf("order_final body should include the order: %+v", ae.Order)
	}
}

func TestPricesAndUnknownRoute(t *testing.T) {
	e := newEnv(t)
	var ps struct{ Prices []market.Price }
	if code := e.do("GET", "/v1/prices", "", nil, &ps); code != 200 || len(ps.Prices) != 2 {
		t.Errorf("prices: %d %+v", code, ps)
	}
	var p market.Price
	if code := e.do("GET", "/v1/prices/MSFT", "", nil, &p); code != 200 || p.Mid != 41000 {
		t.Errorf("price: %d %+v", code, p)
	}
	e.expectError("GET", "/v1/prices/NOPE", "", nil, 404, "not_found")
	e.expectError("GET", "/v1/nothing", "", nil, 404, "not_found")
}

// TestEndToEndWebhook: API -> store -> dispatcher -> signed HTTP receiver.
func TestEndToEndWebhook(t *testing.T) {
	e := newEnv(t)
	o := e.accept(e.quote().ID, "c-1")
	e.do("DELETE", "/v1/orders/"+o.ID, "", nil, nil)

	got := e.waitReceived(2)
	for i, want := range []domain.EventType{domain.EventOrderAccepted, domain.EventOrderPendingCancel} {
		if got[i].Type != want || got[i].Data.Order == nil || got[i].Data.Order.ID != o.ID {
			t.Errorf("webhook %d = %s %+v, want %s", i, got[i].Type, got[i].Data.Order, want)
		}
	}
}
