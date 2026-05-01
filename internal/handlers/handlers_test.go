package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

func setupRouter(t *testing.T) *chi.Mux {
	return setupRouterWithPrefix(t, "/api")
}

// setupRouterWithPrefix creates a test router with the given route prefix.
// Use prefix="/api" for the legacy /api path (used by most tests),
// or prefix="" for root mounting (BASE_PATH="" production mode).
func setupRouterWithPrefix(t *testing.T, prefix string) *chi.Mux {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}
	h := handlers.New(db)
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{AllowedOrigins: []string{"*"}}))
	r.Get("/health", h.Health)
	apiRoute := prefix
	if apiRoute == "" {
		apiRoute = "/"
	}
	r.Route(apiRoute, func(r chi.Router) {
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
	})
	return r
}
func doReq(r *chi.Mux, method, url string, body any) *httptest.ResponseRecorder {
	return doReqAs(r, method, url, body, "")
}

// doReqAs is like doReq but sets the Remote-User header to simulate Authelia injection.
func doReqAs(r *chi.Mux, method, url string, body any, user string) *httptest.ResponseRecorder {
	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}


func parseResp(w *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func parseRespSlice(w *httptest.ResponseRecorder) []any {
	var m []any
	json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

// ─── Health ──────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "GET", "/health", nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestBasePath_RootMount verifies that when BASE_PATH="" routes are served at
// root (i.e. GET /trips, not GET /api/trips).
func TestBasePath_RootMount(t *testing.T) {
	r := setupRouterWithPrefix(t, "") // equivalent to BASE_PATH=""
	// /health should work at root regardless
	if w := doReq(r, "GET", "/health", nil); w.Code != http.StatusOK {
		t.Errorf("expected /health 200, got %d", w.Code)
	}
	// /trips (root mount) should work
	if w := doReq(r, "GET", "/trips", nil); w.Code != http.StatusOK {
		t.Errorf("expected /trips 200 with root mount, got %d", w.Code)
	}
	// /api/trips should NOT exist (wrong prefix)
	if w := doReq(r, "GET", "/api/trips", nil); w.Code == http.StatusOK {
		t.Errorf("expected /api/trips to NOT be routed with root mount")
	}
	// POST /trips should also work
	w := doReq(r, "POST", "/trips", map[string]any{"id": "rt1", "name": "Root Trip"})
	if w.Code != http.StatusCreated {
		t.Errorf("expected POST /trips 201 with root mount, got %d: %s", w.Code, w.Body.String())
	}
}

// ─── API routes (no auth middleware — Authelia handles auth at ingress) ──────

func TestAPI_NoAuthRequired(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "GET", "/api/trips", nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 without auth header, got %d", w.Code)
	}
}

func TestHealth_NoAuth(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "GET", "/health", nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ─── Trips ───────────────────────────────────────────────────────────────────

func TestTrips_EmptyList(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "GET", "/api/trips", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if items := parseRespSlice(w); len(items) != 0 {
		t.Errorf("expected 0, got %d", len(items))
	}
}

func TestTrips_Create(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "POST", "/api/trips", map[string]any{"id": "usa-2026", "name": "Road Trip USA 2026", "emoji": "🇺🇸"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	body := parseResp(w)
	if body["id"] != "usa-2026" {
		t.Errorf("expected usa-2026, got %v", body["id"])
	}
}

func TestTrips_CreateNoName(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "POST", "/api/trips", map[string]any{"emoji": "🏝️"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTrips_CreateAutoID(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "POST", "/api/trips", map[string]any{"name": "My Trip"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if id, ok := parseResp(w)["id"].(string); !ok || len(id) < 8 {
		t.Errorf("expected auto-generated id, got %v", id)
	}
}

func TestTrips_List(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "t1", "name": "Trip 1"})
	doReq(r, "POST", "/api/trips", map[string]any{"id": "t2", "name": "Trip 2"})
	if items := parseRespSlice(doReq(r, "GET", "/api/trips", nil)); len(items) != 2 {
		t.Errorf("expected 2, got %d", len(items))
	}
}

func TestTrips_GetWithDaysCount(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-x", "name": "Trip X"})
	body := parseResp(doReq(r, "GET", "/api/trips/trip-x", nil))
	if body["daysCount"].(float64) != 0 {
		t.Errorf("expected 0, got %v", body["daysCount"])
	}
}

func TestTrips_GetNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "GET", "/api/trips/nonexistent", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTrips_Update(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-u", "name": "Original"})
	body := parseResp(doReq(r, "PUT", "/api/trips/trip-u", map[string]any{"name": "Updated"}))
	if body["name"] != "Updated" {
		t.Errorf("expected Updated, got %v", body["name"])
	}
}

func TestTrips_UpdateNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "PUT", "/api/trips/nonexistent", map[string]any{"name": "X"}); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTrips_UpdateData(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-d", "name": "Data Trip"})
	doReq(r, "PUT", "/api/trips/trip-d", map[string]any{"data": map[string]any{"travelers": []string{"René", "Nicole"}}})
	body := parseResp(doReq(r, "GET", "/api/trips/trip-d", nil))
	data := body["data"].(map[string]any)
	if travelers := data["travelers"].([]any); len(travelers) != 2 {
		t.Errorf("expected 2 travelers, got %d", len(travelers))
	}
}

func TestTrips_Delete(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-del", "name": "Delete Me"})
	if w := doReq(r, "DELETE", "/api/trips/trip-del", nil); w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w := doReq(r, "GET", "/api/trips/trip-del", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTrips_DeleteCascade(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-cas", "name": "Cascade Test"})
	doReq(r, "PUT", "/api/trips/trip-cas/days/1", map[string]any{"date": "2026-01-01"})
	doReq(r, "PUT", "/api/trips/trip-cas/hotels/1", map[string]any{"name": "Hotel 1"})
	doReq(r, "PUT", "/api/trips/trip-cas/lists/list-cas", map[string]any{"type": "packing", "title": "Test", "data": map[string]any{}})
	if w := doReq(r, "DELETE", "/api/trips/trip-cas", nil); w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w := doReq(r, "GET", "/api/trips/trip-cas/days", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for days, got %d", w.Code)
	}
}

func TestTrips_DeleteNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "DELETE", "/api/trips/nonexistent", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTrips_Seed(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "trip-seed", "name": "Seed"})
	doReq(r, "PUT", "/api/trips/trip-seed/days/1", map[string]any{"label": "Day 1"})
	doReq(r, "PUT", "/api/trips/trip-seed/hotels/1", map[string]any{"name": "Hotel"})
	doReq(r, "PUT", "/api/trips/trip-seed/lists/lst-1", map[string]any{"type": "packing", "title": "Valise", "data": map[string]any{}})
	body := parseResp(doReq(r, "GET", "/api/trips/trip-seed/seed", nil))
	if trip := body["trip"].(map[string]any); trip["id"] != "trip-seed" {
		t.Errorf("expected trip-seed, got %v", trip["id"])
	}
	if days := body["days"].([]any); len(days) != 1 {
		t.Errorf("expected 1 day, got %d", len(days))
	}
	if hotels := body["hotels"].([]any); len(hotels) != 1 {
		t.Errorf("expected 1 hotel, got %d", len(hotels))
	}
	if lists := body["lists"].([]any); len(lists) != 1 {
		t.Errorf("expected 1 list, got %d", len(lists))
	}
}

func TestTrips_SeedNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "GET", "/api/trips/nonexistent/seed", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ─── Days ────────────────────────────────────────────────────────────────────

func TestDays_EmptyList(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	if items := parseRespSlice(doReq(r, "GET", "/api/trips/td/days", nil)); len(items) != 0 {
		t.Errorf("expected 0, got %d", len(items))
	}
}

func TestDays_TripNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "GET", "/api/trips/nonexistent/days", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDays_Upsert(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	w := doReq(r, "PUT", "/api/trips/td/days/1", map[string]any{"date": "2026-04-17", "label": "LA", "emoji": "✈️"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseResp(w)
	if body["trip_id"] != "td" {
		t.Errorf("expected td, got %v", body["trip_id"])
	}
	if body["day_num"].(float64) != 1 {
		t.Errorf("expected 1, got %v", body["day_num"])
	}
}

func TestDays_UpsertUpdate(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	doReq(r, "PUT", "/api/trips/td/days/1", map[string]any{"label": "Original"})
	body := parseResp(doReq(r, "PUT", "/api/trips/td/days/1", map[string]any{"label": "Updated"}))
	data := body["data"].(map[string]any)
	if data["label"] != "Updated" {
		t.Errorf("expected Updated, got %v", data["label"])
	}
}

func TestDays_UpsertTripNotFound(t *testing.T) {
	r := setupRouter(t)
	if w := doReq(r, "PUT", "/api/trips/nonexistent/days/1", map[string]any{"label": "X"}); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDays_GetOne(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	doReq(r, "PUT", "/api/trips/td/days/3", map[string]any{"from": "LA"})
	body := parseResp(doReq(r, "GET", "/api/trips/td/days/3", nil))
	if body["day_num"].(float64) != 3 {
		t.Errorf("expected 3, got %v", body["day_num"])
	}
}

func TestDays_GetMissing(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	if w := doReq(r, "GET", "/api/trips/td/days/99", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDays_Ordered(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	doReq(r, "PUT", "/api/trips/td/days/3", map[string]any{"label": "3"})
	doReq(r, "PUT", "/api/trips/td/days/1", map[string]any{"label": "1"})
	doReq(r, "PUT", "/api/trips/td/days/2", map[string]any{"label": "2"})
	items := parseRespSlice(doReq(r, "GET", "/api/trips/td/days", nil))
	for i, expected := range []float64{1, 2, 3} {
		if items[i].(map[string]any)["day_num"].(float64) != expected {
			t.Errorf("day %d: expected %v, got %v", i, expected, items[i].(map[string]any)["day_num"])
		}
	}
}

func TestDays_IncrementsDaysCount(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "td", "name": "D"})
	doReq(r, "PUT", "/api/trips/td/days/1", map[string]any{"label": "D1"})
	doReq(r, "PUT", "/api/trips/td/days/2", map[string]any{"label": "D2"})
	body := parseResp(doReq(r, "GET", "/api/trips/td", nil))
	if body["daysCount"].(float64) != 2 {
		t.Errorf("expected 2, got %v", body["daysCount"])
	}
}

// ─── Hotels ──────────────────────────────────────────────────────────────────

func TestHotels_EmptyList(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "th", "name": "H"})
	if items := parseRespSlice(doReq(r, "GET", "/api/trips/th/hotels", nil)); len(items) != 0 {
		t.Errorf("expected 0, got %d", len(items))
	}
}

func TestHotels_Upsert(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "th", "name": "H"})
	w := doReq(r, "PUT", "/api/trips/th/hotels/1", map[string]any{"name": "Hotel California"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHotels_UpsertNoDuplicate(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "th", "name": "H"})
	doReq(r, "PUT", "/api/trips/th/hotels/5", map[string]any{"name": "First"})
	doReq(r, "PUT", "/api/trips/th/hotels/5", map[string]any{"name": "Second"})
	items := parseRespSlice(doReq(r, "GET", "/api/trips/th/hotels", nil))
	count := 0
	for _, it := range items {
		if it.(map[string]any)["day_num"].(float64) == 5 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 hotel for day 5, got %d", count)
	}
}

func TestHotels_Ordered(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "th", "name": "H"})
	doReq(r, "PUT", "/api/trips/th/hotels/3", map[string]any{"name": "C"})
	doReq(r, "PUT", "/api/trips/th/hotels/1", map[string]any{"name": "A"})
	doReq(r, "PUT", "/api/trips/th/hotels/2", map[string]any{"name": "B"})
	items := parseRespSlice(doReq(r, "GET", "/api/trips/th/hotels", nil))
	for i, expected := range []float64{1, 2, 3} {
		if items[i].(map[string]any)["day_num"].(float64) != expected {
			t.Errorf("hotel %d: expected %v", i, expected)
		}
	}
}

// ─── Lists ───────────────────────────────────────────────────────────────────

func TestLists_Upsert(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tl", "name": "L"})
	w := doReq(r, "PUT", "/api/trips/tl/lists/list-1", map[string]any{"type": "packing", "title": "Valise", "data": map[string]any{}})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if parseResp(w)["id"] != "list-1" {
		t.Errorf("expected list-1")
	}
}

func TestLists_MissingType(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tl", "name": "L"})
	if w := doReq(r, "PUT", "/api/trips/tl/lists/l", map[string]any{"title": "X"}); w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLists_MissingTitle(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tl", "name": "L"})
	if w := doReq(r, "PUT", "/api/trips/tl/lists/l", map[string]any{"type": "packing"}); w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLists_GetWithState(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tl", "name": "L"})
	doReq(r, "PUT", "/api/trips/tl/lists/ls", map[string]any{"type": "packing", "title": "T", "data": map[string]any{}})
	body := parseResp(doReq(r, "GET", "/api/trips/tl/lists/ls", nil))
	state := body["state"].(map[string]any)
	if state["checks"] == nil || state["custom"] == nil {
		t.Errorf("expected state with checks and custom")
	}
}

func TestLists_Delete(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tl", "name": "L"})
	doReq(r, "PUT", "/api/trips/tl/lists/ld", map[string]any{"type": "packing", "title": "Del", "data": map[string]any{}})
	if w := doReq(r, "DELETE", "/api/trips/tl/lists/ld", nil); w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w := doReq(r, "GET", "/api/trips/tl/lists/ld", nil); w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ─── Sync ────────────────────────────────────────────────────────────────────

func setupSync(t *testing.T) *chi.Mux {
	t.Helper()
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "ts", "name": "S"})
	doReq(r, "PUT", "/api/trips/ts/lists/ls", map[string]any{"type": "shopping", "title": "Sync", "data": map[string]any{}})
	return r
}

func doSync(r *chi.Mux, body any) *httptest.ResponseRecorder {
	return doReq(r, "PATCH", "/api/trips/ts/lists/ls/sync", body)
}

func TestSync_TwoDevicesNoConflict(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "a", "lastSyncAt": 0, "checks": map[string]any{"bacon": map[string]any{"checked": true, "updatedAt": 200}}, "custom": map[string]any{}, "hidden": []any{}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "b", "lastSyncAt": 0, "checks": map[string]any{"oeufs": map[string]any{"checked": true, "updatedAt": 210}}, "custom": map[string]any{}, "hidden": []any{}}))
	checks := body["merged"].(map[string]any)["checks"].(map[string]any)
	if checks["bacon"].(map[string]any)["checked"] != true || checks["oeufs"].(map[string]any)["checked"] != true {
		t.Errorf("expected both checked")
	}
}

