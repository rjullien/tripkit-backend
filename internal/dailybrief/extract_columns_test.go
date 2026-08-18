package dailybrief

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExtractDay_UsesColumnsWhenJSONStripped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:extractcols?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}); err != nil {
		t.Fatal(err)
	}
	start := "2026-08-14"
	stripped, _ := json.Marshal(map[string]any{
		"hotels":    map[string]any{},
		"locations": map[string]any{},
	})
	s := string(stripped)
	on := true
	group := "120363000000000001@g.us"
	home := "America/Toronto"
	if err := db.Create(&models.Trip{
		ID: "qc-cols", Name: "Québec", StartDate: &start, Data: &s,
		DailyBrief: &on, WhatsappGroup: &group, HomeTz: &home,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{"title": "J4 — Québec"})
	if err := db.Create(&models.Day{TripID: "qc-cols", DayNum: 4, Data: string(day)}).Error; err != nil {
		t.Fatal(err)
	}

	src, err := ExtractDayOpts(db, "qc-cols", 4, ExtractOpts{RequireConfigured: true})
	if err != nil {
		t.Fatalf("extract must use columns after JSON wipe: %v", err)
	}
	if src.WhatsAppGroup != group {
		t.Fatalf("group=%q", src.WhatsAppGroup)
	}
	if src.Timezone != home {
		t.Fatalf("timezone=%q want homeTz column %q", src.Timezone, home)
	}
}

func TestDayTimezone_FallsBackToHomeTzColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:tzcols?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}); err != nil {
		t.Fatal(err)
	}
	stripped := `{"locations":{}}`
	home := "America/Toronto"
	trip := models.Trip{ID: "tz-cols", Data: &stripped, HomeTz: &home}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Day{TripID: "tz-cols", DayNum: 1, Data: `{"title":"J1"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if got := DayTimezone(db, trip, 1); got != home {
		t.Fatalf("DayTimezone=%q want column homeTz %q", got, home)
	}
}
