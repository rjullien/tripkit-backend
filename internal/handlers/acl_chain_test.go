package handlers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

const (
	// nadiaToken is the machine credential handed to the external contributor's
	// CI (>= 16 chars, like `openssl rand -hex 32`).
	nadiaToken = "nadia-ci-token-0123456789abcdef"
	// adminToken is the legacy full-access credential (TRIPKIT_API_TOKEN).
	adminToken = "static-admin-token-for-tests"
)

// setupChainRouter mounts the exact production chain and route table of
// cmd/api/main.go over an in-memory DB, in strict ACL mode, with a service
// token for the non-admin user "nadia".
//
// It seeds the family trip usa-2026 (group jullien, member nicole) and grants
// nadia access to nadia-2026 without creating that trip, so the importer has to
// create it itself, exactly like a first seed run.
func setupChainRouter(t *testing.T) (*chi.Mux, *gorm.DB) {
	t.Helper()
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	t.Setenv("TRIPKIT_ADMIN_USERS", "admin,rene")
	t.Setenv("TRIPKIT_JWT_SECRET", "test-jwt-secret")
	t.Setenv("TRIPKIT_API_TOKEN", adminToken)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:"+nadiaToken)

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

		r.Get("/me", h.Me)
		r.Get("/my/trips", h.MyTrips)

		r.Get("/groups", h.ListGroups)
		r.Put("/groups/{groupId}", h.UpsertGroup)
		r.Delete("/groups/{groupId}", h.DeleteGroup)

		r.Get("/trips", h.ListTrips)
		r.Get("/debug/trips", h.DebugListTrips)
		r.Post("/trips", h.CreateTrip)
		r.Get("/trips/{tripId}", h.GetTrip)
		r.Put("/trips/{tripId}", h.UpdateTrip)
		r.Delete("/trips/{tripId}", h.DeleteTrip)
		r.Get("/trips/{tripId}/seed", h.SeedTrip)
		r.Get("/trips/{tripId}/version", h.TripVersion)

		r.Get("/trips/{tripId}/days", h.ListDays)
		r.Get("/trips/{tripId}/days/{dayNum}", h.GetDay)
		r.Put("/trips/{tripId}/days/{dayNum}", h.UpsertDay)
		r.Delete("/trips/{tripId}/days/{dayNum}", h.DeleteDay)

		r.Get("/trips/{tripId}/hotels", h.ListHotels)
		r.Put("/trips/{tripId}/hotels/{dayNum}", h.UpsertHotel)
		r.Delete("/trips/{tripId}/hotels/{dayNum}", h.DeleteHotel)

		r.Get("/trips/{tripId}/lists", h.ListLists)
		r.Get("/trips/{tripId}/lists/{listId}", h.GetList)
		r.Put("/trips/{tripId}/lists/{listId}", h.UpsertList)
		r.Delete("/trips/{tripId}/lists/{listId}", h.DeleteList)
		r.Patch("/trips/{tripId}/lists/{listId}/sync", h.SyncList)

		r.Get("/trips/{tripId}/assets", h.ListAssets)
		r.Get("/trips/{tripId}/assets/{filename}", h.GetAsset)
		r.Put("/trips/{tripId}/assets/{filename}", h.UploadAsset)
		r.Delete("/trips/{tripId}/assets/{filename}", h.DeleteAsset)
	})

	grantAccess(t, db, "jullien", "nicole", "usa-2026")
	grantAccess(t, db, "nadia", "nadia", "nadia-2026")
	if err := db.Create(&models.Trip{ID: "usa-2026", Name: "USA 2026"}).Error; err != nil {
		t.Fatalf("failed to create trip usa-2026: %v", err)
	}
	return r, db
}

