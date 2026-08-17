package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// OpenMeteo is the Open-Meteo provider (default for international trips).
type OpenMeteo struct {
	Client *http.Client
}

func NewOpenMeteo() *OpenMeteo {
	return &OpenMeteo{
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *OpenMeteo) Name() string { return "open-meteo" }

func (p *OpenMeteo) Fetch(req ForecastRequest) (*Forecast, error) {
	days := req.Days
	if days <= 0 {
		days = 7
	}
	if days > 16 {
		days = 16
	}

	tz := req.Timezone
	if tz == "" {
		tz = "auto"
	}

	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s"+
			"&daily=temperature_2m_max,temperature_2m_min,weathercode,precipitation_probability_max,windspeed_10m_max,uv_index_max,sunrise,sunset"+
			"&timezone=%s&forecast_days=%d",
		url.QueryEscape(fmt.Sprintf("%g", req.Lat)),
		url.QueryEscape(fmt.Sprintf("%g", req.Lon)),
		url.QueryEscape(tz),
		days,
	)

	resp, err := p.Client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("open-meteo request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("open-meteo HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var parsed struct {
		Timezone string `json:"timezone"`
		Daily    struct {
			Time        []string  `json:"time"`
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
			WeatherCode []int     `json:"weathercode"`
			RainProb    []int     `json:"precipitation_probability_max"`
			WindMax     []float64 `json:"windspeed_10m_max"`
			UVMax       []float64 `json:"uv_index_max"`
			Sunrise     []string  `json:"sunrise"`
			Sunset      []string  `json:"sunset"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("open-meteo decode: %w", err)
	}

	fc := &Forecast{
		Lat:       req.Lat,
		Lon:       req.Lon,
		Timezone:  parsed.Timezone,
		FetchedAt: time.Now().UTC(),
	}

	for i, date := range parsed.Daily.Time {
		// If a specific date was requested, only return that day.
		if req.Date != "" && date != req.Date {
			continue
		}

		day := ForecastDay{
			Date:     date,
			Provider: p.Name(),
		}
		if i < len(parsed.Daily.TempMin) {
			day.TempMin = parsed.Daily.TempMin[i]
		}
		if i < len(parsed.Daily.TempMax) {
			day.TempMax = parsed.Daily.TempMax[i]
		}
		if i < len(parsed.Daily.WeatherCode) {
			day.Code = parsed.Daily.WeatherCode[i]
			day.Conditions = WeatherCodeText(day.Code)
		}
		if i < len(parsed.Daily.RainProb) {
			day.Rain = parsed.Daily.RainProb[i]
		}
		if i < len(parsed.Daily.WindMax) {
			day.WindMax = parsed.Daily.WindMax[i]
		}
		if i < len(parsed.Daily.UVMax) {
			day.UVMax = parsed.Daily.UVMax[i]
		}
		if i < len(parsed.Daily.Sunrise) {
			day.Sunrise = extractTime(parsed.Daily.Sunrise[i])
		}
		if i < len(parsed.Daily.Sunset) {
			day.Sunset = extractTime(parsed.Daily.Sunset[i])
		}
		fc.Days = append(fc.Days, day)
	}

	return fc, nil
}

// extractTime returns "HH:MM" from "2006-01-02T15:04".
func extractTime(isoDateTime string) string {
	if len(isoDateTime) >= 16 {
		return isoDateTime[11:16]
	}
	return ""
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
