// Package provider is the HTTP client for brokersim. It only speaks the
// provider's API: build the request, send it, decode the answer. Deciding
// what an answer means for our orders is the service's job.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	breaker *Breaker // nil: no circuit breaking
}

// New makes a client. breaker may be nil to call the provider unguarded.
func New(baseURL, apiKey string, timeout time.Duration, breaker *Breaker) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
		breaker: breaker,
	}
}

// CircuitState returns the breaker's state ("closed" when there is none).
func (c *Client) CircuitState() string {
	if c.breaker == nil {
		return CircuitClosed
	}
	return c.breaker.State()
}

// CircuitRejecting reports whether calls are currently being refused by the
// circuit breaker (open, or a probe in flight).
func (c *Client) CircuitRejecting() bool {
	return c.breaker != nil && c.breaker.Rejecting()
}

// Quote is a provider quote.
type Quote struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Qty       int64     `json:"qty"`
	Price     int64     `json:"price"`
	Fee       int64     `json:"fee"`
	Total     int64     `json:"total"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Order is the provider's full order snapshot.
type Order struct {
	ID            string    `json:"id"`
	ClientOrderID string    `json:"client_order_id"`
	QuoteID       string    `json:"quote_id"`
	Symbol        string    `json:"symbol"`
	Side          string    `json:"side"`
	Qty           int64     `json:"qty"`
	FilledQty     int64     `json:"filled_qty"`
	RemainingQty  int64     `json:"remaining_qty"`
	Price         int64     `json:"price"`
	AvgFillPrice  int64     `json:"avg_fill_price"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	Version       int64     `json:"version"`
	Fills         []Fill    `json:"fills"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Fill is one provider fill.
type Fill struct {
	ID    string    `json:"id"`
	Qty   int64     `json:"qty"`
	Price int64     `json:"price"`
	Fee   int64     `json:"fee"`
	At    time.Time `json:"at"`
}

// Price is a provider market price.
type Price struct {
	Symbol string `json:"symbol"`
	Bid    int64  `json:"bid"`
	Mid    int64  `json:"mid"`
	Ask    int64  `json:"ask"`
}

// APIError is any non-2xx answer from the provider. Code and Message come
// from the provider's error body when it sent one.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("provider %d %s: %s", e.Status, e.Code, e.Message)
}

// CreateQuote: POST /v1/quotes.
func (c *Client) CreateQuote(ctx context.Context, symbol, side string, qty int64) (Quote, error) {
	body := map[string]any{"symbol": symbol, "side": side, "qty": qty}

	var q Quote
	err := c.do(ctx, http.MethodPost, "/v1/quotes", nil, body, &q)
	return q, err
}

// AcceptQuote: POST /v1/quotes/{id}/accept. clientOrderID makes it
// idempotent at the provider: the same quote and id again returns the same
// order.
func (c *Client) AcceptQuote(ctx context.Context, quoteID, clientOrderID string) (Order, error) {
	headers := map[string]string{"Idempotency-Key": clientOrderID}
	body := map[string]any{"client_order_id": clientOrderID}

	var o Order
	err := c.do(ctx, http.MethodPost, "/v1/quotes/"+url.PathEscape(quoteID)+"/accept", headers, body, &o)
	return o, err
}

// GetOrder: GET /v1/orders/{id}.
func (c *Client) GetOrder(ctx context.Context, providerOrderID string) (Order, error) {
	var o Order
	err := c.do(ctx, http.MethodGet, "/v1/orders/"+url.PathEscape(providerOrderID), nil, nil, &o)
	return o, err
}

// GetOrderByClientOrderID: GET /v1/orders?client_order_id=...
func (c *Client) GetOrderByClientOrderID(ctx context.Context, clientOrderID string) (Order, error) {
	var o Order
	err := c.do(ctx, http.MethodGet, "/v1/orders?client_order_id="+url.QueryEscape(clientOrderID), nil, nil, &o)
	return o, err
}

// CancelOrder: DELETE /v1/orders/{id}. 202 with the order (PENDING_CANCEL)
// means the cancel was accepted; the outcome arrives as a webhook. A final
// order answers 409 order_final.
func (c *Client) CancelOrder(ctx context.Context, providerOrderID string) (Order, error) {
	var o Order
	err := c.do(ctx, http.MethodDelete, "/v1/orders/"+url.PathEscape(providerOrderID), nil, nil, &o)
	return o, err
}

// ListOrders: GET /v1/orders?since=... returns every order changed since
// then (all statuses), newest first, each with its fills.
func (c *Client) ListOrders(ctx context.Context, since time.Time) ([]Order, error) {
	var out struct {
		Orders []Order `json:"orders"`
	}
	path := "/v1/orders?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	err := c.do(ctx, http.MethodGet, path, nil, nil, &out)
	return out.Orders, err
}

// ListPrices: GET /v1/prices.
func (c *Client) ListPrices(ctx context.Context) ([]Price, error) {
	var out struct {
		Prices []Price `json:"prices"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/prices", nil, nil, &out)
	return out.Prices, err
}

// do sends one request through the circuit breaker. While the circuit is
// open it returns ErrCircuitOpen without calling the provider.
func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
	if c.breaker != nil && !c.breaker.Allow() {
		return ErrCircuitOpen
	}
	err := c.send(ctx, method, path, headers, body, out)
	c.record(ctx, err)
	return err
}

// record tells the breaker how the call went. Only signs of provider
// trouble count against it: network errors, timeouts, 5xx and 429.
func (c *Client) record(ctx context.Context, err error) {
	if c.breaker == nil {
		return
	}
	var apiErr *APIError
	switch {
	case err == nil:
		c.breaker.Success()
	case errors.As(err, &apiErr):
		if apiErr.Status >= 500 || apiErr.Status == http.StatusTooManyRequests {
			c.breaker.Failure()
		} else {
			c.breaker.Success() // a 4xx is a real answer: the provider is up
		}
	case ctx.Err() != nil:
		c.breaker.Abandon() // we gave up, not the provider
	default:
		c.breaker.Failure()
	}
}

// send sends one request. A non-2xx answer becomes *APIError; network
// errors and timeouts are returned as they are.
func (c *Client) send(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("provider %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("provider %s %s: read body: %w", method, path, err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		apiErr := &APIError{Status: res.StatusCode}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(resBody, &e) == nil {
			apiErr.Code, apiErr.Message = e.Error.Code, e.Error.Message
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resBody, out); err != nil {
		return fmt.Errorf("provider %s %s: decode: %w", method, path, err)
	}
	return nil
}
