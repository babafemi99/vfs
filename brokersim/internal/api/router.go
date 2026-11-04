// Package api is the simulator's HTTP surface: the public /v1 provider API
// and the /dev control API.
package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
	"github.com/babafem99/vfs/lab/brokersim/internal/engine"
	"github.com/babafem99/vfs/lab/brokersim/internal/market"
	"github.com/babafem99/vfs/lab/brokersim/internal/store"
	"github.com/babafem99/vfs/lab/brokersim/internal/webhook"
)

type Deps struct {
	Store      *store.Store
	Market     *market.Market
	Engine     *engine.Engine      // used by /dev only
	Dispatcher *webhook.Dispatcher // used by /dev only
	APIKey     string
	Log        *slog.Logger

	// DevTrusted may call /dev besides loopback clients (SIM_DEV_TRUSTED_CIDRS).
	DevTrusted []netip.Prefix
}

type Server struct{ Deps }

// NewRouter returns the full handler:
//   - /v1/*  requires X-Api-Key
//   - /dev/* needs no key but only answers loopback clients (and DevTrusted networks)
func NewRouter(d Deps) http.Handler {
	s := &Server{d}

	v1 := http.NewServeMux()
	v1.HandleFunc("POST /v1/quotes", s.createQuote)
	v1.HandleFunc("GET /v1/quotes/{id}", s.getQuote)
	v1.HandleFunc("POST /v1/quotes/{id}/accept", s.acceptQuote)
	v1.HandleFunc("GET /v1/orders", s.listOrders)
	v1.HandleFunc("GET /v1/orders/{id}", s.getOrder)
	v1.HandleFunc("DELETE /v1/orders/{id}", s.cancelOrder)
	v1.HandleFunc("GET /v1/prices", s.listPrices)
	v1.HandleFunc("GET /v1/prices/{symbol}", s.getPrice)
	v1.HandleFunc("/", s.notFound)

	dev := http.NewServeMux()
	dev.HandleFunc("GET /dev/prices", s.devPrices)
	dev.HandleFunc("GET /dev/orders", s.devListOrders)
	dev.HandleFunc("POST /dev/orders/{id}/fill", s.devFill)
	dev.HandleFunc("POST /dev/orders/{id}/reject", s.devReject)
	dev.HandleFunc("POST /dev/orders/{id}/expire", s.devExpire)
	dev.HandleFunc("POST /dev/orders/{id}/cancel", s.devCancel)
	dev.HandleFunc("POST /dev/orders/{id}/cancel/confirm", s.devConfirmCancel)
	dev.HandleFunc("POST /dev/orders/{id}/cancel/reject", s.devRejectCancel)
	dev.HandleFunc("GET /dev/engine", s.devGetEngine)
	dev.HandleFunc("PUT /dev/engine", s.devPutEngine)
	dev.HandleFunc("GET /dev/deliveries", s.devDeliveries)
	dev.HandleFunc("POST /dev/deliveries/{event_id}/redeliver", s.devRedeliver)
	dev.HandleFunc("POST /dev/reset", s.devReset)
	dev.HandleFunc("/", s.notFound)

	root := http.NewServeMux()
	root.Handle("/v1/", s.requireAPIKey(v1))
	root.Handle("/dev/", s.loopbackOnly(dev))
	root.HandleFunc("/", s.notFound)
	return s.logRequests(root)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, s.Log, domain.Errorf(domain.CodeNotFound, "no route %s %s", r.Method, r.URL.Path), nil)
}

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Key")), []byte(s.APIKey)) != 1 {
			writeError(w, s.Log, &domain.Error{Code: codeUnauthorized, Message: "missing or invalid X-Api-Key"}, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.devAllowed(r.RemoteAddr) {
			writeError(w, s.Log, &domain.Error{Code: codeForbidden, Message: "/dev is only available from localhost"}, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// devAllowed: loopback, or an address in a trusted network.
func (s *Server) devAllowed(remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := ap.Addr().Unmap()
	if ip.IsLoopback() {
		return true
	}
	for _, p := range s.DevTrusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start))
	})
}
