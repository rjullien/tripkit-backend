// Package middleware provides HTTP middleware for the TripKit API.
package middleware

import (
	"context"
	"crypto/subtle"
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

// Roles stored in the request context by Auth and read with GetAuthRole.
const (
	// RoleAdmin bypasses the trip ACL entirely.
	RoleAdmin = "admin"
	// RoleViewer is an ordinary user identity subject to the trip ACL.
	RoleViewer = "viewer"
	// RoleService is a machine identity (TRIPKIT_SERVICE_TOKENS) subject to the
	// trip ACL exactly like a human user: it grants no admin right.
	RoleService = "service"
)

// jwtSecret returns the signing key from shared config.
func jwtSecret() []byte {
	return config.JWTSecret()
}

// Auth is a chi middleware that supports four auth modes:
//  1. Admin Bearer token (TRIPKIT_API_TOKEN env var) — full access
//  2. Service Bearer token (TRIPKIT_SERVICE_TOKENS env var) — authenticates as
//     the configured non-admin username, subject to the group ACL
//  3. JWT Bearer token (from magic link login) — scoped to trip_id
//  4. No Bearer token but Remote-User present (Authelia forwardAuth) — viewer
//
// When no credential at all is configured it falls back to dev mode
// (user=dev, role=admin for everyone). That bypass is refused in strict ACL
// mode (see config.ACLStrict), where it would silently disable the ACL.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staticToken := os.Getenv("TRIPKIT_API_TOKEN")
		serviceTokens := config.ServiceTokens()

		// Dev mode: no credential configured at all
		if staticToken == "" && os.Getenv("TRIPKIT_JWT_SECRET") == "" && len(serviceTokens) == 0 {
			if !config.ACLStrict() {
				ctx := context.WithValue(r.Context(), ctxUserName, "dev")
				ctx = context.WithValue(ctx, ctxUserRole, RoleAdmin)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// Strict mode: never hand out admin rights just because nothing is
			// configured. Authelia forwardAuth keeps working as a viewer.
			if remoteUser := r.Header.Get("Remote-User"); remoteUser != "" {
				ctx := context.WithValue(r.Context(), ctxUserName, remoteUser)
				ctx = context.WithValue(ctx, ctxUserRole, RoleViewer)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeUnauthorized(w, "Dev mode disabled in strict ACL mode: set TRIPKIT_JWT_SECRET, TRIPKIT_API_TOKEN or TRIPKIT_SERVICE_TOKENS")
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// 4. No Bearer token but Remote-User present (Authelia forwardAuth)
			if remoteUser := r.Header.Get("Remote-User"); remoteUser != "" {
				ctx := context.WithValue(r.Context(), ctxUserName, remoteUser)
				ctx = context.WithValue(ctx, ctxUserRole, RoleViewer)
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
		if staticToken != "" && subtle.ConstantTimeCompare([]byte(tokenStr), []byte(staticToken)) == 1 {
			ctx := context.WithValue(r.Context(), ctxUserName, "admin")
			ctx = context.WithValue(ctx, ctxUserRole, RoleAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Try the named service tokens (automated importers, CI jobs).
		// They authenticate as a plain non-admin user, so the group ACL applies.
		if user, ok := matchServiceToken(serviceTokens, tokenStr); ok {
			ctx := context.WithValue(r.Context(), ctxUserName, user)
			ctx = context.WithValue(ctx, ctxUserRole, RoleService)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 3. Try JWT
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

// matchServiceToken returns the username owning the presented token.
// Every configured entry is compared in constant time and the loop never exits
// early, so neither the value nor the position of a token leaks through timing.
func matchServiceToken(tokens map[string]string, presented string) (string, bool) {
	user := ""
	found := false
	for name, token := range tokens {
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1 {
			user, found = name, true
		}
	}
	return user, found
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
