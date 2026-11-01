// Package server assembles HTTP routes and shared middleware.
package server

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/siegema/superego-api/internal/work"
)

func NewHandler(entries *work.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/entries", entries.CreateEntry)
	mux.HandleFunc("GET /api/entries", entries.ListEntries)
	mux.HandleFunc("GET /api/entries/{id}", entries.GetEntry)
	mux.HandleFunc("PUT /api/entries/{id}", entries.UpdateEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", entries.DeleteEntry)
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Work memories must not be cached by clients or intermediaries.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Reject arbitrary DNS names resolving to loopback (DNS rebinding).
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" {
			http.Error(w, "local development hosts only", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		protected.ServeHTTP(w, r.WithContext(ctx))
	})
}