func TestSync_ConflictNewerWins(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "a", "lastSyncAt": 0, "checks": map[string]any{"bacon": map[string]any{"checked": true, "updatedAt": 100}}, "custom": map[string]any{}, "hidden": []any{}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "b", "lastSyncAt": 0, "checks": map[string]any{"bacon": map[string]any{"checked": false, "updatedAt": 200}}, "custom": map[string]any{}, "hidden": []any{}}))
	if body["merged"].(map[string]any)["checks"].(map[string]any)["bacon"].(map[string]any)["checked"] != false {
		t.Errorf("expected newer (false) to win")
	}
	if body["conflicts"].(float64) < 1 {
		t.Errorf("expected conflicts > 0")
	}
}

func TestSync_ServerNewerWins(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "a", "lastSyncAt": 0, "checks": map[string]any{"milk": map[string]any{"checked": true, "updatedAt": 500}}, "custom": map[string]any{}, "hidden": []any{}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "b", "lastSyncAt": 0, "checks": map[string]any{"milk": map[string]any{"checked": false, "updatedAt": 100}}, "custom": map[string]any{}, "hidden": []any{}}))
	if body["merged"].(map[string]any)["checks"].(map[string]any)["milk"].(map[string]any)["checked"] != true {
		t.Errorf("expected server (true) to win")
	}
}

