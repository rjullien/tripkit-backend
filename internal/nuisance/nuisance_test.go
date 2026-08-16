package nuisance

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// mockQuerier implements discovery.Querier for testing.
// When err is set every Search fails, which is how the Overpass outage path is
// exercised (the public API is not reachable from the test environment).
// calls counts the queries actually issued, so cache hits are observable.
type mockQuerier struct {
	items map[string][]discovery.Item // themeID -> items
	err   error

	mu    sync.Mutex
	calls int
}

func (m *mockQuerier) Search(_ context.Context, lat, lon float64, theme discovery.Theme) ([]discovery.Item, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if m.items == nil {
		return nil, nil
	}
	return m.items[theme.ID], nil
}

func (m *mockQuerier) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// noPace removes the inter-query pause so tests do not really wait 800ms per
// Overpass call. Production keeps the pause: that is the point of it.
func noPace(_ context.Context, _ time.Duration) error { return nil }

// newTestDB builds an in-memory SQLite DB with a trip holding one located stop.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.ConstructionCheck{}, &models.DiscoveryCache{}); err != nil {
		t.Fatal(err)
	}
	tripData := map[string]any{
		"locations": map[string]any{
			"kingman": map[string]any{
				"name": "Kingman, Arizona",
				"lat":  35.1894,
				"lon":  -114.0530,
			},
		},
	}
	dataBytes, _ := json.Marshal(tripData)
	dataStr := string(dataBytes)
	if err := db.Create(&models.Trip{ID: "test-trip", Name: "Test Trip", Data: &dataStr}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// runCheckSync starts a check and waits for it.
func runCheckSync(t *testing.T, svc *Service) {
	t.Helper()
	job := svc.StartCheck("test-user", CheckRequest{TripID: "test-trip", LocationIDs: []string{"kingman"}})
	<-job.Done()
	if job.Status() != leo.JobDone {
		t.Fatalf("expected job status done, got %s", job.Status())
	}
}

// taggedCategories counts the categories that actually hit Overpass.
func taggedCategories() int {
	n := 0
	for _, c := range Categories {
		if len(c.Tags) > 0 {
			n++
		}
	}
	return n
}

func TestScoreCategoryTrains150m(t *testing.T) {
	// Train at 150m from reference point -> ELEVE
	cat := *CategoryByID("trains")
	refLat, refLon := 35.1894, -114.0530

	// Place a train item ~150m away (approximately 0.0013 degrees latitude = 150m)
	items := []discovery.Item{
		{ID: "osm:way:1", Name: "BNSF Railway", Lat: refLat + 0.00135, Lon: refLon},
	}

	result := ScoreCategory(cat, items, refLat, refLon)
	if result.Level != LevelEleve {
		t.Errorf("expected level %s, got %s (distance=%.0fm)", LevelEleve, result.Level, result.Distance)
	}
	if result.Category != "trains" {
		t.Errorf("expected category trains, got %s", result.Category)
	}
}

func TestScoreCategoryAirports5km(t *testing.T) {
	// Airport at 5km from reference -> MODERE (between 3km and 8km)
	cat := *CategoryByID("airports")
	refLat, refLon := 35.1894, -114.0530

	// Place an airport ~5km away (approximately 0.045 degrees latitude = 5km)
	items := []discovery.Item{
		{ID: "osm:node:2", Name: "Kingman Airport", Lat: refLat + 0.045, Lon: refLon},
	}

	result := ScoreCategory(cat, items, refLat, refLon)
	if result.Level != LevelModere {
		t.Errorf("expected level %s, got %s (distance=%.0fm)", LevelModere, result.Level, result.Distance)
	}
}

func TestGlobalVerdictOneRed(t *testing.T) {
	results := []CategoryResult{
		{Category: "trains", Level: LevelEleve},
		{Category: "airports", Level: LevelFaible},
		{Category: "highways", Level: LevelModere},
		{Category: "nightlife", Level: LevelFaible},
		{Category: "industrial", Level: LevelFaible},
		{Category: "security", Level: LevelFaible},
	}
	verdict := GlobalVerdict(results)
	if verdict != LevelEleve {
		t.Errorf("expected %s, got %s", LevelEleve, verdict)
	}
}

func TestGlobalVerdictAllGreen(t *testing.T) {
	results := []CategoryResult{
		{Category: "trains", Level: LevelFaible},
		{Category: "airports", Level: LevelFaible},
		{Category: "highways", Level: LevelFaible},
		{Category: "nightlife", Level: LevelFaible},
		{Category: "industrial", Level: LevelFaible},
		{Category: "security", Level: LevelFaible},
	}
	verdict := GlobalVerdict(results)
	if verdict != LevelFaible {
		t.Errorf("expected %s, got %s", LevelFaible, verdict)
	}
}

func TestServiceCheckWithMocks(t *testing.T) {
	// Set up in-memory SQLite.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&models.Trip{}, &models.ConstructionCheck{})

	// Create a trip with location data.
	tripData := map[string]any{
		"locations": map[string]any{
			"kingman": map[string]any{
				"name": "Kingman, Arizona",
				"lat":  35.1894,
				"lon":  -114.0530,
			},
		},
	}
	dataBytes, _ := json.Marshal(tripData)
	dataStr := string(dataBytes)
	trip := models.Trip{ID: "test-trip", Name: "Test Trip", Data: &dataStr}
	db.Create(&trip)

	// Mock querier: returns a train item at ~150m for the trains theme.
	querier := &mockQuerier{
		items: map[string][]discovery.Item{
			"nuisance-trains": {
				{ID: "osm:way:1", Name: "BNSF Railway", Lat: 35.1894 + 0.00135, Lon: -114.0530},
			},
		},
	}

	// Mock completer: returns a valid synthesis response.
	completer := bifrost.CompleteFn(func(system, user string) (string, error) {
		return `{"recommendations": [{"locationId": "kingman", "text": "Trains BNSF proches.", "alternatives": ["Route 66 Motel"]}]}`, nil
	})

	svc := &Service{
		DB:       db,
		Overpass: querier,
		Bifrost:  completer,
		Hub:      leo.NewHub(),
		Sleep:    noPace,
	}

	// Start a check job.
	job := svc.StartCheck("test-user", CheckRequest{
		TripID:      "test-trip",
		LocationIDs: []string{"kingman"},
	})

	// Wait for job to finish.
	<-job.Done()

	if job.Status() != leo.JobDone {
		t.Fatalf("expected job status done, got %s", job.Status())
	}

	// Verify stored result.
	result, err := svc.GetResult("test-trip", "kingman")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if result.Verdict != LevelEleve {
		t.Errorf("expected verdict %s, got %s", LevelEleve, result.Verdict)
	}
	if result.Recommendation != "Trains BNSF proches." {
		t.Errorf("expected recommendation from Bifrost, got %q", result.Recommendation)
	}

	// Verify categories are present.
	if len(result.Categories) != 6 {
		t.Errorf("expected 6 categories, got %d", len(result.Categories))
	}
}

func TestScoreCategoryNightlifeCount(t *testing.T) {
	cat := *CategoryByID("nightlife")
	refLat, refLon := 48.8566, 2.3522 // Paris

	// 3 bars within 200m -> MODERE (2-5)
	items := []discovery.Item{
		{ID: "osm:node:10", Name: "Bar A", Lat: refLat + 0.0005, Lon: refLon},
		{ID: "osm:node:11", Name: "Bar B", Lat: refLat - 0.0005, Lon: refLon},
		{ID: "osm:node:12", Name: "Bar C", Lat: refLat, Lon: refLon + 0.0005},
	}

	result := ScoreCategory(cat, items, refLat, refLon)
	if result.Level != LevelModere {
		t.Errorf("expected %s for 3 bars, got %s", LevelModere, result.Level)
	}
	if result.Count != 3 {
		t.Errorf("expected count 3, got %d", result.Count)
	}
}

func TestScoreCategoryNightlifeRed(t *testing.T) {
	cat := *CategoryByID("nightlife")
	refLat, refLon := 48.8566, 2.3522

	// 7 bars -> ELEVE (>5)
	items := make([]discovery.Item, 7)
	for i := range items {
		items[i] = discovery.Item{
			ID:   "osm:node:" + formatInt(i+20),
			Name: "Club " + formatInt(i+1),
			Lat:  refLat + float64(i)*0.0001,
			Lon:  refLon,
		}
	}

	result := ScoreCategory(cat, items, refLat, refLon)
	if result.Level != LevelEleve {
		t.Errorf("expected %s for 7 bars, got %s", LevelEleve, result.Level)
	}
}

func TestUnavailableCategory_IsIndetermine(t *testing.T) {
	cat := *CategoryByID("trains")
	got := UnavailableCategory(cat)
	if got.Level != LevelIndetermine {
		t.Errorf("level=%s, want %s", got.Level, LevelIndetermine)
	}
	if !got.Unavailable {
		t.Error("Unavailable must be true for a failed query")
	}
	if got.Detail != "Donnée indisponible (Overpass injoignable)." {
		t.Errorf("unexpected detail %q", got.Detail)
	}
}

func TestGlobalVerdict_IndetermineOutranksModere(t *testing.T) {
	results := []CategoryResult{
		{Category: "trains", Level: LevelIndetermine, Unavailable: true},
		{Category: "highways", Level: LevelModere},
		{Category: "security", Level: LevelFaible},
	}
	if got := GlobalVerdict(results); got != LevelIndetermine {
		t.Errorf("verdict=%s, want %s", got, LevelIndetermine)
	}
	if got := VerdictEmoji(LevelIndetermine); got == "🟢" {
		t.Error("INDETERMINE must not render as the green emoji")
	}
}

func TestGlobalVerdict_EleveOutranksIndetermine(t *testing.T) {
	results := []CategoryResult{
		{Category: "trains", Level: LevelIndetermine, Unavailable: true},
		{Category: "highways", Level: LevelEleve},
	}
	if got := GlobalVerdict(results); got != LevelEleve {
		t.Errorf("verdict=%s, want %s", got, LevelEleve)
	}
}

// A total Overpass outage must never surface as a green verdict.
func TestServiceCheck_OverpassFailure_IndetermineAndIncomplete(t *testing.T) {
	db := newTestDB(t)
	querier := &mockQuerier{err: errors.New("overpass HTTP 429")}
	svc := &Service{DB: db, Overpass: querier, Hub: leo.NewHub(), Sleep: noPace}

	runCheckSync(t, svc)

	result, err := svc.GetResult("test-trip", "kingman")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if result.Verdict != LevelIndetermine {
		t.Errorf("verdict=%s, want %s", result.Verdict, LevelIndetermine)
	}
	if !result.Incomplete {
		t.Error("Incomplete must be true when a category failed")
	}
	if len(result.FailedCategories) != taggedCategories() {
		t.Errorf("failedCategories=%v, want %d entries", result.FailedCategories, taggedCategories())
	}
	for _, cat := range result.Categories {
		if cat.Category == "security" {
			continue // placeholder category, never queried
		}
		if !cat.Unavailable || cat.Level != LevelIndetermine {
			t.Errorf("category %s: level=%s unavailable=%v, want INDETERMINE/true", cat.Category, cat.Level, cat.Unavailable)
		}
	}
}

// A failed query must not be cached: a transient outage must not freeze a
// wrong verdict for the whole TTL (same bug fixed for discovery in PR #60).
func TestServiceCheck_FailedQueryIsNotCached(t *testing.T) {
	db := newTestDB(t)
	querier := &mockQuerier{err: errors.New("overpass HTTP 504")}
	svc := &Service{DB: db, Overpass: querier, Hub: leo.NewHub(), Sleep: noPace}

	runCheckSync(t, svc)

	var rows int64
	if err := db.Model(&models.DiscoveryCache{}).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("failed queries were cached: %d rows", rows)
	}

	// The next run must query again rather than serve a cached failure.
	before := querier.callCount()
	runCheckSync(t, svc)
	if querier.callCount() != before*2 {
		t.Errorf("second run issued %d queries in total, want %d", querier.callCount(), before*2)
	}
}

