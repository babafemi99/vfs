// Package store keeps all simulator state in memory behind one mutex.
// Every change goes through a domain method; the resulting event is passed
// to the sink while the lock is held, so events reach the sink in the exact
// order the changes happened.
package store

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/clock"
	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
)

type Config struct {
	QuoteTTL       time.Duration
	FeeBps         int64
	CancelDelayMin time.Duration
	CancelDelayMax time.Duration
	MarketOpen     bool
}

// Sink receives every event. It is called with the store lock held, so it
// must not block and must not call back into the store.
type Sink func(domain.Event)

type Store struct {
	mu     sync.Mutex
	cfg    Config
	clock  clock.Clock
	rng    *rand.Rand // ids and cancel delays; guarded by mu
	market *market.Market
	sink   Sink

	quotes          map[string]*domain.Quote
	quoteList       []*domain.Quote // creation order
	orders          map[string]*domain.Order
	orderList       []*domain.Order // creation order
	byClientOrderID map[string]*domain.Order
	byQuoteID       map[string]*domain.Order
}

func New(cfg Config, clk clock.Clock, rng *rand.Rand, mkt *market.Market, sink Sink) *Store {
	if sink == nil {
		sink = func(domain.Event) {}
	}
	return &Store{
		cfg: cfg, clock: clk, rng: rng, market: mkt, sink: sink,
		quotes:          map[string]*domain.Quote{},
		orders:          map[string]*domain.Order{},
		byClientOrderID: map[string]*domain.Order{},
		byQuoteID:       map[string]*domain.Order{},
	}
}

func (s *Store) SetMarketOpen(open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.MarketOpen = open
}

func (s *Store) MarketOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.MarketOpen
}

// Reset drops all quotes and orders. Config and RNG state are kept.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotes = map[string]*domain.Quote{}
	s.quoteList = nil
	s.orders = map[string]*domain.Order{}
	s.orderList = nil
	s.byClientOrderID = map[string]*domain.Order{}
	s.byQuoteID = map[string]*domain.Order{}
}

// ---- quotes ----

// CreateQuote prices a quote from the current market: ask for BUY, bid for SELL.
func (s *Store) CreateQuote(symbol string, side domain.Side, qty int64) (domain.Quote, error) {
	if !side.Valid() {
		return domain.Quote{}, domain.Errorf(domain.CodeValidation, "side must be BUY or SELL, got %q", side)
	}
	p, ok := s.market.Price(symbol)
	if !ok {
		return domain.Quote{}, domain.Errorf(domain.CodeValidation, "unknown symbol %q", symbol)
	}
	price := p.Ask
	if side == domain.Sell {
		price = p.Bid
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := domain.NewQuote(s.newID("qt"), symbol, side, qty, price, s.cfg.FeeBps, s.clock.Now(), s.cfg.QuoteTTL)
	if err != nil {
		return domain.Quote{}, err
	}
	s.quotes[q.ID] = q
	s.quoteList = append(s.quoteList, q)
	return *q, nil
}

// GetQuote returns the quote as the client should see it now.
func (s *Store) GetQuote(id string) (domain.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quotes[id]
	if !ok {
		return domain.Quote{}, domain.Errorf(domain.CodeNotFound, "quote %s not found", id)
	}
	return q.View(s.clock.Now()), nil
}

// ExpiredQuoteIDs returns ACTIVE quotes past their expiry, oldest first.
func (s *Store) ExpiredQuoteIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	var ids []string
	for _, q := range s.quoteList {
		if q.Status == domain.QuoteActive && q.PastExpiry(now) {
			ids = append(ids, q.ID)
		}
	}
	return ids
}

// ExpireQuote marks an ACTIVE quote past its expiry as EXPIRED and emits quote.expired.
func (s *Store) ExpireQuote(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quotes[id]
	if !ok {
		return domain.Errorf(domain.CodeNotFound, "quote %s not found", id)
	}
	ev, err := q.Expire(s.clock.Now())
	s.emit(ev)
	return err
}

