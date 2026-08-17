package dailybrief

import (
	"fmt"
	"math"
	"strings"
)

// Port of tripkit-seeds seed-qa.py §10 geo_backtracking (Kiro, 2026-08-17 bba1f01).
// Haversine + nearest-neighbour (origin = first point). Warn if > 30% detour.
// Pedestrian days are included (no 5 km floor).
const (
	geoBacktrackThreshold = 1.30
	geoEarthRadiusKm      = 6371.0
)

// RouteOrder is the deterministic timeline geo-order check for the WhatsApp brief.
type RouteOrder struct {
	Optimal   bool     `json:"optimal"`
	ActualKm  float64  `json:"actualKm"`
	OptimalKm float64  `json:"optimalKm"`
	DetourPct int      `json:"detourPct,omitempty"`
	Stops     int      `json:"stops"`
	Suggested []string `json:"suggested,omitempty"`
	Paragraph string   `json:"paragraph"`
}

type geoStop struct {
	lat, lon float64
	label    string
}

func geoHaversineKm(a, b geoStop) float64 {
	rlat1 := a.lat * math.Pi / 180
	rlat2 := b.lat * math.Pi / 180
	dlat := (b.lat - a.lat) * math.Pi / 180
	dlon := (b.lon - a.lon) * math.Pi / 180
	x := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return geoEarthRadiusKm * 2 * math.Atan2(math.Sqrt(x), math.Sqrt(1-x))
}

func geoTotalDistance(stops []geoStop) float64 {
	total := 0.0
	for i := 0; i < len(stops)-1; i++ {
		total += geoHaversineKm(stops[i], stops[i+1])
	}
	return total
}

// geoNearestNeighbour returns distance + visit order, first point fixed (Kiro).
func geoNearestNeighbour(stops []geoStop) (float64, []geoStop) {
	if len(stops) < 2 {
		return 0, stops
	}
	visited := []geoStop{stops[0]}
	remaining := append([]geoStop(nil), stops[1:]...)
	total := 0.0
	for len(remaining) > 0 {
		last := visited[len(visited)-1]
		bestIdx := 0
		bestDist := geoHaversineKm(last, remaining[0])
		for j := 1; j < len(remaining); j++ {
			d := geoHaversineKm(last, remaining[j])
			if d < bestDist {
				bestDist = d
				bestIdx = j
			}
		}
		total += bestDist
		visited = append(visited, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return total, visited
}

func timelineGeoStops(timeline []map[string]any) []geoStop {
	var out []geoStop
	for _, entry := range timeline {
		lat, lok := asFloat(entry["lat"])
		lon, nok := asFloat(entry["lon"])
		if !lok || !nok {
			continue
		}
		if len(out) > 0 && out[len(out)-1].lat == lat && out[len(out)-1].lon == lon {
			continue
		}
		label := firstString(entry, "place", "label", "d", "title")
		if label == "" {
			label = fmt.Sprintf("%.3f,%.3f", lat, lon)
		}
		out = append(out, geoStop{lat: lat, lon: lon, label: compactStopLabel(label)})
	}
	return out
}

func compactStopLabel(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " ("); i > 8 {
		s = strings.TrimSpace(s[:i])
	}
	runes := []rune(s)
	if len(runes) > 42 {
		s = strings.TrimSpace(string(runes[:40])) + "…"
	}
	return s
}

// AssessTimelineOrder ports seed-qa geo_backtracking. Nil when the check does not apply.
func AssessTimelineOrder(timeline []map[string]any) *RouteOrder {
	stops := timelineGeoStops(timeline)
	if len(stops) < 3 {
		return nil
	}
	actual := geoTotalDistance(stops)
	optimal, order := geoNearestNeighbour(stops)
	if optimal < 0.5 {
		return nil
	}
	ratio := actual / optimal
	pct := int((ratio - 1) * 100)
	if pct < 0 {
		pct = 0
	}
	out := &RouteOrder{
		Optimal:   ratio <= geoBacktrackThreshold,
		ActualKm:  actual,
		OptimalKm: optimal,
		DetourPct: pct,
		Stops:     len(stops),
	}
	if out.Optimal {
		out.Paragraph = "🗺️ *Ordre des étapes*\nL'ordre du programme est *optimal* (trajet proche du plus court à partir du premier arrêt)."
		return out
	}
	var names []string
	for _, s := range order {
		names = append(names, s.label)
	}
	out.Suggested = names
	out.Paragraph = fmt.Sprintf(
		"🗺️ *Ordre des étapes*\nL'enchaînement n'est *pas optimal* : l'ordre des activités parcourt %.1f km, un réordonnancement en ferait %.1f km (%d%% de détour). Plus court : %s.",
		actual, optimal, pct, strings.Join(names, " → "),
	)
	return out
}
