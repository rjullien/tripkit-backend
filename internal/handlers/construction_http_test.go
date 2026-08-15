package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

func constructionRouter(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	h.SetConstruction(&construction.Service{DB: db})

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/trips/{tripId}/travel-profile/request", h.CreateProfileRequest)
	r.Put("/trips/{tripId}/construction/phase", h.TransitionPhase)
	r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
	return h, r
}

func seedConstructionTrip(t *testing.T, h *Handler) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	if err := h.db.Create(&models.Trip{
		ID: "trip-constr", Name: "Test Construction",
		StartDate: &start, EndDate: &end,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// A valid body passes validation but the profile-edit write path is not wired:
// the endpoint must answer 501, start no job and persist no row (it used to
// leave rows stuck at status "running" forever -- review finding 5).
func TestCreateProfileRequest_NotImplemented(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	body := `{"target":"travelStyle","text":"Nous preferons un rythme lent avec des pauses"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["error"] != "not_implemented" {
		t.Fatalf("expected error=not_implemented, got: %s", rec.Body.String())
	}
	if detail, ok := resp["detail"].(string); !ok || detail == "" {
		t.Fatalf("expected a non-empty detail, got: %s", rec.Body.String())
	}
	if _, ok := resp["jobId"]; ok {
		t.Fatalf("expected no jobId (no job must be started), got: %s", rec.Body.String())
	}

	// No row must be created.
	var count int64
	h.db.Model(&models.ConstructionProfileRequest{}).Where("trip_id = ?", "trip-constr").Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 profile request rows, got %d", count)
	}
}

func TestCreateProfileRequest_InvalidTarget(t *testing.T) {
	_, r := constructionRouter(t)

	body := `{"target":"invalidSection","text":"some text"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_MissingText(t *testing.T) {
	_, r := constructionRouter(t)

	body := `{"target":"interests","text":""}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_NoAuth(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	// Without the UserIdentity middleware injecting a user, the context has no
	// user at all. But in our test setup we do use UserIdentity, so an empty
	// Remote-User becomes "anonymous". The handler checks for empty string,
	// which mirrors DiscoverySearch behavior: real auth is handled by the Auth
	// middleware layer in main.go. Verify that anonymous still proceeds (the
	// Auth middleware would block unauthenticated requests in production).
	body := `{"target":"interests","text":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Remote-User header - defaults to "anonymous" via UserIdentity middleware
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// With UserIdentity, anonymous is a valid user string (not empty), so the
	// request reaches the (unwired) write path and gets the same 501.
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 (anonymous passes handler check), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_AllTargets(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	targets := []string{"travelStyle", "budgetRules", "interests", "mealPattern", "lessons"}
	for _, target := range targets {
		body := `{"target":"` + target + `","text":"some modification"}`
		req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Remote-User", "rene")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("target %q: expected 501, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}

// ── Phase transition: force gating and structured blockers ──────────────────

// seedBlockedTrip seeds a trip whose QA produces a red blocker (day_gap: day 2
// is missing between day 1 and day 3).
func seedBlockedTrip(t *testing.T, h *Handler, tripID string) {
	t.Helper()
	data := `{"startDate":"2026-08-14","days":[` +
		`{"dayNum":1,"date":"2026-08-14","transport":{"mode":"train","status":"booked"}},` +
		`{"dayNum":3,"date":"2026-08-16","transport":{"mode":"train","status":"booked"}}` +
		`],"hotels":[{"dayNum":1,"status":"booked"},{"dayNum":3,"status":"booked"}]}`
	if err := h.db.Create(&models.Trip{ID: tripID, Name: "Blocked", Data: &data}).Error; err != nil {
		t.Fatal(err)
	}
}

func putPhase(r http.Handler, tripID, query, user string, phase int) *httptest.ResponseRecorder {
	body := `{"phase":` + strconv.Itoa(phase) + `}`
	req := httptest.NewRequest(http.MethodPut, "/trips/"+tripID+"/construction/phase"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestTransitionPhase_Force_NonAdminForbidden(t *testing.T) {
	t.Setenv("TRIPKIT_ADMIN_USERS", "admin,rene")
	h, r := constructionRouter(t)
	seedBlockedTrip(t, h, "trip-force")

	rec := putPhase(r, "trip-force", "?force=1", "nadia", 3)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "admin_required" {
		t.Fatalf("expected error=admin_required, got: %s", rec.Body.String())
	}

	// The phase must not have moved and nothing must be logged.
	state, _, err := h.construction.GetConstruction("trip-force")
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != 0 {
		t.Fatalf("phase=%d want 0", state.Phase)
	}
	var logs int64
	h.db.Model(&models.ConstructionPhaseLog{}).Where("trip_id = ?", "trip-force").Count(&logs)
	if logs != 0 {
		t.Fatalf("expected no phase log, got %d", logs)
	}
}

func TestTransitionPhase_Force_AdminSucceeds(t *testing.T) {
	t.Setenv("TRIPKIT_ADMIN_USERS", "admin,rene")
	h, r := constructionRouter(t)
	seedBlockedTrip(t, h, "trip-force-ok")

	rec := putPhase(r, "trip-force-ok", "?force=1", "rene", 3)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	state, _, err := h.construction.GetConstruction("trip-force-ok")
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != 3 {
		t.Fatalf("phase=%d want 3", state.Phase)
	}

	var logs []models.ConstructionPhaseLog
	h.db.Where("trip_id = ?", "trip-force-ok").Find(&logs)
	if len(logs) != 1 {
		t.Fatalf("log count=%d want 1", len(logs))
	}
	if logs[0].ForcedBy != "rene" {
		t.Fatalf("ForcedBy=%q want rene", logs[0].ForcedBy)
	}
}

func TestTransitionPhase_Blocked_StructuredBlockers(t *testing.T) {
	h, r := constructionRouter(t)
	seedBlockedTrip(t, h, "trip-blocked")

	rec := putPhase(r, "trip-blocked", "", "nadia", 3)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Error    string                     `json:"error"`
		Blockers []construction.QAViolation `json:"blockers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.Error != "transition_blocked" {
		t.Fatalf("expected error=transition_blocked, got %q", resp.Error)
	}
	// The error field must stay a plain code: no marshalled JSON inside it.
	if strings.ContainsAny(resp.Error, "[{") {
		t.Fatalf("blockers must not be stringified into error: %q", resp.Error)
	}
	if len(resp.Blockers) == 0 {
		t.Fatalf("expected a non-empty blockers array, got: %s", rec.Body.String())
	}
	for _, b := range resp.Blockers {
		if b.Severity != "red" {
			t.Errorf("blocker %q severity=%q want red", b.Code, b.Severity)
		}
		if b.Code == "" || b.Message == "" {
			t.Errorf("blocker missing code/message: %+v", b)
		}
	}

	// The phase must not have moved.
	state, _, err := h.construction.GetConstruction("trip-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != 0 {
		t.Fatalf("phase=%d want 0", state.Phase)
	}
}

// putPhaseRaw sends an arbitrary body, which putPhase cannot express.
func putPhaseRaw(r http.Handler, tripID, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/trips/"+tripID+"/construction/phase", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// A body carrying no usable `phase` must be refused, not read as phase 0. Phase 0
// is "not started" and a valid target since the range check landed, so an `int`
// field would turn `{}` or a typo'd field name into a silent rewind of the whole
// construction state, complete with an audit row claiming it was asked for.
func TestTransitionPhase_MissingPhaseKey(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	// Start from a real phase, so a rewind would be visible.
	if rec := putPhase(r, "trip-constr", "", "nadia", 2); rec.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, body := range []string{`{}`, `{"target":3}`, `{"phase":null}`, `{"Phase ":1}`} {
		rec := putPhaseRaw(r, "trip-constr", "nadia", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body %s: json decode: %v", body, err)
		}
		if resp["error"] != "phase is required" {
			t.Errorf("body %s: error=%v want \"phase is required\"", body, resp["error"])
		}

		state, _, err := h.construction.GetConstruction("trip-constr")
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase != 2 {
			t.Fatalf("body %s: phase=%d want 2 (the state must not move)", body, state.Phase)
		}
	}

	// Only the legitimate transition may be recorded.
	var logs int64
	h.db.Model(&models.ConstructionPhaseLog{}).Where("trip_id = ?", "trip-constr").Count(&logs)
	if logs != 1 {
		t.Fatalf("phase logs=%d want 1 (a refused body must not be audited)", logs)
	}
}

// An explicit 0 stays legitimate: resetting a trip to "not started" is a real
// operation and must not be collateral damage of the presence check above.
func TestTransitionPhase_ExplicitZeroAccepted(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	if rec := putPhase(r, "trip-constr", "", "nadia", 2); rec.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := putPhaseRaw(r, "trip-constr", "nadia", `{"phase":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	state, _, err := h.construction.GetConstruction("trip-constr")
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != 0 {
		t.Fatalf("phase=%d want 0", state.Phase)
	}
}
