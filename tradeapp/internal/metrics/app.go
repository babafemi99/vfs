package metrics

// App is every metric the trade app records, in one place, so a reader can
// see what is measured without searching the code.
type App struct {
	Registry *Registry

	HTTPRequests *CounterVec   // method, route, status
	HTTPDuration *HistogramVec // method, route
	Webhooks     *CounterVec   // outcome (accepted, duplicate, rejected_signature, rejected_replay, ...)
	RateLimited  *CounterVec   // group (orders, quotes, fund)
	Idempotency  *CounterVec   // result (new, replayed, conflict, in_progress)
	Jobs         *CounterVec   // kind, outcome (done, failed, deferred, dead)
}

func NewApp() *App {
	r := NewRegistry()
	return &App{
		Registry:     r,
		HTTPRequests: r.NewCounterVec("http_requests_total", "HTTP requests by route and status.", "method", "route", "status"),
		HTTPDuration: r.NewHistogramVec("http_request_duration_seconds", "HTTP request latency.", DefaultBuckets, "method", "route"),
		Webhooks:     r.NewCounterVec("webhooks_received_total", "Provider webhook requests by outcome.", "outcome"),
		RateLimited:  r.NewCounterVec("rate_limited_total", "Requests refused by the rate limiter.", "group"),
		Idempotency:  r.NewCounterVec("idempotency_requests_total", "Idempotent requests by result.", "result"),
		Jobs:         r.NewCounterVec("jobs_processed_total", "Outbox job attempts by kind and outcome.", "kind", "outcome"),
	}
}
