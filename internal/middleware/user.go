// Package middleware provides HTTP middleware for the TripKit API.
package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const userContextKey contextKey = "remote-user"

// UserIdentity extracts the Remote-User header (set by Authelia) and stores
// it in the request context. If TRIPKIT_REQUIRE_USER=true and the header
// is missing, it returns 401.
func UserIdentity(next http.Handler) http.Handler {
	requireUser := os.Getenv("TRIPKIT_REQUIRE_USER") == "true"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := strings.ToLower(r.Header.Get("Remote-User"))
		if user == "" {
			if requireUser {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"Remote-User header required"}`))
				return
			}
			user = "anonymous"
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUser returns the authenticated username from the request context.
// Returns "anonymous" if not set.
func GetUser(r *http.Request) string {
	if user, ok := r.Context().Value(userContextKey).(string); ok {
		return user
	}
	return "anonymous"
}
