package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// limiter is a set of token buckets, one per key (user + route group).
//
// Each bucket holds up to burst tokens and refills at perSecond tokens per
// second. A request takes one token; with none left it is refused, and the
// caller is told how long until the next token. In memory, so the limit is
// per API instance; with several instances the same logic would live in
// Redis (a Lua script keeps read-and-update atomic).
type limiter struct {
	mu        sync.Mutex
	perSecond float64
	burst     float64
	now       func() time.Time
	buckets   map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newLimiter returns nil (no limiting) when perMinute or burst is 0.
func newLimiter(perMinute, burst int) *limiter {
	if perMinute <= 0 || burst <= 0 {
		return nil
	}
	return &limiter{
		perSecond: float64(perMinute) / 60,
		burst:     float64(burst),
		now:       time.Now,
		buckets:   map[string]*bucket{},
	}
}

// allow takes a token for key. When refused, wait is how long until a token
// is available again.
func (l *limiter) allow(key string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	// Refill for the time since the last request, up to the burst size.
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	missing := 1 - b.tokens
	return false, time.Duration(missing / l.perSecond * float64(time.Second))
}

// rateLimited refuses requests over the limit with 429 and Retry-After.
// group names the bucket ("orders", "fund"), so each route group has its own
// budget per user.
func (s *API) rateLimited(group string, l *limiter, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ok, wait := l.allow(group + ":" + strconv.Itoa(DemoUserID))
		if !ok {
			s.Dependency.Metrics.RateLimited.Inc(group)
			seconds := int(math.Ceil(wait.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			s.Dependency.Logger.WarnContext(r.Context(), "rate limited", "group", group, "user_id", DemoUserID, "retry_after_s", seconds)
			writeError(w, http.StatusTooManyRequests, "rate_limited",
				"too many requests, try again in "+strconv.Itoa(seconds)+"s")
			return
		}
		next(w, r)
	}
}
