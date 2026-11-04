package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// readEvents reads SSE events (id and data lines) until n arrive or time runs out.
func readEvents(t *testing.T, res *http.Response, n int) []map[string]string {
	t.Helper()
	sc := bufio.NewScanner(res.Body)
	var events []map[string]string
	cur := map[string]string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur["data"] != "" {
					events = append(events, cur)
					if len(events) == n {
						return
					}
				}
				cur = map[string]string{}
				continue
			}
			if k, v, ok := strings.Cut(line, ": "); ok {
				cur[k] = v
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("got %d of %d events", len(events), n)
	}
	return events
}

func TestStreamReplaysThenFollows(t *testing.T) {
	e := setup(t)
	e.do(t, "POST", "/api/account/fund", "a", `{"amount": 100}`)
	e.do(t, "POST", "/api/account/fund", "b", `{"amount": 200}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/stream", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// Two stored items are replayed, then a new one arrives live.
	go func() {
		time.Sleep(300 * time.Millisecond)
		e.do(t, "POST", "/api/account/fund", "c", `{"amount": 300}`)
	}()
	events := readEvents(t, res, 3)
	if !strings.Contains(events[0]["data"], "$1.00") || !strings.Contains(events[2]["data"], "$3.00") {
		t.Fatalf("events = %+v", events)
	}
	if events[0]["event"] != "activity" || events[0]["id"] == "" {
		t.Fatalf("event fields = %+v", events[0])
	}

	// Resuming after the second id only replays the third.
	req2, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/stream", nil)
	req2.Header.Set("Last-Event-ID", events[1]["id"])
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if resumed := readEvents(t, res2, 1); resumed[0]["id"] != events[2]["id"] {
		t.Fatalf("resumed at %+v, want id %s", resumed[0], events[2]["id"])
	}

	if r := e.do(t, "GET", "/api/stream?after=x", "", ""); r.status != 400 {
		t.Fatalf("bad cursor: %d", r.status)
	}
}
