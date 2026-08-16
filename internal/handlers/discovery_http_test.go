package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

type stubQ struct {
	items []discovery.Item
}

func (s stubQ) Search(_ context.Context, lat, lon float64, theme discovery.Theme) ([]discovery.Item, error) {
	out := append([]discovery.Item(nil), s.items...)
	for i := range out {
		out[i].ThemeID = theme.ID
		out[i].Source = "osm"
	}
	return out, nil
}

func discoveryRouter(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	h.SetDiscovery(&discovery.Service{
		DB: db,
		Overpass: stubQ{items: []discovery.Item{
			{ID: "osm:node:1", Name: "Village de marques", Lat: 48.15, Lon: -69.72},
		}},
		Now: func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) },
	})
	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Get("/discovery/themes", h.DiscoveryCatalog)
	r.Get("/trips/{tripId}/discovery/themes", h.DiscoveryThemes)
	r.Post("/trips/{tripId}/discovery/search", h.DiscoverySearch)
	r.Get("/trips/{tripId}/discovery/results", h.DiscoveryResults)
	r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
	return h, r
}

func seedDiscoveryTrip(t *testing.T, h *Handler) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"locations": map[string]any{
			"tadoussac":        map[string]any{"lat": 48.1454, "lon": -69.7173, "tz": "America/Toronto"},
			"baie-saint-paul":  map[string]any{"lat": 47.4411, "lon": -70.4989, "name": "Baie-Saint-Paul"},
			"riviere-eternite": map[string]any{"lat": 48.256, "lon": -70.414, "name": "Rivière-Éternité"},
		},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := h.db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{"to": "Tadoussac", "locationId": "tadoussac"})
	if err := h.db.Create(&models.Day{TripID: "quebec-2026", DayNum: 8, Data: string(day)}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryThemes_CatalogAndTrip(t *testing.T) {
	h, r := discoveryRouter(t)
	seedDiscoveryTrip(t, h)

	req := httptest.NewRequest(http.MethodGet, "/discovery/themes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("catalog %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"outlets"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/discovery/themes", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("trip themes %d %s", rec.Code, rec.Body.String())
	}
}

func TestDiscoverySearch_JobThenCache(t *testing.T) {
	h, r := discoveryRouter(t)
	seedDiscoveryTrip(t, h)

	body := `{"themes":["outlets"],"scope":{"dayNum":8}}`
	req := httptest.NewRequest(http.MethodPost, "/trips/quebec-2026/discovery/search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("search %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.JobID == "" {
		t.Fatalf("jobId: %s", rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/discovery/results?dayNum=8", nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		got = rec.Body.String()
		if rec.Code == 200 && strings.Contains(got, "Village de marques") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(got, "Village de marques") {
		t.Fatalf("results=%s", got)
	}
}

type stubEd struct {
	items []discovery.Item
	last  discovery.EditorialQuery
}

func (s *stubEd) Search(_ context.Context, q discovery.EditorialQuery) ([]discovery.Item, error) {
	s.last = q
	out := append([]discovery.Item(nil), s.items...)
	return out, nil
}

func TestDiscoverySearch_EditorialFestivals(t *testing.T) {
	h, r := discoveryRouter(t)
	seedDiscoveryTrip(t, h)
	ed := &stubEd{items: []discovery.Item{{
		ID: "editorial:festivals:festifoule", ThemeID: "festivals",
		Name: "Festifoule", When: "2026-08-21", URL: "https://festifoule.ca",
		Note: "Tadoussac", Source: "editorial",
	}}}
	h.SetDiscovery(&discovery.Service{
		DB:        h.db,
		Overpass:  stubQ{},
		Editorial: ed,
		Now:       func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) },
	})

	body := `{"themes":["festivals"],"scope":{"dayNum":8}}`
	req := httptest.NewRequest(http.MethodPost, "/trips/quebec-2026/discovery/search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("search %d %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/discovery/results?dayNum=8", nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		got = rec.Body.String()
		if rec.Code == 200 && strings.Contains(got, "Festifoule") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(got, "Festifoule") || !strings.Contains(got, `"source":"editorial"`) {
		t.Fatalf("results=%s last=%+v", got, ed.last)
	}
	if ed.last.DateISO != "2026-08-21" {
		t.Fatalf("dateISO=%q", ed.last.DateISO)
	}
}

func TestDiscoverySearch_Corridor(t *testing.T) {
	h, r := discoveryRouter(t)
	seedDiscoveryTrip(t, h)

	body := `{"themes":["outlets"],"scope":{"corridor":["baie-saint-paul","riviere-eternite"],"dateISO":"2026-08-19"}}`
	req := httptest.NewRequest(http.MethodPost, "/trips/quebec-2026/discovery/search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("search %d %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/discovery/results?fromLoc=baie-saint-paul&toLoc=riviere-eternite&dateISO=2026-08-19", nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		got = rec.Body.String()
		if rec.Code == 200 && strings.Contains(got, "Village de marques") && strings.Contains(got, `"detourEstimated":true`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(got, "Village de marques") {
		t.Fatalf("results=%s", got)
	}
	if !strings.Contains(got, `"detourEstimated":true`) {
		t.Fatalf("expected detourEstimated: %s", got)
	}
}