func TestSync_Idempotent(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{"sugar": map[string]any{"checked": true, "updatedAt": 300}}, "custom": map[string]any{}, "hidden": []any{}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{"sugar": map[string]any{"checked": true, "updatedAt": 300}}, "custom": map[string]any{}, "hidden": []any{}}))
	if body["conflicts"].(float64) != 0 {
		t.Errorf("expected 0 conflicts for idempotent")
	}
}

func TestSync_CustomItemUnion(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "a", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{"ci-1": map[string]any{"text": "Beurre", "section": 0, "createdAt": 1000}}, "hidden": []any{}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "b", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}}))
	custom := body["merged"].(map[string]any)["custom"].(map[string]any)
	if custom["ci-1"] == nil {
		t.Errorf("expected custom item from device A")
	}
}

func TestSync_HiddenPerDevice(t *testing.T) {
	r := setupSync(t)
	bodyA := parseResp(doSync(r, map[string]any{"deviceId": "phone", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{"chargeur", "adaptateur"}}))
	if len(bodyA["hidden"].([]any)) != 2 {
		t.Errorf("expected 2 hidden for phone")
	}
	bodyB := parseResp(doSync(r, map[string]any{"deviceId": "ipad", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{"maillot"}}))
	if len(bodyB["hidden"].([]any)) != 1 {
		t.Errorf("expected 1 hidden for ipad")
	}
}

