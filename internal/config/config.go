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

// IsAdmin checks if a username is in the admin list.
func IsAdmin(username string) bool {
	for _, u := range AdminUsers() {
		if strings.EqualFold(u, username) {
			return true
		}
	}
	return false
}
