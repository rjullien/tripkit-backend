package formalities

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Service orchestrates admin-check and health-check operations.
// The Completer field is optional: if nil, synthesis (LLM-powered summaries)
// is skipped and the service returns deterministic rule-engine results only.
// A warning is logged at startup if Completer is nil so operators notice.
type Service struct {
	DB        *gorm.DB
	Completer bifrost.Completer
}

// AdminCheck runs the full admin-check pipeline for a trip:
// 1. Load trip data
// 2. Detect countries
// 3. Extract nationalities from people/travelers
// 4. Match rules
// 5. Compute verdict
func (s *Service) AdminCheck(tripID string) (*AdminCheckResult, error) {
	tripData, err := s.loadTripData(tripID)
	if err != nil {
		return nil, fmt.Errorf("formalities: load trip %s: %w", tripID, err)
	}

	countries := DetectCountries(tripData)
	if len(countries) == 0 {
		return &AdminCheckResult{Verdict: "ok", Countries: nil, Items: nil}, nil
	}

	nationalities := extractNationalities(tripData)
	if len(nationalities) == 0 {
		return &AdminCheckResult{Verdict: "ok", Countries: countries, Items: nil}, nil
	}

	items := MatchAdminRules(countries, nationalities)

	var statuses []string
	for _, item := range items {
		statuses = append(statuses, item.Status)
	}
	verdict := worstVerdict(statuses)

	return &AdminCheckResult{
		Verdict:   verdict,
		Countries: countries,
		Items:     items,
	}, nil
}

// HealthCheck runs the full health-check pipeline for a trip.
func (s *Service) HealthCheck(tripID string) (*HealthCheckResult, error) {
	tripData, err := s.loadTripData(tripID)
	if err != nil {
		return nil, fmt.Errorf("formalities: load trip %s: %w", tripID, err)
	}

	countries := DetectCountries(tripData)
	if len(countries) == 0 {
		return &HealthCheckResult{Verdict: "none", Countries: nil, Items: nil}, nil
	}

	nationalities := extractNationalities(tripData)
	items := MatchHealthRules(countries, nationalities)

	if items == nil {
		return &HealthCheckResult{Verdict: "none", Countries: countries, Items: nil}, nil
	}

	var statuses []string
	for _, item := range items {
		statuses = append(statuses, item.Status)
	}
	verdict := worstVerdict(statuses)

	return &HealthCheckResult{
		Verdict:   verdict,
		Countries: countries,
		Items:     items,
	}, nil
}

// loadTripData fetches and parses a trip's JSON data field.
func (s *Service) loadTripData(tripID string) (map[string]any, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, err
	}
	if trip.Data == nil || *trip.Data == "" {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return nil, fmt.Errorf("invalid trip data JSON: %w", err)
	}
	return data, nil
}

// extractNationalities pulls all unique nationality codes from the trip data.
// Looks in: people[].nationalities, travelers[].nationalities, travelers[].nationality.
// The result is sorted: it lands in AdminCheckItem.AppliesTo, so it must be
// stable across runs (Go map iteration is not).
func extractNationalities(tripData map[string]any) []string {
	seen := map[string]bool{}

	// Check people section (map of personID -> {nationalities: [...], ...})
	if people, ok := tripData["people"].(map[string]any); ok {
		for _, v := range people {
			p, _ := v.(map[string]any)
			if p == nil {
				continue
			}
			extractNatsFromPerson(p, seen)
		}
	}

	// Check people as array
	if people, ok := tripData["people"].([]any); ok {
		for _, v := range people {
			p, _ := v.(map[string]any)
			if p == nil {
				continue
			}
			extractNatsFromPerson(p, seen)
		}
	}

	// Check travelers section (array or map)
	if travelers, ok := tripData["travelers"].([]any); ok {
		for _, v := range travelers {
			t, _ := v.(map[string]any)
			if t == nil {
				continue
			}
			extractNatsFromPerson(t, seen)
		}
	}
	if travelers, ok := tripData["travelers"].(map[string]any); ok {
		for _, v := range travelers {
			t, _ := v.(map[string]any)
			if t == nil {
				continue
			}
			extractNatsFromPerson(t, seen)
		}
	}

	result := make([]string, 0, len(seen))
	for n := range seen {
		result = append(result, n)
	}
	sort.Strings(result)
	return result
}

// extractNatsFromPerson extracts nationalities from a person/traveler map.
func extractNatsFromPerson(person map[string]any, seen map[string]bool) {
	// nationalities: ["FR", "US"]
	if nats, ok := person["nationalities"].([]any); ok {
		for _, n := range nats {
			if s, ok := n.(string); ok && s != "" {
				seen[s] = true
			}
		}
	}
	// nationality: "FR" (single string fallback)
	if nat, ok := person["nationality"].(string); ok && nat != "" {
		seen[nat] = true
	}
}
