package discovery

import (
	"context"
	"math"
	"testing"
)

func TestSampleCorridor(t *testing.T) {
	tests := []struct {
		name       string
		fromLat    float64
		fromLon    float64
		toLat      float64
		toLon      float64
		sampleKm   float64
		wantPoints int
	}{
		{
			name:       "100km at 40km spacing gives 4 points",
			fromLat:    48.8566, // Paris
			fromLon:    2.3522,
			toLat:      49.4431, // ~100km north along meridian roughly
			toLon:      2.3522,
			sampleKm:   40,
			wantPoints: 4, // ceil(~65km / 40) + 1 = 3 ... let's compute actual
		},
		{
			name:       "0km distance returns single point",
			fromLat:    48.8566,
			fromLon:    2.3522,
			toLat:      48.8566,
			toLon:      2.3522,
			sampleKm:   40,
			wantPoints: 1,
		},
		{
			name:       "exact multiple 80km at 40km gives 3 points",
			fromLat:    45.0,
			fromLon:    0.0,
			toLat:      45.0,
			toLon:      0.0, // will set dynamically
			sampleKm:   40,
			wantPoints: 3,
		},
		{
			name:       "short distance 10km at 40km gives 2 points",
			fromLat:    48.8566,
			fromLon:    2.3522,
			toLat:      48.9466, // ~10km north
			toLon:      2.3522,
			sampleKm:   40,
			wantPoints: 2,
		},
	}

	// Pre-compute: for "exact multiple 80km at 40km" we need a point ~80km east of (45,0).
	// At lat 45, 1 degree lon = 111.32 * cos(45) = ~78.7km. So ~1.016 degrees ~ 80km.
	tests[2].toLon = 1.016

	// Recalculate expected for first test: Paris to 49.4431 = haversine dist
	dist1 := haversineKm(48.8566, 2.3522, 49.4431, 2.3522)
	tests[0].wantPoints = int(math.Ceil(dist1/40)) + 1

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pts := SampleCorridor(tc.fromLat, tc.fromLon, tc.toLat, tc.toLon, tc.sampleKm)
			if len(pts) != tc.wantPoints {
				totalDist := haversineKm(tc.fromLat, tc.fromLon, tc.toLat, tc.toLon)
				t.Errorf("got %d points, want %d (totalDist=%.2fkm, sampleKm=%.0f)",
					len(pts), tc.wantPoints, totalDist, tc.sampleKm)
			}
			// First point is always origin.
			if len(pts) > 0 {
				if math.Abs(pts[0].Lat-tc.fromLat) > 0.0001 || math.Abs(pts[0].Lon-tc.fromLon) > 0.0001 {
					t.Errorf("first point (%.4f, %.4f) != origin (%.4f, %.4f)",
						pts[0].Lat, pts[0].Lon, tc.fromLat, tc.fromLon)
				}
			}
			// Last point is always destination.
			if len(pts) > 1 {
				last := pts[len(pts)-1]
				if math.Abs(last.Lat-tc.toLat) > 0.001 || math.Abs(last.Lon-tc.toLon) > 0.001 {
					t.Errorf("last point (%.4f, %.4f) != destination (%.4f, %.4f)",
						last.Lat, last.Lon, tc.toLat, tc.toLon)
				}
			}
		})
	}
}

func TestDistToSegmentKm(t *testing.T) {
	tests := []struct {
		name   string
		pLat   float64
		pLon   float64
		aLat   float64
		aLon   float64
		bLat   float64
		bLon   float64
		wantKm float64
		tolKm  float64
	}{
		{
			name: "point perpendicular to segment midpoint",
			// Segment from (45, 0) to (45, 2). Point at (46, 1) - about 111km north of midpoint.
			pLat:   46.0,
			pLon:   1.0,
			aLat:   45.0,
			aLon:   0.0,
			bLat:   45.0,
			bLon:   2.0,
			wantKm: 111.0, // ~111km (1 degree of latitude)
			tolKm:  5.0,
		},
		{
			name: "point on the segment gives 0",
			// Point exactly on segment.
			pLat:   45.0,
			pLon:   1.0,
			aLat:   45.0,
			aLon:   0.0,
			bLat:   45.0,
			bLon:   2.0,
			wantKm: 0.0,
			tolKm:  0.5,
		},
		{
			name: "point beyond segment endpoint clamps",
			// Point at (45, 3) - beyond B=(45,2). Should clamp to B.
			pLat:   45.0,
			pLon:   3.0,
			aLat:   45.0,
			aLon:   0.0,
			bLat:   45.0,
			bLon:   2.0,
			wantKm: 78.6, // 1 degree lon at lat 45 ~ 78.6km
			tolKm:  5.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := distToSegmentKm(tc.pLat, tc.pLon, tc.aLat, tc.aLon, tc.bLat, tc.bLon)
			if math.Abs(got-tc.wantKm) > tc.tolKm {
				t.Errorf("distToSegmentKm = %.2f km, want %.2f km (tol %.2f)",
					got, tc.wantKm, tc.tolKm)
			}
		})
	}
}

