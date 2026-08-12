package dailybrief

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// EnrichDay adds weather / dress code / dynamic alerts (deterministic).
func EnrichDay(data *DayBriefData, lat, lon float64) error {
	if data == nil {
		return fmt.Errorf("nil day data")
	}
	if lat == 0 && lon == 0 {
		// Skip weather if no coords — still OK.
		return nil
	}
	w, err := fetchOpenMeteo(lat, lon)
	if err != nil {
		// Soft: keep going without weather.
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible temporairement")
		return nil
	}
	data.Weather = w
	if tmax, ok := w["tempMax"].(float64); ok {
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

func fetchOpenMeteo(lat, lon float64) (map[string]any, error) {
	return fetchOpenMeteoOnDate(lat, lon, "")
}

// fetchOpenMeteoOnDate returns daily weather for dateYYYYMMDD ("2006-01-02").
// Empty date → first forecast day (today at that location).
func fetchOpenMeteoOnDate(lat, lon float64, dateYYYYMMDD string) (map[string]any, error) {
	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s&daily=temperature_2m_max,temperature_2m_min,weathercode&timezone=auto&forecast_days=16",
		url.QueryEscape(fmt.Sprintf("%g", lat)),
		url.QueryEscape(fmt.Sprintf("%g", lon)),
	)
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("open-meteo HTTP %d", res.StatusCode)
	}
	var parsed struct {
		Daily struct {
			Time        []string  `json:"time"`
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
			WeatherCode []int     `json:"weathercode"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	idx := 0
	if dateYYYYMMDD != "" {
		idx = -1
		for i, t := range parsed.Daily.Time {
			if t == dateYYYYMMDD {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("date %s not in forecast", dateYYYYMMDD)
		}
	}
	out := map[string]any{}
	if dateYYYYMMDD != "" {
		out["date"] = dateYYYYMMDD
	} else if len(parsed.Daily.Time) > 0 {
		out["date"] = parsed.Daily.Time[0]
	}
	if idx < len(parsed.Daily.TempMin) {
		out["tempMin"] = parsed.Daily.TempMin[idx]
	}
	if idx < len(parsed.Daily.TempMax) {
		out["tempMax"] = parsed.Daily.TempMax[idx]
	}
	if idx < len(parsed.Daily.WeatherCode) {
		code := parsed.Daily.WeatherCode[idx]
		out["weatherCode"] = code
		out["conditions"] = weatherCodeText(code)
	}
	return out, nil
}

// EnrichDayOnDate is EnrichDay but picks the forecast row for dateYYYYMMDD.
func EnrichDayOnDate(data *DayBriefData, lat, lon float64, dateYYYYMMDD string) error {
	if data == nil {
		return fmt.Errorf("nil day data")
	}
	if lat == 0 && lon == 0 {
		return nil
	}
	w, err := fetchOpenMeteoOnDate(lat, lon, dateYYYYMMDD)
	if err != nil {
		data.DynamicAlerts = append(data.DynamicAlerts, "Météo indisponible temporairement")
		return nil
	}
	data.Weather = w
	if tmax, ok := w["tempMax"].(float64); ok {
		cond, _ := w["conditions"].(string)
		data.DressCode = dressCode(tmax, cond)
	}
	return nil
}

func weatherCodeText(code int) string {
	switch code {
	case 0:
		return "Ensoleillé"
	case 1, 2:
		return "Peu nuageux"
	case 3:
		return "Couvert"
	case 45, 48:
		return "Brouillard"
	case 51, 53, 55, 61, 63, 65, 80, 81, 82:
		return "Pluie"
	case 71, 73, 75, 85, 86:
		return "Neige"
	case 95, 96, 99:
		return "Orage"
	default:
		return "Variable"
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
