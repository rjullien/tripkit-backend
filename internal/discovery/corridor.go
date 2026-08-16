package discovery

import (
	"context"
	"log"
	"math"
	"sync"
)

// Point is a lat/lon pair used for corridor sampling.
type Point struct {
	Lat float64
	Lon float64
}

const defaultSampleKm = 40.0

// SampleCorridor returns sample points along the great-circle path from
// (fromLat, fromLon) to (toLat, toLon), spaced approximately sampleKm apart.
// The first point is always the origin and the last is always the destination.
// If the total distance is 0, it returns a single point (the origin).
func SampleCorridor(fromLat, fromLon, toLat, toLon, sampleKm float64) []Point {
	if sampleKm <= 0 {
		sampleKm = defaultSampleKm
	}
	totalKm := haversineKm(fromLat, fromLon, toLat, toLon)
	if totalKm == 0 {
		return []Point{{Lat: fromLat, Lon: fromLon}}
	}

	n := int(math.Ceil(totalKm / sampleKm))
	if n < 1 {
		n = 1
	}

	points := make([]Point, 0, n+1)
	for i := 0; i <= n; i++ {
		frac := float64(i) / float64(n)
		lat, lon := interpolateGreatCircle(fromLat, fromLon, toLat, toLon, frac)
		points = append(points, Point{Lat: lat, Lon: lon})
	}
	return points
}

// interpolateGreatCircle returns the point at fraction f (0..1) along the
// great-circle arc from (lat1, lon1) to (lat2, lon2).
func interpolateGreatCircle(lat1, lon1, lat2, lon2, f float64) (float64, float64) {
	φ1 := lat1 * math.Pi / 180
	λ1 := lon1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	λ2 := lon2 * math.Pi / 180

	d := 2 * math.Asin(math.Sqrt(
		math.Pow(math.Sin((φ2-φ1)/2), 2)+
			math.Cos(φ1)*math.Cos(φ2)*math.Pow(math.Sin((λ2-λ1)/2), 2)))

	if d < 1e-12 {
		return lat1, lon1
	}

	a := math.Sin((1-f)*d) / math.Sin(d)
	b := math.Sin(f*d) / math.Sin(d)

	x := a*math.Cos(φ1)*math.Cos(λ1) + b*math.Cos(φ2)*math.Cos(λ2)
	y := a*math.Cos(φ1)*math.Sin(λ1) + b*math.Cos(φ2)*math.Sin(λ2)
	z := a*math.Sin(φ1) + b*math.Sin(φ2)

	lat := math.Atan2(z, math.Sqrt(x*x+y*y)) * 180 / math.Pi
	lon := math.Atan2(y, x) * 180 / math.Pi
	return lat, lon
}

// distToSegmentKm computes the perpendicular distance in km from point P to
// the closest point on the segment A-B (using flat approximation with
// haversine for the final distance). If the closest point is beyond A or B,
// it clamps to the nearest endpoint.
func distToSegmentKm(pLat, pLon, aLat, aLon, bLat, bLon float64) float64 {
	// Vector math in a local flat approximation (good enough for < 100km segments).
	// Convert to radians for local projection.
	cosLat := math.Cos((aLat + bLat) / 2 * math.Pi / 180)

	ax := 0.0
	ay := 0.0
	bx := (bLon - aLon) * cosLat
	by := bLat - aLat
	px := (pLon - aLon) * cosLat
	py := pLat - aLat

	abx := bx - ax
	aby := by - ay
	apx := px - ax
	apy := py - ay

	abLen2 := abx*abx + aby*aby
	if abLen2 == 0 {
		return haversineKm(pLat, pLon, aLat, aLon)
	}

	t := (apx*abx + apy*aby) / abLen2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	closestLat := aLat + t*(bLat-aLat)
	closestLon := aLon + t*(bLon-aLon)
	return haversineKm(pLat, pLon, closestLat, closestLon)
}

// minDistToCorridorKm finds the minimum distance from point P to any segment
// in the corridor polyline.
func minDistToCorridorKm(pLat, pLon float64, points []Point) float64 {
	if len(points) == 0 {
		return 0
	}
	if len(points) == 1 {
		return haversineKm(pLat, pLon, points[0].Lat, points[0].Lon)
	}
	minDist := math.MaxFloat64
	for i := 0; i < len(points)-1; i++ {
		d := distToSegmentKm(pLat, pLon, points[i].Lat, points[i].Lon, points[i+1].Lat, points[i+1].Lon)
		if d < minDist {
			minDist = d
		}
	}
	return minDist
}

// CorridorSearch runs an Overpass search along a corridor between two points.
// It samples the corridor, searches at each sample, deduplicates by item ID,
// and computes detour estimates.
func (s *Service) CorridorSearch(ctx context.Context, fromLat, fromLon, toLat, toLon float64, themes []Theme, progress ProgressFunc) (*Result, error) {
	cfg := s.cfg()
	sampleKm := defaultSampleKm
	if s != nil && s.CorridorSampleKm != nil {
		if km := s.CorridorSampleKm(); km > 0 {
			sampleKm = km
		}
	}

	conc := cfg.Overpass.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}

	points := SampleCorridor(fromLat, fromLon, toLat, toLon, sampleKm)

	res := &Result{
		Lat:     fromLat,
		Lon:     fromLon,
		ByTheme: map[string][]Item{},
	}

	// Filter to geo/corridor themes only.
	var corridorThemes []Theme
	for _, t := range themes {
		if t.Engine == engineGeo && t.Corridor {
			corridorThemes = append(corridorThemes, t)
			res.Themes = append(res.Themes, t.ID)
		}
	}

	if len(corridorThemes) == 0 || len(points) == 0 {
		return res, nil
	}

	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	// allItems collects everything before dedup.
	allItems := map[string]Item{}

	var wg sync.WaitGroup
	for _, pt := range points {
		for _, theme := range corridorThemes {
			pt := pt
			theme := theme
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				q := s.querier()
				items, err := q.Search(ctx, pt.Lat, pt.Lon, theme)
				if err != nil {
					log.Printf("discovery: corridor overpass theme=%s at (%.4f,%.4f): %v (soft-fail)", theme.ID, pt.Lat, pt.Lon, err)
					return
				}
				mu.Lock()
				for _, it := range items {
					if _, exists := allItems[it.ID]; !exists {
						allItems[it.ID] = it
					}
				}
				mu.Unlock()
			}()
		}
	}
	wg.Wait()

	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	// Compute detour and assemble final items.
	for _, it := range allItems {
		dist := minDistToCorridorKm(it.Lat, it.Lon, points)
		it.DetourKm = round1(dist * 2) // round trip deviation
		it.DetourEstimated = true
		res.Items = append(res.Items, it)
		res.ByTheme[it.ThemeID] = append(res.ByTheme[it.ThemeID], it)
	}

	// Sort by detour (shortest first).
	for i := 0; i < len(res.Items); i++ {
		for j := i + 1; j < len(res.Items); j++ {
			if res.Items[j].DetourKm < res.Items[i].DetourKm {
				res.Items[i], res.Items[j] = res.Items[j], res.Items[i]
			}
		}
	}

	if progress != nil {
		for _, theme := range corridorThemes {
			progress(theme.ID, theme.Label, res.ByTheme[theme.ID], false)
		}
	}

	return res, nil
}
