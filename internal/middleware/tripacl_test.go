package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// aclFixture holds the router under test plus the DB it was built on.
type aclFixture struct {
	router *chi.Mux
	db     *gorm.DB
	// reached is set by the terminal handler when the ACL let the request pass.
	reached *bool
}

// newACLFixture builds the real middleware chain (UserIdentity -> TripACL) over
// the same trip routes as cmd/api/main.go, with a terminal handler recording
// that it was reached.
func newACLFixture(t *testing.T, db *gorm.DB) *aclFixture {
	t.Helper()
	reached := false
	terminal := func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(UserIdentity)
		r.Use(TripACL(db))
		r.Get("/my/trips", terminal)
		r.Get("/groups", terminal)
		r.Put("/groups/{groupId}", terminal)
		r.Get("/trips", terminal)
		r.Post("/trips", terminal)
		r.Get("/trips/{tripId}", terminal)
		r.Put("/trips/{tripId}", terminal)
		r.Delete("/trips/{tripId}", terminal)
		r.Get("/trips/{tripId}/days/{dayNum}", terminal)
		r.Put("/trips/{tripId}/days/{dayNum}", terminal)
		r.Put("/trips/{tripId}/hotels/{dayNum}", terminal)
		r.Put("/trips/{tripId}/lists/{listId}", terminal)
		r.Put("/trips/{tripId}/assets/{filename}", terminal)
	})
	return &aclFixture{router: r, db: db, reached: &reached}
}

// do issues a request with the given Remote-User header and reports the status
// code and whether the terminal handler was reached.
func (f *aclFixture) do(method, url, user string) (int, bool) {
	*f.reached = false
	req := httptest.NewRequest(method, url, nil)
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w.Code, *f.reached
}

// doAsAdminRole issues a request whose auth context carries role=admin, as
// middleware.Auth does in dev mode or for the static API token.
func (f *aclFixture) doAsAdminRole(method, url, user string) (int, bool) {
	*f.reached = false
	req := httptest.NewRequest(method, url, nil)
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	ctx := context.WithValue(req.Context(), ctxUserRole, "admin")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req.WithContext(ctx))
	return w.Code, *f.reached
}

// seedACLDB creates the trips, groups and access rules used by the tests:
// group jullien (nicole) -> usa-2026, group nadia (nadia) -> nadia-2026,
// plus orphan-2026 which has no trip_accesses row at all.
func seedACLDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	for _, id := range []string{"usa-2026", "nadia-2026", "orphan-2026"} {
		if err := db.Create(&models.Trip{ID: id, Name: id}).Error; err != nil {
			t.Fatalf("failed to create trip %s: %v", id, err)
		}
	}
	groups := []struct {
		id, member, trip string
	}{
		{"jullien", "nicole", "usa-2026"},
		{"nadia", "nadia", "nadia-2026"},
	}
	for _, g := range groups {
		if err := db.Create(&models.Group{ID: g.id, Name: g.id}).Error; err != nil {
			t.Fatalf("failed to create group %s: %v", g.id, err)
		}
		if err := db.Create(&models.GroupMember{GroupID: g.id, Username: g.member}).Error; err != nil {
			t.Fatalf("failed to create member %s: %v", g.member, err)
		}
		if err := db.Create(&models.TripAccess{TripID: g.trip, GroupID: g.id}).Error; err != nil {
			t.Fatalf("failed to create access %s: %v", g.trip, err)
		}
	}
	return db
}

// emptyACLDB creates trips but no group / access rule at all.
func emptyACLDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	for _, id := range []string{"usa-2026", "nadia-2026"} {
		if err := db.Create(&models.Trip{ID: id, Name: id}).Error; err != nil {
			t.Fatalf("failed to create trip %s: %v", id, err)
		}
	}
	return db
}

// ─── Scoped access ───────────────────────────────────────────────────────────

func TestTripACL_ScopedUserReachesOwnTrip(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	f := newACLFixture(t, seedACLDB(t))

	cases := []struct{ method, url string }{
		{"GET", "/api/trips/usa-2026"},
		{"PUT", "/api/trips/usa-2026"},
		{"GET", "/api/trips/usa-2026/days/3"},
		{"PUT", "/api/trips/usa-2026/days/3"},
	}
	for _, c := range cases {
		code, reached := f.do(c.method, c.url, "nicole")
		if code != http.StatusOK || !reached {
			t.Errorf("%s %s as nicole: expected 200 and handler reached, got %d reached=%v", c.method, c.url, code, reached)
		}
	}
}

