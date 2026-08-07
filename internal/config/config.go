// Package config provides shared configuration helpers.
package config

import (
	"log"
	"os"
	"strings"
)

// JWTSecret returns the JWT signing key from TRIPKIT_JWT_SECRET.
// Panics in production (TRIPKIT_ENV != "dev") if the env var is not set.
func JWTSecret() []byte {
	s := os.Getenv("TRIPKIT_JWT_SECRET")
	if s == "" {
		env := os.Getenv("TRIPKIT_ENV")
		if env != "" && env != "dev" {
			log.Fatal("FATAL: TRIPKIT_JWT_SECRET is not set — refusing to start in non-dev mode")
		}
		log.Println("WARNING: TRIPKIT_JWT_SECRET not set — using insecure dev fallback. DO NOT use in production.")
		s = "tripkit-dev-secret-change-me"
	}
	return []byte(s)
}

// AdminUsers returns the list of admin usernames from TRIPKIT_ADMIN_USERS env var.
// Defaults to ["admin", "rene"] if not set.
func AdminUsers() []string {
	s := os.Getenv("TRIPKIT_ADMIN_USERS")
	if s == "" {
		return []string{"admin", "rene"}
	}
	parts := strings.Split(s, ",")
	users := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			users = append(users, p)
		}
	}
	return users
}

// ACLStrict reports whether the group-based trip ACL must fail closed.
//
// In strict mode a non-admin identity only reaches a trip when one of its
// groups explicitly grants access to it: an empty trip_accesses table or a
// trip without any access rule no longer opens the data to everyone.
//
// It is driven by TRIPKIT_ACL_MODE ("strict" or "open"). When that variable is
// not set it falls back to TRIPKIT_ENV: any non-empty value other than "dev"
// means strict. With nothing configured at all the legacy open behaviour is
// kept, so existing deployments and local dev are unaffected.
func ACLStrict() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRIPKIT_ACL_MODE"))) {
	case "strict":
		return true
	case "open":
		return false
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("TRIPKIT_ENV")))
	return env != "" && env != "dev"
}

// minServiceTokenLen is the minimum length accepted for a service token.
// `openssl rand -hex 32` produces 64 characters, well above this floor.
const minServiceTokenLen = 16

// ServiceTokens returns the configured machine credentials as a
// username -> token map, parsed from TRIPKIT_SERVICE_TOKENS.
//
// Format: "user1:token1,user2:token2". Entries are separated by ',' and each
// entry is split on its FIRST ':' only, so a token may itself contain ':'.
// Usernames are trimmed and lowercased.
//
// An entry is skipped with a warning when it is malformed, when its token is
// shorter than minServiceTokenLen characters, or when its username is an admin
// (see IsAdmin): a service token authenticates as an ordinary non-admin user
// subject to the group ACL and must never be able to authenticate as an admin.
func ServiceTokens() map[string]string {
	raw := os.Getenv("TRIPKIT_SERVICE_TOKENS")
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	tokens := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, token, found := strings.Cut(entry, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		token = strings.TrimSpace(token)
		if !found || name == "" || token == "" {
			log.Println("WARNING: TRIPKIT_SERVICE_TOKENS: ignoring malformed entry (expected \"username:token\")")
			continue
		}
		if len(token) < minServiceTokenLen {
			log.Printf("WARNING: TRIPKIT_SERVICE_TOKENS: ignoring service token for %q — shorter than %d characters", name, minServiceTokenLen)
			continue
		}
		if IsAdmin(name) {
			log.Printf("WARNING: TRIPKIT_SERVICE_TOKENS: ignoring service token for %q — a service token must not authenticate as an admin", name)
			continue
		}
		tokens[name] = token
	}
	return tokens
}

// IsAdmin checks if a username is in the admin list.
func IsAdmin(username string) bool {
	for _, u := range AdminUsers() {
		if strings.EqualFold(u, username) {
			return true
		}
	}
	return false
}
