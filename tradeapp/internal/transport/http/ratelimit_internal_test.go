package httpapi

import (
	"testing"
	"time"
)

func TestLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newLimiter(60, 3) // 1 token per second, up to 3
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.allow("u"); !ok {
			t.Fatalf("request %d refused inside the burst", i+1)
		}
	}
	ok, wait := l.allow("u")
	if ok || wait != time.Second {
		t.Fatalf("4th request: ok=%v wait=%v, want refused with 1s", ok, wait)
	}
	if ok, _ := l.allow("other"); !ok {
		t.Fatal("another key shares the bucket")
	}

	now = now.Add(1500 * time.Millisecond) // 1.5 tokens back
	if ok, _ := l.allow("u"); !ok {
		t.Fatal("not refilled after 1.5s")
	}
	if ok, wait := l.allow("u"); ok || wait != 500*time.Millisecond {
		t.Fatalf("after using the refill: ok=%v wait=%v, want 500ms", ok, wait)
	}

	now = now.Add(time.Hour) // refill never exceeds the burst
	for i := range 3 {
		if ok, _ := l.allow("u"); !ok {
			t.Fatalf("request %d after a long idle", i+1)
		}
	}
	if ok, _ := l.allow("u"); ok {
		t.Fatal("bucket refilled past its burst")
	}
}

func TestLimiterOff(t *testing.T) {
	if newLimiter(0, 10) != nil || newLimiter(60, 0) != nil {
		t.Fatal("a zero rate or burst should turn limiting off")
	}
}
