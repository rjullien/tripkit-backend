package handlers_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// setupConstructionRouter creates a test router with construction endpoints.
func setupConstructionRouter(t *testing.T) *chi.Mux {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	h := handlers.New(db)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.UserIdentity)
		r.Post("/trips", h.CreateTrip)
		r.Post("/trips/{tripId}/discovery/retain", h.RetainDiscoveryItem)
		r.Post("/trips/{tripId}/nuisance-check/pin", h.PinNuisanceToSeed)
	})
	return r
}

func TestRetainDiscoveryItem_ValidItem(t *testing.T) {
	r := setupConstructionRouter(t)
	// Create a trip first
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-retain", "name": "Retain Test"}, "rene")

	// Post a retain request
	w := doReqAs(r, "POST", "/api/trips/trip-retain/discovery/retain", map[string]any{
		"item": map[string]any{
			"id":      "node-12345",
			"name":    "Musee du Louvre",
			"themeId": "musees",
			"lat":     48.8606,
			"lon":     2.3376,
			"distKm":  1.2,
			"url":     "https://maps.google.com/?q=48.8606,2.3376",
			"source":  "osm",
		},
	}, "rene")

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	body := parseResp(w)
	jobId, ok := body["jobId"].(string)
	if !ok || jobId == "" {
		t.Errorf("expected non-empty jobId, got %v", body["jobId"])
	}
}

func TestRetainDiscoveryItem_MissingName(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-r2", "name": "Test"}, "rene")

	w := doReqAs(r, "POST", "/api/trips/trip-r2/discovery/retain", map[string]any{
		"item": map[string]any{
			"id":      "node-99",
			"themeId": "musees",
		},
	}, "rene")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRetainDiscoveryItem_NoAuth(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-r3", "name": "Test"}, "rene")

	// Without Remote-User: middleware defaults to "anonymous" which is non-empty,
	// so the handler accepts the request (matches CreateProfileRequest pattern).
	w := doReq(r, "POST", "/api/trips/trip-r3/discovery/retain", map[string]any{
		"item": map[string]any{
			"id":   "node-1",
			"name": "Test",
		},
	})

	// Anonymous user is allowed (auth is at ingress via Authelia, not in handler)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for anonymous user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPinNuisanceToSeed_Valid(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-pin", "name": "Pin Test"}, "rene")

	w := doReqAs(r, "POST", "/api/trips/trip-pin/nuisance-check/pin", map[string]any{}, "rene")

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	body := parseResp(w)
	jobId, ok := body["jobId"].(string)
	if !ok || jobId == "" {
		t.Errorf("expected non-empty jobId, got %v", body["jobId"])
	}
}

func TestPinNuisanceToSeed_NoAuth(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-pin2", "name": "Pin Test"}, "rene")

	// Anonymous user is allowed (auth is at ingress via Authelia, not in handler)
	w := doReq(r, "POST", "/api/trips/trip-pin2/nuisance-check/pin", map[string]any{})

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for anonymous user, got %d: %s", w.Code, w.Body.String())
	}
}
