package formalities

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Service orchestrates admin-check and health-check operations.
// The Completer fields are optional: if nil, synthesis is skipped, `summary`
// stays absent (omitempty), and the service returns deterministic results only.
// Split per check so ops can point admin and health at different models.
type Service struct {
	DB        *gorm.DB
	Completer bifrost.Completer
	// HealthCompleter is used by HealthCheck. Falls back to Completer when nil.
	HealthCompleter bifrost.Completer
}

// healthCompleter returns the completer to use for health formatting.
func (s *Service) healthCompleter() bifrost.Completer {
	if s.HealthCompleter != nil {
		return s.HealthCompleter
	}
	return s.Completer
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
		return &AdminCheckResult{Verdict: "ok", Countries: nil, Travelers: nil, Items: nil}, nil
	}

	travelers := extractTravelers(tripData)
	if len(travelers) == 0 {
		// No people recorded at all: we cannot answer, and saying "ok" would be a
		// silent false negative on visas. Surface it as a warning instead.
		return &AdminCheckResult{
			Verdict:   "warning",
			Countries: countries,
			Items:     []AdminCheckItem{unknownTravelersItem()},
		}, nil
	}

	var statuses []string
	checklists := make([]TravelerChecklist, 0, len(travelers))

	for _, t := range travelers {
		var items []AdminCheckItem
		if len(t.Nationalities) == 0 {
			items = []AdminCheckItem{unknownNationalityItem(t)}
		} else {
			items = MatchAdminRules(countries, t.Nationalities)
		}

		var travelerStatuses []string
		for _, item := range items {
			travelerStatuses = append(travelerStatuses, item.Status)
			statuses = append(statuses, item.Status)
		}

		checklists = append(checklists, TravelerChecklist{
			ID:            t.ID,
			Name:          t.Name,
			Nationalities: t.Nationalities,
			Verdict:       worstVerdict(travelerStatuses),
			Items:         items,
		})
	}

	result := &AdminCheckResult{
		Verdict:   worstVerdict(statuses),
		Countries: countries,
		Travelers: checklists,
		Items:     unionItems(checklists),
	}
	if s.Completer != nil {
		if summary, err := FormatAdminResults(s.Completer, result); err == nil {
			result.Summary = summary
		}
	}
	return result, nil
}

// unknownTravelersItem flags a trip with no recorded people.
func unknownTravelersItem() AdminCheckItem {
	return AdminCheckItem{
		Type:   "nationality_unknown",
		Label:  "Voyageurs inconnus",
		Status: "warning",
		Detail: "Aucun voyageur enregistré : impossible de vérifier les formalités. Renseigne people.js.",
	}
}

// unknownNationalityItem flags a traveler whose passports are not recorded.
func unknownNationalityItem(t Traveler) AdminCheckItem {
	return AdminCheckItem{
		Type:   "nationality_unknown",
		Label:  "Nationalité non renseignée",
		Status: "warning",
		Detail: "Aucune nationalité pour " + t.Name + " : formalités non vérifiables. Ajoute nationalities dans people.js.",
	}
}

// unionItems de-duplicates the per-traveler items by country+type, keeping the
// most severe status seen for each. The flat list is the frontend contract
// (items[]); the per-traveler lists answer "who has to do it".
func unionItems(checklists []TravelerChecklist) []AdminCheckItem {
	var out []AdminCheckItem
	idx := map[string]int{}
	for _, cl := range checklists {
		for _, item := range cl.Items {
			key := item.Country + "|" + item.Type
			if i, ok := idx[key]; ok {
				if worstVerdict([]string{out[i].Status, item.Status}) != out[i].Status {
					out[i].Status = item.Status
				}
				continue
			}
			idx[key] = len(out)
			out = append(out, item)
		}
	}
	return out
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

	if len(items) == 0 {
		// SPEC §7.2 silence rule: nothing to say, and no section to display.
		// No Summary either — a paragraph saying "rien à signaler" is the exact
		// noise the rule exists to avoid.
		return &HealthCheckResult{Verdict: "none", Countries: countries, Items: nil}, nil
	}

	var statuses []string
	for _, item := range items {
		statuses = append(statuses, item.Status)
	}
	verdict := worstVerdict(statuses)

	result := &HealthCheckResult{
		Verdict:   verdict,
		Countries: countries,
		Items:     items,
	}
	if c := s.healthCompleter(); c != nil {
		if summary, err := FormatHealthResults(c, result); err == nil {
			result.Summary = summary
		}
	}
	return result, nil
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

// extractTravelers pulls one Traveler per person from the trip data, keeping
// each person's passports separate. Looks in `people` then `travelers`, each of
// which may be a map keyed by person id or a plain array.
//
// This is deliberately NOT a union: see Traveler for why crossing the group's
// combined nationalities against a destination produces false negatives.
func extractTravelers(tripData map[string]any) []Traveler {
	var out []Traveler
	seenID := map[string]bool{}

	add := func(id string, raw any) {
		p, _ := raw.(map[string]any)
		if p == nil {
			return
		}
		nats := map[string]bool{}
		extractNatsFromPerson(p, nats)

		name, _ := p["name"].(string)
		if strings.TrimSpace(name) == "" {
			name, _ = p["firstName"].(string)
		}
		if strings.TrimSpace(name) == "" {
			name = id
		}
		if pid, ok := p["id"].(string); ok && strings.TrimSpace(pid) != "" {
			id = pid
		}
		if id == "" {
			id = name
		}
		if seenID[id] {
			return
		}
		seenID[id] = true

		out = append(out, Traveler{
			ID:            id,
			Name:          name,
			Nationalities: sortedKeys(nats),
		})
	}

	for _, section := range []string{"people", "travelers"} {
		switch v := tripData[section].(type) {
		case map[string]any:
			// Deterministic order: map iteration is random in Go.
			for _, key := range sortedMapKeys(v) {
				add(key, v[key])
			}
		case []any:
			for i, item := range v {
				add(fmt.Sprintf("%s-%d", section, i), item)
			}
		}
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
