package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/config"
	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/metrics"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/internal/service"
	"github.com/babafem99/vfs/tradeapp/internal/sys"
	httpapi "github.com/babafem99/vfs/tradeapp/internal/transport/http"
)

const webhookSecret = "whsec_test"

type env struct {
	srv    *httptest.Server
	repo   repository.Repository
	svc    *service.Service
	broker *providertest.Broker
}

func setup(t *testing.T) env {
	t.Helper()
	return setupWithConfig(t, service.DefaultConfig())
}

func setupWithConfig(t *testing.T, cfg service.Config) env {
	t.Helper()
	return newEnv(t, cfg, config.Config{WebhookSecret: webhookSecret, WebhookTolerance: 5 * time.Minute})
}

// newEnv builds the dependencies the way sys.NewDependencies does, but on a
// test database and a fake broker, and serves the API.
func newEnv(t *testing.T, svcCfg service.Config, appCfg config.Config) env {
	t.Helper()
	repo := repository.New(dbtest.New(t))
	broker := providertest.New(t)
	svc := service.New(repo, provider.New(broker.URL, providertest.APIKey, 2*time.Second, nil), svcCfg)
	m := metrics.NewApp()
	sys.RegisterCollectors(m, svc)
	api := &httpapi.API{Dependency: &sys.Dependencies{
		Config:     &appCfg,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    m,
		TradeSrv:   svc,
		IdempoSrv:  service.NewIdempotencyService(repo, service.DefaultIdempotencyConfig(), nil),
		WebhookSrv: service.NewWebhookService(repo),
	}}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return env{srv: srv, repo: repo, svc: svc, broker: broker}
}

type resp struct {
	status   int
	replayed bool
	raw      string
	body     map[string]any
}

func (e env) do(t *testing.T, method, path, key, body string) resp {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, replayed: res.Header.Get("Idempotent-Replayed") == "true", raw: string(b)}
	if err := json.Unmarshal(b, &r.body); err != nil {
		t.Fatalf("%s %s: decode %q: %v", method, path, b, err)
	}
	return r
}

