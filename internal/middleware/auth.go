// Package middleware provides HTTP middleware for the TripKit API.
package middleware

import (
	"net/http"
	"os"
	"strings"
)

// Auth is a chi middleware that checks for a valid Bearer token.
// If TRIPKIT_API_TOKEN is not set, all requests pass (dev mode).
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("TRIPKIT_API_TOKEN")
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"Authorization header required"}`))
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] != token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"Invalid token"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}
