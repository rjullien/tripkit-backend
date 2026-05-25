// Package middleware — Trip ACL based on groups.
package middleware

import (
	"net/http"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// isAdmin returns true for users who bypass ACL checks.
func isAdmin(username string) bool {
	return username == "admin" || username == "rene"
}

// TripACL checks if the current user has access to the requested trip.
// Admin users bypass the check. If no groups/access are configured, all trips are visible (open mode).
func TripACL(db *gorm.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user from Authelia header (set by UserIdentity middleware)
			user := GetUser(r)

			// Admin bypass
			if isAdmin(user) || GetAuthRole(r) == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			// If no trip_access entries exist at all, open mode (no restrictions)
			var count int64
			db.Model(&models.TripAccess{}).Count(&count)
			if count == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Get trip ID from URL
			tripID := extractTripID(r)
			if tripID == "" {
				// Not a trip-scoped request (e.g. /api/trips list) — handle in handler
				next.ServeHTTP(w, r)
				return
			}

			// Check if trip has any access rules
			var tripAccessCount int64
			db.Model(&models.TripAccess{}).Where("trip_id = ?", tripID).Count(&tripAccessCount)
			if tripAccessCount == 0 {
				// Trip has no access rules — open to all
				next.ServeHTTP(w, r)
				return
			}

			// Check if user is in a group that has access to this trip
			var accessCount int64
			db.Model(&models.TripAccess{}).
				Joins("JOIN group_members ON group_members.group_id = trip_accesses.group_id").
				Where("trip_accesses.trip_id = ? AND LOWER(group_members.username) = LOWER(?)", tripID, user).
				Count(&accessCount)

			if accessCount > 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Denied
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"Access denied to this trip"}`))
		})
	}
}

// AllowedTripIDs returns the list of trip IDs a user can access.
// If no access rules exist, returns nil (meaning all trips visible).
// Admin users always get nil (all trips visible).
func AllowedTripIDs(db *gorm.DB, username string) []string {
	// Admin bypass — same users as TripACL middleware
	if isAdmin(username) {
		return nil
	}

	var count int64
	db.Model(&models.TripAccess{}).Count(&count)
	if count == 0 {
		return nil // open mode
	}

	var tripIDs []string
	db.Model(&models.TripAccess{}).
		Joins("JOIN group_members ON group_members.group_id = trip_accesses.group_id").
		Where("LOWER(group_members.username) = LOWER(?)", username).
		Pluck("trip_accesses.trip_id", &tripIDs)
	return tripIDs
}

// extractTripID pulls tripId from chi URL params or path.
func extractTripID(r *http.Request) string {
	// chi stores route params in context — we need to import chi
	// But to avoid circular deps, parse from URL path
	// Path format: /api/trips/{tripId}/...
	parts := splitPath(r.URL.Path)
	for i, p := range parts {
		if p == "trips" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range splitOn(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitOn(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}
