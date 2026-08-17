package dailybrief

import (
	"encoding/json"
	"fmt"
)

// EnrichDay adds weather / dress code / dynamic alerts (deterministic).
// Uses the centralized weather service if available, otherwise skips gracefully.
func EnrichDay(data *DayBriefData, lat, lon float64, country string, wp WeatherProvider) error {
	if data == nil {
		return fmt.Errorf("nil day data")
	}
	if lat == 0 && lon == 0 {
		// Skip weather if no coords — still OK.
		return nil
	}

	if wp == nil {
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible (service non configuré)")
		return nil
	}

	date := data.Date // "2006-01-02"
	w, err := wp.GetDay(lat, lon, country, date)
	if err != nil {
		// Soft: keep going without weather.
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible temporairement")
		return nil
	}
	data.Weather = w
	if tmax, ok := asFloat(w["tempMax"]); ok {
		cond, _ := w["conditions"].(string)
		data.DressCode = dressCode(tmax, cond)
	}
	return nil
}

// EnrichDayOnDate is EnrichDay but for a specific calendar date (used by PlusChat).
func EnrichDayOnDate(data *DayBriefData, lat, lon float64, country, date string, wp WeatherProvider) error {
	if data == nil {
		return fmt.Errorf("nil day data")
	}
	if lat == 0 && lon == 0 {
		return nil
	}
	if wp == nil {
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible (service non configuré)")
		return nil
	}

	w, err := wp.GetDay(lat, lon, country, date)
	if err != nil {
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible temporairement")
		return nil
	}
	data.Weather = w
	if tmax, ok := asFloat(w["tempMax"]); ok {
		cond, _ := w["conditions"].(string)
		data.DressCode = dressCode(tmax, cond)
	}
	return nil
}

func dressCode(tempMax float64, _ string) string {
	switch {
	case tempMax > 30:
		return "Short, t-shirt, chapeau, crème solaire obligatoire"
	case tempMax > 20:
		return "Tenue légère, prévoir une couche pour le soir"
	case tempMax > 10:
		return "Couches superposées, veste légère"
	default:
		return "Tenue chaude, coupe-vent"
	}
}

// CoordsFromTripData tries locations[day.locationId] or hotel coords.
func CoordsFromTripData(tripData map[string]any, dayData map[string]any) (lat, lon float64, ok bool) {
	if locID, _ := dayData["locationId"].(string); locID != "" {
		if locs, _ := tripData["locations"].(map[string]any); locs != nil {
			if loc, _ := locs[locID].(map[string]any); loc != nil {
				lat, lon, ok = asFloatPair(loc["lat"], loc["lon"])
				if ok {
					return
				}
			}
		}
	}
	return 0, 0, false
}

func asFloatPair(a, b any) (float64, float64, bool) {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	return af, bf, aok && bok
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