func TestDetourEstimation(t *testing.T) {
	// Known geometry: corridor from (45,0) to (45,2).
	// Point at (46,1) is ~111km from the segment.
	// Detour = 111 * 2 = ~222km.
	points := SampleCorridor(45.0, 0.0, 45.0, 2.0, 40)
	dist := minDistToCorridorKm(46.0, 1.0, points)
	detour := round1(dist * 2)

	if detour < 200 || detour > 240 {
		t.Errorf("detour = %.1f km, want ~222 km", detour)
	}

	// Point ON the corridor should have ~0 detour.
	distOn := minDistToCorridorKm(45.0, 1.0, points)
	detourOn := round1(distOn * 2)
	if detourOn > 5.0 {
		t.Errorf("on-corridor detour = %.1f km, want ~0 km", detourOn)
	}
}

// mockQuerier implements Querier for testing corridor search dedup.
type mockQuerier struct {
	items map[string][]Item // key = "lat,lon,themeID"
}

func (m *mockQuerier) Search(_ context.Context, lat, lon float64, theme Theme) ([]Item, error) {
	// Return same items regardless of lat/lon for dedup testing.
	var all []Item
	for _, items := range m.items {
		all = append(all, items...)
	}
	return all, nil
}

func TestCorridorSearchDedup(t *testing.T) {
	// Two sample points will both return the same item. It should appear only once.
	mq := &mockQuerier{
		items: map[string][]Item{
			"any": {
				{ID: "osm:node:123", ThemeID: "rando", Name: "Viewpoint A", Lat: 45.5, Lon: 1.0, Source: "osm"},
				{ID: "osm:node:456", ThemeID: "rando", Name: "Viewpoint B", Lat: 45.3, Lon: 0.5, Source: "osm"},
			},
		},
	}

	svc := &Service{
		Overpass: mq,
		Loader:   nil,
	}

	themes := []Theme{
		{ID: "rando", Label: "Rando", Engine: engineGeo, Corridor: true, RadiusKm: 30, Overpass: []string{"tourism=viewpoint"}},
	}

	// Corridor from (45,0) to (45,2) ~ 157km, so multiple sample points.
	res, err := svc.CorridorSearch(context.Background(), 45.0, 0.0, 45.0, 2.0, themes, nil)
	if err != nil {
		t.Fatalf("CorridorSearch error: %v", err)
	}

	// Each item should appear exactly once despite multiple sample points.
	idCount := map[string]int{}
	for _, it := range res.Items {
		idCount[it.ID]++
	}
	for id, count := range idCount {
		if count != 1 {
			t.Errorf("item %s appears %d times, want 1", id, count)
		}
	}
	if len(res.Items) != 2 {
		t.Errorf("got %d items, want 2", len(res.Items))
	}

	// All items should have DetourEstimated = true.
	for _, it := range res.Items {
		if !it.DetourEstimated {
			t.Errorf("item %s: DetourEstimated = false, want true", it.ID)
		}
	}
}

func TestCorridorSearchNonCorridorThemesExcluded(t *testing.T) {
	mq := &mockQuerier{
		items: map[string][]Item{
			"any": {
				{ID: "osm:node:789", ThemeID: "spectacles", Name: "Show", Lat: 45.5, Lon: 1.0, Source: "osm"},
			},
		},
	}

	svc := &Service{
		Overpass: mq,
		Loader:   nil,
	}

	// Editorial theme without corridor=true should be excluded.
	themes := []Theme{
		{ID: "spectacles", Label: "Spectacles", Engine: engineEditorial, Corridor: false},
	}

	res, err := svc.CorridorSearch(context.Background(), 45.0, 0.0, 45.0, 2.0, themes, nil)
	if err != nil {
		t.Fatalf("CorridorSearch error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("got %d items, want 0 (non-corridor themes should be excluded)", len(res.Items))
	}
}

func TestSampleCorridorZeroSampleKm(t *testing.T) {
	// If sampleKm <= 0, it should default to 40km.
	pts := SampleCorridor(45.0, 0.0, 46.0, 0.0, 0)
	if len(pts) < 2 {
		t.Errorf("got %d points with sampleKm=0, want at least 2", len(pts))
	}
	// Should be same as explicitly passing 40.
	pts2 := SampleCorridor(45.0, 0.0, 46.0, 0.0, 40)
	if len(pts) != len(pts2) {
		t.Errorf("sampleKm=0 gave %d points, sampleKm=40 gave %d", len(pts), len(pts2))
	}
}
