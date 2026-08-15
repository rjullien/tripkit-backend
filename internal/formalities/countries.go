package formalities

import "strings"

// DetectCountries extracts deduplicated ISO country codes from trip seed data.
// It reads locations[].country, flights[].from/to/stopovers fields.
func DetectCountries(tripData map[string]any) []string {
	seen := map[string]bool{}

	// Extract from locations (map of locationID -> {country: "XX", ...})
	if locs, ok := tripData["locations"].(map[string]any); ok {
		for _, v := range locs {
			loc, _ := v.(map[string]any)
			if loc == nil {
				continue
			}
			if c, ok := loc["country"].(string); ok && c != "" {
				seen[strings.ToUpper(c)] = true
			}
		}
	}

	// Extract from flights (array of flight objects)
	extractFlightCountries(tripData, "flights", seen)

	// Extract from transit (same structure as flights)
	extractFlightCountries(tripData, "transit", seen)

	result := make([]string, 0, len(seen))
	for c := range seen {
		result = append(result, c)
	}
	return result
}

// extractFlightCountries pulls country codes from a named array of flight-like objects.
func extractFlightCountries(tripData map[string]any, key string, seen map[string]bool) {
	raw, ok := tripData[key]
	if !ok {
		return
	}

	flights, ok := raw.([]any)
	if !ok {
		return
	}

	for _, f := range flights {
		flight, _ := f.(map[string]any)
		if flight == nil {
			continue
		}
		// from/to can be country codes or objects with a country field
		for _, field := range []string{"from", "to"} {
			extractCountryField(flight, field, seen)
		}
		// stopovers is an array of country codes or objects
		if stopovers, ok := flight["stopovers"].([]any); ok {
			for _, s := range stopovers {
				switch v := s.(type) {
				case string:
					if v != "" {
						seen[strings.ToUpper(v)] = true
					}
				case map[string]any:
					if c, ok := v["country"].(string); ok && c != "" {
						seen[strings.ToUpper(c)] = true
					}
				}
			}
		}
	}
}

// extractCountryField extracts a country code from a flight field (string or object with country key).
func extractCountryField(flight map[string]any, field string, seen map[string]bool) {
	v := flight[field]
	switch val := v.(type) {
	case string:
		if val != "" {
			seen[strings.ToUpper(val)] = true
		}
	case map[string]any:
		if c, ok := val["country"].(string); ok && c != "" {
			seen[strings.ToUpper(c)] = true
		}
	}
}
