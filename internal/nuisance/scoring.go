package nuisance

import (
	"math"

	"github.com/rjullien/tripkit-backend/internal/discovery"
)

// Scoring levels (French, matching SPEC-nuisance-check.md).
const (
	LevelEleve  = "ELEVE"  // Red - high nuisance
	LevelModere = "MODERE" // Yellow - moderate nuisance
	LevelFaible = "FAIBLE" // Green - low/no nuisance
)

// CategoryResult is the deterministic output of scoring one nuisance category.
type CategoryResult struct {
	Category string  `json:"category"`
	Level    string  `json:"level"`
	Emoji    string  `json:"emoji"`
	Distance float64 `json:"distance"` // meters to nearest item (0 if count-based or no items)
	Count    int     `json:"count"`    // number of items found (for count-based)
	Detail   string  `json:"detail"`
}

// ScoreCategory scores a single nuisance category given the Overpass items found
// and the reference point (hotel/location coordinates). The scoring is fully
// deterministic: same inputs always produce the same output.
func ScoreCategory(cat NuisanceCategory, items []discovery.Item, refLat, refLon float64) CategoryResult {
	result := CategoryResult{
		Category: cat.ID,
		Emoji:    cat.Emoji,
	}

	// Security is a placeholder: always returns green.
	if cat.ID == "security" {
		result.Level = LevelFaible
		result.Detail = "Pas de signalement particulier."
		return result
	}

	if len(items) == 0 {
		result.Level = LevelFaible
		result.Detail = "Aucun element detecte."
		return result
	}

	if cat.CountBased {
		result.Count = len(items)
		// Also compute the closest distance for informational purposes.
		if len(items) > 0 {
			result.Distance = nearestDistanceMeters(items, refLat, refLon)
		}
		if result.Count > cat.RedAbove {
			result.Level = LevelEleve
		} else if result.Count >= cat.YellowAbove {
			result.Level = LevelModere
		} else {
			result.Level = LevelFaible
		}
		result.Detail = countDetail(cat, result.Count)
		return result
	}

	// Distance-based scoring.
	distM := nearestDistanceMeters(items, refLat, refLon)
	result.Distance = distM

	if distM < cat.RedBelow {
		result.Level = LevelEleve
	} else if distM < cat.YellowBelow {
		result.Level = LevelModere
	} else {
		result.Level = LevelFaible
	}
	result.Detail = distanceDetail(cat, distM)
	return result
}

// GlobalVerdict returns the highest severity across all category results.
// Any red = ELEVE, any yellow = MODERE, all green = FAIBLE.
func GlobalVerdict(results []CategoryResult) string {
	hasYellow := false
	for _, r := range results {
		switch r.Level {
		case LevelEleve:
			return LevelEleve
		case LevelModere:
			hasYellow = true
		}
	}
	if hasYellow {
		return LevelModere
	}
	return LevelFaible
}

// VerdictEmoji returns the emoji for a verdict level.
func VerdictEmoji(level string) string {
	switch level {
	case LevelEleve:
		return "🔴"
	case LevelModere:
		return "🟡"
	default:
		return "🟢"
	}
}

// nearestDistanceMeters returns the distance in meters to the closest item.
func nearestDistanceMeters(items []discovery.Item, lat, lon float64) float64 {
	minDist := math.MaxFloat64
	for _, it := range items {
		d := haversineKm(lat, lon, it.Lat, it.Lon) * 1000 // convert km to meters
		if d < minDist {
			minDist = d
		}
	}
	return math.Round(minDist)
}

// haversineKm computes the great-circle distance between two points in km.
// Duplicated from discovery/geo.go (unexported there).
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func distanceDetail(cat NuisanceCategory, distM float64) string {
	if distM >= 1000 {
		return cat.Label + " a " + formatKm(distM/1000) + "km."
	}
	return cat.Label + " a " + formatM(distM) + "m."
}

func countDetail(cat NuisanceCategory, count int) string {
	if count == 0 {
		return "Aucun " + cat.Label + " detecte."
	}
	if count == 1 {
		return "1 etablissement detecte dans un rayon de 200m."
	}
	return formatInt(count) + " etablissements detectes dans un rayon de 200m."
}

func formatKm(v float64) string {
	if v == math.Trunc(v) {
		return formatInt(int(v))
	}
	// one decimal
	return strconv(v)
}

func formatM(v float64) string {
	return formatInt(int(v))
}

func formatInt(v int) string {
	s := ""
	if v < 0 {
		s = "-"
		v = -v
	}
	t := ""
	for v > 0 {
		if t != "" {
			t = " " + t
		}
		if v >= 1000 {
			part := v % 1000
			if part < 10 {
				t = "00" + intToStr(part) + t
			} else if part < 100 {
				t = "0" + intToStr(part) + t
			} else {
				t = intToStr(part) + t
			}
		} else {
			t = intToStr(v) + t
		}
		v /= 1000
	}
	if t == "" {
		return "0"
	}
	return s + t
}

func strconv(v float64) string {
	// Simple one-decimal formatting without importing strconv.
	whole := int(v)
	frac := int(math.Round((v - float64(whole)) * 10))
	if frac == 10 {
		whole++
		frac = 0
	}
	if frac == 0 {
		return formatInt(whole)
	}
	return formatInt(whole) + "." + intToStr(frac)
}

func intToStr(v int) string {
	if v == 0 {
		return "0"
	}
	s := ""
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}