func TestSync_HiddenReplace(t *testing.T) {
	r := setupSync(t)
	doSync(r, map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{"a", "b"}})
	body := parseResp(doSync(r, map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{"c"}}))
	hidden := body["hidden"].([]any)
	if len(hidden) != 1 || hidden[0] != "c" {
		t.Errorf("expected [c], got %v", hidden)
	}
}

func TestSync_MissingDeviceID(t *testing.T) {
	r := setupSync(t)
	if w := doSync(r, map[string]any{"lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}}); w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSync_NonexistentList(t *testing.T) {
	r := setupSync(t)
	w := doReq(r, "PATCH", "/api/trips/ts/lists/nope/sync", map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}})
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSync_ServerSyncAt(t *testing.T) {
	r := setupSync(t)
	body := parseResp(doSync(r, map[string]any{"deviceId": "d", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}}))
	if body["serverSyncAt"].(float64) <= 0 {
		t.Errorf("expected positive serverSyncAt")
	}
}

// ─── User identity middleware ─────────────────────────────────────────────────

func TestUser_ContextMiddleware(t *testing.T) {
	r := setupRouter(t)
	// With Remote-User header: should succeed (200 on /api/trips)
	w := doReqAs(r, "GET", "/api/trips", nil, "rene")
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with Remote-User header, got %d", w.Code)
	}
	// Without Remote-User header and TRIPKIT_REQUIRE_USER not set: should default to anonymous (200)
	w = doReq(r, "GET", "/api/trips", nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 without Remote-User (anonymous fallback), got %d", w.Code)
	}
}

