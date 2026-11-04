package api

import "net/http"

// devPrices lists market prices for the provider console without needing
// the API key (the console reaches /dev through the web app's proxy).
func (s *Server) devPrices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prices": s.Market.Prices()})
}
