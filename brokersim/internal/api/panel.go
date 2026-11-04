package api

import (
	_ "embed"
	"net/http"
)

// panel.html is the provider console: a single page (plain HTML and JS, no
// build step) that drives the /dev API. Served at GET /dev, loopback only.
//
//go:embed panel.html
var panelHTML []byte

func (s *Server) devPanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(panelHTML)
}

// devPrices lists market prices for the panel without needing the API key.
func (s *Server) devPrices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prices": s.Market.Prices()})
}
