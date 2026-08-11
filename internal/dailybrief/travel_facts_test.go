package dailybrief

import (
	"strings"
	"testing"
)

func TestRoutePlaceHint_FromTimeline(t *testing.T) {
	data := &DayBriefData{
		From: "Québec", To: "Baie-Saint-Paul", Dist: "95 km", Duration: "1h15", TravelDay: true,
		Timeline: []map[string]any{
			{"time": "09:30", "label": "🚗 Route panoramique fleuve (1h30)"},
			{"time": "11:00", "label": "🎨 Arrivée Baie-Saint-Paul"},
		},
	}
	if got := routePlaceHint(data); got != "Fleuve Saint-Laurent" {
		t.Fatalf("want fleuve hint, got %q", got)
	}
}

func TestEnrichTravelPlaceFacts_SegmentsWithoutWiki(t *testing.T) {
	data := &DayBriefData{
		From: "AlphaCityXYZ", To: "BetaCityXYZ", Dist: "95 km", Duration: "1h15", TravelDay: true,
		Timeline: []map[string]any{
			{"time": "09:30", "label": "🚗 Route panoramique fleuve"},
		},
	}
	enrichTravelPlaceFacts(data)
	if len(data.PlaceFacts) == 0 {
		t.Fatal("expected at least trajet fact")
	}
	foundRoute := false
	for _, f := range data.PlaceFacts {
		if strings.Contains(f, "Trajet") {
			foundRoute = true
			break
		}
	}
	if !foundRoute {
		t.Fatalf("expected Trajet line in %#v", data.PlaceFacts)
	}
	if data.PlaceFactsBySegment == nil || len(data.PlaceFactsBySegment["route"]) == 0 {
		t.Fatalf("segment route missing: %#v", data.PlaceFactsBySegment)
	}
}
