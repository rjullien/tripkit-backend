package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

type fakeQ struct {
	mu       sync.Mutex
	items    map[string][]Item
	err      map[string]error
	failOnce map[string]error
	calls    []string
}

func (f *fakeQ) Search(_ context.Context, lat, lon float64, theme Theme) ([]Item, error) {
	f.mu.Lock()
	f.calls = append(f.calls, theme.ID)
	if f.failOnce != nil {
		if e, ok := f.failOnce[theme.ID]; ok {
			delete(f.failOnce, theme.ID)
			f.mu.Unlock()
			return nil, e
		}
	}
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
	seedTripNamed(t, db, "quebec-2026")
}

func seedTripNamed(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"locations": map[string]any{
			"tadoussac":        map[string]any{"lat": 48.1454, "lon": -69.7173, "tz": "America/Toronto"},
			"baie-saint-paul":  map[string]any{"lat": 47.4411, "lon": -70.4989, "name": "Baie-Saint-Paul"},
			"riviere-eternite": map[string]any{"lat": 48.256, "lon": -70.414, "name": "Rivière-Éternité"},
		},
		"travelProfile": map[string]any{
			"family": "jullien",
			"themes": map[string]any{"disabled": []string{"eau"}},
		},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := db.Create(&models.Trip{
		ID: id, Name: "Boucle Québec",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{
		"label": "Tadoussac", "to": "Tadoussac", "locationId": "tadoussac",
	})
	if err := db.Create(&models.Day{TripID: id, DayNum: 8, Data: string(day)}).Error; err != nil {
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

	// rando failed — must not poison the cache
	fq.reset()
	res3, err := svc.Search(context.Background(), "quebec-2026", Scope{DayNum: 8}, []string{"rando"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fq.snapshot()) != 1 {
		t.Fatalf("rando error must not be cached, calls=%v", fq.snapshot())
	}
	if len(res3.ByTheme["rando"]) != 0 {
		t.Fatalf("rando still soft-fails, got %v", res3.ByTheme["rando"])
	}
}

func TestSearch_DoesNotCacheOverpassError(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTripNamed(t, db, "quebec-neg")
	fq := &fakeQ{
		items: map[string][]Item{
			"outlets": {{ID: "osm:node:1", Name: "Outlet Village", Lat: 48.15, Lon: -69.72}},
		},
		failOnce: map[string]error{"outlets": errors.New("429 Too Many Requests")},
	}
	svc := &Service{DB: db, Overpass: fq, Now: func() time.Time {
		return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	}}
	res, err := svc.Search(context.Background(), "quebec-neg", Scope{DayNum: 8}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ByTheme["outlets"]) != 0 {
		t.Fatalf("first search should be empty after 429, got %v", res.ByTheme["outlets"])
	}
	if n := len(fq.snapshot()); n != 1 {
		t.Fatalf("calls=%d", n)
	}

	res2, err := svc.Search(context.Background(), "quebec-neg", Scope{DayNum: 8}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(fq.snapshot()); n != 2 {
		t.Fatalf("transient 429 must not lock empty cache, calls=%d", n)
	}
	if len(res2.ByTheme["outlets"]) != 1 {
		t.Fatalf("retry should return the outlet, got %v", res2.ByTheme["outlets"])
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

type fakeEd struct {
	mu       sync.Mutex
	calls    []EditorialQuery
	items    []Item
	err      error
	failOnce error
}

func (f *fakeEd) Search(_ context.Context, q EditorialQuery) ([]Item, error) {
	f.mu.Lock()
	f.calls = append(f.calls, q)
	once := f.failOnce
	f.failOnce = nil
	f.mu.Unlock()
	if once != nil {
		return nil, once
	}
	if f.err != nil {
		return nil, f.err
	}
	return append([]Item(nil), f.items...), nil
}

func (f *fakeEd) snapshot() []EditorialQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]EditorialQuery(nil), f.calls...)
}

func (f *fakeEd) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func TestSearch_EditorialDateISOSoftFailAndCache(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTripNamed(t, db, "quebec-ed")
	ed := &fakeEd{
		items: []Item{{
			ID: "editorial:festivals:festifoule", ThemeID: "festivals",
			Name: "Festifoule", When: "2026-08-21", URL: "https://festifoule.ca",
			Source: "editorial",
		}},
	}
	svc := &Service{
		DB: db, Overpass: &fakeQ{}, Editorial: ed,
		Now: func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) },
	}
	res, err := svc.Search(context.Background(), "quebec-ed", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := ed.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	if calls[0].DateISO != "2026-08-21" {
		t.Fatalf("dateISO=%q (must be the displayed day, not now)", calls[0].DateISO)
	}
	if calls[0].Place != "Tadoussac" || calls[0].TripName != "Boucle Québec" {
		t.Fatalf("query=%+v", calls[0])
	}
	if !calls[0].Theme.Seasonal {
		t.Fatal("festivals should be seasonal")
	}
	if len(res.ByTheme["festivals"]) != 1 || res.ByTheme["festivals"][0].Name != "Festifoule" {
		t.Fatalf("%+v", res.ByTheme["festivals"])
	}

	ed.reset()
	res2, err := svc.Search(context.Background(), "quebec-ed", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.snapshot()) != 0 {
		t.Fatal("expected editorial cache hit")
	}
	if len(res2.Items) != 1 || !res2.Items[0].Cached {
		t.Fatalf("cached %+v", res2.Items)
	}

	edFail := &fakeEd{err: context.DeadlineExceeded}
	svc.Editorial = edFail
	svc.Now = func() time.Time { return time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC) } // past 6h TTL
	res3, err := svc.Search(context.Background(), "quebec-ed", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.ByTheme["festivals"]) != 0 {
		t.Fatalf("soft-fail got %+v", res3.ByTheme["festivals"])
	}
	edFail.reset()
	_, err = svc.Search(context.Background(), "quebec-ed", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(edFail.snapshot()) != 1 {
		t.Fatal("editorial error must not be cached")
	}
}

func TestSearch_CachesEmptySuccess(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTripNamed(t, db, "quebec-empty")
	fq := &fakeQ{items: map[string][]Item{"outlets": {}}}
	svc := &Service{DB: db, Overpass: fq, Now: func() time.Time {
		return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	}}
	res, err := svc.Search(context.Background(), "quebec-empty", Scope{DayNum: 8}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ByTheme["outlets"]) != 0 {
		t.Fatalf("empty success should stay empty, got %v", res.ByTheme["outlets"])
	}
	fq.reset()
	res2, err := svc.Search(context.Background(), "quebec-empty", Scope{DayNum: 8}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fq.snapshot()) != 0 {
		t.Fatalf("legitimate empty result may be cached, calls=%v", fq.snapshot())
	}
	if items, ok := res2.ByTheme["outlets"]; !ok || len(items) != 0 {
		t.Fatalf("cached empty: ok=%v items=%v", ok, items)
	}
}

func TestSearch_DoesNotCacheEditorialError(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTripNamed(t, db, "quebec-ed-neg")
	ed := &fakeEd{
		failOnce: errors.New("429"),
		items: []Item{{
			ID: "editorial:festivals:ok", ThemeID: "festivals",
			Name: "OK", Source: "editorial",
		}},
	}
	svc := &Service{
		DB: db, Overpass: &fakeQ{}, Editorial: ed,
		Now: func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) },
	}
	res, err := svc.Search(context.Background(), "quebec-ed-neg", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ByTheme["festivals"]) != 0 {
		t.Fatalf("first search should be empty, got %+v", res.ByTheme["festivals"])
	}
	if n := len(ed.snapshot()); n != 1 {
		t.Fatalf("calls=%d", n)
	}

	res2, err := svc.Search(context.Background(), "quebec-ed-neg", Scope{DayNum: 8}, []string{"festivals"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ed.snapshot()); n != 2 {
		t.Fatalf("Leo error must not lock empty cache, calls=%d", n)
	}
	if len(res2.ByTheme["festivals"]) != 1 || res2.ByTheme["festivals"][0].Name != "OK" {
		t.Fatalf("retry should return the festival, got %+v", res2.ByTheme["festivals"])
	}
}

func TestSearch_EditorialNilIsSoftFail(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTripNamed(t, db, "quebec-ed-nil")
	svc := &Service{DB: db, Overpass: &fakeQ{}, Now: func() time.Time {
		return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	}}
	res, err := svc.Search(context.Background(), "quebec-ed-nil", Scope{DayNum: 8}, []string{"spectacles"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ByTheme["spectacles"]) != 0 {
		t.Fatalf("%+v", res.ByTheme["spectacles"])
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

func TestScopeKey_LocationIncludesDate(t *testing.T) {
	if got := scopeKey(Scope{LocationID: "tadoussac"}); got != "loc:tadoussac" {
		t.Fatalf("no date: %s", got)
	}
	if got := scopeKey(Scope{LocationID: "tadoussac", DateISO: "2026-08-21"}); got != "loc:tadoussac:2026-08-21" {
		t.Fatalf("with date: %s", got)
	}
	if scopeKey(Scope{LocationID: "tadoussac", DateISO: "2026-08-21"}) == scopeKey(Scope{LocationID: "tadoussac", DateISO: "2026-09-04"}) {
		t.Fatal("same location on two dates must not share a cache key")
	}
	if got := scopeKey(Scope{DayNum: 8, DateISO: "2026-08-21"}); got != "day:8" {
		t.Fatalf("day path stays dayNum-only: %s", got)
	}
	if got := scopeKey(Scope{Corridor: []string{"baie-saint-paul", "riviere-eternite"}, DateISO: "2026-08-19"}); got != "corridor:baie-saint-paul:riviere-eternite:2026-08-19" {
		t.Fatalf("corridor+date: %s", got)
	}
	if scopeKey(Scope{Corridor: []string{"a", "b"}, DateISO: "2026-08-19"}) == scopeKey(Scope{Corridor: []string{"a", "b"}, DateISO: "2026-08-20"}) {
		t.Fatal("same corridor on two dates must not share a cache key")
	}
}

func TestSearch_CorridorAlongDrive(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	seedTrip(t, db)
	q := &fakeQ{items: map[string][]Item{
		"outlets": {{ID: "osm:outlet:1", Name: "Village de marques", Lat: 47.8, Lon: -70.45}},
	}}
	svc := &Service{DB: db, Overpass: q}
	res, err := svc.Search(context.Background(), "quebec-2026", Scope{
		Corridor: []string{"baie-saint-paul", "riviere-eternite"},
		DateISO:  "2026-08-19",
	}, []string{"outlets"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items=%d %+v", len(res.Items), res.Items)
	}
	if !res.Items[0].DetourEstimated {
		t.Fatal("detour must be marked estimated")
	}
	if res.Place != "Baie-Saint-Paul → Rivière-Éternité" {
		t.Fatalf("place=%q", res.Place)
	}
	cached, err := svc.Results("quebec-2026", Scope{
		Corridor: []string{"baie-saint-paul", "riviere-eternite"},
		DateISO:  "2026-08-19",
	}, []string{"outlets"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cached.Items) != 1 || !cached.Items[0].Cached {
		t.Fatalf("cache miss: %+v", cached.Items)
	}
}
