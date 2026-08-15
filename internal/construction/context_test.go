package construction

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Trip{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestBuildLeoContext_WithTravelProfile(t *testing.T) {
	db := setupTestDB(t)

	data := map[string]any{
		"people": []map[string]any{
			{"name": "Alice", "nationality": "FR", "isChild": false},
			{"name": "Bob", "nationality": "US", "isChild": true, "ageLabel": "8 ans", "healthNote": "allergie noix"},
		},
		"travelProfile": map[string]any{
			"travelStyle": map[string]any{
				"pace":             "modere",
				"maxDrivingPerDay": "3h",
				"majorSitesPerDay": 2,
			},
			"budgetRules": map[string]any{
				"accommodation": map[string]any{"maxPerNight": 180, "currency": "EUR"},
				"restaurant":    map[string]any{"maxPerPerson": 50},
				"activities":    map[string]any{"maxPerPerson": 40},
			},
			"interests": map[string]any{
				"alice": map[string]any{"likes": []string{"museums", "parks"}, "dislikes": []string{"shopping"}},
				"bob":   map[string]any{"likes": []string{"trains"}, "dislikes": []string{}},
			},
		},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)

	trip := models.Trip{ID: "test-trip", Name: "Sicile 2026", Data: &s}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	ctx, err := BuildLeoContext(db, "test-trip")
	if err != nil {
		t.Fatalf("BuildLeoContext error: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// Trip name
	if ctx.TripName != "Sicile 2026" {
		t.Errorf("TripName=%q want %q", ctx.TripName, "Sicile 2026")
	}

	// Travelers
	if len(ctx.Travelers) != 2 {
		t.Fatalf("travelers count=%d want 2", len(ctx.Travelers))
	}
	if ctx.Travelers[0].Name != "Alice" {
		t.Errorf("traveler[0].Name=%q", ctx.Travelers[0].Name)
	}
	if ctx.Travelers[0].Nationality != "FR" {
		t.Errorf("traveler[0].Nationality=%q", ctx.Travelers[0].Nationality)
	}
	if ctx.Travelers[1].IsChild != true {
		t.Errorf("traveler[1].IsChild=%v", ctx.Travelers[1].IsChild)
	}
	if ctx.Travelers[1].AgeLabel != "8 ans" {
		t.Errorf("traveler[1].AgeLabel=%q", ctx.Travelers[1].AgeLabel)
	}
	if ctx.Travelers[1].HealthNote != "allergie noix" {
		t.Errorf("traveler[1].HealthNote=%q", ctx.Travelers[1].HealthNote)
	}

	// Style
	if ctx.Style == nil {
		t.Fatal("Style is nil")
	}
	if ctx.Style.Pace != "modere" {
		t.Errorf("Style.Pace=%q", ctx.Style.Pace)
	}
	if ctx.Style.MaxDrivingPerDay != "3h" {
		t.Errorf("Style.MaxDrivingPerDay=%q", ctx.Style.MaxDrivingPerDay)
	}
	if ctx.Style.MajorSitesPerDay != 2 {
		t.Errorf("Style.MajorSitesPerDay=%d", ctx.Style.MajorSitesPerDay)
	}

	// Budget
	if ctx.Budget == nil {
		t.Fatal("Budget is nil")
	}
	if ctx.Budget.AccommodationMax != 180 {
		t.Errorf("Budget.AccommodationMax=%d", ctx.Budget.AccommodationMax)
	}
	if ctx.Budget.RestaurantMax != 50 {
		t.Errorf("Budget.RestaurantMax=%d", ctx.Budget.RestaurantMax)
	}
	if ctx.Budget.ActivitiesMax != 40 {
		t.Errorf("Budget.ActivitiesMax=%d", ctx.Budget.ActivitiesMax)
	}
	if ctx.Budget.Currency != "EUR" {
		t.Errorf("Budget.Currency=%q", ctx.Budget.Currency)
	}

	// Interests
	if len(ctx.Interests) != 2 {
		t.Fatalf("interests count=%d want 2", len(ctx.Interests))
	}
	alice, ok := ctx.Interests["alice"]
	if !ok {
		t.Fatal("interests missing alice")
	}
	if len(alice.Likes) != 2 || alice.Likes[0] != "museums" {
		t.Errorf("alice.Likes=%v", alice.Likes)
	}
	if len(alice.Dislikes) != 1 || alice.Dislikes[0] != "shopping" {
		t.Errorf("alice.Dislikes=%v", alice.Dislikes)
	}
}

func TestBuildLeoContext_NoData_ReturnsNil(t *testing.T) {
	db := setupTestDB(t)

	// Trip with nil Data
	trip := models.Trip{ID: "empty-trip", Name: "Empty"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	ctx, err := BuildLeoContext(db, "empty-trip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != nil {
		t.Fatalf("expected nil context for trip without data, got %+v", ctx)
	}
}

func TestBuildLeoContext_EmptyData_ReturnsNil(t *testing.T) {
	db := setupTestDB(t)

	// Trip with empty JSON object in Data
	s := "{}"
	trip := models.Trip{ID: "no-profile-trip", Name: "No Profile", Data: &s}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	ctx, err := BuildLeoContext(db, "no-profile-trip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != nil {
		t.Fatalf("expected nil context for trip without travel profile, got %+v", ctx)
	}
}

func TestBuildLeoContext_TripNotFound_ReturnsNil(t *testing.T) {
	db := setupTestDB(t)

	ctx, err := BuildLeoContext(db, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != nil {
		t.Fatalf("expected nil for missing trip, got %+v", ctx)
	}
}

func TestBuildLeoContext_MalformedJSON_ReturnsNil(t *testing.T) {
	db := setupTestDB(t)

	s := "not valid json {"
	trip := models.Trip{ID: "bad-json", Name: "Bad", Data: &s}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	ctx, err := BuildLeoContext(db, "bad-json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != nil {
		t.Fatalf("expected nil for malformed json, got %+v", ctx)
	}
}

func TestBuildLeoContext_PeopleOnly(t *testing.T) {
	db := setupTestDB(t)

	data := map[string]any{
		"people": []map[string]any{
			{"name": "Solo", "nationality": "DE"},
		},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)

	trip := models.Trip{ID: "people-only", Name: "People Only", Data: &s}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	ctx, err := BuildLeoContext(db, "people-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected non-nil context when people are present")
	}
	if len(ctx.Travelers) != 1 || ctx.Travelers[0].Name != "Solo" {
		t.Errorf("travelers=%+v", ctx.Travelers)
	}
	if ctx.Style != nil {
		t.Errorf("expected nil style, got %+v", ctx.Style)
	}
	if ctx.Budget != nil {
		t.Errorf("expected nil budget, got %+v", ctx.Budget)
	}
}
