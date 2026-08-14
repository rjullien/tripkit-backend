package discovery

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

type fakeQ struct {
	mu    sync.Mutex
	items map[string][]Item
	err   map[string]error
	calls []string
}

func (f *fakeQ) Search(_ context.Context, lat, lon float64, theme Theme) ([]Item, error) {
	f.mu.Lock()
	f.calls = append(f.calls, theme.ID)
	f.mu.Unlock()
	if f.err != nil {
		if e, ok := f.err[theme.ID]; ok {
			return nil, e
		}
	}
	out := append([]Item(nil), f.items[theme.ID]...)
	for i := range out {
		out[i].ThemeID = theme.ID
		out[i].DistKm = round1(haversineKm(lat, lon, out[i].Lat, out[i].Lon))
	}
	return out, nil
}

func (f *fakeQ) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeQ) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func seedTrip(t *testing.T, db *gorm.DB) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"locations": map[string]any{
			"tadoussac": map[string]any{"lat": 48.1454, "lon": -69.7173, "tz": "America/Toronto"},
		},
		"travelProfile": map[string]any{
			"family": "jullien",
			"themes": map[string]any{"disabled": []string{"eau"}},
		},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{
		"label": "Tadoussac", "to": "Tadoussac", "locationId": "tadoussac",
	})
	if err := db.Create(&models.Day{TripID: "quebec-2026", DayNum: 8, Data: string(day)}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSearch_PointSoftFailAndCache(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTrip(t, db)
	fq := &fakeQ{
		items: map[string][]Item{
			"outlets": {{ID: "osm:node:1", Name: "Outlet Village", Lat: 48.15, Lon: -69.72}},
		},
		err: map[string]error{"rando": context.DeadlineExceeded},
	}
	svc := &Service{DB: db, Overpass: fq, Now: func() time.Time {
		return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	}}
	res, err := svc.Search(context.Background(), "quebec-2026", Scope{DayNum: 8}, []string{"outlets", "rando", "eau"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Place != "Tadoussac" {
		t.Fatalf("place=%q", res.Place)
	}
	if len(res.ByTheme["outlets"]) != 1 {
		t.Fatalf("outlets=%v", res.ByTheme["outlets"])
	}
	if len(res.ByTheme["rando"]) != 0 {
		t.Fatalf("rando soft-fail should be empty, got %v", res.ByTheme["rando"])
	}
	// eau is disabled by travel-profile — not searched
	for _, id := range fq.snapshot() {
		if id == "eau" {
			t.Fatal("disabled theme should not be queried")
		}
	}
	// second call hits cache
	fq.reset()
	res2, err := svc.Search(context.Background(), "quebec-2026", Scope{DayNum: 8}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fq.snapshot()) != 0 {
		t.Fatalf("expected cache hit, calls=%v", fq.snapshot())
	}
	if len(res2.Items) != 1 || !res2.Items[0].Cached {
		t.Fatalf("cached items: %+v", res2.Items)
	}
}

func TestThemesForTrip_Disabled(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTrip(t, db)
	svc := &Service{DB: db}
	got, err := svc.ThemesForTrip("quebec-2026")
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range got {
		if th.ID == "eau" {
			t.Fatal("eau should be disabled")
		}
	}
}

func TestHaversineKnownDistance(t *testing.T) {
	// Tadoussac → ~1 km nearby point
	d := haversineKm(48.1454, -69.7173, 48.1544, -69.7173)
	if d < 0.8 || d > 1.2 {
		t.Fatalf("d=%v", d)
	}
}

func TestParseConfigJSON(t *testing.T) {
	raw := []byte(`{"version":1,"themes":[{"id":"outlets","label":"O","engine":"geo","radiusKm":10,"overpass":["shop=outlet"]}]}`)
	c, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Overpass.BaseURL == "" || c.Overpass.TimeoutSec != defaultTimeoutSec {
		t.Fatalf("defaults: %+v", c.Overpass)
	}
	_, err = parseConfigJSON([]byte(`{"themes":[]}`))
	if err == nil {
		t.Fatal("empty themes")
	}
}

func TestRankItems(t *testing.T) {
	got := rankItems([]Item{{Name: "far", DistKm: 12}, {Name: "near", DistKm: 1.2}})
	if got[0].Name != "near" {
		t.Fatalf("%+v", got)
	}
}

func TestNoCoords(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start := "2026-08-14"
	s := `{"locations":{}}`
	_ = db.Create(&models.Trip{ID: "x", Name: "x", StartDate: &start, Data: &s}).Error
	_ = db.Create(&models.Day{TripID: "x", DayNum: 1, Data: `{"label":"Home"}`}).Error
	svc := &Service{DB: db, Overpass: &fakeQ{}}
	_, err = svc.Search(context.Background(), "x", Scope{DayNum: 1}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no coordinates") {
		t.Fatalf("err=%v", err)
	}
}
