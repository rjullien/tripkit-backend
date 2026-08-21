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

// MSC is the Meteorological Service of Canada provider.
// Uses the OGC API: api.weather.gc.ca/collections/citypageweather-realtime
// (the old weather:forecasts collection was removed by ECCC in 2026).
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
	// Build a small bbox around the target point (~50 km) to find the nearest station.
	dlat := 0.5
	dlon := 0.7
	bbox := fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
		req.Lon-dlon, req.Lat-dlat, req.Lon+dlon, req.Lat+dlat)

	u := fmt.Sprintf(
		"https://api.weather.gc.ca/collections/citypageweather-realtime/items?bbox=%s&limit=5",
		bbox,
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
			Geometry struct {
				Coordinates []float64 `json:"coordinates"` // [lon, lat]
			} `json:"geometry"`
			Properties json.RawMessage `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("msc decode: %w", err)
	}

	if len(parsed.Features) == 0 {
		return nil, fmt.Errorf("msc: no stations found near %.4f,%.4f", req.Lat, req.Lon)
	}

	// Pick the nearest station.
	bestIdx := 0
	bestDist := math.MaxFloat64
	for i, f := range parsed.Features {
		if len(f.Geometry.Coordinates) >= 2 {
			d := haversineKm(req.Lat, req.Lon, f.Geometry.Coordinates[1], f.Geometry.Coordinates[0])
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}
	}

	// Parse the nearest station's properties.
	var props struct {
		ForecastGroup struct {
			Forecasts []mscForecast `json:"forecasts"`
		} `json:"forecastGroup"`
	}
	if err := json.Unmarshal(parsed.Features[bestIdx].Properties, &props); err != nil {
		return nil, fmt.Errorf("msc props decode: %w", err)
	}

	forecasts := props.ForecastGroup.Forecasts
	if len(forecasts) == 0 {
		return nil, fmt.Errorf("msc: station has no forecasts")
	}

	// Build ForecastDay list. MSC returns day/night pairs (e.g. "Friday" high, "Friday night" low).
	// We combine consecutive high+low into one ForecastDay.
	// Use the request timezone (from location.tz) for correct date assignment.
	tzName := req.Timezone
	if tzName == "" {
		tzName = "America/Toronto"
	}

	fc := &Forecast{
		Lat:       req.Lat,
		Lon:       req.Lon,
		Timezone:  tzName,
		FetchedAt: time.Now().UTC(),
	}

	days := req.Days
	if days <= 0 {
		days = 7
	}
	if days > 16 {
		days = 16
	}

	// Walk forecasts and pair day/night.
	// Falls back to America/Toronto if not provided.
	loc, _ := time.LoadLocation(tzName)
	if loc == nil {
		loc = time.FixedZone("EDT", -4*3600) // fallback: summer EDT
	}
	today := time.Now().In(loc)
	dayOffset := 0

	i := 0
	for i < len(forecasts) && len(fc.Days) < days {
		entry := forecasts[i]
		tempClass := entry.tempClass()
		tempVal := entry.tempValue()
		icon := entry.iconCode()
		cond := entry.textSummary()

		var hi, lo float64
		var hiSet, loSet bool

		if tempClass == "high" {
			hi = tempVal
			hiSet = true
			// Next entry should be the night (low)
			if i+1 < len(forecasts) && forecasts[i+1].tempClass() == "low" {
				lo = forecasts[i+1].tempValue()
				loSet = true
				i += 2
			} else {
				i++
			}
		} else if tempClass == "low" {
			// Night-only entry (e.g. "Tonight") — pair with next day if available
			lo = tempVal
			loSet = true
			if i+1 < len(forecasts) && forecasts[i+1].tempClass() == "high" {
				hi = forecasts[i+1].tempValue()
				hiSet = true
				icon = forecasts[i+1].iconCode()
				cond = forecasts[i+1].textSummary()
				i += 2
			} else {
				i++
			}
		} else {
			i++
			continue
		}

		date := today.AddDate(0, 0, dayOffset)
		dayOffset++

		day := ForecastDay{
			Date:     date.Format("2006-01-02"),
			Provider: p.Name(),
		}
		if hiSet {
			day.TempMax = hi
		}
		if loSet {
			day.TempMin = lo
		}
		day.Code = mscIconToWMO(icon)
		day.Conditions = cond
		fc.Days = append(fc.Days, day)
	}

	if len(fc.Days) == 0 {
		return nil, fmt.Errorf("msc: could not build any forecast days")
	}

	return fc, nil
}

// mscForecast represents a single forecast period from citypageweather-realtime.
type mscForecast struct {
	Temperatures struct {
		Temperature []struct {
			Class struct {
				En string `json:"en"`
			} `json:"class"`
			Value struct {
				En float64 `json:"en"`
			} `json:"value"`
		} `json:"temperature"`
	} `json:"temperatures"`
	AbbreviatedForecast struct {
		Icon struct {
			Value int `json:"value"`
		} `json:"icon"`
		TextSummary struct {
			En string `json:"en"`
		} `json:"textSummary"`
	} `json:"abbreviatedForecast"`
	Period struct {
		TextForecastName struct {
			En string `json:"en"`
		} `json:"textForecastName"`
	} `json:"period"`
}

func (f *mscForecast) tempClass() string {
	if len(f.Temperatures.Temperature) > 0 {
		return f.Temperatures.Temperature[0].Class.En
	}
	return ""
}

func (f *mscForecast) tempValue() float64 {
	if len(f.Temperatures.Temperature) > 0 {
		return f.Temperatures.Temperature[0].Value.En
	}
	return 0
}

func (f *mscForecast) iconCode() int {
	return f.AbbreviatedForecast.Icon.Value
}

func (f *mscForecast) textSummary() string {
	return f.AbbreviatedForecast.TextSummary.En
}

// mscIconToWMO maps MSC icon codes to WMO weather codes.
// Reference: https://weather.gc.ca/weathericons/
func mscIconToWMO(icon int) int {
	switch {
	case icon == 0 || icon == 1:
		return 0 // Sunny / Clear
	case icon == 2 || icon == 3:
		return 1 // Partly cloudy
	case icon == 4 || icon == 5 || icon == 22:
		return 2 // Mostly cloudy
	case icon == 6 || icon == 10:
		return 3 // Overcast
	case icon >= 7 && icon <= 9:
		return 51 // Light rain / drizzle / showers
	case icon == 11 || icon == 12 || icon == 28:
		return 61 // Rain
	case icon == 13 || icon == 14:
		return 63 // Heavy rain
	case icon == 15 || icon == 16 || icon == 17:
		return 73 // Snow
	case icon == 18:
		return 75 // Heavy snow
	case icon == 19:
		return 95 // Thunderstorm
	case icon == 23 || icon == 24:
		return 45 // Fog / haze
	case icon == 25:
		return 48 // Freezing drizzle / rain
	case icon == 26 || icon == 27:
		return 67 // Freezing rain
	case icon >= 30 && icon <= 35:
		// Night equivalents: 30=clear, 31=few clouds, 32=partly, 33=mostly, 34=overcast
		if icon == 30 {
			return 0
		}
		if icon == 31 || icon == 32 {
			return 1
		}
		return 3
	case icon == 36 || icon == 37 || icon == 38 || icon == 39:
		// Night precipitation: 36=chance showers, 37=showers, 38=snow, 39=thunderstorm
		if icon == 36 || icon == 37 {
			return 61
		}
		if icon == 38 {
			return 73
		}
		return 95
	default:
		return 3 // Default: overcast
	}
}

// haversineKm computes the great-circle distance between two lat/lon pairs in km.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// mscConditionToWMO maps MSC text summaries to WMO codes (legacy fallback).
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