func errCode(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (e env) available(t *testing.T) float64 {
	t.Helper()
	r := e.do(t, "GET", "/api/account", "", "")
	if r.status != 200 {
		t.Fatalf("GET /api/account: %d %s", r.status, r.raw)
	}
	return r.body["cash"].(map[string]any)["available"].(float64)
}

func (e env) fundEntries(t *testing.T) int {
	t.Helper()
	var n int
	err := e.repo.RunInTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		n, err = repository.LedgerRepo{}.CountJournalEntries(context.Background(), tx, domain.EntryFund)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFundValidation(t *testing.T) {
	e := setup(t)
	cases := []struct {
		name, key, body, code string
	}{
		{"missing key", "", `{"amount": 100}`, "idempotency_key_required"},
		{"key too long", strings.Repeat("k", 256), `{"amount": 100}`, "validation_error"},
		{"zero", "k1", `{"amount": 0}`, "validation_error"},
		{"negative", "k2", `{"amount": -5}`, "validation_error"},
		{"too large", "k3", `{"amount": 10000001}`, "validation_error"},
		{"float", "k4", `{"amount": 10.5}`, "validation_error"},
		{"unknown field", "k5", `{"amount": 10, "x": 1}`, "validation_error"},
		{"not json", "k6", `nope`, "validation_error"},
		{"two objects", "k7", `{"amount": 1}{"amount": 2}`, "validation_error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.do(t, "POST", "/api/account/fund", c.key, c.body)
			if r.status != 400 || errCode(r) != c.code {
				t.Fatalf("got %d %q, want 400 %q", r.status, errCode(r), c.code)
			}
		})
	}
	if got := e.available(t); got != 0 {
		t.Fatalf("available = %v after only invalid requests", got)
	}
}

func TestFundIdempotency(t *testing.T) {
	e := setup(t)
	first := e.do(t, "POST", "/api/account/fund", "fund-1", `{"amount": 100000}`)
	if first.status != 200 || first.replayed {
		t.Fatalf("first: %d %s", first.status, first.raw)
	}
	second := e.do(t, "POST", "/api/account/fund", "fund-1", `{"amount": 100000}`)
	if second.status != 200 || !second.replayed || second.raw != first.raw {
		t.Fatalf("second: %d replayed=%v %s (first %s)", second.status, second.replayed, second.raw, first.raw)
	}
	conflict := e.do(t, "POST", "/api/account/fund", "fund-1", `{"amount": 5}`)
	if conflict.status != 422 || errCode(conflict) != "idempotency_conflict" {
		t.Fatalf("conflict: %d %s", conflict.status, conflict.raw)
	}
	if n := e.fundEntries(t); n != 1 {
		t.Fatalf("fund entries = %d, want 1", n)
	}
	if got := e.available(t); got != 100_000 {
		t.Fatalf("available = %v, want 100000", got)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

// A business rejection (4xx) is stored like a success: the same request with
// the same key replays it instead of running again, and a changed request
// with that key is a client bug (422). A new attempt needs a new key.
func TestBusinessRejectionIsReplayed(t *testing.T) {
	e := setup(t)
	bad := e.do(t, "POST", "/api/account/fund", "rejected", `{"amount": 0}`)
	if bad.status != 400 || bad.replayed {
		t.Fatalf("bad: %d %s", bad.status, bad.raw)
	}
	again := e.do(t, "POST", "/api/account/fund", "rejected", `{"amount": 0}`)
	if again.status != 400 || !again.replayed || again.raw != bad.raw {
		t.Fatalf("retry: %d replayed=%v %s", again.status, again.replayed, again.raw)
	}
	changed := e.do(t, "POST", "/api/account/fund", "rejected", `{"amount": 700}`)
	if changed.status != 422 || errCode(changed) != "idempotency_conflict" {
		t.Fatalf("changed body: %d %s", changed.status, changed.raw)
	}
	if good := e.do(t, "POST", "/api/account/fund", "new-key", `{"amount": 700}`); good.status != 200 {
		t.Fatalf("new key: %d %s", good.status, good.raw)
	}
}

// The same key on a different route is also reuse, even with the same body.
func TestKeyReusedOnAnotherRoute(t *testing.T) {
	e := setup(t)
	if r := e.do(t, "POST", "/api/account/fund", "k", `{"amount": 100}`); r.status != 200 {
		t.Fatalf("fund: %d %s", r.status, r.raw)
	}
	r := e.do(t, "POST", "/api/orders", "k", `{"amount": 100}`)
	if r.status != 422 || errCode(r) != "idempotency_conflict" {
		t.Fatalf("other route: %d %s", r.status, r.raw)
	}
}

// 20 concurrent requests with one key: exactly one funds the account; the
// others either replay its response or see it still in progress (409).
func TestFundConcurrentSameKey(t *testing.T) {
	e := setup(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", e.srv.URL+"/api/account/fund", strings.NewReader(`{"amount": 2500}`))
			req.Header.Set("Idempotency-Key", "same")
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
		if s != 200 && s != 409 {
			t.Fatalf("status %d, want 200 or 409", s)
		}
	}
	if n := e.fundEntries(t); n != 1 {
		t.Fatalf("fund entries = %d, want 1", n)
	}
	if got := e.available(t); got != 2_500 {
		t.Fatalf("available = %v, want 2500", got)
	}
	dbtest.AssertInvariants(t, e.svc.CheckInvariants)
}

func TestAccountAndActivity(t *testing.T) {
	e := setup(t)
	r := e.do(t, "GET", "/api/account", "", "")
	if ps, ok := r.body["positions"].([]any); r.status != 200 || !ok || len(ps) != 0 {
		t.Fatalf("account: %d %s", r.status, r.raw)
	}

	e.do(t, "POST", "/api/account/fund", "a", `{"amount": 123456}`)
	a := e.do(t, "GET", "/api/activity", "", "")
	items, _ := a.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("activity: %s", a.raw)
	}
	item := items[0].(map[string]any)
	if item["kind"] != "account.funded" || item["message"] != "Added $1,234.56 to your account" {
		t.Fatalf("item = %v", item)
	}
	after := e.do(t, "GET", fmt.Sprintf("/api/activity?after=%d", int64(item["id"].(float64))), "", "")
	if items, _ := after.body["items"].([]any); len(items) != 0 {
		t.Fatalf("after cursor: %s", after.raw)
	}
	for _, q := range []string{"after=x", "after=-1", "limit=0", "limit=501"} {
		if bad := e.do(t, "GET", "/api/activity?"+q, "", ""); bad.status != 400 {
			t.Errorf("%s: %d", q, bad.status)
		}
	}
}

func TestHealthAndUnknownRoute(t *testing.T) {
	e := setup(t)
	if r := e.do(t, "GET", "/healthz", "", ""); r.status != 200 {
		t.Fatalf("healthz: %d", r.status)
	}
	if r := e.do(t, "GET", "/nope", "", ""); r.status != 404 || errCode(r) != "not_found" {
		t.Fatalf("unknown route: %d %s", r.status, r.raw)
	}
}
