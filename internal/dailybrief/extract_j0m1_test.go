package dailybrief

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExtractDay_SyntheticJ0m1_NoSeedDayMinus1(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:j0m1synth?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Trip{}, &models.Day{})
	start := "2026-08-14"
	td, _ := json.Marshal(map[string]any{
		"dailyBrief": true, "whatsappGroup": "120363000000000001@g.us",
		"homeTz": "Europe/Paris",
		"locations": map[string]any{"nice": map[string]any{"tz": "Europe/Paris"}},
	})
	tds := string(td)
	_ = db.Create(&models.Trip{ID: "qc", Name: "Québec", StartDate: &start, Data: &tds}).Error
	dd, _ := json.Marshal(map[string]any{
		"label": "Préparation & valises (Nice)", "locationId": "nice",
		"timeline": []map[string]any{
			{"t": "11:00", "d": "🧳 Préparation des valises"},
			{"t": "18:00", "d": "🔌 Charge + télécharger apps"},
			{"t": "21:00", "d": "😴 Coucher tôt — vol demain 09:10"},
		},
	})
	_ = db.Create(&models.Day{TripID: "qc", DayNum: 0, Data: string(dd)}).Error
	// Intentionally NO day_num=-1 row.

	src, err := ExtractDayOpts(db, "qc", -1, ExtractOpts{RequireConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if src.DayNumber != -1 {
		t.Fatalf("dayNumber=%d", src.DayNumber)
	}
	if src.Date != "2026-08-12" {
		t.Fatalf("date=%s want 2026-08-12", src.Date)
	}
	if !strings.Contains(src.Label, "J0-1") {
		t.Fatalf("want synthetic J0-1 label, got %q", src.Label)
	}
	for _, e := range src.Timeline {
		lab, _ := e["label"].(string)
		low := strings.ToLower(lab)
		if strings.Contains(low, "coucher") || strings.Contains(low, "vol demain") {
			t.Fatalf("veille-only timeline leaked into J0-1: %q", lab)
		}
	}
}
