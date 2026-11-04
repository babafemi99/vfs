// Command receiver is a tiny webhook endpoint for local testing. It verifies
// signatures, prints one line per event and flags duplicates by event id.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/webhook"
)

func main() {
	addr := flag.String("addr", ":8091", "listen address")
	secret := flag.String("secret", envOr("SIM_WEBHOOK_SECRET", "whsec_test"), "webhook HMAC secret")
	failRate := flag.Float64("fail-rate", 0, "fraction of requests answered with 500, to exercise retries")
	flag.Parse()

	r := &receiver{secret: *secret, failRate: *failRate, seen: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/provider", r.handle)
	log.Printf("receiver listening on %s (fail-rate %.2f)", *addr, *failRate)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type receiver struct {
	secret   string
	failRate float64

	mu   sync.Mutex
	seen map[string]bool
}

func (rc *receiver) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ts, _ := strconv.ParseInt(r.Header.Get(webhook.HeaderTimestamp), 10, 64)
	if err := webhook.Verify(rc.secret, ts, r.Header.Get(webhook.HeaderNonce), body, r.Header.Get(webhook.HeaderSignature), 5*time.Minute); err != nil {
		fmt.Printf("REJECTED %s: %v\n", r.Header.Get(webhook.HeaderEventID), err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if rand.Float64() < rc.failRate {
		fmt.Printf("FAKE-500 %s\n", r.Header.Get(webhook.HeaderEventID))
		http.Error(w, "simulated failure", http.StatusInternalServerError)
		return
	}

	var p webhook.Payload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	rc.mu.Lock()
	dup := rc.seen[p.ID]
	rc.seen[p.ID] = true
	rc.mu.Unlock()

	line := fmt.Sprintf("%s %s", p.ID, p.Type)
	switch {
	case p.Data.Order != nil:
		o := p.Data.Order
		line += fmt.Sprintf(" %s %s %d/%d v%d", o.ID, o.Status, o.FilledQty, o.Qty, o.Version)
	case p.Data.Quote != nil:
		line += fmt.Sprintf(" %s %s", p.Data.Quote.ID, p.Data.Quote.Status)
	}
	if p.Data.Reason != "" {
		line += " reason=" + p.Data.Reason
	}
	if dup {
		line += " DUPLICATE"
	}
	fmt.Println(line)
	w.WriteHeader(http.StatusOK)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
