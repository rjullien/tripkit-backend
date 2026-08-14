package polarsteps

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
)

func TestBuildInput_TruncatesEveningAndStripsPNR(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"homeTz":     "Europe/Paris",
		"polarsteps": map[string]any{"enabled": true},
		"travelers":  []any{map[string]any{"personId": "rene"}},
		"phases":     []any{map[string]any{"name": "Québec & Charlevoix"}},
		"locations":  map[string]any{"montreal": map[string]any{"tz": "America/Toronto"}},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec 2026",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{
		"label": "Vol Nice → Montréal", "from": "Nice", "to": "Montréal",
		"locationId": "montreal",
		"timeline": []any{
			map[string]any{"t": "09:10", "tz": "Europe/Paris", "d": "Décollage Nice → Genève — LX523 · PNR 8WQZPY"},
			map[string]any{"t": "22:00", "tz": "America/Toronto", "d": "Soirée jazz secret"},
		},
	})
	if err := db.Create(&models.Day{TripID: "quebec-2026", DayNum: 1, Data: string(day)}).Error; err != nil {
		t.Fatal(err)
	}

	now, err := time.Parse(time.RFC3339, "2026-08-14T18:00:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	in, _, _, err := BuildInput(db, "quebec-2026", now, "escale un peu longue")
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "opening" || in.Day != 1 {
		t.Fatalf("kind=%s day=%d", in.Kind, in.Day)
	}
	if in.UserNote != "escale un peu longue" {
		t.Fatalf("note=%q", in.UserNote)
	}
	if len(in.Happened) != 1 {
		t.Fatalf("happened=%+v", in.Happened)
	}
	d := in.Happened[0].D
	if strings.Contains(d, "PNR") || strings.Contains(d, "LX523") {
		t.Fatalf("PNR leaked: %q", d)
	}
	if strings.Contains(d, "jazz") {
		t.Fatalf("future evening leaked: %q", d)
	}
	if in.Nights != 18 {
		t.Fatalf("nights=%d want 18", in.Nights)
	}
}

func TestTripActive(t *testing.T) {
	if !TripActive("2026-08-14", "2026-09-01", "2026-08-14") {
		t.Fatal("J1 should be active")
	}
	if TripActive("2026-08-14", "2026-09-01", "2026-08-13") {
		t.Fatal("J0 should not be active")
	}
}

func TestTripPolarstepsAbsent(t *testing.T) {
	g := TripPolarsteps(map[string]any{})
	if g.Enabled {
		t.Fatal("absent must be disabled at the helper (Status treats missing as on)")
	}
}

func TestTripPolarstepsNestedTrip(t *testing.T) {
	g := TripPolarsteps(map[string]any{
		"trip": map[string]any{"polarsteps": map[string]any{"enabled": true, "tripUrl": "https://polarsteps.com/x"}},
	})
	if !g.Enabled || g.TripURL == "" {
		t.Fatalf("nested: %+v", g)
	}
}