// doChain sends a request through the production chain. An empty token or
// remoteUser omits the corresponding header. A body of type io.Reader is sent
// as-is (asset upload), anything else is JSON-encoded.
func doChain(r *chi.Mux, method, url string, body any, token, remoteUser string) *httptest.ResponseRecorder {
	var reqBody io.Reader
	switch v := body.(type) {
	case nil:
	case io.Reader:
		reqBody = v
	default:
		reqBody = jsonBody(v)
	}
	req := httptest.NewRequest(method, url, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if remoteUser != "" {
		req.Header.Set("Remote-User", remoteUser)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// tripIDsOf extracts the trip ids of a JSON array response.
func tripIDsOf(w *httptest.ResponseRecorder) []string {
	ids := make([]string, 0)
	for _, item := range parseRespSlice(w) {
		if m, ok := item.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// TestACLChain_ServiceTokenRunsFullSeedImport walks the exact sequence of
// tripkit-frontend/seed-import.cjs with a scoped service token and no
// Remote-User header, which is what a CI job sends.
func TestACLChain_ServiceTokenRunsFullSeedImport(t *testing.T) {
	r, _ := setupChainRouter(t)

	// 1. GET the trip: allowed (the id is in her scope), 404 because it does
	// not exist yet — this is how the importer decides between POST and PUT.
	if w := doChain(r, "GET", "/api/trips/nadia-2026", nil, nadiaToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on her not-yet-created trip, got %d: %s", w.Code, w.Body.String())
	}

	// 2. POST /trips creates it.
	w := doChain(r, "POST", "/api/trips", map[string]any{"id": "nadia-2026", "name": "Nadia 2026"}, nadiaToken, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating her own trip, got %d: %s", w.Code, w.Body.String())
	}

	// 3. PUT /trips/{id} on a later run.
	if w := doChain(r, "PUT", "/api/trips/nadia-2026", map[string]any{"name": "Nadia 2026 v2"}, nadiaToken, ""); w.Code != http.StatusOK {
		t.Fatalf("expected 200 updating her own trip, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Days, hotels, lists and assets.
	cases := []struct {
		method string
		url    string
		body   any
	}{
		{"PUT", "/api/trips/nadia-2026/days/0", map[string]any{"title": "Jour 1"}},
		{"PUT", "/api/trips/nadia-2026/hotels/0", map[string]any{"name": "Hotel"}},
		{"PUT", "/api/trips/nadia-2026/lists/packing", map[string]any{"type": "packing", "title": "Valise"}},
		{"PUT", "/api/trips/nadia-2026/assets/map.png", strings.NewReader("fake-png-bytes")},
	}
	for _, c := range cases {
		if w := doChain(r, c.method, c.url, c.body, nadiaToken, ""); w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d: %s", c.method, c.url, w.Code, w.Body.String())
		}
	}

	// The whole sequence ran without ever seeing an admin role.
	if w := doChain(r, "GET", "/api/groups", nil, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected the importer to stay non-admin, got %d on /api/groups", w.Code)
	}
}

// TestACLChain_ServiceTokenDeniedOnOtherTrips is the core isolation guarantee:
// the contributor's token must not reach the family trip on any route.
func TestACLChain_ServiceTokenDeniedOnOtherTrips(t *testing.T) {
	r, _ := setupChainRouter(t)

	cases := []struct {
		method string
		url    string
		body   any
	}{
		{"GET", "/api/trips/usa-2026", nil},
		{"PUT", "/api/trips/usa-2026", map[string]any{"name": "hacked"}},
		{"DELETE", "/api/trips/usa-2026", nil},
		{"GET", "/api/trips/usa-2026/seed", nil},
		{"GET", "/api/trips/usa-2026/days", nil},
		{"GET", "/api/trips/usa-2026/days/0", nil},
		{"PUT", "/api/trips/usa-2026/days/0", map[string]any{"title": "hacked"}},
		{"GET", "/api/trips/usa-2026/hotels", nil},
		{"PUT", "/api/trips/usa-2026/hotels/0", map[string]any{"name": "hacked"}},
		{"GET", "/api/trips/usa-2026/lists", nil},
		{"PUT", "/api/trips/usa-2026/lists/packing", map[string]any{"type": "packing", "title": "hacked"}},
		{"GET", "/api/trips/usa-2026/assets", nil},
		{"GET", "/api/trips/usa-2026/assets/map.png", nil},
		{"PUT", "/api/trips/usa-2026/assets/map.png", strings.NewReader("fake-png-bytes")},
	}
	for _, c := range cases {
		w := doChain(r, c.method, c.url, c.body, nadiaToken, "")
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d: %s", c.method, c.url, w.Code, w.Body.String())
			continue
		}
		if got := parseResp(w)["error"]; got != "Access denied to this trip" {
			t.Errorf("%s %s: unexpected error body: %v", c.method, c.url, got)
		}
	}

	// And she cannot create a trip outside her scope either.
	if w := doChain(r, "POST", "/api/trips", map[string]any{"id": "usa-2027", "name": "USA 2027"}, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected 403 creating usa-2027, got %d: %s", w.Code, w.Body.String())
	}
}

func TestACLChain_ServiceTokenListsOnlyItsOwnTrips(t *testing.T) {
	r, db := setupChainRouter(t)
	if err := db.Create(&models.Trip{ID: "nadia-2026", Name: "Nadia 2026"}).Error; err != nil {
		t.Fatalf("failed to create trip nadia-2026: %v", err)
	}

	for _, url := range []string{"/api/trips", "/api/my/trips"} {
		w := doChain(r, "GET", url, nil, nadiaToken, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", url, w.Code, w.Body.String())
		}
		ids := tripIDsOf(w)
		if len(ids) != 1 || ids[0] != "nadia-2026" {
			t.Errorf("GET %s: expected only nadia-2026, got %v", url, ids)
		}
	}
}

// TestACLChain_ServiceTokenCannotManageGroups: a scoped token must not be able
// to widen its own scope.
func TestACLChain_ServiceTokenCannotManageGroups(t *testing.T) {
	r, db := setupChainRouter(t)

	if w := doChain(r, "GET", "/api/groups", nil, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected 403 on GET /api/groups, got %d: %s", w.Code, w.Body.String())
	}
	body := map[string]any{"name": "jullien", "members": []string{"nadia"}, "trips": []string{"usa-2026"}}
	if w := doChain(r, "PUT", "/api/groups/jullien", body, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected 403 on PUT /api/groups/jullien, got %d: %s", w.Code, w.Body.String())
	}
	if w := doChain(r, "DELETE", "/api/groups/jullien", nil, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected 403 on DELETE /api/groups/jullien, got %d: %s", w.Code, w.Body.String())
	}

	// The attempted self-grant changed nothing.
	var count int64
	db.Model(&models.GroupMember{}).Where("group_id = ? AND username = ?", "jullien", "nadia").Count(&count)
	if count != 0 {
		t.Errorf("expected nadia not to be a member of jullien, got %d rows", count)
	}
	if w := doChain(r, "GET", "/api/trips/usa-2026", nil, nadiaToken, ""); w.Code != http.StatusForbidden {
		t.Errorf("expected usa-2026 to stay out of reach, got %d", w.Code)
	}
}

// TestACLChain_ServiceTokenWithForgedRemoteUser: a client that does not go
// through Authelia can set Remote-User itself, so the token identity must win.
func TestACLChain_ServiceTokenWithForgedRemoteUser(t *testing.T) {
	r, db := setupChainRouter(t)
	if err := db.Create(&models.Trip{ID: "nadia-2026", Name: "Nadia 2026"}).Error; err != nil {
		t.Fatalf("failed to create trip nadia-2026: %v", err)
	}

	for _, forged := range []string{"rene", "nicole", "admin"} {
		if w := doChain(r, "GET", "/api/trips/usa-2026", nil, nadiaToken, forged); w.Code != http.StatusForbidden {
			t.Errorf("Remote-User %q: expected 403 on usa-2026, got %d: %s", forged, w.Code, w.Body.String())
		}
		if ids := tripIDsOf(doChain(r, "GET", "/api/trips", nil, nadiaToken, forged)); len(ids) != 1 || ids[0] != "nadia-2026" {
			t.Errorf("Remote-User %q: expected only nadia-2026 in GET /api/trips, got %v", forged, ids)
		}
		if w := doChain(r, "POST", "/api/trips", map[string]any{"id": "usa-2027", "name": "USA"}, nadiaToken, forged); w.Code != http.StatusForbidden {
			t.Errorf("Remote-User %q: expected 403 creating usa-2027, got %d", forged, w.Code)
		}
	}
}

// TestACLChain_AutheliaUserStillScoped checks the human path (forwardAuth
// injects Remote-User, no Authorization header) is unaffected.
func TestACLChain_AutheliaUserStillScoped(t *testing.T) {
	r, db := setupChainRouter(t)
	if err := db.Create(&models.Trip{ID: "nadia-2026", Name: "Nadia 2026"}).Error; err != nil {
		t.Fatalf("failed to create trip nadia-2026: %v", err)
	}

	if w := doChain(r, "GET", "/api/trips/usa-2026", nil, "", "nicole"); w.Code != http.StatusOK {
		t.Errorf("expected nicole to read usa-2026, got %d: %s", w.Code, w.Body.String())
	}
	if w := doChain(r, "GET", "/api/trips/nadia-2026", nil, "", "nicole"); w.Code != http.StatusForbidden {
		t.Errorf("expected nicole to be denied on nadia-2026, got %d: %s", w.Code, w.Body.String())
	}
	// Neither credential nor header: unauthenticated.
	if w := doChain(r, "GET", "/api/trips", nil, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without any identity, got %d: %s", w.Code, w.Body.String())
	}
}

// TestACLChain_StaticAdminTokenUnrestricted keeps the operator credential
// working: it is what the admin uses to create the groups and trip accesses.
func TestACLChain_StaticAdminTokenUnrestricted(t *testing.T) {
	r, _ := setupChainRouter(t)

	if w := doChain(r, "GET", "/api/trips/usa-2026", nil, adminToken, ""); w.Code != http.StatusOK {
		t.Errorf("expected the admin token to read usa-2026, got %d: %s", w.Code, w.Body.String())
	}
	if w := doChain(r, "POST", "/api/trips", map[string]any{"id": "brand-new-2030", "name": "New"}, adminToken, ""); w.Code != http.StatusCreated {
		t.Errorf("expected the admin token to create any trip, got %d: %s", w.Code, w.Body.String())
	}
	if w := doChain(r, "GET", "/api/groups", nil, adminToken, ""); w.Code != http.StatusOK {
		t.Errorf("expected the admin token to list groups, got %d: %s", w.Code, w.Body.String())
	}
	// Onboarding a contributor: create her group and her trip access.
	body := map[string]any{"name": "nadia", "members": []string{"nadia"}, "trips": []string{"nadia-2026"}}
	if w := doChain(r, "PUT", "/api/groups/nadia", body, adminToken, ""); w.Code != http.StatusOK {
		t.Errorf("expected the admin token to upsert a group, got %d: %s", w.Code, w.Body.String())
	}
}
