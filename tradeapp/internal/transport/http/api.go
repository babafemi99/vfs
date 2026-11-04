// Package httpapi is the HTTP transport layer: it decodes requests, calls the
// services in sys.Dependencies and encodes responses. It never touches the
// repository. HTTP concerns live here: routing, middleware (request ids,
// logging, panics, rate limits) and the Idempotency-Key protocol, whose
// records are kept by IdempoSrv.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/reqid"
	"github.com/babafem99/vfs/tradeapp/internal/sys"
)

// DemoUserID is the only user (no auth in this app).
const DemoUserID = 1

const headerRequestID = "X-Request-Id"

type API struct {
	Server     *http.Server
	Dependency *sys.Dependencies

	// Per-user token buckets for the routes that create things, one budget
	// per route group. Built in Handler from the config.
	ordersLimit, quotesLimit, fundLimit *limiter
}

// Serve listens on the configured address until Shutdown is called (then it
// returns http.ErrServerClosed).
func (a *API) Serve() error {
	a.Server = &http.Server{
		Addr:              a.Dependency.Config.Addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		// No WriteTimeout: the SSE streams stay open. Ordinary handlers are
		// bounded by their contexts and the provider client's timeout.
	}
	a.Dependency.Logger.Info("listening", "addr", a.Server.Addr)
	return a.Server.ListenAndServe()
}

// Shutdown stops accepting connections and waits for in-flight requests.
func (a *API) Shutdown(ctx context.Context) error {
	if a.Server == nil {
		return nil
	}
	return a.Server.Shutdown(ctx)
}

// Handler is the full HTTP handler: routes plus middleware. Tests serve it
// with httptest.
func (a *API) Handler() http.Handler {
	cfg := a.Dependency.Config
	a.ordersLimit = newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	a.quotesLimit = newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	a.fundLimit = newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /metrics", a.metrics)
	a.RegisterRoutes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})

	// Outermost first: every request gets an id, is logged, and a panic
	// becomes a 500 (logged with that id).
	return a.requestID(a.observe(mux, a.recoverer(a.withActor(mux))))
}

// metrics is GET /metrics in the Prometheus text format. (In production it
// would sit on an internal port; here the web proxy only forwards /api, so
// browsers can't reach it anyway.)
func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	a.Dependency.Metrics.Registry.WriteTo(r.Context(), w)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Dependency.TradeSrv.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// withActor puts who the request acts for in the context, where services
// that need it (idempotency) read it. There is no auth in this app, so it is
// always the demo user; with auth, this is where the token's user goes.
func (a *API) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := domain.WithActor(r.Context(), domain.Actor{UserID: DemoUserID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestID keeps the caller's X-Request-Id (or makes one), echoes it on the
// response and puts it in the context: every log line written with that
// context, and every job the request queues, carries it.
func (a *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			r.Header.Set(headerRequestID, id)
		}
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(reqid.With(r.Context(), id)))
	})
}

// observe logs every request and records its count and latency. Metrics
// are labelled by route pattern ("/api/orders/{id}"), not the raw path, so
// ids don't explode the number of series.
func (a *API) observe(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		route := routeOf(mux, r)
		a.Dependency.Metrics.HTTPRequests.Inc(r.Method, route, strconv.Itoa(sw.status))
		a.Dependency.Metrics.HTTPDuration.ObserveSince(start, r.Method, route)
		if route == "/metrics" || route == "/healthz" {
			return // scraped and probed constantly: count them, don't log them
		}
		a.Dependency.Logger.InfoContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur", time.Since(start))
	})
}

// routeOf returns the pattern that matched r without its method
// ("/api/orders/{id}"), or "unmatched".
func routeOf(mux *http.ServeMux, r *http.Request) string {
	_, pattern := mux.Handler(r)
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	if pattern == "" || pattern == "/" {
		return "unmatched"
	}
	return pattern
}

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				a.Dependency.Logger.ErrorContext(r.Context(), "panic", "method", r.Method, "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (to flush a
// stream or change its deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
