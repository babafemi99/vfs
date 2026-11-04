package httpapi

import "net/http"

// RegisterRoutes adds every API route to mux. Creating routes are rate
// limited per user; the ones that move money or place orders also need an
// Idempotency-Key.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	// Account
	mux.HandleFunc("GET /api/account", a.getAccount)
	mux.HandleFunc("POST /api/account/fund", a.rateLimited("fund", a.fundLimit, a.idempotent("account.fund", a.fundAccount)))
	mux.HandleFunc("GET /api/activity", a.listActivity)
	mux.HandleFunc("GET /api/stream", a.stream)

	// Market and orders
	mux.HandleFunc("GET /api/prices", a.listPrices)
	mux.HandleFunc("POST /api/quotes", a.rateLimited("quotes", a.quotesLimit, a.createQuote))
	mux.HandleFunc("POST /api/orders", a.rateLimited("orders", a.ordersLimit, a.idempotent("order.place", a.placeOrder)))
	mux.HandleFunc("GET /api/orders", a.listOrders)
	mux.HandleFunc("GET /api/orders/{id}", a.getOrder)
	mux.HandleFunc("DELETE /api/orders/{id}", a.cancelOrder)
	mux.HandleFunc("GET /api/orders/{id}/compare", a.compareOrder)

	// Reconciliation
	mux.HandleFunc("POST /api/recon/run", a.runRecon)
	mux.HandleFunc("GET /api/recon/latest", a.latestRecon)
	mux.HandleFunc("GET /api/breaks", a.listBreaks)

	// Provider webhooks
	mux.HandleFunc("POST /webhooks/provider", a.providerWebhook)

	// Operations
	mux.HandleFunc("GET /api/admin/invariants", a.checkInvariants)
	mux.HandleFunc("GET /api/admin/webhooks", a.listWebhooks)
	mux.HandleFunc("GET /api/admin/webhooks/stream", a.webhookStream)
	mux.HandleFunc("GET /api/admin/webhooks/{id}", a.getWebhook)
	mux.HandleFunc("GET /api/admin/jobs", a.listJobs)
	mux.HandleFunc("GET /api/admin/jobs/stats", a.jobStats)
	mux.HandleFunc("POST /api/admin/jobs/retry", a.retryDeadJobs)
	mux.HandleFunc("POST /api/admin/jobs/{id}/retry", a.retryJob)
}
