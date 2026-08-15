package nuisance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// mockQuerier implements discovery.Querier for testing.
type mockQuerier struct {
	items map[string][]discovery.Item // themeID -> items
}

func (m *mockQuerier) Search(_ context.Context, lat, lon float64, theme discovery.Theme) ([]discovery.Item, error) {
	if m.items == nil {
		return nil, nil
	}
	return m.items[theme.ID], nil
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

func TestScoreCategorySecurity(t *testing.T) {
	cat := *CategoryByID("security")
	// Security always returns FAIBLE (placeholder).
	result := ScoreCategory(cat, nil, 35.0, -114.0)
	if result.Level != LevelFaible {
		t.Errorf("expected %s for security, got %s", LevelFaible, result.Level)
	}
}
