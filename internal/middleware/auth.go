// Package middleware provides HTTP middleware for the TripKit API.
package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
)

type authContextKey string

const (
	ctxUserName authContextKey = "auth-user"
	ctxUserRole authContextKey = "auth-role"
	ctxTripID   authContextKey = "auth-trip-id"
)

// jwtSecret returns the signing key from shared config.
func jwtSecret() []byte {
	return config.JWTSecret()
}

// Auth is a chi middleware that supports three auth modes:
//  1. Admin Bearer token (TRIPKIT_API_TOKEN env var) — full access
//  2. JWT Bearer token (from magic link login) — scoped to trip_id
//  3. No token env set — dev mode, everything allowed
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staticToken := os.Getenv("TRIPKIT_API_TOKEN")

		// Dev mode: no token required
		if staticToken == "" && os.Getenv("TRIPKIT_JWT_SECRET") == "" {
			ctx := context.WithValue(r.Context(), ctxUserName, "dev")
			ctx = context.WithValue(ctx, ctxUserRole, "admin")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// 3. No Bearer token but Remote-User present (Authelia forwardAuth)
			if remoteUser := r.Header.Get("Remote-User"); remoteUser != "" {
				ctx := context.WithValue(r.Context(), ctxUserName, remoteUser)
				ctx = context.WithValue(ctx, ctxUserRole, "viewer")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeUnauthorized(w, "Authorization header required")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			writeUnauthorized(w, "Invalid Authorization format (Bearer <token>)")
			return
		}
		tokenStr := parts[1]

		// 1. Try static admin token first
		if staticToken != "" && tokenStr == staticToken {
			ctx := context.WithValue(r.Context(), ctxUserName, "admin")
			ctx = context.WithValue(ctx, ctxUserRole, "admin")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Try JWT
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return jwtSecret(), nil
		})
		if err != nil || !token.Valid {
			writeUnauthorized(w, "Invalid or expired token")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeUnauthorized(w, "Invalid token claims")
			return
		}

		name, _ := claims["sub"].(string)
		role, _ := claims["role"].(string)
		tripID, _ := claims["trip_id"].(string)

		ctx := context.WithValue(r.Context(), ctxUserName, name)
		ctx = context.WithValue(ctx, ctxUserRole, role)
		ctx = context.WithValue(ctx, ctxTripID, tripID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

// GetAuthUser returns the authenticated user name from context.
func GetAuthUser(r *http.Request) string {
	if v, ok := r.Context().Value(ctxUserName).(string); ok {
		return v
	}
	return "anonymous"
}

// GetAuthRole returns the user role from context.
func GetAuthRole(r *http.Request) string {
	if v, ok := r.Context().Value(ctxUserRole).(string); ok {
		return v
	}
	return "viewer"
}

// GetAuthTripID returns the trip_id scope from JWT context.
func GetAuthTripID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxTripID).(string); ok {
		return v
	}
	return ""
}