// ─── Personal Lists ───────────────────────────────────────────────────────────

func TestLists_PersonalOwner(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tp", "name": "P"})

	// Create a personal list owned by rene
	w := doReqAs(r, "PUT", "/api/trips/tp/lists/pl-rene",
		map[string]any{"type": "packing", "title": "René's list", "data": map[string]any{}, "owner_user": "rene"},
		"rene")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := parseResp(w)
	if body["owner_user"] != "rene" {
		t.Errorf("expected owner_user=rene, got %v", body["owner_user"])
	}
}

func TestLists_FilterByOwner(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tf", "name": "F"})

	// Shared list (no owner)
	doReq(r, "PUT", "/api/trips/tf/lists/shared-1",
		map[string]any{"type": "packing", "title": "Shared", "data": map[string]any{}})

	// René's personal list
	doReqAs(r, "PUT", "/api/trips/tf/lists/rene-1",
		map[string]any{"type": "packing", "title": "René", "data": map[string]any{}, "owner_user": "rene"},
		"rene")

	// Nicole's personal list
	doReqAs(r, "PUT", "/api/trips/tf/lists/nicole-1",
		map[string]any{"type": "packing", "title": "Nicole", "data": map[string]any{}, "owner_user": "nicole"},
		"nicole")

	// Filter by ?owner=rene: should return shared + rene's list, NOT nicole's
	w := doReqAs(r, "GET", "/api/trips/tf/lists?owner=rene", nil, "rene")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	items := parseRespSlice(w)
	if len(items) != 2 {
		t.Errorf("expected 2 lists (shared + rene's), got %d", len(items))
	}
	// No filter: all 3 lists
	w = doReq(r, "GET", "/api/trips/tf/lists", nil)
	items = parseRespSlice(w)
	if len(items) != 3 {
		t.Errorf("expected 3 lists without filter, got %d", len(items))
	}
}

