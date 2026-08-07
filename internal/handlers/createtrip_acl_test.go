package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// setupACLRouter mounts the exact middleware chain used in production
// (Auth -> UserIdentity -> TripACL) because CreateTrip authorization only makes
// sense inside that chain.
//
// TRIPKIT_API_TOKEN is set so middleware.Auth does not fall back to dev mode,
// where every request would get role=admin and bypass the ACL.
func setupACLRouter(t *testing.T) (*chi.Mux, *gorm.DB) {
	t.Helper()
	t.Setenv("TRIPKIT_API_TOKEN", "admin-secret")

	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	h := handlers.New(db)

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Auth)
		r.Use(middleware.UserIdentity)
		r.Use(middleware.TripACL(db))
		r.Get("/trips", h.ListTrips)
		r.Post("/trips", h.CreateTrip)
		r.Get("/trips/{tripId}", h.GetTrip)
		r.Get("/my/trips", h.MyTrips)
	})
	return r, db
}

// jsonBody marshals v into a request body reader.
func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// grantAccess gives username access to tripID through a dedicated group.
func grantAccess(t *testing.T, db *gorm.DB, groupID, username, tripID string) {
	t.Helper()
	if err := db.Create(&models.Group{ID: groupID, Name: groupID}).Error; err != nil {
		t.Fatalf("failed to create group: %v", err)
	}
	if err := db.Create(&models.GroupMember{GroupID: groupID, Username: username}).Error; err != nil {
		t.Fatalf("failed to create group member: %v", err)
	}
	if err := db.Create(&models.TripAccess{TripID: tripID, GroupID: groupID}).Error; err != nil {
		t.Fatalf("failed to create trip access: %v", err)
	}
}

func TestCreateTrip_StrictMode_ScopedUser(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	r, db := setupACLRouter(t)
	grantAccess(t, db, "nadia", "nadia", "nadia-2026")

	// Her own trip id → created.
	w := doReqAs(r, "POST", "/api/trips", map[string]any{"id": "nadia-2026", "name": "Nadia 2026"}, "nadia")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for her own trip, got %d: %s", w.Code, w.Body.String())
	}

	// Another trip id → denied.
	w = doReqAs(r, "POST", "/api/trips", map[string]any{"id": "famille-2027", "name": "Famille"}, "nadia")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for famille-2027, got %d: %s", w.Code, w.Body.String())
	}
	if got := parseResp(w)["error"]; got != "Access denied to this trip" {
		t.Errorf("unexpected error body: %v", got)
	}

	// No explicit id → denied (a random uuid would be outside her scope).
	w = doReqAs(r, "POST", "/api/trips", map[string]any{"name": "Sans id"}, "nadia")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without explicit id, got %d: %s", w.Code, w.Body.String())
	}

	// Nothing outside her scope was persisted.
	var count int64
	db.Model(&models.Trip{}).Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 trip in DB, got %d", count)
	}

	// Admin can create any trip.
	w = doReqAs(r, "POST", "/api/trips", map[string]any{"id": "famille-2027", "name": "Famille"}, "rene")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for admin rene, got %d: %s", w.Code, w.Body.String())
	}
	w = doReqAs(r, "POST", "/api/trips", map[string]any{"name": "Sans id admin"}, "rene")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for admin rene without id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateTrip_StrictMode_ListsOnlyAllowedTrips(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	r, db := setupACLRouter(t)
	grantAccess(t, db, "nadia", "nadia", "nadia-2026")
	grantAccess(t, db, "jullien", "nicole", "usa-2026")
	for _, id := range []string{"nadia-2026", "usa-2026"} {
		if err := db.Create(&models.Trip{ID: id, Name: id}).Error; err != nil {
			t.Fatalf("failed to create trip: %v", err)
		}
	}

	w := doReqAs(r, "GET", "/api/trips", nil, "nadia")
	trips := parseRespSlice(w)
	if w.Code != http.StatusOK || len(trips) != 1 {
		t.Fatalf("expected 200 with 1 trip for nadia, got %d %s", w.Code, w.Body.String())
	}
	if id := trips[0].(map[string]any)["id"]; id != "nadia-2026" {
		t.Errorf("expected nadia-2026, got %v", id)
	}

	// GET /api/my/trips is filtered the same way.
	w = doReqAs(r, "GET", "/api/my/trips", nil, "nadia")
	if trips := parseRespSlice(w); w.Code != http.StatusOK || len(trips) != 1 {
		t.Fatalf("expected 200 with 1 trip on /my/trips, got %d %s", w.Code, w.Body.String())
	}

	// A user with no group at all sees nothing (not everything).
	if trips := parseRespSlice(doReqAs(r, "GET", "/api/trips", nil, "stranger")); len(trips) != 0 {
		t.Errorf("expected no trip for stranger on /trips, got %v", trips)
	}
	if trips := parseRespSlice(doReqAs(r, "GET", "/api/my/trips", nil, "stranger")); len(trips) != 0 {
		t.Errorf("expected no trip for stranger on /my/trips, got %v", trips)
	}
	// ...and cannot read a trip directly either.
	if w := doReqAs(r, "GET", "/api/trips/usa-2026", nil, "stranger"); w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for stranger on usa-2026, got %d", w.Code)
	}
}

// TestCreateTrip_OpenMode_Unchanged is a regression test for the current
// deployment, which runs in open mode: a user with no group at all must keep
// being able to create trips exactly as before.
func TestCreateTrip_OpenMode_Unchanged(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "open")
	r, _ := setupACLRouter(t)

	if w := doReqAs(r, "POST", "/api/trips", map[string]any{"id": "libre-2026", "name": "Libre"}, "nogroup"); w.Code != http.StatusCreated {
		t.Fatalf("expected 201 in open mode with explicit id, got %d: %s", w.Code, w.Body.String())
	}
	if w := doReqAs(r, "POST", "/api/trips", map[string]any{"name": "Sans id"}, "nogroup"); w.Code != http.StatusCreated {
		t.Fatalf("expected 201 in open mode without id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateTrip_OpenMode_ScopedUserStillRestricted proves the hole is closed
// for a scoped user even without enabling strict mode.
func TestCreateTrip_OpenMode_ScopedUserStillRestricted(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "open")
	r, db := setupACLRouter(t)
	grantAccess(t, db, "nadia", "nadia", "nadia-2026")

	if w := doReqAs(r, "POST", "/api/trips", map[string]any{"id": "nadia-2026", "name": "Nadia"}, "nadia"); w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for her own trip, got %d: %s", w.Code, w.Body.String())
	}
	if w := doReqAs(r, "POST", "/api/trips", map[string]any{"id": "famille-2027", "name": "Famille"}, "nadia"); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for another trip, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateTrip_TokenIdentityNotOverriddenByHeader checks that a caller
// proving an admin identity with the static API token is not downgraded by a
// forged Remote-User header.
func TestCreateTrip_TokenIdentityNotOverriddenByHeader(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	r, db := setupACLRouter(t)
	grantAccess(t, db, "nadia", "nadia", "nadia-2026")

	req := httptest.NewRequest("POST", "/api/trips", jsonBody(map[string]any{"id": "famille-2027", "name": "Famille"}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "nadia")
	req.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the static admin token, got %d: %s", w.Code, w.Body.String())
	}
}