func TestServiceCheck_CacheHitAvoidsSecondOverpassCall(t *testing.T) {
	db := newTestDB(t)
	querier := &mockQuerier{items: map[string][]discovery.Item{
		"nuisance-trains": {{ID: "osm:way:1", Name: "BNSF Railway", Lat: 35.1894 + 0.00135, Lon: -114.0530}},
	}}
	svc := &Service{DB: db, Overpass: querier, Hub: leo.NewHub(), Sleep: noPace}

	runCheckSync(t, svc)
	first := querier.callCount()
	if first != taggedCategories() {
		t.Fatalf("first run issued %d queries, want %d", first, taggedCategories())
	}

	runCheckSync(t, svc)
	if got := querier.callCount(); got != first {
		t.Errorf("second run issued %d extra queries, want 0 (cache miss)", got-first)
	}

	// Cached data must still produce the same verdict.
	result, err := svc.GetResult("test-trip", "kingman")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if result.Verdict != LevelEleve {
		t.Errorf("verdict=%s, want %s", result.Verdict, LevelEleve)
	}
	if result.Incomplete {
		t.Error("Incomplete must be false when every query succeeded")
	}
}

func TestServiceCheck_ExpiredCacheRequeries(t *testing.T) {
	db := newTestDB(t)
	querier := &mockQuerier{}
	now := time.Now()
	svc := &Service{DB: db, Overpass: querier, Hub: leo.NewHub(), Sleep: noPace, Now: func() time.Time { return now }}

	runCheckSync(t, svc)
	first := querier.callCount()

	// Move the clock past the TTL: the cache must no longer serve.
	now = now.Add(cacheTTL + time.Minute)
	runCheckSync(t, svc)
	if got := querier.callCount(); got != first*2 {
		t.Errorf("after TTL expiry total queries=%d, want %d", got, first*2)
	}
}

func TestConcurrencyRespectsOverpassSlots(t *testing.T) {
	if concurrency != 1 {
		t.Errorf("concurrency=%d, want 1 (nuisance queries Overpass serially; the frontend reads the job asynchronously)", concurrency)
	}
}

func TestScoreCategorySecurity(t *testing.T) {
	cat := *CategoryByID("security")
	// Security always returns FAIBLE (placeholder).
	result := ScoreCategory(cat, nil, 35.0, -114.0)
	if result.Level != LevelFaible {
		t.Errorf("expected %s for security, got %s", LevelFaible, result.Level)
	}
}
