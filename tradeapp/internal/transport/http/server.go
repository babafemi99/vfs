// Package httpapi is the HTTP transport layer: it decodes requests, calls the
// service and encodes responses. It also owns request-level idempotency
// (Idempotency-Key on POST routes), which is about HTTP retries rather than
// business rules.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

// DemoUserID is the only user (no auth in this app).
const DemoUserID = 1

// Config holds the transport's own settings.
type Config struct {
	WebhookSecret    string        // shared with the provider, for webhook signatures
	WebhookTolerance time.Duration // max webhook timestamp age

	// Per-user rate limits on the routes that create things (0 = off).
	// Each route group (orders, quotes, fund) has its own budget.
	RateLimitPerMinute int
	RateLimitBurst     int
}

type Server struct {
	svc         *service.Service
	repo        repository.Repository
	keyRepo     repository.IdempotencyRepo
	webhookRepo repository.WebhookRepo
	cfg         Config
	log         *slog.Logger

	ordersLimit, quotesLimit, fundLimit *limiter
}

func New(svc *service.Service, repo repository.Repository, cfg Config, log *slog.Logger) *Server {
	return &Server{
		svc: svc, repo: repo, cfg: cfg, log: log,
		ordersLimit: newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst),
		quotesLimit: newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst),
		fundLimit:   newLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/account", s.getAccount)
	mux.HandleFunc("POST /api/account/fund", s.rateLimited("fund", s.fundLimit, s.idempotent(s.fundAccount)))
	mux.HandleFunc("GET /api/activity", s.listActivity)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/prices", s.listPrices)
	mux.HandleFunc("POST /api/quotes", s.rateLimited("quotes", s.quotesLimit, s.createQuote))
	mux.HandleFunc("POST /api/orders", s.rateLimited("orders", s.ordersLimit, s.idempotent(s.placeOrder)))
	mux.HandleFunc("GET /api/orders", s.listOrders)
	mux.HandleFunc("GET /api/orders/{id}", s.getOrder)
	mux.HandleFunc("GET /api/orders/{id}/compare", s.compareOrder)
	mux.HandleFunc("POST /api/recon/run", s.runRecon)
	mux.HandleFunc("GET /api/recon/latest", s.latestRecon)
	mux.HandleFunc("DELETE /api/orders/{id}", s.cancelOrder)
	mux.HandleFunc("GET /api/breaks", s.listBreaks)
	mux.HandleFunc("GET /api/admin/invariants", s.checkInvariants)
	mux.HandleFunc("POST /webhooks/provider", s.providerWebhook)
	mux.HandleFunc("GET /api/admin/webhooks", s.listWebhooks)
	mux.HandleFunc("GET /api/admin/webhooks/stream", s.webhookStream)
	mux.HandleFunc("GET /api/admin/webhooks/{id}", s.getWebhook)
	mux.HandleFunc("GET /api/admin/jobs", s.listJobs)
	mux.HandleFunc("GET /api/admin/jobs/stats", s.jobStats)
	mux.HandleFunc("POST /api/admin/jobs/retry", s.retryDeadJobs)
	mux.HandleFunc("POST /api/admin/jobs/{id}/retry", s.retryJob)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	return s.recoverer(s.logRequests(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "method", r.Method, "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
