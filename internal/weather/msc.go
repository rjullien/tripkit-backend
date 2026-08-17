package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MSC is the Meteorological Service of Canada provider.
// Uses the OGC API: api.weather.gc.ca/collections/weather:forecasts
type MSC struct {
	Client *http.Client
}

func NewMSC() *MSC {
	return &MSC{
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *MSC) Name() string { return "msc" }

func (p *MSC) Fetch(req ForecastRequest) (*Forecast, error) {
	days := req.Days
	if days <= 0 {
		days = 7
	}

	u := fmt.Sprintf(
		"https://api.weather.gc.ca/collections/weather:forecasts/items?lat=%.4f&lon=%.4f&limit=%d",
		req.Lat, req.Lon, days*2, // day + night periods
	)

	httpReq, _ := http.NewRequest("GET", u, nil)
	httpReq.Header.Set("Accept", "application/geo+json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("msc request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("msc HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var parsed struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("msc decode: %w", err)
	}

	// Group features by date. MSC returns individual time-step features.
	type dayData struct {
		hi, lo    float64
		hiSet     bool
		loSet     bool
		condition string
		pop       int
	}
	dailyMap := map[string]*dayData{}
	var dateOrder []string

	for _, f := range parsed.Features {
		props := f.Properties
		if props == nil {
			continue
		}

		// Extract datetime → date
		dt, _ := props["datetime"].(string)
		if dt == "" {
			// Some responses use "forecast_datetime" or "time"
			dt, _ = props["forecast_datetime"].(string)
		}
		if len(dt) < 10 {
			continue
		}
		iso := dt[:10]

		dd, exists := dailyMap[iso]
		if !exists {
			dd = &dayData{lo: 100, hi: -100}
			dailyMap[iso] = dd
			dateOrder = append(dateOrder, iso)
		}

		// Temperature
		if temp, ok := mscFloat(props["temperature"]); ok {
			if temp > dd.hi {
				dd.hi = temp
				dd.hiSet = true
			}
			if temp < dd.lo {
				dd.lo = temp
				dd.loSet = true
			}
		}
		if tmax, ok := mscFloat(props["temperature_maximum"]); ok {
			dd.hi = tmax
			dd.hiSet = true
		}
		if tmin, ok := mscFloat(props["temperature_minimum"]); ok {
			dd.lo = tmin
			dd.loSet = true
		}

		// Condition text
		if cond, _ := props["text_summary"].(string); cond != "" {
			dd.condition = cond
		}
		if cond, _ := props["icon_code"].(string); cond != "" && dd.condition == "" {
			dd.condition = cond
		}

		// POP
		if pop, ok := mscFloat(props["probability_of_precipitation"]); ok {
			if int(pop) > dd.pop {
				dd.pop = int(pop)
			}
		}
	}

	fc := &Forecast{
		Lat:       req.Lat,
		Lon:       req.Lon,
		Timezone:  "America/Toronto", // MSC doesn't return tz; default to Eastern
		FetchedAt: time.Now().UTC(),
	}

	for _, iso := range dateOrder {
		if len(fc.Days) >= days {
			break
		}
		dd := dailyMap[iso]
		day := ForecastDay{
			Date:     iso,
			Provider: p.Name(),
			Rain:     dd.pop,
		}
		if dd.hiSet {
			day.TempMax = dd.hi
		}
		if dd.loSet {
			day.TempMin = dd.lo
		}
		day.Code = mscConditionToWMO(dd.condition)
		day.Conditions = WeatherCodeText(day.Code)
		fc.Days = append(fc.Days, day)
	}

	return fc, nil
}

func mscFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case int:
		return float64(t), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(t, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// mscConditionToWMO maps MSC text summaries to WMO codes.
func mscConditionToWMO(cond string) int {
	s := strings.ToLower(cond)
	switch {
	case strings.Contains(s, "thunder") || strings.Contains(s, "orage"):
		return 95
	case strings.Contains(s, "snow") || strings.Contains(s, "neige"):
		return 73
	case strings.Contains(s, "freezing") || strings.Contains(s, "vergla"):
		return 48
	case strings.Contains(s, "rain") || strings.Contains(s, "pluie"):
		return 61
	case strings.Contains(s, "drizzle") || strings.Contains(s, "bruine"):
		return 51
	case strings.Contains(s, "fog") || strings.Contains(s, "brouillard"):
		return 45
	case strings.Contains(s, "overcast") || strings.Contains(s, "couvert"):
		return 3
	case strings.Contains(s, "cloudy") || strings.Contains(s, "nuageux"):
		return 2
	case strings.Contains(s, "clear") || strings.Contains(s, "dégagé") || strings.Contains(s, "sunny") || strings.Contains(s, "soleil"):
		return 0
	default:
		return 3
	}
}
