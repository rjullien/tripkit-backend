package pluschat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBuildTripContext_TodayTomorrowHotel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:pluschat_ctx?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.Hotel{})

	start, end := "2026-08-14", "2026-09-01"
	tripData := map[string]any{
		"homeTz": "America/Toronto",
		"hotels": map[string]any{
			"quebec": map[string]any{
				"name":               "Les Lofts",
				"addr":               "20 Côte",
				"pin":                "4360",
				"checkin":            "16:00",
				"confirmationNumber": "6005444671",
			},
		},
		"locations": map[string]any{
			"quebec": map[string]any{"lat": 46.8, "lon": -71.2, "tz": "America/Toronto"},
		},
	}
	raw, _ := json.Marshal(tripData)
	s := string(raw)
	_ = db.Create(&models.Trip{
		ID: "quebec-2026", Name: "QC", StartDate: &start, EndDate: &end, Data: &s,
	}).Error

	day0, _ := json.Marshal(map[string]any{
		"day": 0, "label": "Veille", "locationId": "quebec", "hotelId": "quebec",
		"timeline": []any{map[string]any{"t": "20:00", "d": "Préparer"}},
	})
	_ = db.Create(&models.Day{TripID: "quebec-2026", DayNum: 0, Data: string(day0)}).Error
	day1, _ := json.Marshal(map[string]any{
		"day": 1, "label": "Arrivée Montréal", "locationId": "quebec", "hotelId": "quebec",
	})
	_ = db.Create(&models.Day{TripID: "quebec-2026", DayNum: 1, Data: string(day1)}).Error

	loc, _ := time.LoadLocation("America/Toronto")
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, loc) // day 0 today, day 1 tomorrow

	ctx, err := BuildTripContext(db, "quebec-2026", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Today == nil || ctx.Today.DayNumber != 0 {
		t.Fatalf("today=%+v", ctx.Today)
	}
	if ctx.Tomorrow == nil || ctx.Tomorrow.DayNumber != 1 {
		t.Fatalf("tomorrow=%+v", ctx.Tomorrow)
	}
	hotel, _ := ctx.Today.Bookings["hotel"].(map[string]any)
	if hotel == nil || hotel["pin"] != "4360" {
		t.Fatalf("hotel bookings=%v", ctx.Today.Bookings)
	}
	sys := SystemPrompt(PromptContext{Username: "rene", TripID: "quebec-2026", Trip: ctx})
	for _, want := range []string{"4360", "CONTEXTE_JSON", "Les Lofts"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}
