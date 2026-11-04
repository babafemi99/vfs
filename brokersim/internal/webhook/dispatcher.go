package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
)

type State string

const (
	StatePending   State = "pending"
	StateDelivered State = "delivered"
	StateFailed    State = "failed"
)

// DefaultBackoff is the wait before each retry. Max attempts = 1 + len(Backoff).
var DefaultBackoff = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second,
}

// Payload is the JSON body POSTed to the webhook URL.
type Payload struct {
	ID        string           `json:"id"`
	Type      domain.EventType `json:"type"`
	CreatedAt time.Time        `json:"created_at"`
	Data      Data             `json:"data"`
}

type Data struct {
	Order  *domain.Order `json:"order,omitempty"`
	Quote  *domain.Quote `json:"quote,omitempty"`
	Fill   *domain.Fill  `json:"fill,omitempty"`
	Reason string        `json:"reason,omitempty"`
}

// Delivery is one row of the delivery log.
type Delivery struct {
	EventID    string           `json:"event_id"`
	Type       domain.EventType `json:"type"`
	OrderID    string           `json:"order_id,omitempty"`
	Attempts   int              `json:"attempts"`
	LastStatus int              `json:"last_status_code"`
	LastError  string           `json:"last_error,omitempty"`
	State      State            `json:"state"`
}

type Options struct {
	URL     string
	Secret  string
	Backoff []time.Duration // default DefaultBackoff
	Client  *http.Client    // default 10s timeout
	Rand    *rand.Rand      // event ids and jitter; required
	Logger  *slog.Logger    // default slog.Default()
}

// Dispatcher delivers events asynchronously. Enqueue never blocks on the
// network. First attempts go out in enqueue order from a single worker;
// retries are scheduled on timers so a failing event doesn't hold up others.
type Dispatcher struct {
	opts Options

	mu    sync.Mutex
	queue []*job
	log   []*Delivery
	jobs  map[string]*job // by event id, for redelivery
	gen   int             // bumped by Reset; stale jobs are dropped
	wake  chan struct{}
}

type job struct {
	d          *Delivery
	body       []byte
	gen        int
	cycleStart int // Attempts value when the current delivery cycle began
}

func New(o Options) *Dispatcher {
	if o.Backoff == nil {
		o.Backoff = DefaultBackoff
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Dispatcher{opts: o, jobs: map[string]*job{}, wake: make(chan struct{}, 1)}
}

// Enqueue assigns the event an id and queues it. Safe to call with other
// locks held: it only takes the dispatcher's own short lock.
func (d *Dispatcher) Enqueue(ev domain.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := Payload{
		ID:        fmt.Sprintf("evt_%016x", d.opts.Rand.Uint64()),
		Type:      ev.Type,
		CreatedAt: ev.At,
		Data:      Data{Order: ev.Order, Quote: ev.Quote, Fill: ev.Fill, Reason: ev.Reason},
	}
	body, err := json.Marshal(p)
	if err != nil {
		d.opts.Logger.Error("webhook marshal", "type", ev.Type, "err", err)
		return
	}
	del := &Delivery{EventID: p.ID, Type: p.Type, State: StatePending}
	if ev.Order != nil {
		del.OrderID = ev.Order.ID
	}
	d.log = append(d.log, del)
	j := &job{d: del, body: body, gen: d.gen}
	d.jobs[p.ID] = j
	d.pushLocked(j)
}

// Redeliver sends a delivered or failed event again, with the same event id
// and body, and a fresh set of retries.
func (d *Dispatcher) Redeliver(eventID string) (Delivery, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[eventID]
	if !ok {
		return Delivery{}, domain.Errorf(domain.CodeNotFound, "event %s not found", eventID)
	}
	if j.d.State == StatePending {
		return *j.d, domain.Errorf(domain.CodeInvalidTransition, "event %s is still pending", eventID)
	}
	j.d.State = StatePending
	j.cycleStart = j.d.Attempts
	d.pushLocked(j)
	return *j.d, nil
}

// Reset clears the queue and delivery log. Retries already scheduled are dropped.
func (d *Dispatcher) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gen++
	d.queue = nil
	d.log = nil
	d.jobs = map[string]*job{}
}

// Run processes the queue until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		j := d.pop()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		d.attempt(ctx, j)
	}
}

// Deliveries returns the delivery log, newest first.
func (d *Dispatcher) Deliveries() []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Delivery, 0, len(d.log))
	for i := len(d.log) - 1; i >= 0; i-- {
		out = append(out, *d.log[i])
	}
	return out
}

func (d *Dispatcher) attempt(ctx context.Context, j *job) {
	status, err := d.send(ctx, j)
	ok := err == nil && status >= 200 && status < 300
	if err == nil && !ok {
		err = fmt.Errorf("http %d", status)
	}

	d.mu.Lock()
	del := j.d
	del.Attempts++
	del.LastStatus = status
	del.LastError = ""
	if err != nil {
		del.LastError = err.Error()
	}
	var wait time.Duration
	switch {
	case ok:
		del.State = StateDelivered
	case del.Attempts-j.cycleStart > len(d.opts.Backoff):
		del.State = StateFailed
	default:
		wait = d.opts.Backoff[del.Attempts-j.cycleStart-1]
		wait += time.Duration(d.opts.Rand.Int64N(int64(wait)/5 + 1)) // up to +20% jitter
	}
	snap := *del
	d.mu.Unlock()

	log := d.opts.Logger.With("event_id", snap.EventID, "type", snap.Type, "attempt", snap.Attempts, "status", snap.LastStatus)
	switch snap.State {
	case StateDelivered:
		log.Info("webhook delivered")
	case StateFailed:
		log.Error("webhook failed", "err", snap.LastError)
	default:
		log.Warn("webhook retry scheduled", "err", snap.LastError, "in", wait)
		time.AfterFunc(wait, func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.pushLocked(j)
		})
	}
}

func (d *Dispatcher) send(ctx context.Context, j *job) (int, error) {
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.opts.URL, bytes.NewReader(j.body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderEventID, j.d.EventID) // safe: EventID never changes
	nonce := NewNonce()                        // a new one per attempt: retries are not replays
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, Sign(d.opts.Secret, ts, nonce, j.body))
	resp, err := d.opts.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

func (d *Dispatcher) pushLocked(j *job) {
	if j.gen != d.gen {
		return
	}
	d.queue = append(d.queue, j)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) pop() *job {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queue) == 0 {
		return nil
	}
	j := d.queue[0]
	d.queue[0] = nil
	d.queue = d.queue[1:]
	return j
}
