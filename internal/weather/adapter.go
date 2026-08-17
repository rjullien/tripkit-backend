package weather

// DailyBriefAdapter wraps the weather Service into the map[string]any interface
// expected by the daily brief pipeline.
type DailyBriefAdapter struct {
	Svc *Service
}

// GetDay returns weather for a single date as a map compatible with DayBriefData.Weather.
func (a *DailyBriefAdapter) GetDay(lat, lon float64, country, date string) (map[string]any, error) {
	day, err := a.Svc.GetDay(lat, lon, country, date)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"date":       day.Date,
		"tempMin":    day.TempMin,
		"tempMax":    day.TempMax,
		"weatherCode": day.Code,
		"conditions": day.Conditions,
		"precipProbability": day.Rain,
		"windMaxKmh": day.WindMax,
		"uvMax":      day.UVMax,
		"sunrise":    day.Sunrise,
		"sunset":     day.Sunset,
		"provider":   day.Provider,
	}, nil
}
