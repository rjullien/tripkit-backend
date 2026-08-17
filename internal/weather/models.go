// Package weather provides a centralized weather service with multi-provider routing.
// All providers return a unified ForecastDay format regardless of upstream API shape.
package weather

import "time"

// ForecastDay holds normalised daily weather for a single date.
type ForecastDay struct {
	Date       string  `json:"date"`                 // "2006-01-02"
	TempMin    float64 `json:"tempMin"`              // °C
	TempMax    float64 `json:"tempMax"`              // °C
	Code       int     `json:"weatherCode"`          // WMO code (0–99)
	Conditions string  `json:"conditions"`           // Human-readable (French)
	Rain       int     `json:"precipProbability"`    // 0–100 %
	WindMax    float64 `json:"windMaxKmh,omitempty"` // km/h
	UVMax      float64 `json:"uvMax,omitempty"`      // UV index
	Sunrise    string  `json:"sunrise,omitempty"`    // "HH:MM"
	Sunset     string  `json:"sunset,omitempty"`     // "HH:MM"
	Provider   string  `json:"provider"`             // "open-meteo", "nws", "msc"
}

// Forecast is the unified response from the weather service.
type Forecast struct {
	Lat      float64       `json:"lat"`
	Lon      float64       `json:"lon"`
	Timezone string        `json:"timezone,omitempty"`
	Days     []ForecastDay `json:"days"`
	FetchedAt time.Time    `json:"fetchedAt"`
}

// ForecastRequest describes what the caller wants.
type ForecastRequest struct {
	Lat      float64
	Lon      float64
	Country  string // ISO 2-letter: "US", "CA", "FR", etc.
	Days     int    // how many days of forecast (max 16)
	Date     string // optional: single date "2006-01-02" — returns only that day
	Timezone string // IANA timezone (for Open-Meteo)
}

// Provider is the interface each weather backend implements.
type Provider interface {
	Name() string
	Fetch(req ForecastRequest) (*Forecast, error)
}

// WMO weather code → French text mapping.
func WeatherCodeText(code int) string {
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
