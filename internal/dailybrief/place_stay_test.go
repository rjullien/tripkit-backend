package dailybrief

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func seedPresenceTrip(t *testing.T, db *gorm.DB) {
	t.Helper()
	// Mirrors boucle Québec J1–J6 enough for presence.
	days := []struct {
		n    int
		data map[string]any
	}{
		{1, map[string]any{"locationId": "montreal", "from": "Nice", "to": "Montréal", "dist": "flight", "title": "✈ Nice → Montréal"}},
		{2, map[string]any{
			"locationId": "quebec", "from": "Montréal", "to": "Québec", "dist": "250 km", "dur": "3h",
			"title": "🚗 Montréal → Québec",
			"timeline": []map[string]any{
				{"t": "10:00", "d": "🚗 Pickup Avis"},
				{"t": "11:00", "d": "🚗 Départ Montréal → Québec"},
				{"t": "15:00", "d": "🏠 Installation loft ilewa"},
				{"t": "19:00", "d": "🍽️ Dîner Vieux-Québec"},
			},
		}},
		{3, map[string]any{
			"locationId": "quebec", "from": "Québec", "dist": "Local", "dur": "-",
			"timeline": []map[string]any{{"t": "09:00", "d": "🚶 Petit-Champlain"}},
		}},
		{4, map[string]any{
			"locationId": "quebec", "from": "Québec", "dist": "Local", "dur": "-",
			"timeline": []map[string]any{{"t": "08:30", "d": "Petit-déjeuner"}},
		}},
		{5, map[string]any{
			"locationId": "baie-saint-paul", "from": "Québec", "to": "Baie-Saint-Paul", "dist": "95 km", "dur": "1h15",
			"title": "🚗 Québec → Baie-Saint-Paul",
			"timeline": []map[string]any{
				{"t": "09:30", "d": "🚗 Route panoramique fleuve"},
				{"t": "11:00", "d": "🎨 Arrivée Baie-Saint-Paul"},
				{"t": "19:00", "d": "🍽️ Dîner au village"},
			},
		}},
		{6, map[string]any{
			"locationId": "riviere-eternite", "from": "Baie-Saint-Paul", "to": "Rivière-Éternité", "dist": "180 km", "dur": "2h25",
			"timeline": []map[string]any{
				{"t": "09:00", "d": "🚗 Route vers Saguenay"},
				{"t": "12:00", "d": "🏠 Arrivée hébergement"},
			},
		}},
	}
	for _, d := range days {
		raw, _ := json.Marshal(d.data)
		if err := db.Create(&models.Day{TripID: "qc", DayNum: d.n, Data: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaceStayWindow_ContiguousLocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:place_stay?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Day{})
	seedPresenceTrip(t, db)

	from, to := PlaceStayWindow(db, "qc", 3, "quebec", "2026-08-14")
	if from != "2026-08-15" || to != "2026-08-17" {
		t.Fatalf("J3 quebec overnight want 2026-08-15…2026-08-17 got %s…%s", from, to)
	}
}

func TestResolveActuPresence_QuebecStayIncludesMorningDepart(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:presence_qc?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Day{})
	seedPresenceTrip(t, db)

	// On J3 (on site): Québec from J2 15:00 install → J5 09:30 depart
	p := ResolveActuPresence(db, "qc", 3, "2026-08-14")
	if p.LocationID != "quebec" || p.Focus != "on_site" {
		t.Fatalf("J3 focus %#v", p)
	}
	if p.FromDate != "2026-08-15" || p.FromTime != "15:00" {
		t.Fatalf("J3 from want 2026-08-15 15:00 got %s %s", p.FromDate, p.FromTime)
	}
	if p.ToDate != "2026-08-18" || p.ToTime != "09:30" {
		t.Fatalf("J3 to want 2026-08-18 09:30 (leave Québec morning) got %s %s", p.ToDate, p.ToTime)
	}
}

func TestResolveActuPresence_MorningDepartFocusArrival(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:presence_arrive?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Day{})
	seedPresenceTrip(t, db)

	// J5 leave Québec 09:30 → Actualité = Baie-Saint-Paul, not Québec evening shows
	p := ResolveActuPresence(db, "qc", 5, "2026-08-14")
	if p.Focus != "arrival" || p.LocationID != "baie-saint-paul" {
		t.Fatalf("J5 want arrival BSP got %#v", p)
	}
	if p.PlaceName != "Baie-Saint-Paul" {
		t.Fatalf("place name %#v", p.PlaceName)
	}
	if p.FromDate != "2026-08-18" || p.FromTime != "11:00" {
		t.Fatalf("BSP from want 2026-08-18 11:00 got %s %s", p.FromDate, p.FromTime)
	}
	if p.ToDate != "2026-08-19" || p.ToTime != "09:00" {
		t.Fatalf("BSP to want 2026-08-19 09:00 got %s %s", p.ToDate, p.ToTime)
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
	}
	ok := "Les Grands Feux Loto-Québec ce soir à Lévis"
	if newsVagueDeny(ok) {
		t.Fatalf("unexpected vague deny for %q", ok)
	}
}
