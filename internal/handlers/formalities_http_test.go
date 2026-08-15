package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/formalities"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

func formalitiesRouter(t *testing.T) (*Handler, http.Handler) {
	return formalitiesRouterWith(t, nil)
}

// formalitiesRouterWith builds the formalities router with an optional Bifrost
// completer, so the optional `summary` field can be exercised both ways.
func formalitiesRouterWith(t *testing.T, completer bifrost.Completer) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	h.SetConstruction(&construction.Service{DB: db})
	h.SetFormalities(&formalities.Service{DB: db, Completer: completer})

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/trips/{tripId}/admin-check", h.RunAdminCheck)
	r.Get("/trips/{tripId}/admin-check", h.GetAdminCheck)
	r.Post("/trips/{tripId}/health-check", h.RunHealthCheck)
	r.Get("/trips/{tripId}/health-check", h.GetHealthCheck)
	return h, r
}

func seedFormalitiesTrip(t *testing.T, h *Handler, tripData string) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	var dataPtr *string
	if tripData != "" {
		dataPtr = &tripData
	}
	if err := h.db.Create(&models.Trip{
		ID: "trip-form", Name: "Test Formalities",
		StartDate: &start, EndDate: &end, Data: dataPtr,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRunAdminCheck_OK(t *testing.T) {
	h, r := formalitiesRouter(t)
	// Seed a trip with countries and nationalities so rules fire.
	tripData := `{"people":{"p1":{"nationalities":["FR"]}},"days":{"1":{"locations":[{"country":"US"}]}}}`
	seedFormalitiesTrip(t, h, tripData)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/admin-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if _, ok := resp["verdict"]; !ok {
		t.Fatalf("expected verdict in response, got: %v", resp)
	}

	// Verify stored in construction_checks
	var check models.ConstructionCheck
	if err := h.db.Where("trip_id = ? AND kind = ?", "trip-form", "admin").First(&check).Error; err != nil {
		t.Fatalf("expected check stored, got: %v", err)
	}
	if check.Data == "" {
		t.Fatal("expected non-empty data in stored check")
	}
}

func TestGetAdminCheck_NotFound(t *testing.T) {
	h, r := formalitiesRouter(t)
	seedFormalitiesTrip(t, h, "")

	req := httptest.NewRequest(http.MethodGet, "/trips/trip-form/admin-check", nil)
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAdminCheck_ReturnsCached(t *testing.T) {
	h, r := formalitiesRouter(t)
	seedFormalitiesTrip(t, h, "")

	// Pre-store a check result
	h.db.Create(&models.ConstructionCheck{
		TripID: "trip-form",
		Kind:   "admin",
		Data:   `{"verdict":"ok","countries":["US"],"items":[]}`,
	})

	req := httptest.NewRequest(http.MethodGet, "/trips/trip-form/admin-check", nil)
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["cached"] != true {
		t.Fatalf("expected cached=true, got: %v", resp)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %v", resp["result"])
	}
	if result["verdict"] != "ok" {
		t.Fatalf("expected verdict=ok, got: %v", result["verdict"])
	}
}

func TestRunHealthCheck_OK(t *testing.T) {
	h, r := formalitiesRouter(t)
	tripData := `{"people":{"p1":{"nationalities":["FR"]}},"days":{"1":{"locations":[{"country":"TH"}]}}}`
	seedFormalitiesTrip(t, h, tripData)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/health-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if _, ok := resp["verdict"]; !ok {
		t.Fatalf("expected verdict in response, got: %v", resp)
	}

	// Verify stored in construction_checks
	var check models.ConstructionCheck
	if err := h.db.Where("trip_id = ? AND kind = ?", "trip-form", "health").First(&check).Error; err != nil {
		t.Fatalf("expected check stored, got: %v", err)
	}
}

func TestGetHealthCheck_NotFound(t *testing.T) {
	h, r := formalitiesRouter(t)
	seedFormalitiesTrip(t, h, "")

	req := httptest.NewRequest(http.MethodGet, "/trips/trip-form/health-check", nil)
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetHealthCheck_ReturnsCached(t *testing.T) {
	h, r := formalitiesRouter(t)
	seedFormalitiesTrip(t, h, "")

	// Pre-store a check result
	h.db.Create(&models.ConstructionCheck{
		TripID: "trip-form",
		Kind:   "health",
		Data:   `{"verdict":"warning","countries":["TH"],"items":[{"country":"TH","type":"vaccine","label":"Hep A","status":"warning","detail":"Recommande"}]}`,
	})

	req := httptest.NewRequest(http.MethodGet, "/trips/trip-form/health-check", nil)
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["cached"] != true {
		t.Fatalf("expected cached=true, got: %v", resp)
	}
}

func TestRunAdminCheck_TripNotFound(t *testing.T) {
	_, r := formalitiesRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/trips/nonexistent/admin-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunHealthCheck_TripNotFound(t *testing.T) {
	_, r := formalitiesRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/trips/nonexistent/health-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunAdminCheck_NoFormalities(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	// Do NOT set formalities service

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/trips/{tripId}/admin-check", h.RunAdminCheck)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/admin-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunAdminCheck_EmptyTrip(t *testing.T) {
	h, r := formalitiesRouter(t)
	// Seed trip with no data - should return verdict ok/none with no items
	seedFormalitiesTrip(t, h, `{}`)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/admin-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["verdict"] != "ok" {
		t.Fatalf("expected verdict=ok for empty trip, got: %v", resp["verdict"])
	}
}

// With a completer wired, admin-check carries an LLM `summary` on top of the
// deterministic items (finding 18: FormatAdminResults had no callers).
func TestRunAdminCheck_SummaryFromCompleter(t *testing.T) {
	called := false
	completer := bifrost.CompleteFn(func(system, user string) (string, error) {
		called = true
		return "Résumé : ESTA à demander.", nil
	})
	h, r := formalitiesRouterWith(t, completer)
	seedFormalitiesTrip(t, h, `{"people":{"p1":{"nationalities":["FR"]}},"locations":{"nyc":{"country":"US"}}}`)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/admin-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("completer must be called when configured")
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["summary"] != "Résumé : ESTA à demander." {
		t.Fatalf("expected the LLM summary, got %v", resp["summary"])
	}
	if items, _ := resp["items"].([]any); len(items) == 0 {
		t.Fatal("deterministic items must still be present alongside the summary")
	}
}

// A failing LLM must never degrade the deterministic result: the summary falls
// back to plain text and the items are untouched.
func TestRunHealthCheck_CompleterFailureSoftFails(t *testing.T) {
	completer := bifrost.CompleteFn(func(system, user string) (string, error) {
		return "", errors.New("bifrost HTTP 503")
	})
	h, r := formalitiesRouterWith(t, completer)
	seedFormalitiesTrip(t, h, `{"people":{"p1":{"nationalities":["FR"]}},"locations":{"bkk":{"country":"TH"}}}`)

	req := httptest.NewRequest(http.MethodPost, "/trips/trip-form/health-check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 despite the LLM failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	items, _ := resp["items"].([]any)
	if len(items) == 0 {
		t.Fatal("deterministic items must survive an LLM failure")
	}
	summary, _ := resp["summary"].(string)
	if !strings.Contains(summary, "Conseils santé") {
		t.Fatalf("expected the plain-text fallback summary, got %q", summary)
	}
}
