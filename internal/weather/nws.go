package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// NWS is the National Weather Service provider (US only).
// Two-step: api.weather.gov/points → forecast URL → periods.
type NWS struct {
	Client *http.Client
}

func NewNWS() *NWS {
	return &NWS{
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *NWS) Name() string { return "nws" }

func (p *NWS) Fetch(req ForecastRequest) (*Forecast, error) {
	// Step 1: resolve grid point.
	pointsURL := fmt.Sprintf("https://api.weather.gov/points/%.4f,%.4f", req.Lat, req.Lon)
	pointsData, err := p.fetchJSON(pointsURL)
	if err != nil {
		return nil, fmt.Errorf("nws points: %w", err)
	}

	props, _ := pointsData["properties"].(map[string]any)
	if props == nil {
		return nil, fmt.Errorf("nws: missing properties in points response")
	}
	forecastURL, _ := props["forecast"].(string)
	if forecastURL == "" {
		return nil, fmt.Errorf("nws: missing forecast URL in points response")
	}
	tz, _ := props["timeZone"].(string)

	// Step 2: fetch the forecast.
	fcData, err := p.fetchJSON(forecastURL)
	if err != nil {
		return nil, fmt.Errorf("nws forecast: %w", err)
	}

	fcProps, _ := fcData["properties"].(map[string]any)
	if fcProps == nil {
		return nil, fmt.Errorf("nws: missing properties in forecast response")
	}
	periodsRaw, _ := fcProps["periods"].([]any)
	if len(periodsRaw) == 0 {
		return nil, fmt.Errorf("nws: no forecast periods")
	}

	// Group periods by date (day + night).
	type dayNight struct {
		hi, lo  float64
		hiSet   bool
		loSet   bool
		short   string
		rain    int
		windMax float64
	}
	dailyMap := map[string]*dayNight{}
	var dateOrder []string

	for _, pr := range periodsRaw {
		period, _ := pr.(map[string]any)
		if period == nil {
			continue
		}
		startTime, _ := period["startTime"].(string)
		if len(startTime) < 10 {
			continue
		}
		iso := startTime[:10]

		dn, exists := dailyMap[iso]
		if !exists {
			dn = &dayNight{lo: 100, hi: -100}
			dailyMap[iso] = dn
			dateOrder = append(dateOrder, iso)
		}

		temp := jsonFloat(period["temperature"])
		unit, _ := period["temperatureUnit"].(string)
		if unit == "F" {
			temp = (temp - 32) * 5 / 9
		}

		isDaytime, _ := period["isDaytime"].(bool)
		if isDaytime {
			dn.hi = math.Max(dn.hi, temp)
			dn.hiSet = true
			if s, _ := period["shortForecast"].(string); s != "" {
				dn.short = s
			}
		} else {
			dn.lo = math.Min(dn.lo, temp)
			dn.loSet = true
		}

		if pop, ok := period["probabilityOfPrecipitation"].(map[string]any); ok {
			if v := int(jsonFloat(pop["value"])); v > dn.rain {
				dn.rain = v
			}
		}

		if ws, _ := period["windSpeed"].(string); ws != "" {
			// "15 mph" or "10 to 20 mph"
			parts := strings.Fields(ws)
			for _, p := range parts {
				var v float64
				if _, err := fmt.Sscanf(p, "%f", &v); err == nil {
					// Convert mph to km/h
					kmh := v * 1.60934
					if kmh > dn.windMax {
						dn.windMax = kmh
					}
				}
			}
		}
	}

	fc := &Forecast{
		Lat:       req.Lat,
		Lon:       req.Lon,
		Timezone:  tz,
		FetchedAt: time.Now().UTC(),
	}

	maxDays := req.Days
	if maxDays <= 0 {
		maxDays = 7
	}

	for _, iso := range dateOrder {
		if len(fc.Days) >= maxDays {
			break
		}
		if req.Date != "" && iso != req.Date {
			continue
		}
		dn := dailyMap[iso]
		day := ForecastDay{
			Date:     iso,
			Provider: p.Name(),
			Rain:     dn.rain,
			WindMax:  math.Round(dn.windMax),
		}
		if dn.hiSet {
			day.TempMax = math.Round(dn.hi)
		}
		if dn.loSet {
			day.TempMin = math.Round(dn.lo)
		}
		day.Code = nwsShortToWMO(dn.short)
		day.Conditions = WeatherCodeText(day.Code)
		fc.Days = append(fc.Days, day)
	}

	return fc, nil
}

func (p *NWS) fetchJSON(url string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "TripKit/1.0 (tripkit.bapttf.com)")
	req.Header.Set("Accept", "application/geo+json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return result, nil
}

// nwsShortToWMO maps NWS shortForecast to closest WMO code.
func nwsShortToWMO(short string) int {
	s := strings.ToLower(short)
	switch {
	case strings.Contains(s, "thunder"):
		return 95
	case strings.Contains(s, "snow") || strings.Contains(s, "blizzard"):
		return 73
	case strings.Contains(s, "heavy rain"):
		return 65
	case strings.Contains(s, "rain") || strings.Contains(s, "shower"):
		return 61
	case strings.Contains(s, "drizzle"):
		return 51
	case strings.Contains(s, "fog"):
		return 45
	case strings.Contains(s, "overcast"):
		return 3
	case strings.Contains(s, "mostly cloudy"):
		return 3
	case strings.Contains(s, "partly"):
		return 2
	case strings.Contains(s, "mostly sunny") || strings.Contains(s, "mostly clear"):
		return 1
	case strings.Contains(s, "sunny") || strings.Contains(s, "clear"):
		return 0
	default:
		return 3
	}
}

func jsonFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case int:
		return float64(t)
	default:
		return 0
	}
}
