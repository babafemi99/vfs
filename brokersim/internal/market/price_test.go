package market

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

func newTestMarket(seed uint64) *Market {
	syms, _ := ParseSymbols("AAPL:19000,TINY:2")
	return New(syms, 20, rand.New(rand.NewPCG(seed, 0)))
}

func TestParseSymbols(t *testing.T) {
	got, err := ParseSymbols(" AAPL:19000 , MSFT:41000")
	if err != nil {
		t.Fatal(err)
	}
	want := []Symbol{{"AAPL", 19000}, {"MSFT", 41000}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"", "AAPL", "AAPL:x", "AAPL:0", ":5", "A:1,A:2"} {
		if _, err := ParseSymbols(bad); err == nil {
			t.Errorf("ParseSymbols(%q) should fail", bad)
		}
	}
}

func TestSpread(t *testing.T) {
	m := newTestMarket(1)
	p, ok := m.Price("AAPL")
	if !ok || p.Mid != 19000 || p.Bid != 18981 || p.Ask != 19019 {
		t.Errorf("price = %+v", p)
	}
	if _, ok := m.Price("NOPE"); ok {
		t.Error("unknown symbol should not be found")
	}
}

func TestStepDeterministicAndBounded(t *testing.T) {
	a, b := newTestMarket(42), newTestMarket(42)
	for i := 0; i < 1000; i++ {
		prev, _ := a.Price("AAPL")
		a.Step()
		b.Step()
		cur, _ := a.Price("AAPL")
		if d := cur.Mid - prev.Mid; d*10000 > prev.Mid*maxMoveBps || -d*10000 > prev.Mid*maxMoveBps {
			t.Fatalf("move %d from %d exceeds ±0.2%%", d, prev.Mid)
		}
		for _, p := range a.Prices() {
			if p.Mid < 1 || p.Bid < 1 || p.Bid > p.Mid || p.Ask < p.Mid {
				t.Fatalf("bad price %+v", p)
			}
		}
	}
	if !reflect.DeepEqual(a.Prices(), b.Prices()) {
		t.Error("same seed produced different prices")
	}
}
