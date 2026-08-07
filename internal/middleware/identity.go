// Package middleware — caller identity resolution shared by middleware and handlers.
package middleware

import (
	"net/http"
	"strings"
)

// EffectiveUser returns the identity of the caller for authorization purposes.
//
// The token-derived identity (static API token or magic-link JWT, see Auth)
// always wins over the Remote-User header: a client that reaches the backend
// without going through the Authelia forwardAuth middleware can set
// Remote-User itself, so an identity proven by a token must never be
// overridden by that header. When no token identity is available we fall back
// to Remote-User, which UserIdentity already lowercased and defaults to
// "anonymous".
//
// Middleware and handlers must both use this helper so they can never disagree
// on who the caller is.
func EffectiveUser(r *http.Request) string {
	if user := strings.ToLower(strings.TrimSpace(GetAuthUser(r))); user != "" && user != "anonymous" {
		return user
	}
	return GetUser(r)
}
