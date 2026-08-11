package dailybrief

import (
	"strings"
	"testing"
)

func TestSelectDayTips_QuebecStayRainNoKids(t *testing.T) {
	data := &DayBriefData{
		PlaceName: "Québec City",
		Label:     "Québec City — Journée complète",
		HasKids:   false,
		TravelDay: false,
		Highlights: []string{
			"Petit-Champlain",
			"Chutes Montmorency",
		},
		Weather: map[string]any{"conditions": "Pluie", "weatherCode": 65},
		Hotel:   map[string]any{"name": "Lofts", "checkin": "16:00"},
	}
	SelectDayTips(data)
	if data.PracticalTip == nil || data.PracticalTip.Text == "" {
		t.Fatal("practical tip mandatory")
	}
	if data.CultureExpress == nil || data.CultureExpress.Text == "" {
		t.Fatal("culture express expected")
	}
	low := strings.ToLower(data.CultureExpress.Text)
	if !strings.Contains(low, "bienvenue") && !strings.Contains(low, "pourboire") {
		t.Fatalf("quebec culture tip expected, got %q", data.CultureExpress.Text)
	}
	for _, tip := range data.Tips {
		if tip.Kind == "famille" {
			t.Fatal("no famille tip when hasKids=false")
		}
	}
	if len(data.Tips) > maxOptionalTips {
		t.Fatalf("too many tips: %d", len(data.Tips))
	}
}

func TestSelectDayTips_TravelDay(t *testing.T) {
	data := &DayBriefData{
		PlaceName: "Québec",
		From:      "Montréal",
		Dist:      "250 km",
		Duration:  "3h",
		TravelDay: true,
		HasKids:   false,
	}
	SelectDayTips(data)
	if data.PracticalTip == nil {
		t.Fatal("practical required")
	}
	low := strings.ToLower(data.PracticalTip.Text)
	if !strings.Contains(low, "250") && !strings.Contains(low, "trajet") && !strings.Contains(low, "route") {
		t.Fatalf("travel practical tip expected, got %q", data.PracticalTip.Text)
	}
}

func TestIsTravelDay(t *testing.T) {
	if !isTravelDay(map[string]any{"dist": "250 km", "dur": "3h"}, "🚗 Montréal → Québec") {
		t.Fatal("expected travel")
	}
	if isTravelDay(map[string]any{"dist": "Local", "dur": "-"}, "Québec City — Journée complète (2026-08-15 → 2026-08-18)") {
		t.Fatal("expected stay (date range arrow must not imply travel)")
	}
}
