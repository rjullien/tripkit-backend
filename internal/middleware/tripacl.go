// Package middleware — Trip ACL based on groups.
package middleware

import (
	"net/http"

	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// isAdmin returns true for users who bypass ACL checks.
func isAdmin(username string) bool {
	return config.IsAdmin(username)
}

// TripACL checks if the current user has access to the requested trip.
// Admin users bypass the check.
//
// In open mode (the default, see config.ACLStrict) the ACL fails open: an empty
// trip_accesses table or a trip without any access rule is reachable by
// everyone. In strict mode it fails closed: a non-admin only reaches a trip
// that one of its groups explicitly grants access to.
func TripACL(db *gorm.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Resolve the caller the same way the handlers do: token identity
			// first, Remote-User (set by UserIdentity) as fallback.
			user := EffectiveUser(r)
			strict := config.ACLStrict()

			// Admin bypass
			if isAdmin(user) || GetAuthRole(r) == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			// If no trip_access entries exist at all, open mode (no restrictions).
			// In strict mode we keep evaluating, so the request ends up denied.
			var count int64
			db.Model(&models.TripAccess{}).Count(&count)
			if count == 0 && !strict {
				next.ServeHTTP(w, r)
				return
			}

			// Get trip ID from URL
			tripID := extractTripID(r)
			if tripID == "" {
				// Not a trip-scoped request: POST /trips, GET /trips,
				// /groups/..., /my/trips, /me, /debug/trips. Each of these
				// handlers MUST perform its own authorization (CreateTrip and
				// the group handlers do, ListTrips/MyTrips filter by
				// AllowedTripIDs).
				next.ServeHTTP(w, r)
				return
			}

			// Check if trip has any access rules
			var tripAccessCount int64
			db.Model(&models.TripAccess{}).Where("trip_id = ?", tripID).Count(&tripAccessCount)
			if tripAccessCount == 0 {
				if !strict {
					// Trip has no access rules — open to all
					next.ServeHTTP(w, r)
					return
				}
				writeAccessDenied(w)
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
			writeAccessDenied(w)
		})
	}
}

// writeAccessDenied writes the standard 403 ACL response.
func writeAccessDenied(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"Access denied to this trip"}`))
}

// AllowedTripIDs returns the list of trip IDs a user can access.
//
// The nil vs empty-slice distinction is part of the contract and callers must
// respect it (getting it wrong caused the v1.12.6 regression where every trip
// became visible):
//   - nil means "no restriction, all trips visible" (admins, and open mode with
//     an empty trip_accesses table),
//   - a non-nil empty slice means "nothing visible" and callers must return an
//     empty result without running a `WHERE id IN (...)` query.
//
// Admin users always get nil. When the trip_accesses table is empty, open mode
// returns nil while strict mode returns an empty non-nil slice.
func AllowedTripIDs(db *gorm.DB, username string) []string {
	// Admin bypass — same users as TripACL middleware
	if isAdmin(username) {
		return nil
	}

	var count int64
	db.Model(&models.TripAccess{}).Count(&count)
	if count == 0 {
		if config.ACLStrict() {
			return []string{} // fail closed: nothing visible
		}
		return nil // open mode
	}

	var tripIDs []string
	db.Model(&models.TripAccess{}).
		Joins("JOIN group_members ON group_members.group_id = trip_accesses.group_id").
		Where("LOWER(group_members.username) = LOWER(?)", username).
		Pluck("trip_accesses.trip_id", &tripIDs)
	if tripIDs == nil && config.ACLStrict() {
		// A user without any group grant must see nothing, not everything.
		return []string{}
	}
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