func TestTripACL_ScopedUserDeniedOnOtherTrip(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	f := newACLFixture(t, seedACLDB(t))

	cases := []struct{ method, url, user string }{
		{"GET", "/api/trips/nadia-2026", "nicole"},
		{"PUT", "/api/trips/nadia-2026", "nicole"},
		{"DELETE", "/api/trips/nadia-2026", "nicole"},
		{"PUT", "/api/trips/nadia-2026/days/0", "nicole"},
		{"PUT", "/api/trips/nadia-2026/hotels/0", "nicole"},
		{"PUT", "/api/trips/nadia-2026/lists/packing", "nicole"},
		{"PUT", "/api/trips/nadia-2026/assets/map.png", "nicole"},
		{"GET", "/api/trips/usa-2026", "nadia"},
		{"PUT", "/api/trips/usa-2026", "nadia"},
		{"PUT", "/api/trips/usa-2026/days/0", "nadia"},
	}
	for _, c := range cases {
		code, reached := f.do(c.method, c.url, c.user)
		if code != http.StatusForbidden || reached {
			t.Errorf("%s %s as %s: expected 403 and handler not reached, got %d reached=%v", c.method, c.url, c.user, code, reached)
		}
	}
}

func TestTripACL_ScopedUserDeniedIsSameInOpenMode(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "open")
	f := newACLFixture(t, seedACLDB(t))

	if code, reached := f.do("GET", "/api/trips/nadia-2026", "nicole"); code != http.StatusForbidden || reached {
		t.Errorf("expected 403 for nicole on nadia-2026 in open mode, got %d reached=%v", code, reached)
	}
	if code, reached := f.do("GET", "/api/trips/usa-2026", "nicole"); code != http.StatusOK || !reached {
		t.Errorf("expected 200 for nicole on usa-2026 in open mode, got %d reached=%v", code, reached)
	}
}

// ─── Admin bypass ────────────────────────────────────────────────────────────

func TestTripACL_AdminBypass(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	f := newACLFixture(t, seedACLDB(t))

	// Hardcoded admin username (config default admin list).
	for _, url := range []string{"/api/trips/usa-2026", "/api/trips/nadia-2026", "/api/trips/orphan-2026"} {
		if code, reached := f.do("GET", url, "rene"); code != http.StatusOK || !reached {
			t.Errorf("GET %s as rene: expected 200, got %d reached=%v", url, code, reached)
		}
	}
	// Auth role admin (dev mode / static token).
	if code, reached := f.doAsAdminRole("PUT", "/api/trips/nadia-2026", "someone"); code != http.StatusOK || !reached {
		t.Errorf("PUT nadia-2026 with role=admin: expected 200, got %d reached=%v", code, reached)
	}
}

// ─── Trips without any access rule ───────────────────────────────────────────

func TestTripACL_TripWithoutAccessRule(t *testing.T) {
	t.Run("open mode is reachable", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "open")
		f := newACLFixture(t, seedACLDB(t))
		if code, reached := f.do("GET", "/api/trips/orphan-2026", "nicole"); code != http.StatusOK || !reached {
			t.Errorf("expected 200 in open mode, got %d reached=%v", code, reached)
		}
	})
	t.Run("strict mode is denied", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		f := newACLFixture(t, seedACLDB(t))
		if code, reached := f.do("GET", "/api/trips/orphan-2026", "nicole"); code != http.StatusForbidden || reached {
			t.Errorf("expected 403 in strict mode, got %d reached=%v", code, reached)
		}
		if code, _ := f.do("PUT", "/api/trips/orphan-2026/days/1", "nicole"); code != http.StatusForbidden {
			t.Errorf("expected 403 on nested route in strict mode, got %d", code)
		}
	})
}

// ─── Empty trip_accesses table ───────────────────────────────────────────────

func TestTripACL_EmptyAccessTable(t *testing.T) {
	t.Run("open mode grants everything", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "open")
		f := newACLFixture(t, emptyACLDB(t))
		if code, reached := f.do("GET", "/api/trips/usa-2026", "nicole"); code != http.StatusOK || !reached {
			t.Errorf("expected 200 in open mode, got %d reached=%v", code, reached)
		}
	})
	t.Run("strict mode denies non-admin", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		f := newACLFixture(t, emptyACLDB(t))
		if code, reached := f.do("GET", "/api/trips/usa-2026", "nicole"); code != http.StatusForbidden || reached {
			t.Errorf("expected 403 in strict mode, got %d reached=%v", code, reached)
		}
	})
	t.Run("strict mode still lets admins through", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		f := newACLFixture(t, emptyACLDB(t))
		if code, reached := f.do("GET", "/api/trips/usa-2026", "rene"); code != http.StatusOK || !reached {
			t.Errorf("expected 200 for admin rene, got %d reached=%v", code, reached)
		}
		if code, reached := f.doAsAdminRole("GET", "/api/trips/usa-2026", "dev"); code != http.StatusOK || !reached {
			t.Errorf("expected 200 for role=admin, got %d reached=%v", code, reached)
		}
	})
}