// AcceptQuote turns a quote into an OPEN order. It is idempotent on
// clientOrderID: replaying the same (quote, key) returns the same order with
// created=false and emits nothing.
func (s *Store) AcceptQuote(quoteID, clientOrderID string) (order domain.Order, created bool, err error) {
	if clientOrderID == "" {
		return domain.Order{}, false, domain.Errorf(domain.CodeValidation, "client_order_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if o, ok := s.byClientOrderID[clientOrderID]; ok {
		if o.QuoteID != quoteID {
			return domain.Order{}, false, domain.Errorf(domain.CodeIdempotencyConflict,
				"client_order_id %s already used for quote %s", clientOrderID, o.QuoteID)
		}
		return o.Snapshot(), false, nil
	}
	q, ok := s.quotes[quoteID]
	if !ok {
		return domain.Order{}, false, domain.Errorf(domain.CodeNotFound, "quote %s not found", quoteID)
	}
	if q.Status == domain.QuoteAccepted {
		return domain.Order{}, false, domain.Errorf(domain.CodeQuoteAlreadyAccepted, "quote %s already accepted", quoteID)
	}
	if !s.cfg.MarketOpen {
		return domain.Order{}, false, domain.Errorf(domain.CodeMarketClosed, "market is closed")
	}

	now := s.clock.Now()
	orderID := s.newID("ord")
	if err := q.Accept(orderID, now); err != nil {
		return domain.Order{}, false, err
	}
	o, ev := domain.NewOrder(orderID, clientOrderID, q, now)
	s.orders[o.ID] = o
	s.orderList = append(s.orderList, o)
	s.byClientOrderID[clientOrderID] = o
	s.byQuoteID[quoteID] = o
	s.emit(ev)
	return o.Snapshot(), true, nil
}

// ---- orders: reads ----

func (s *Store) GetOrder(id string) (domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.order(id)
	if err != nil {
		return domain.Order{}, err
	}
	return o.Snapshot(), nil
}

func (s *Store) GetOrderByClientOrderID(clientOrderID string) (domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byClientOrderID[clientOrderID]
	if !ok {
		return domain.Order{}, domain.Errorf(domain.CodeNotFound, "no order with client_order_id %s", clientOrderID)
	}
	return o.Snapshot(), nil
}

type ListFilter struct {
	Statuses    []domain.OrderStatus // empty = any
	Since       time.Time            // zero = any; matches UpdatedAt >= Since
	OldestFirst bool                 // default newest first
}

// ListOrders returns matching orders, newest first unless f.OldestFirst.
func (s *Store) ListOrders(f ListFilter) []domain.Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Order{}
	n := len(s.orderList)
	for i := range n {
		o := s.orderList[n-1-i]
		if f.OldestFirst {
			o = s.orderList[i]
		}
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, o.Status) {
			continue
		}
		if !f.Since.IsZero() && o.UpdatedAt.Before(f.Since) {
			continue
		}
		out = append(out, o.Snapshot())
	}
	return out
}

// ---- orders: changes ----

// RequestCancel moves the order to PENDING_CANCEL with a random resolve time.
// On error the current order is still returned (the API sends it with order_final).
func (s *Store) RequestCancel(id string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.RequestCancel(now, now.Add(s.cancelDelay()))
	})
}

func (s *Store) FillOrder(id string, qty int64) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.Fill(s.newID("fill"), qty, s.cfg.FeeBps, now)
	})
}

// FillRemaining fills everything still open on the order, in one fill.
func (s *Store) FillRemaining(id string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.Fill(s.newID("fill"), o.RemainingQty, s.cfg.FeeBps, now)
	})
}

func (s *Store) ResolveCancel(id string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.ResolveCancel(now)
	})
}

func (s *Store) RejectCancel(id, reason string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.RejectCancel(reason, now)
	})
}

func (s *Store) RejectOrder(id, reason string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.Reject(reason, now)
	})
}

func (s *Store) ExpireOrder(id, reason string) (domain.Order, error) {
	return s.mutate(id, func(o *domain.Order, now time.Time) (*domain.Event, error) {
		return o.Expire(reason, now)
	})
}

// ---- internals ----

func (s *Store) mutate(id string, fn func(*domain.Order, time.Time) (*domain.Event, error)) (domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.order(id)
	if err != nil {
		return domain.Order{}, err
	}
	ev, err := fn(o, s.clock.Now())
	s.emit(ev)
	return o.Snapshot(), err
}

func (s *Store) order(id string) (*domain.Order, error) {
	o, ok := s.orders[id]
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "order %s not found", id)
	}
	return o, nil
}

func (s *Store) emit(ev *domain.Event) {
	if ev != nil {
		s.sink(*ev)
	}
}

// newID must be called with mu held.
func (s *Store) newID(prefix string) string {
	return fmt.Sprintf("%s_%012x", prefix, s.rng.Uint64()&0xffffffffffff)
}

// cancelDelay must be called with mu held.
func (s *Store) cancelDelay() time.Duration {
	lo, hi := s.cfg.CancelDelayMin, s.cfg.CancelDelayMax
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(s.rng.Int64N(int64(hi-lo)+1))
}
