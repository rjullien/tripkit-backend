package dailybrief

import (
	"math"
	"strings"
	"testing"
)

func TestGeoHaversine_KnownDistance(t *testing.T) {
	// Montréal → Québec City ~233 km great-circle.
	a := geoStop{lat: 45.5017, lon: -73.5673}
	b := geoStop{lat: 46.8139, lon: -71.2080}
	d := geoHaversineKm(a, b)
	if d < 220 || d > 250 {
		t.Fatalf("mtl-qc haversine=%.1f, want ~233", d)
	}
}

func TestAssessTimelineOrder_ZigzagNotOptimal(t *testing.T) {
	// A → C (far) → B (back near A): same NN-from-first as seed-qa.
	tl := []map[string]any{
		{"place": "A", "lat": 46.8, "lon": -71.2},
		{"place": "C", "lat": 47.4, "lon": -70.5},
		{"place": "B", "lat": 46.85, "lon": -71.15},
	}
	got := AssessTimelineOrder(tl)
	if got == nil {
		t.Fatal("expected assessment")
	}
	if got.Optimal {
		t.Fatalf("zigzag should not be optimal: actual=%.1f opt=%.1f", got.ActualKm, got.OptimalKm)
	}
	if got.DetourPct < 30 {
		t.Fatalf("detourPct=%d want >30 (threshold 1.30)", got.DetourPct)
	}
	if !strings.Contains(got.Paragraph, "pas optimal") {
		t.Fatalf("paragraph: %s", got.Paragraph)
	}
	if len(got.Suggested) != 3 || got.Suggested[0] != "A" {
		t.Fatalf("suggested origin-fixed: %v", got.Suggested)
	}
}

func TestAssessTimelineOrder_LinearIsOptimal(t *testing.T) {
	tl := []map[string]any{
		{"place": "Ouest", "lat": 46.80, "lon": -71.40},
		{"place": "Centre", "lat": 46.81, "lon": -71.20},
		{"place": "Est", "lat": 46.82, "lon": -71.00},
	}
	got := AssessTimelineOrder(tl)
	if got == nil {
		t.Fatal("expected assessment")
	}
	if !got.Optimal {
		t.Fatalf("linear should be optimal: actual=%.1f opt=%.1f pct=%d", got.ActualKm, got.OptimalKm, got.DetourPct)
	}
	if !strings.Contains(got.Paragraph, "optimal") || strings.Contains(got.Paragraph, "pas optimal") {
		t.Fatalf("paragraph: %s", got.Paragraph)
	}
}

func TestAssessTimelineOrder_SkipRules(t *testing.T) {
	if AssessTimelineOrder([]map[string]any{
		{"lat": 46.8, "lon": -71.2},
		{"lat": 46.81, "lon": -71.21},
	}) != nil {
		t.Fatal("< 3 points must skip")
	}
	dups := []map[string]any{
		{"lat": 46.8, "lon": -71.2},
		{"lat": 46.8, "lon": -71.2},
		{"lat": 46.8, "lon": -71.2},
	}
	if AssessTimelineOrder(dups) != nil {
		t.Fatal("consecutive duplicates collapse; not enough unique stops")
	}
}

func TestAssessTimelineOrder_WalkingDayStillChecked(t *testing.T) {
	// Vieux-Québec scale (~1 km triangle): used to be skipped by the 5 km floor.
	tl := []map[string]any{
		{"place": "Château", "lat": 46.8126, "lon": -71.2050},
		{"place": "Citadelle", "lat": 46.8076, "lon": -71.2108},
		{"place": "Petit-Champlain", "lat": 46.8122, "lon": -71.2026},
	}
	got := AssessTimelineOrder(tl)
	if got == nil {
		t.Fatal("walking day must still be assessed")
	}
	if got.ActualKm >= 5 {
		t.Fatalf("fixture should stay under 5 km, got %.2f", got.ActualKm)
	}
}

func TestAssessTimelineOrder_KiroJ3StyleDetour(t *testing.T) {
	// Same rule as seed-qa: ratio > 1.30. Synthetic island loop (out and back).
	tl := []map[string]any{
		{"place": "Québec", "lat": 46.9003, "lon": -71.134},
		{"place": "Ste-Pétronille", "lat": 46.859, "lon": -71.132},
		{"place": "Ferme Audet", "lat": 46.988, "lon": -70.82},
		{"place": "Québec retour", "lat": 46.8139, "lon": -71.208},
	}
	got := AssessTimelineOrder(tl)
	if got == nil {
		t.Fatal("expected assessment")
	}
	if got.OptimalKm < 0.5 {
		t.Fatalf("distances actual=%.1f opt=%.1f", got.ActualKm, got.OptimalKm)
	}
	ratio := got.ActualKm / got.OptimalKm
	if ratio <= geoBacktrackThreshold && !got.Optimal {
		t.Fatal("Optimal flag must match threshold")
	}
	if ratio > geoBacktrackThreshold && got.Optimal {
		t.Fatalf("ratio=%.3f should not be optimal", ratio)
	}
	if math.Abs(ratio-1)*100 < 1 && got.DetourPct != 0 {
		t.Fatalf("pct=%d", got.DetourPct)
	}
}
