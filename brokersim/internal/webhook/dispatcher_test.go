package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
)

const secret = "whsec_test"

type hit struct {
	eventID string
	payload Payload
}

// testServer answers 500 for the first failFirst requests, then 200.
// It verifies every signature.
func testServer(t *testing.T, failFirst int) (*httptest.Server, func() []hit) {
	t.Helper()
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get(HeaderTimestamp), 10, 64)
		if err := Verify(secret, ts, r.Header.Get(HeaderNonce), body, r.Header.Get(HeaderSignature), time.Minute); err != nil {
			t.Errorf("signature: %v", err)
		}
		var p Payload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("payload: %v", err)
		}
		mu.Lock()
		hits = append(hits, hit{eventID: r.Header.Get(HeaderEventID), payload: p})
		n := len(hits)
		mu.Unlock()
		if n <= failFirst {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []hit {
		mu.Lock()
		defer mu.Unlock()
		return append([]hit(nil), hits...)
	}
}

func startDispatcher(t *testing.T, url string, backoff []time.Duration) *Dispatcher {
	t.Helper()
	d := New(Options{
		URL: url, Secret: secret, Backoff: backoff,
		Rand:   rand.New(rand.NewPCG(1, 1)),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.Run(ctx)
	return d
}

func waitState(t *testing.T, d *Dispatcher, want State) Delivery {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ds := d.Deliveries(); len(ds) > 0 && ds[0].State == want {
			return ds[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("delivery never reached %s: %+v", want, d.Deliveries())
	return Delivery{}
}

func orderEvent() domain.Event {
	return domain.Event{
		Type: domain.EventOrderPartiallyFilled,
		At:   time.Unix(1_800_000_000, 0).UTC(),
		Order: &domain.Order{ID: "ord_1", Status: domain.OrderPartiallyFilled, Qty: 10, FilledQty: 4,
			RemainingQty: 6, Version: 2},
		Fill: &domain.Fill{ID: "fill_1", Qty: 4, Price: 100, Fee: 2},
	}
}

func TestDispatcherRetriesThenSucceeds(t *testing.T) {
	srv, hits := testServer(t, 2)
	d := startDispatcher(t, srv.URL, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	d.Enqueue(orderEvent())

	del := waitState(t, d, StateDelivered)
	if del.Attempts != 3 || del.LastStatus != 200 || del.OrderID != "ord_1" {
		t.Errorf("delivery = %+v", del)
	}
	hs := hits()
	if len(hs) != 3 {
		t.Fatalf("server saw %d requests, want 3", len(hs))
	}
	for _, h := range hs {
		if h.eventID != del.EventID || h.payload.ID != del.EventID {
			t.Errorf("event id changed across retries: header %s body %s want %s", h.eventID, h.payload.ID, del.EventID)
		}
	}
	p := hs[0].payload
	if p.Type != domain.EventOrderPartiallyFilled || p.Data.Order.FilledQty != 4 || p.Data.Fill.Qty != 4 {
		t.Errorf("payload = %+v", p)
	}
}

func TestDispatcherGivesUp(t *testing.T) {
	srv, hits := testServer(t, 1000)
	d := startDispatcher(t, srv.URL, []time.Duration{time.Millisecond, time.Millisecond})
	d.Enqueue(orderEvent())

	del := waitState(t, d, StateFailed)
	if del.Attempts != 3 || del.LastStatus != 500 || del.LastError == "" {
		t.Errorf("delivery = %+v", del)
	}
	if n := len(hits()); n != 3 {
		t.Errorf("server saw %d requests, want 3", n)
	}
}

func TestDispatcherKeepsOrder(t *testing.T) {
	srv, hits := testServer(t, 0)
	d := startDispatcher(t, srv.URL, nil)
	types := []domain.EventType{domain.EventOrderAccepted, domain.EventOrderPartiallyFilled, domain.EventOrderFilled}
	for _, typ := range types {
		ev := orderEvent()
		ev.Type = typ
		d.Enqueue(ev)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(hits()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hs := hits()
	if len(hs) != 3 {
		t.Fatalf("got %d hits", len(hs))
	}
	for i, h := range hs {
		if h.payload.Type != types[i] {
			t.Errorf("hit %d = %s, want %s", i, h.payload.Type, types[i])
		}
	}
}

func TestRedeliverAndReset(t *testing.T) {
	srv, hits := testServer(t, 0)
	d := startDispatcher(t, srv.URL, []time.Duration{time.Millisecond})
	d.Enqueue(orderEvent())
	first := waitState(t, d, StateDelivered)

	if _, err := d.Redeliver("evt_nope"); err == nil {
		t.Error("redeliver unknown id should fail")
	}
	if _, err := d.Redeliver(first.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Redeliver(first.EventID); err == nil {
		t.Error("redeliver while pending should fail")
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(hits()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hs := hits()
	if len(hs) != 2 || hs[1].eventID != first.EventID || hs[1].payload.ID != first.EventID {
		t.Fatalf("redelivery should resend the same event: %+v", hs)
	}
	if del := waitState(t, d, StateDelivered); del.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", del.Attempts)
	}

	d.Reset()
	if n := len(d.Deliveries()); n != 0 {
		t.Errorf("log has %d rows after reset", n)
	}
	if _, err := d.Redeliver(first.EventID); err == nil {
		t.Error("event should be gone after reset")
	}
}
