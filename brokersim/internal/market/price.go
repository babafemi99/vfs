// Package market holds one mid price per symbol and moves it with a seeded random walk.
package market

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
)

// maxMoveBps is the largest move per Step: ±0.2%.
const maxMoveBps = 20

type Symbol struct {
	Name string
	Mid  int64 // minor units
}

// ParseSymbols parses "AAPL:19000,MSFT:41000".
func ParseSymbols(s string) ([]Symbol, error) {
	var out []Symbol
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, priceStr, ok := strings.Cut(part, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("symbol %q: want NAME:PRICE", part)
		}
		price, err := strconv.ParseInt(priceStr, 10, 64)
		if err != nil || price <= 0 {
			return nil, fmt.Errorf("symbol %q: price must be a positive integer", part)
		}
		if seen[name] {
			return nil, fmt.Errorf("symbol %q listed twice", name)
		}
		seen[name] = true
		out = append(out, Symbol{Name: name, Mid: price})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no symbols")
	}
	return out, nil
}

type Price struct {
	Symbol string `json:"symbol"`
	Bid    int64  `json:"bid"`
	Mid    int64  `json:"mid"`
	Ask    int64  `json:"ask"`
}

type Market struct {
	mu        sync.Mutex
	rng       *rand.Rand
	spreadBps int64
	names     []string // config order, so iteration is deterministic
	mids      map[string]int64
}

func New(symbols []Symbol, spreadBps int64, rng *rand.Rand) *Market {
	m := &Market{rng: rng, spreadBps: spreadBps, mids: make(map[string]int64, len(symbols))}
	for _, s := range symbols {
		m.names = append(m.names, s.Name)
		m.mids[s.Name] = s.Mid
	}
	return m
}

// Step moves every mid by a random amount up to ±0.2%, never below 1.
func (m *Market) Step() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range m.names {
		mid := m.mids[name]
		moveBps := m.rng.Int64N(2*maxMoveBps+1) - maxMoveBps
		mid += mid * moveBps / 10000
		m.mids[name] = max(mid, 1)
	}
}

func (m *Market) Price(symbol string) (Price, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mid, ok := m.mids[symbol]
	if !ok {
		return Price{}, false
	}
	return m.price(symbol, mid), true
}

func (m *Market) Prices() []Price {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Price, 0, len(m.names))
	for _, name := range m.names {
		out = append(out, m.price(name, m.mids[name]))
	}
	return out
}

func (m *Market) price(symbol string, mid int64) Price {
	half := mid * m.spreadBps / 10000 / 2
	return Price{Symbol: symbol, Bid: max(mid-half, 1), Mid: mid, Ask: mid + half}
}