// ─── Non trip-scoped routes reach their handler ──────────────────────────────

func TestTripACL_NonTripScopedRoutesPassThrough(t *testing.T) {
	t.Setenv("TRIPKIT_ACL_MODE", "strict")
	f := newACLFixture(t, seedACLDB(t))

	for _, url := range []string{"/api/trips", "/api/my/trips", "/api/groups"} {
		if code, reached := f.do("GET", url, "nicole"); code != http.StatusOK || !reached {
			t.Errorf("GET %s: expected the handler to authorize itself, got %d reached=%v", url, code, reached)
		}
	}
	if code, reached := f.do("POST", "/api/trips", "nicole"); code != http.StatusOK || !reached {
		t.Errorf("POST /api/trips: expected the handler to authorize itself, got %d reached=%v", code, reached)
	}
}

// ─── Identity resolution ─────────────────────────────────────────────────────

func TestEffectiveUser_TokenIdentityWinsOverRemoteUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/trips", nil)
	req.Header.Set("Remote-User", "Attacker")

	// No token identity: fall back to the (lowercased) Remote-User header.
	var got string
	UserIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = EffectiveUser(r)
	})).ServeHTTP(httptest.NewRecorder(), req)
	if got != "attacker" {
		t.Errorf("expected fallback to remote-user %q, got %q", "attacker", got)
	}

	// Token identity present: it must win over the header.
	ctx := context.WithValue(req.Context(), ctxUserName, "Nadia")
	UserIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = EffectiveUser(r)
	})).ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	if got != "nadia" {
		t.Errorf("expected token identity %q to win, got %q", "nadia", got)
	}

	// No identity at all.
	plain := httptest.NewRequest("GET", "/api/trips", nil)
	UserIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = EffectiveUser(r)
	})).ServeHTTP(httptest.NewRecorder(), plain)
	if got != "anonymous" {
		t.Errorf("expected %q, got %q", "anonymous", got)
	}
}

// ─── AllowedTripIDs ──────────────────────────────────────────────────────────

func TestAllowedTripIDs(t *testing.T) {
	t.Run("admin gets nil", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		if ids := AllowedTripIDs(seedACLDB(t), "rene"); ids != nil {
			t.Errorf("expected nil for admin rene, got %v", ids)
		}
	})
	t.Run("scoped user gets its trips", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		ids := AllowedTripIDs(seedACLDB(t), "nadia")
		if len(ids) != 1 || ids[0] != "nadia-2026" {
			t.Errorf("expected [nadia-2026], got %v", ids)
		}
	})
	t.Run("user without group gets empty slice in strict mode", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		ids := AllowedTripIDs(seedACLDB(t), "stranger")
		if ids == nil || len(ids) != 0 {
			t.Errorf("expected non-nil empty slice, got %v (nil=%v)", ids, ids == nil)
		}
	})
	t.Run("empty table gives nil in open mode", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "open")
		if ids := AllowedTripIDs(emptyACLDB(t), "nicole"); ids != nil {
			t.Errorf("expected nil (open mode), got %v", ids)
		}
	})
	t.Run("empty table gives empty slice in strict mode", func(t *testing.T) {
		t.Setenv("TRIPKIT_ACL_MODE", "strict")
		ids := AllowedTripIDs(emptyACLDB(t), "nicole")
		if ids == nil || len(ids) != 0 {
			t.Errorf("expected non-nil empty slice, got %v (nil=%v)", ids, ids == nil)
		}
	})
}

// ─── extractTripID ───────────────────────────────────────────────────────────

func TestExtractTripID(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/api/trips", ""},
		{"/api/my/trips", ""},
		{"/api/groups/nadia", ""},
		{"/api/trips/usa-2026", "usa-2026"},
		{"/api/trips/usa-2026/days/3", "usa-2026"},
		{"/trips/nadia-2026/assets/map.png", "nadia-2026"},
	}
	for _, c := range cases {
		got := extractTripID(httptest.NewRequest("GET", c.path, nil))
		if got != c.want {
			t.Errorf("extractTripID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