func TestSync_PersonalListForbidden(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tpf", "name": "PF"})
	// Create a list owned by rene
	doReqAs(r, "PUT", "/api/trips/tpf/lists/rene-list",
		map[string]any{"type": "packing", "title": "René", "data": map[string]any{}, "owner_user": "rene"},
		"rene")

	// Nicole tries to sync René's personal list → 403
	w := doReqAs(r, "PATCH", "/api/trips/tpf/lists/rene-list/sync",
		map[string]any{"deviceId": "nicole-phone", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}},
		"nicole")
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSync_PersonalListOwnerOK(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tpo", "name": "PO"})
	// Create a list owned by rene
	doReqAs(r, "PUT", "/api/trips/tpo/lists/rene-list",
		map[string]any{"type": "packing", "title": "René", "data": map[string]any{}, "owner_user": "rene"},
		"rene")

	// René syncs his own list → 200
	w := doReqAs(r, "PATCH", "/api/trips/tpo/lists/rene-list/sync",
		map[string]any{"deviceId": "rene-phone", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}},
		"rene")
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSync_SharedListAnyone(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tsa", "name": "SA"})
	// Create a shared list (no owner)
	doReq(r, "PUT", "/api/trips/tsa/lists/shared-list",
		map[string]any{"type": "shopping", "title": "Shared", "data": map[string]any{}})

	syncBody := map[string]any{"deviceId": "any-device", "lastSyncAt": 0, "checks": map[string]any{}, "custom": map[string]any{}, "hidden": []any{}}

	// Nicole can sync the shared list → 200
	w := doReqAs(r, "PATCH", "/api/trips/tsa/lists/shared-list/sync", syncBody, "nicole")
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for nicole on shared list, got %d: %s", w.Code, w.Body.String())
	}
	// Anonymous also can sync → 200
	w = doReq(r, "PATCH", "/api/trips/tsa/lists/shared-list/sync", syncBody)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for anonymous on shared list, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteList_PersonalForbidden(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tdp", "name": "DP"})
	// Create a list owned by rene
	doReqAs(r, "PUT", "/api/trips/tdp/lists/rene-del",
		map[string]any{"type": "packing", "title": "René del", "data": map[string]any{}, "owner_user": "rene"},
		"rene")

	// Nicole tries to delete René's list → 403
	w := doReqAs(r, "DELETE", "/api/trips/tdp/lists/rene-del", nil, "nicole")
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	// List must still exist
	w = doReq(r, "GET", "/api/trips/tdp/lists/rene-del", nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected list to still exist (200), got %d", w.Code)
	}
}

// ─── Weather ──────────────────────────────────────────────────────────────────

func TestWeather_MissingParams(t *testing.T) {
	r := setupRouter(t)
	doReq(r, "POST", "/api/trips", map[string]any{"id": "tw", "name": "W"})

	// No lat/lon → 400
	w := doReq(r, "GET", "/api/trips/tw/weather", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with no params, got %d", w.Code)
	}
	// Only lat → 400
	w = doReq(r, "GET", "/api/trips/tw/weather?lat=48.85", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with only lat, got %d", w.Code)
	}
	// Only lon → 400
	w = doReq(r, "GET", "/api/trips/tw/weather?lon=2.35", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with only lon, got %d", w.Code)
	}
}

func TestWeather_TripNotFound(t *testing.T) {
	r := setupRouter(t)
	w := doReq(r, "GET", "/api/trips/nonexistent-trip/weather?lat=48.85&lon=2.35", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
