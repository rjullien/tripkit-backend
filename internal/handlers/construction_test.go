package handlers_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

func setupConstructionRouter(t *testing.T) *chi.Mux {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	h := handlers.New(db)
	h.SetConstruction(&construction.Service{DB: db})
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.UserIdentity)
		r.Post("/trips", h.CreateTrip)
		r.Post("/trips/{tripId}/discovery/retain", h.RetainDiscoveryItem)
		r.Post("/trips/{tripId}/nuisance-check/pin", h.PinNuisanceToSeed)
	})
	return r
}

func TestRetainDiscoveryItem_WritesCandidate(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-retain", "name": "Retain Test"}, "rene")

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

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := parseResp(w)
	if _, ok := body["jobId"]; ok {
		t.Errorf("retain is typed seedgit, not a Léo job, got jobId=%v", body["jobId"])
	}
	act, _ := body["activity"].(map[string]any)
	if act["bookingStatus"] != "candidate" || act["name"] != "Musee du Louvre" {
		t.Fatalf("activity=%v", act)
	}
	if act["id"] != "node-12345" || act["theme"] != "musees" {
		t.Fatalf("activity=%v", act)
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

	w := doReq(r, "POST", "/api/trips/trip-r3/discovery/retain", map[string]any{
		"item": map[string]any{
			"id":   "node-1",
			"name": "Test",
		},
	})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for anonymous user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPinNuisanceToSeed_NothingToPin(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-pin", "name": "Pin Test"}, "rene")

	w := doReqAs(r, "POST", "/api/trips/trip-pin/nuisance-check/pin", map[string]any{}, "rene")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := parseResp(w)["jobId"]; ok {
		t.Errorf("expected no jobId, got %s", w.Body.String())
	}
}

func TestPinNuisanceToSeed_NoAuth_NothingToPin(t *testing.T) {
	r := setupConstructionRouter(t)
	doReqAs(r, "POST", "/api/trips", map[string]any{"id": "trip-pin2", "name": "Pin Test"}, "rene")

	w := doReq(r, "POST", "/api/trips/trip-pin2/nuisance-check/pin", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (anonymous reaches write path), got %d: %s", w.Code, w.Body.String())
	}
}
