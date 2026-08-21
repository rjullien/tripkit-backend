package weather

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Service is the centralized weather service.
// It routes requests to the appropriate provider based on country code
// and provides a unified response format.
type Service struct {
	openMeteo *OpenMeteo
	nws       *NWS
	msc       *MSC

	// Simple in-memory cache: key → cached entry.
	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

type cacheEntry struct {
	forecast *Forecast
	expiry   time.Time
}

const cacheTTL = 30 * time.Minute

// New creates a new weather Service with all providers initialized.
func New() *Service {
	return &Service{
		openMeteo: NewOpenMeteo(),
		nws:       NewNWS(),
		msc:       NewMSC(),
		cache:     make(map[string]*cacheEntry),
	}
}

// GetForecast returns a multi-day forecast using the best provider for the given country.
// Country is ISO 2-letter (e.g. "US", "CA", "FR"). Empty defaults to Open-Meteo.
func (s *Service) GetForecast(req ForecastRequest) (*Forecast, error) {
	// A specific date needs the full window so we can match it, or keep
	// "today" when the date falls in the past (TZ / UTC-today mismatch).
	// The PWA used to send days=1&date=… which dropped the current day.
	if req.Date != "" {
		req.Days = 16
	}

	key := s.cacheKey(req)

	// Check cache.
	s.mu.RLock()
	if ce, ok := s.cache[key]; ok && time.Now().Before(ce.expiry) {
		s.mu.RUnlock()
		return ce.forecast, nil
	}
	s.mu.RUnlock()

	provider := s.providerFor(req.Country)
	var fc *Forecast
	var err error

	// For CA/US (MSC/NWS): if a specific date is requested and it's beyond
	// the provider's horizon (MSC=6 days, NWS=7 days), skip directly to
	// Open-Meteo to avoid a useless call + fallback delay.
	if provider.Name() != "open-meteo" && req.Date != "" {
		horizon := 7 // NWS
		if provider.Name() == "msc" {
			horizon = 6
		}
		if daysUntil(req.Date) >= horizon {
			// Beyond MSC/NWS horizon — go straight to Open-Meteo
			fc, err = s.openMeteo.Fetch(req)
			if err != nil {
				return nil, err
			}
		}
	}

	// Normal path: try the preferred provider, fallback to Open-Meteo
	if fc == nil {
		fc, err = provider.Fetch(req)
		if err != nil {
			if provider.Name() != "open-meteo" {
				log.Printf("weather: %s failed (%v), falling back to open-meteo", provider.Name(), err)
				fc, err = s.openMeteo.Fetch(req)
				if err != nil {
					return nil, fmt.Errorf("weather: all providers failed: %w", err)
				}
			} else {
				return nil, err
			}
		} else if provider.Name() != "open-meteo" && fc != nil && len(fc.Days) > 0 {
			// Enrich MSC/NWS response with missing fields from Open-Meteo
			// (UV, wind max, precip probability) and mark as combined provider.
			s.enrichWithOpenMeteo(fc, req)
		}
	}

	if fc != nil {
		fc.Days = SelectDate(fc.Days, req.Date)
	}

	// Cache the result.
	s.mu.Lock()
	s.cache[key] = &cacheEntry{forecast: fc, expiry: time.Now().Add(cacheTTL)}
	s.mu.Unlock()

	return fc, nil
}

// daysUntil returns how many days from today (UTC) the given ISO date is.
// Returns 0 for today, negative for past dates.
func daysUntil(isoDate string) int {
	t, err := time.Parse("2006-01-02", isoDate)
	if err != nil {
		return 0
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	return int(t.Sub(now).Hours() / 24)
}

// enrichWithOpenMeteo fills missing fields (UV, wind max, precip probability,
// sunrise, sunset) in a MSC/NWS forecast using Open-Meteo data.
// On success, marks provider as "msc+open-meteo" or "nws+open-meteo".
func (s *Service) enrichWithOpenMeteo(fc *Forecast, req ForecastRequest) {
	omReq := ForecastRequest{
		Lat:      req.Lat,
		Lon:      req.Lon,
		Days:     16,
		Date:     req.Date,
		Timezone: req.Timezone,
	}
	omFc, err := s.openMeteo.Fetch(omReq)
	if err != nil {
		// Best-effort: if Open-Meteo fails, keep MSC data as-is.
		return
	}

	// Index Open-Meteo days by date for quick lookup.
	omByDate := make(map[string]*ForecastDay, len(omFc.Days))
	for i := range omFc.Days {
		omByDate[omFc.Days[i].Date] = &omFc.Days[i]
	}

	enriched := false
	for i := range fc.Days {
		om, ok := omByDate[fc.Days[i].Date]
		if !ok {
			continue
		}
		// Fill missing fields only (don't overwrite MSC temps/conditions).
		if fc.Days[i].UVMax == 0 && om.UVMax > 0 {
			fc.Days[i].UVMax = om.UVMax
			enriched = true
		}
		if fc.Days[i].WindMax == 0 && om.WindMax > 0 {
			fc.Days[i].WindMax = om.WindMax
			enriched = true
		}
		if fc.Days[i].Rain == 0 && om.Rain > 0 {
			fc.Days[i].Rain = om.Rain
			enriched = true
		}
		if fc.Days[i].Sunrise == "" && om.Sunrise != "" {
			fc.Days[i].Sunrise = om.Sunrise
			enriched = true
		}
		if fc.Days[i].Sunset == "" && om.Sunset != "" {
			fc.Days[i].Sunset = om.Sunset
			enriched = true
		}
	}

	if enriched {
		// Mark all days as combined provider.
		for i := range fc.Days {
			if fc.Days[i].Provider != "" && fc.Days[i].Provider != "open-meteo" {
				fc.Days[i].Provider = fc.Days[i].Provider + "+open-meteo"
			}
		}
	}
}

// GetDay returns weather for a single date. Convenience wrapper around GetForecast.
func (s *Service) GetDay(lat, lon float64, country, date string) (*ForecastDay, error) {
	fc, err := s.GetForecast(ForecastRequest{
		Lat:     lat,
		Lon:     lon,
		Country: country,
		Days:    16,
		Date:    date,
	})
	if err != nil {
		return nil, err
	}
	if len(fc.Days) == 0 {
		return nil, fmt.Errorf("weather: date %s not in forecast window", date)
	}
	return &fc.Days[0], nil
}

// GetToday returns today's weather for a location.
func (s *Service) GetToday(lat, lon float64, country string) (*ForecastDay, error) {
	today := time.Now().UTC().Format("2006-01-02")
	return s.GetDay(lat, lon, country, today)
}

func (s *Service) providerFor(country string) Provider {
	switch country {
	case "US":
		return s.nws
	case "CA":
		return s.msc
	default:
		return s.openMeteo
	}
}

func (s *Service) cacheKey(req ForecastRequest) string {
	return fmt.Sprintf("%s:%.3f:%.3f:%d:%s", req.Country, req.Lat, req.Lon, req.Days, req.Date)
}

// Purge removes expired cache entries. Call periodically if needed.
func (s *Service) Purge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, ce := range s.cache {
		if now.After(ce.expiry) {
			delete(s.cache, k)
		}
	}
}
