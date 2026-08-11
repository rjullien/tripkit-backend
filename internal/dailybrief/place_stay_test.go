package dailybrief

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPlaceStayWindow_ContiguousLocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:place_stay?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Day{})

	// Québec J2–J4, then Baie-Saint-Paul J5 (mirrors boucle Québec).
	seed := []struct {
		n   int
		loc string
	}{
		{1, "montreal"},
		{2, "quebec"},
		{3, "quebec"},
		{4, "quebec"},
		{5, "baie-saint-paul"},
	}
	for _, s := range seed {
		raw, _ := json.Marshal(map[string]any{"locationId": s.loc, "title": s.loc})
		if err := db.Create(&models.Day{TripID: "qc", DayNum: s.n, Data: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}

	from, to := PlaceStayWindow(db, "qc", 3, "quebec", "2026-08-14")
	if from != "2026-08-15" || to != "2026-08-17" {
		t.Fatalf("J3 quebec stay want 2026-08-15…2026-08-17 got %s…%s", from, to)
	}
	from, to = PlaceStayWindow(db, "qc", 2, "quebec", "2026-08-14")
	if from != "2026-08-15" || to != "2026-08-17" {
		t.Fatalf("J2 quebec stay want same window got %s…%s", from, to)
	}
	from, to = PlaceStayWindow(db, "qc", 5, "baie-saint-paul", "2026-08-14")
	if from != "2026-08-18" || to != "2026-08-18" {
		t.Fatalf("J5 single-day stay want 2026-08-18 got %s…%s", from, to)
	}
}

func TestNewsVagueDeny_Listicles(t *testing.T) {
	junk := []string{
		"Six sorties gratuites à faire en août",
		"20 shows à voir en août 2026",
		"Que faire en août à Québec",
	}
	for _, title := range junk {
		if !newsVagueDeny(title) {
			t.Fatalf("expected vague deny for %q", title)
		}
		if travelerRelevant(title) {
			t.Fatalf("listicle must not be travelerRelevant: %q", title)
		}
	}
	ok := "Les Grands Feux Loto-Québec ce soir à Lévis"
	if newsVagueDeny(ok) {
		t.Fatalf("unexpected vague deny for %q", ok)
	}
}
