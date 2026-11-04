// Package providertest is a small in-process fake of brokersim's /v1 API for
// tests. It covers quotes, accept (with scripted failures), order lookups and
// prices, and follows brokersim's contract for each.
package providertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

const APIKey = "test_key"

// Accept behaviours.
const (
	AcceptOK               = "ok"                  // create the order, 201
	AcceptFail500          = "fail_500"            // 500, nothing created
	AcceptFail500AfterOpen = "fail_500_after_open" // create the order, then answer 500
	AcceptQuoteExpired     = "quote_expired"       // 409 quote_expired
	AcceptMarketClosed     = "market_closed"       // 409 market_closed
)

type Broker struct {
	URL string

	mu          sync.Mutex
	down        bool
	acceptMode  string
	acceptCalls int
	quoteTTL    time.Duration
	prices      map[string]provider.Price
	quotes      map[string]*provider.Quote
	orders      map[string]*provider.Order // by provider order id
	byClientID  map[string]string          // client_order_id → provider order id
	nextID      int
	nextEvent   int
}

// New starts a fake broker that is closed when the test ends.
func New(t testing.TB) *Broker {
	b := &Broker{
		acceptMode: AcceptOK,
		quoteTTL:   15 * time.Second,
		prices: map[string]provider.Price{
			"AAPL": {Symbol: "AAPL", Bid: 18_990, Mid: 19_000, Ask: 19_010},
			"MSFT": {Symbol: "MSFT", Bid: 40_990, Mid: 41_000, Ask: 41_010},
		},
		quotes:     map[string]*provider.Quote{},
		orders:     map[string]*provider.Order{},
		byClientID: map[string]string{},
	}
	srv := httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(srv.Close)
	b.URL = srv.URL
	return b
}

// SetDown makes every endpoint answer 503 (true) or work again (false).
func (b *Broker) SetDown(down bool) { b.mu.Lock(); b.down = down; b.mu.Unlock() }

// SetAcceptMode scripts what the next accepts do.
func (b *Broker) SetAcceptMode(mode string) { b.mu.Lock(); b.acceptMode = mode; b.mu.Unlock() }

// SetQuoteTTL changes how long new quotes are valid.
func (b *Broker) SetQuoteTTL(d time.Duration) { b.mu.Lock(); b.quoteTTL = d; b.mu.Unlock() }

// AcceptCalls is how many accept requests arrived.
func (b *Broker) AcceptCalls() int { b.mu.Lock(); defer b.mu.Unlock(); return b.acceptCalls }

// OrderCount is how many orders the broker has created.
func (b *Broker) OrderCount() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.orders) }

func (b *Broker) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if r.Header.Get("X-Api-Key") != APIKey {
		writeErr(w, 401, "unauthorized", "bad api key")
		return
	}
	if b.down {
		writeErr(w, 503, "internal", "broker is down")
		return
	}

	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/v1/quotes":
		b.createQuote(w, r)
	case r.Method == "POST" && strings.HasPrefix(path, "/v1/quotes/") && strings.HasSuffix(path, "/accept"):
		b.accept(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v1/quotes/"), "/accept"))
	case r.Method == "GET" && path == "/v1/orders" && r.URL.Query().Get("client_order_id") != "":
		id, ok := b.byClientID[r.URL.Query().Get("client_order_id")]
		if !ok {
			writeErr(w, 404, "not_found", "no such order")
			return
		}
		writeJSON(w, 200, b.orders[id])
	case r.Method == "GET" && path == "/v1/orders":
		list := []provider.Order{}
		for _, o := range b.orders {
			list = append(list, copyOrder(o))
		}
		writeJSON(w, 200, map[string]any{"orders": list})
	case r.Method == "GET" && strings.HasPrefix(path, "/v1/orders/"):
		o, ok := b.orders[strings.TrimPrefix(path, "/v1/orders/")]
		if !ok {
			writeErr(w, 404, "not_found", "no such order")
			return
		}
		writeJSON(w, 200, o)
	case r.Method == "DELETE" && strings.HasPrefix(path, "/v1/orders/"):
		b.cancel(w, strings.TrimPrefix(path, "/v1/orders/"))
	case r.Method == "GET" && path == "/v1/prices":
		var list []provider.Price
		for _, sym := range []string{"AAPL", "MSFT"} {
			list = append(list, b.prices[sym])
		}
		writeJSON(w, 200, map[string]any{"prices": list})
	default:
		writeErr(w, 404, "not_found", "no such route")
	}
}

func (b *Broker) createQuote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol string `json:"symbol"`
		Side   string `json:"side"`
		Qty    int64  `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Qty <= 0 || (req.Side != "BUY" && req.Side != "SELL") {
		writeErr(w, 400, "validation_error", "bad quote request")
		return
	}
	p, ok := b.prices[req.Symbol]
	if !ok {
		writeErr(w, 400, "validation_error", fmt.Sprintf("unknown symbol %q", req.Symbol))
		return
	}

	price := p.Ask
	if req.Side == "SELL" {
		price = p.Bid
	}
	notional := price * req.Qty
	fee := (notional*50 + 5_000) / 10_000 // 50 bps, rounded half up
	total := notional + fee
	if req.Side == "SELL" {
		total = notional - fee
	}

	b.nextID++
	q := &provider.Quote{
		ID: fmt.Sprintf("qt_%d", b.nextID), Symbol: req.Symbol, Side: req.Side, Qty: req.Qty,
		Price: price, Fee: fee, Total: total, Status: "ACTIVE",
		ExpiresAt: time.Now().Add(b.quoteTTL).UTC().Truncate(time.Millisecond),
	}
	b.quotes[q.ID] = q
	writeJSON(w, 201, q)
}

func (b *Broker) accept(w http.ResponseWriter, r *http.Request, quoteID string) {
	b.acceptCalls++
	var req struct {
		ClientOrderID string `json:"client_order_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	q, ok := b.quotes[quoteID]
	if !ok {
		writeErr(w, 404, "not_found", "no such quote")
		return
	}

	// Same quote and client order id again: the same order, 200.
	if id, ok := b.byClientID[req.ClientOrderID]; ok {
		writeJSON(w, 200, b.orders[id])
		return
	}

	switch b.acceptMode {
	case AcceptFail500:
		writeErr(w, 500, "internal", "something broke")
		return
	case AcceptQuoteExpired:
		writeErr(w, 409, "quote_expired", "quote expired")
		return
	case AcceptMarketClosed:
		writeErr(w, 409, "market_closed", "market is closed")
		return
	}

	if q.Status == "ACCEPTED" {
		writeErr(w, 409, "quote_already_accepted", "quote already accepted")
		return
	}

	b.nextID++
	now := time.Now().UTC()
	o := &provider.Order{
		ID: fmt.Sprintf("ord_%d", b.nextID), ClientOrderID: req.ClientOrderID, QuoteID: q.ID,
		Symbol: q.Symbol, Side: q.Side, Qty: q.Qty, RemainingQty: q.Qty, Price: q.Price,
		Status: "OPEN", Version: 1, Fills: []provider.Fill{}, CreatedAt: now, UpdatedAt: now,
	}
	q.Status = "ACCEPTED"
	b.orders[o.ID] = o
	b.byClientID[o.ClientOrderID] = o.ID

	if b.acceptMode == AcceptFail500AfterOpen {
		writeErr(w, 500, "internal", "order created but the answer was lost")
		return
	}
	writeJSON(w, 201, o)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
