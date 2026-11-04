package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

// postWebhook sends body signed with secret at time ts, with a fresh nonce
// (as the provider does for every attempt).
func (e env) postWebhook(t *testing.T, body []byte, secret string, ts time.Time) int {
	t.Helper()
	return e.postWebhookNonce(t, body, secret, ts, fmt.Sprintf("%032x", time.Now().UnixNano()))
}

// postWebhookNonce sends body signed with secret at time ts with this nonce.
func (e env) postWebhookNonce(t *testing.T, body []byte, secret string, ts time.Time, nonce string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/webhooks/provider", bytes.NewReader(body))
	unix := ts.Unix()
	req.Header.Set(provider.HeaderTimestamp, strconv.FormatInt(unix, 10))
	if nonce != "" {
		req.Header.Set(provider.HeaderNonce, nonce)
	}
	req.Header.Set(provider.HeaderSignature, provider.Sign(secret, unix, nonce, body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (e env) eventCount(t *testing.T) int {
	t.Helper()
	var n int
	_ = e.repo.RunInTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		n, err = repository.EventRepo{}.CountEvents(context.Background(), tx)
		return err
	})
	return n
}

// openOrder places a BUY of qty AAPL and lets the worker submit it.
func (e env) openOrder(t *testing.T, qty int) (string, string) {
	t.Helper()
	e.do(t, "POST", "/api/account/fund", "fund-"+t.Name(), `{"amount": 5000000}`)
	placed := e.do(t, "POST", "/api/orders", "order-"+t.Name(), fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", qty)))
	e.runJobs(t)
	id := placed.body["id"].(string)
	return id, e.broker.OrderIDFor(t, id)
}

func TestWebhookSignature(t *testing.T) {
	e := setup(t)
	_, pid := e.openOrder(t, 10)
	body := providertest.Body(t, e.broker.Fill(t, pid, 4))
	now := time.Now()

	if s := e.postWebhook(t, body, "wrong_secret", now); s != 401 {
		t.Errorf("bad signature: %d", s)
	}
	if s := e.postWebhook(t, body, webhookSecret, now.Add(-10*time.Minute)); s != 401 {
		t.Errorf("old timestamp: %d", s)
	}
	// The window is ±2m: 150s either way is refused, 90s old is fine.
	if s := e.postWebhook(t, body, webhookSecret, now.Add(-150*time.Second)); s != 401 {
		t.Errorf("150s old: %d, want 401", s)
	}
	if s := e.postWebhook(t, body, webhookSecret, now.Add(150*time.Second)); s != 401 {
		t.Errorf("150s in the future: %d, want 401", s)
	}
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("rejected webhooks were stored: %d", n)
	}
	if s := e.postWebhook(t, body, webhookSecret, now.Add(-90*time.Second)); s != 200 {
		t.Fatalf("90s old (inside the window): %d, want 200", s)
	}

	if s := e.postWebhook(t, body, webhookSecret, now); s != 200 {
		t.Fatalf("good webhook: %d", s)
	}
	if s := e.postWebhook(t, body, webhookSecret, now); s != 200 {
		t.Fatalf("duplicate webhook: %d", s)
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("events stored = %d, want 1", n)
	}
	if s := e.postWebhook(t, []byte(`not json`), webhookSecret, now); s != 400 {
		t.Errorf("signed garbage: %d", s)
	}
}

func TestWebhookFillsOrder(t *testing.T) {
	e := setup(t)
	orderID, pid := e.openOrder(t, 10)

	e.postWebhook(t, providertest.Body(t, e.broker.Fill(t, pid, 0)), webhookSecret, time.Now())
	e.runJobs(t)

	got := e.do(t, "GET", "/api/orders/"+orderID, "", "")
	if got.body["status"] != "FILLED" || got.body["filled_qty"].(float64) != 10 {
		t.Fatalf("order: %s", got.raw)
	}
	acct := e.do(t, "GET", "/api/account", "", "")
	positions := acct.body["positions"].([]any)
	if len(positions) != 1 || positions[0].(map[string]any)["qty"].(float64) != 10 {
		t.Fatalf("account: %s", acct.raw)
	}
	inv := e.do(t, "GET", "/api/admin/invariants", "", "")
	if inv.status != 200 || inv.body["ok"] != true {
		t.Fatalf("invariants: %s", inv.raw)
	}
}

func TestCancelEndpoint(t *testing.T) {
	e := setup(t)
	orderID, pid := e.openOrder(t, 10)

	first := e.do(t, "DELETE", "/api/orders/"+orderID, "", "")
	if first.status != 202 || first.body["status"] != "PENDING_CANCEL" {
		t.Fatalf("cancel: %d %s", first.status, first.raw)
	}
	if again := e.do(t, "DELETE", "/api/orders/"+orderID, "", ""); again.status != 202 {
		t.Fatalf("cancel again: %d", again.status)
	}
	e.runJobs(t)

	e.postWebhook(t, providertest.Body(t, e.broker.ConfirmCancel(t, pid)), webhookSecret, time.Now())
	e.runJobs(t)

	if r := e.do(t, "DELETE", "/api/orders/"+orderID, "", ""); r.status != 409 || errCode(r) != "order_final" {
		t.Fatalf("cancel final: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "DELETE", "/api/orders/not-an-id", "", ""); r.status != 404 {
		t.Fatalf("cancel unknown: %d", r.status)
	}

	// An order never sent to the broker cancels right away: 200.
	placed := e.do(t, "POST", "/api/orders", "o2", fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1)))
	if r := e.do(t, "DELETE", "/api/orders/"+placed.body["id"].(string), "", ""); r.status != 200 || r.body["status"] != "CANCELLED" {
		t.Fatalf("cancel before submit: %d %s", r.status, r.raw)
	}

	if r := e.do(t, "GET", "/api/breaks", "", ""); r.status != 200 || len(r.body["breaks"].([]any)) != 0 {
		t.Fatalf("breaks: %s", r.raw)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

func TestReconEndpoints(t *testing.T) {
	e := setup(t)
	if r := e.do(t, "GET", "/api/recon/latest", "", ""); r.status != 404 {
		t.Fatalf("latest before any run: %d %s", r.status, r.raw)
	}
	orderID, pid := e.openOrder(t, 10)

	run := e.do(t, "POST", "/api/recon/run", "", "")
	if run.status != 200 || run.body["ok"] != true || run.body["invariants_ok"] != true {
		t.Fatalf("run: %d %s", run.status, run.raw)
	}
	latest := e.do(t, "GET", "/api/recon/latest", "", "")
	if latest.status != 200 || latest.body["run_id"] != run.body["run_id"] {
		t.Fatalf("latest: %d %s", latest.status, latest.raw)
	}

	cmp := e.do(t, "GET", "/api/orders/"+orderID+"/compare", "", "")
	if cmp.status != 200 || cmp.body["agree"] != true {
		t.Fatalf("compare in sync: %d %s", cmp.status, cmp.raw)
	}
	e.broker.Fill(t, pid, 4) // dropped webhook
	cmp = e.do(t, "GET", "/api/orders/"+strings.ToLower(orderID)+"/compare", "", "")
	if cmp.status != 200 || cmp.body["agree"] != false || len(cmp.body["mismatches"].([]any)) != 3 {
		t.Fatalf("compare after dropped fill: %d %s", cmp.status, cmp.raw)
	}
	if r := e.do(t, "GET", "/api/orders/nope/compare", "", ""); r.status != 404 {
		t.Fatalf("compare unknown: %d", r.status)
	}
}

// A captured webhook sent again (same nonce) is refused before anything is
// stored; the provider's own retries carry a new nonce and get through.
func TestWebhookReplayIsRefused(t *testing.T) {
	e := setup(t)
	_, pid := e.openOrder(t, 10)
	body := providertest.Body(t, e.broker.Fill(t, pid, 4))
	now := time.Now()
	nonce := "0123456789abcdef0123456789abcdef"

	if s := e.postWebhookNonce(t, body, webhookSecret, now, nonce); s != 200 {
		t.Fatalf("first delivery: %d", s)
	}
	if s := e.postWebhookNonce(t, body, webhookSecret, now, nonce); s != 409 {
		t.Fatalf("replay: %d, want 409", s)
	}
	if s := e.postWebhookNonce(t, body, webhookSecret, now, ""); s != 401 {
		t.Fatalf("no nonce: %d, want 401", s)
	}
	// A genuine retry: same event, new timestamp and nonce. Accepted as a duplicate.
	if s := e.postWebhook(t, body, webhookSecret, now.Add(time.Second)); s != 200 {
		t.Fatalf("retry: %d", s)
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("events stored = %d, want 1", n)
	}
}

func TestWebhookLogAndInspector(t *testing.T) {
	e := setup(t)
	_, pid := e.openOrder(t, 10)
	good := providertest.Body(t, e.broker.Fill(t, pid, 4))
	now := time.Now()
	nonce := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	e.postWebhookNonce(t, good, webhookSecret, now, nonce)          // accepted
	e.postWebhookNonce(t, good, webhookSecret, now, nonce)          // replay
	e.postWebhook(t, good, webhookSecret, now.Add(time.Second))     // duplicate
	e.postWebhook(t, good, "wrong", now)                            // bad signature
	e.postWebhook(t, good, webhookSecret, now.Add(-10*time.Minute)) // stale
	e.postWebhook(t, []byte(`not json`), webhookSecret, now)        // invalid body
	e.runJobs(t)

	list := e.do(t, "GET", "/api/admin/webhooks", "", "")
	items := list.body["webhooks"].([]any)
	var outcomes []string
	for _, it := range items {
		outcomes = append(outcomes, it.(map[string]any)["outcome"].(string))
	}
	want := []string{"accepted", "rejected_replay", "duplicate", "rejected_signature", "rejected_timestamp", "invalid_body"}
	if fmt.Sprint(outcomes) != fmt.Sprint(want) {
		t.Fatalf("outcomes = %v, want %v", outcomes, want)
	}

	first := items[0].(map[string]any)
	if first["verified"] != true || first["event_type"] != "order.partially_filled" || first["processed_at"] == nil || first["order_id"] == nil {
		t.Fatalf("accepted entry: %v", first)
	}
	if _, hasBody := first["body"]; hasBody {
		t.Fatal("list should not carry bodies")
	}
	if items[3].(map[string]any)["verified"] != false {
		t.Fatalf("bad signature marked verified: %v", items[3])
	}

	detail := e.do(t, "GET", fmt.Sprintf("/api/admin/webhooks/%d", int64(first["id"].(float64))), "", "")
	if detail.status != 200 || !strings.Contains(detail.body["body"].(string), `"order.partially_filled"`) || detail.body["nonce"] != nonce {
		t.Fatalf("detail: %d %s", detail.status, detail.raw)
	}
	if r := e.do(t, "GET", "/api/admin/webhooks/999999", "", ""); r.status != 404 {
		t.Fatalf("unknown id: %d", r.status)
	}

	// The live stream replays the log too.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/admin/webhooks/stream", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if events := readEvents(t, res, 6); !strings.Contains(events[5]["data"], "invalid_body") || events[0]["event"] != "webhook" {
		t.Fatalf("stream = %+v", events)
	}
}
