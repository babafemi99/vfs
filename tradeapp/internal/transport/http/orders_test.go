package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

// runJobs runs every due job once, standing in for the worker.
func (e env) runJobs(t *testing.T) {
	t.Helper()
	jobs, err := e.svc.ClaimJobs(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		_, _ = e.svc.RunJob(context.Background(), j)
	}
}

func (e env) quote(t *testing.T, symbol, side string, qty int) string {
	t.Helper()
	r := e.do(t, "POST", "/api/quotes", "", fmt.Sprintf(`{"symbol":%q,"side":%q,"qty":%d}`, symbol, side, qty))
	if r.status != 201 {
		t.Fatalf("POST /api/quotes: %d %s", r.status, r.raw)
	}
	return r.body["id"].(string)
}

func (e env) orderCount(t *testing.T) int {
	t.Helper()
	r := e.do(t, "GET", "/api/orders?status=all", "", "")
	return len(r.body["orders"].([]any))
}

func TestQuoteToOpenOrder(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 500000}`)

	prices := e.do(t, "GET", "/api/prices", "", "")
	if list, _ := prices.body["prices"].([]any); prices.status != 200 || len(list) != 2 {
		t.Fatalf("prices: %d %s", prices.status, prices.raw)
	}

	quoteID := e.quote(t, "AAPL", "BUY", 10)
	placed := e.do(t, "POST", "/api/orders", "o-1", fmt.Sprintf(`{"quote_id":%q}`, quoteID))
	if placed.status != 201 || placed.body["status"] != "PENDING_SUBMIT" {
		t.Fatalf("place: %d %s", placed.status, placed.raw)
	}
	orderID := placed.body["id"].(string)

	e.runJobs(t)

	got := e.do(t, "GET", "/api/orders/"+orderID, "", "")
	if got.status != 200 || got.body["status"] != "OPEN" || got.body["provider_order_id"] == nil {
		t.Fatalf("order: %d %s", got.status, got.raw)
	}
	if fills, _ := got.body["fills"].([]any); fills == nil || len(fills) != 0 {
		t.Fatalf("fills = %v", got.body["fills"])
	}
	if acts, _ := got.body["activity"].([]any); len(acts) != 2 {
		t.Fatalf("activity = %v", got.body["activity"])
	}

	open := e.do(t, "GET", "/api/orders?status=open", "", "")
	if list, _ := open.body["orders"].([]any); len(list) != 1 {
		t.Fatalf("open orders: %s", open.raw)
	}
	acct := e.do(t, "GET", "/api/account", "", "")
	if held := acct.body["cash"].(map[string]any)["held"].(float64); held != 191_051 {
		t.Fatalf("held = %v, want the quote total 191051", held)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

func TestPlaceOrderIdempotent(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 5000000}`)
	body := fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1))

	first := e.do(t, "POST", "/api/orders", "same", body)
	second := e.do(t, "POST", "/api/orders", "same", body)
	if first.status != 201 || second.status != 201 || !second.replayed || second.raw != first.raw {
		t.Fatalf("first %d, second %d replayed=%v", first.status, second.status, second.replayed)
	}

	// 20 concurrent requests with one key and a fresh quote: one order.
	body2 := fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1))
	var wg sync.WaitGroup
	statuses := make(chan int, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", e.srv.URL+"/api/orders", strings.NewReader(body2))
			req.Header.Set("Idempotency-Key", "burst")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				statuses <- -1
				return
			}
			res.Body.Close()
			statuses <- res.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	for s := range statuses {
		if s != 201 && s != 409 {
			t.Fatalf("status %d, want 201 or 409", s)
		}
	}

	if n := e.orderCount(t); n != 2 {
		t.Fatalf("orders = %d, want 2", n)
	}
	var holds int
	_ = e.repo.RunInTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		holds, err = repository.LedgerRepo{}.CountJournalEntries(context.Background(), tx, domain.EntryHold)
		return err
	})
	if holds != 2 {
		t.Fatalf("hold entries = %d, want 2", holds)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

func TestOrderErrors(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 1000}`)

	cases := []struct {
		name, method, path, key, body, code string
		status                              int
	}{
		{"missing key", "POST", "/api/orders", "", `{"quote_id":"qt_1"}`, "idempotency_key_required", 400},
		{"empty quote id", "POST", "/api/orders", "k1", `{"quote_id":""}`, "validation_error", 400},
		{"unknown quote", "POST", "/api/orders", "k2", `{"quote_id":"qt_nope"}`, "not_found", 404},
		{"bad side", "POST", "/api/quotes", "", `{"symbol":"AAPL","side":"HOLD","qty":1}`, "validation_error", 400},
		{"unknown symbol", "POST", "/api/quotes", "", `{"symbol":"NOPE","side":"BUY","qty":1}`, "validation_error", 400},
		{"bad order id", "GET", "/api/orders/xyz", "", "", "not_found", 404},
		{"bad status", "GET", "/api/orders?status=weird", "", "", "validation_error", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.do(t, c.method, c.path, c.key, c.body)
			if r.status != c.status || errCode(r) != c.code {
				t.Fatalf("got %d %q, want %d %q (%s)", r.status, errCode(r), c.status, c.code, r.raw)
			}
		})
	}

	// $10 can't buy a $190 share.
	r := e.do(t, "POST", "/api/orders", "k3", fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1)))
	if r.status != 422 || errCode(r) != "insufficient_funds" {
		t.Fatalf("insufficient funds: %d %s", r.status, r.raw)
	}

	// A quote can back one order only, whatever the key.
	e.do(t, "POST", "/api/account/fund", "f2", `{"amount": 1000000}`)
	body := fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1))
	e.do(t, "POST", "/api/orders", "k4", body)
	if r := e.do(t, "POST", "/api/orders", "k5", body); r.status != 409 || errCode(r) != "quote_already_used" {
		t.Fatalf("reused quote: %d %s", r.status, r.raw)
	}

	e.broker.SetDown(true)
	if r := e.do(t, "POST", "/api/quotes", "", `{"symbol":"AAPL","side":"BUY","qty":1}`); r.status != 502 || errCode(r) != "provider_unavailable" {
		t.Fatalf("broker down: %d %s", r.status, r.raw)
	}
}

func TestAdminDeadLetterQueue(t *testing.T) {
	cfg := service.DefaultConfig()
	cfg.JobMaxAttempts = 1 // dead after one failure
	e := setupWithConfig(t, cfg)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 1000000}`)
	placed := e.do(t, "POST", "/api/orders", "o", fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1)))

	e.broker.SetDown(true)
	e.runJobs(t)

	dead := e.do(t, "GET", "/api/admin/jobs", "", "") // defaults to status=dead
	jobs, _ := dead.body["jobs"].([]any)
	if dead.status != 200 || len(jobs) != 1 {
		t.Fatalf("dead jobs: %d %s", dead.status, dead.raw)
	}
	job := jobs[0].(map[string]any)
	if job["order_id"] != placed.body["id"] || job["last_error"] == nil {
		t.Fatalf("job = %v", job)
	}
	jobPath := fmt.Sprintf("/api/admin/jobs/%d/retry", int64(job["id"].(float64)))

	e.broker.SetDown(false)
	if r := e.do(t, "POST", jobPath, "", ""); r.status != 200 || r.body["status"] != "pending" {
		t.Fatalf("retry: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "POST", jobPath, "", ""); r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("retry pending job: %d %s", r.status, r.raw)
	}
	for _, path := range []string{"/api/admin/jobs/abc/retry", "/api/admin/jobs/99999/retry"} {
		if r := e.do(t, "POST", path, "", ""); r.status != 404 {
			t.Fatalf("%s: %d", path, r.status)
		}
	}
	if r := e.do(t, "GET", "/api/admin/jobs?status=bogus", "", ""); r.status != 400 {
		t.Fatalf("bad status: %d", r.status)
	}

	e.runJobs(t)
	if got := e.do(t, "GET", "/api/orders/"+placed.body["id"].(string), "", ""); got.body["status"] != "OPEN" {
		t.Fatalf("after retry: %s", got.raw)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

func TestJobStatsAndBulkRetryEndpoints(t *testing.T) {
	cfg := service.DefaultConfig()
	cfg.JobMaxAttempts = 1
	e := setupWithConfig(t, cfg)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 1000000}`)
	for i := range 2 {
		e.do(t, "POST", "/api/orders", fmt.Sprintf("o%d", i), fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1)))
	}
	e.broker.SetDown(true)
	e.runJobs(t)

	stats := e.do(t, "GET", "/api/admin/jobs/stats", "", "")
	if stats.status != 200 || stats.body["dead"].(float64) != 2 || stats.body["ok"] != false {
		t.Fatalf("stats: %d %s", stats.status, stats.raw)
	}
	if alerts := stats.body["alerts"].([]any); len(alerts) == 0 || alerts[0] != "dead_letter_queue_not_empty" {
		t.Fatalf("alerts: %s", stats.raw)
	}

	if r := e.do(t, "POST", "/api/admin/jobs/retry", "", `{"kind":"bogus"}`); r.status != 400 {
		t.Fatalf("bad kind: %d %s", r.status, r.raw)
	}
	e.broker.SetDown(false)
	r := e.do(t, "POST", "/api/admin/jobs/retry", "", "")
	if r.status != 200 || r.body["retried"].(float64) != 2 {
		t.Fatalf("bulk retry: %d %s", r.status, r.raw)
	}
	e.runJobs(t)
	if open := e.do(t, "GET", "/api/orders?status=open", "", ""); len(open.body["orders"].([]any)) != 2 {
		t.Fatalf("orders after retry: %s", open.raw)
	}
	if stats := e.do(t, "GET", "/api/admin/jobs/stats", "", ""); stats.body["ok"] != true {
		t.Fatalf("stats after recovery: %s", stats.raw)
	}
}
