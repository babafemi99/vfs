package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The request id sent with an order is stored on the job it queues, so the
// worker can log with it later.
func TestRequestIDFollowsTheOrderIntoItsJob(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "f", `{"amount": 5000000}`)
	body := fmt.Sprintf(`{"quote_id":%q}`, e.quote(t, "AAPL", "BUY", 1))

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/orders", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "o1")
	req.Header.Set("X-Request-Id", "trace-order-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 && res.StatusCode != 200 {
		t.Fatalf("place: %d", res.StatusCode)
	}
	if got := res.Header.Get("X-Request-Id"); got != "trace-order-1" {
		t.Fatalf("echoed request id = %q", got)
	}

	jobs := e.do(t, "GET", "/api/admin/jobs?status=pending", "", "")
	list, _ := jobs.body["jobs"].([]any)
	found := false
	for _, j := range list {
		job := j.(map[string]any)
		if job["kind"] == "submit_order" {
			found = job["request_id"] == "trace-order-1"
		}
	}
	if !found {
		t.Fatalf("submit job should carry the request id: %s", jobs.raw)
	}

	// A request without an id gets one generated.
	res, err = http.Get(e.srv.URL + "/api/account")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(res.Header.Get("X-Request-Id")) != 16 {
		t.Fatalf("generated request id = %q", res.Header.Get("X-Request-Id"))
	}
}

func TestMetricsEndpoint(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "m1", `{"amount": 100}`)
	e.do(t, "POST", "/api/account/fund", "m1", `{"amount": 100}`) // replay
	e.do(t, "POST", "/api/account/fund", "m1", `{"amount": 5}`)   // conflict
	e.do(t, "GET", "/api/orders/order_nope", "", "")

	res, err := http.Get(e.srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := string(b)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		`http_requests_total{method="POST",route="/api/account/fund",status="200"} 2`,
		`http_requests_total{method="POST",route="/api/account/fund",status="422"} 1`,
		`http_requests_total{method="GET",route="/api/orders/{id}",status="404"} 1`, // pattern, not the raw id
		`http_request_duration_seconds_count{method="POST",route="/api/account/fund"} 3`,
		`idempotency_requests_total{result="new"} 1`,
		`idempotency_requests_total{result="replayed"} 1`,
		`idempotency_requests_total{result="conflict"} 1`,
		`jobs_dead 0`,
		`provider_circuit_state{state="closed"} 1`,
		`metrics_collector_errors 0`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	if t.Failed() {
		t.Logf("metrics output:\n%s", out)
	}
	var probe map[string]any
	if json.Unmarshal(b, &probe) == nil {
		t.Fatal("metrics should be text, not JSON")
	}
}
