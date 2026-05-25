package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// TestE2E_FullTripLifecycle tests the complete trip workflow:
// create trip → upsert days → upload assets → verify seed → verify assets
func TestE2E_FullTripLifecycle(t *testing.T) {
	r := setupE2ERouter(t)

	// 1. Health check
	t.Run("health", func(t *testing.T) {
		w := doReq(r, "GET", "/health", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["status"] != "ok" {
			t.Fatalf("expected status=ok, got %s", resp["status"])
		}
	})

	// 2. Create trip with mapImage and routeUrl in data
	t.Run("create_trip", func(t *testing.T) {
		body := map[string]any{
			"id":         "e2e-test-trip",
			"name":       "E2E Test Trip",
			"emoji":      "🧪",
			"start_date": "2026-08-01",
			"end_date":   "2026-08-10",
			"data": map[string]any{
				"travelers": []map[string]any{
					{"name": "Tester", "role": "owner"},
				},
				"phases": []map[string]any{
					{"name": "Phase 1", "days": []int{0, 4}},
					{"name": "Phase 2", "days": []int{5, 9}},
				},
				"mapImage": "map-overview.png",
				"routeUrl": "https://www.google.com/maps/dir/A/B/C",
			},
		}
		w := doReq(r, "POST", "/api/trips", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	// 3. Upsert days
	t.Run("upsert_days", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			day := map[string]any{
				"day":        i,
				"emoji":      "📍",
				"label":      fmt.Sprintf("Day %d", i+1),
				"dist":       fmt.Sprintf("%d km", (i+1)*50),
				"highlights": []string{"Highlight A", "Highlight B"},
				"hotelId":    fmt.Sprintf("hotel-%d", i%3),
			}
			w := doReq(r, "PUT", fmt.Sprintf("/api/trips/e2e-test-trip/days/%d", i), day)
			if w.Code != http.StatusOK {
				t.Fatalf("day %d: expected 200, got %d: %s", i, w.Code, w.Body.String())
			}
		}
	})

	// 4. Upsert hotels
	t.Run("upsert_hotels", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			hotel := map[string]any{
				"id":   fmt.Sprintf("hotel-%d", i),
				"name": fmt.Sprintf("Hotel %d", i+1),
				"city": fmt.Sprintf("City %d", i+1),
			}
			w := doReq(r, "PUT", fmt.Sprintf("/api/trips/e2e-test-trip/hotels/%d", i), hotel)
			if w.Code != http.StatusOK {
				t.Fatalf("hotel %d: expected 200, got %d: %s", i, w.Code, w.Body.String())
			}
		}
	})

	// 5. Upload asset (map image)
	t.Run("upload_asset", func(t *testing.T) {
		// Create a fake PNG (just some bytes)
		fakeImg := bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 1000) // ~4KB

		req := httptest.NewRequest("PUT", "/api/trips/e2e-test-trip/assets/map-overview.png", bytes.NewReader(fakeImg))
		req.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["filename"] != "map-overview.png" {
			t.Fatalf("expected filename=map-overview.png, got %v", resp["filename"])
		}
		if resp["size"].(float64) != 4000 {
			t.Fatalf("expected size=4000, got %v", resp["size"])
		}
	})

	// 6. List assets
	t.Run("list_assets", func(t *testing.T) {
		w := doReq(r, "GET", "/api/trips/e2e-test-trip/assets", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var assets []map[string]any
		json.Unmarshal(w.Body.Bytes(), &assets)
		if len(assets) != 1 {
			t.Fatalf("expected 1 asset, got %d", len(assets))
		}
		if assets[0]["filename"] != "map-overview.png" {
			t.Fatalf("expected map-overview.png, got %v", assets[0]["filename"])
		}
	})

	// 7. Download asset
	t.Run("download_asset", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/trips/e2e-test-trip/assets/map-overview.png", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("expected image/png, got %s", w.Header().Get("Content-Type"))
		}
		if w.Body.Len() != 4000 {
			t.Fatalf("expected 4000 bytes, got %d", w.Body.Len())
		}
	})

	// 8. Verify seed endpoint returns complete data including mapImage
	t.Run("seed_complete", func(t *testing.T) {
		w := doReq(r, "GET", "/api/trips/e2e-test-trip/seed", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var seed map[string]any
		json.Unmarshal(w.Body.Bytes(), &seed)

		// Trip data should have mapImage
		trip := seed["trip"].(map[string]any)
		data := trip["data"].(map[string]any)
		if data["mapImage"] != "map-overview.png" {
			t.Fatalf("expected mapImage=map-overview.png in seed, got %v", data["mapImage"])
		}
		if data["routeUrl"] != "https://www.google.com/maps/dir/A/B/C" {
			t.Fatalf("expected routeUrl in seed, got %v", data["routeUrl"])
		}

		// Should have 10 days
		days := seed["days"].([]any)
		if len(days) != 10 {
			t.Fatalf("expected 10 days, got %d", len(days))
		}

		// Should have 3 hotels
		hotels := seed["hotels"].([]any)
		if len(hotels) != 3 {
			t.Fatalf("expected 3 hotels, got %d", len(hotels))
		}
	})

	// 9. Version endpoint
	t.Run("version", func(t *testing.T) {
		w := doReq(r, "GET", "/api/trips/e2e-test-trip/version", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var ver map[string]any
		json.Unmarshal(w.Body.Bytes(), &ver)
		if ver["version"] == nil {
			t.Fatal("expected version field")
		}
	})

	// 10. Trip list includes our trip
	t.Run("list_trips", func(t *testing.T) {
		w := doReq(r, "GET", "/api/trips", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var trips []map[string]any
		json.Unmarshal(w.Body.Bytes(), &trips)
		found := false
		for _, trip := range trips {
			if trip["id"] == "e2e-test-trip" {
				found = true
				if trip["name"] != "E2E Test Trip" {
					t.Fatalf("expected name=E2E Test Trip, got %v", trip["name"])
				}
			}
		}
		if !found {
			t.Fatal("e2e-test-trip not found in list")
		}
	})

	// 11. Asset validation (bad filename)
	t.Run("asset_bad_filename", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/trips/e2e-test-trip/assets/../etc/passwd", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		// Should reject path traversal
		if w.Code == http.StatusOK {
			t.Fatal("expected non-200 for path traversal attempt")
		}
	})

	// 12. Asset for non-existent trip
	t.Run("asset_nonexistent_trip", func(t *testing.T) {
		w := doReq(r, "GET", "/api/trips/nonexistent/assets/foo.png", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	// 13. Delete trip and verify cleanup
	t.Run("delete_trip", func(t *testing.T) {
		w := doReq(r, "DELETE", "/api/trips/e2e-test-trip", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
		}

		// Verify trip is gone
		w = doReq(r, "GET", "/api/trips/e2e-test-trip", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 after delete, got %d", w.Code)
		}
	})
}

// TestE2E_AssetSizeLimit tests the 5MB upload limit
func TestE2E_AssetSizeLimit(t *testing.T) {
	r := setupE2ERouter(t)

	// Create trip first
	doReq(r, "POST", "/api/trips", map[string]any{
		"id": "limit-test", "name": "Limit Test", "start_date": "2026-01-01",
	})

	// Try uploading > 5MB
	bigFile := make([]byte, 6*1024*1024) // 6MB
	req := httptest.NewRequest("PUT", "/api/trips/limit-test/assets/big.png", bytes.NewReader(bigFile))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail
	if w.Code == http.StatusOK {
		t.Fatal("expected rejection for 6MB upload")
	}
}

// setupE2ERouter creates a router with ALL endpoints including assets
func setupE2ERouter(t *testing.T) *chi.Mux {
	t.Helper()
	tmpDir := t.TempDir()
	return setupE2ERouterFull(t, tmpDir)
}

func setupE2ERouterFull(t *testing.T, assetsDir string) *chi.Mux {
	t.Helper()
	t.Setenv("ASSETS_DIR", assetsDir)

	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	h := handlers.New(db)

	r := chi.NewRouter()
	r.Get("/health", h.Health)
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.UserIdentity)
		r.Get("/trips", h.ListTrips)
		r.Post("/trips", h.CreateTrip)
		r.Get("/trips/{tripId}", h.GetTrip)
		r.Put("/trips/{tripId}", h.UpdateTrip)
		r.Delete("/trips/{tripId}", h.DeleteTrip)
		r.Get("/trips/{tripId}/seed", h.SeedTrip)
		r.Get("/trips/{tripId}/version", h.TripVersion)
		r.Get("/trips/{tripId}/days", h.ListDays)
		r.Get("/trips/{tripId}/days/{dayNum}", h.GetDay)
		r.Put("/trips/{tripId}/days/{dayNum}", h.UpsertDay)
		r.Get("/trips/{tripId}/hotels", h.ListHotels)
		r.Put("/trips/{tripId}/hotels/{dayNum}", h.UpsertHotel)
		r.Get("/trips/{tripId}/lists", h.ListLists)
		r.Get("/trips/{tripId}/lists/{listId}", h.GetList)
		r.Put("/trips/{tripId}/lists/{listId}", h.UpsertList)
		r.Delete("/trips/{tripId}/lists/{listId}", h.DeleteList)
		r.Patch("/trips/{tripId}/lists/{listId}/sync", h.SyncList)
		r.Get("/trips/{tripId}/weather", h.GetWeather)
		r.Get("/trips/{tripId}/assets", h.ListAssets)
		r.Get("/trips/{tripId}/assets/{filename}", h.GetAsset)
		r.Put("/trips/{tripId}/assets/{filename}", h.UploadAsset)
	})
	return r
}

// Helpers (redeclare for this file since they're in the other test file)
func doE2EReq(r http.Handler, method, url string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
