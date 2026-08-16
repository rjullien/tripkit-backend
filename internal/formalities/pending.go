package formalities

// PendingAdminActions returns the action_required items the deterministic
// admin engine would emit for this trip (all travelers, union). Used by
// construction QA (admin_action_required) so a missing formality weighs on
// phase transitions without requiring a prior POST /admin-check.
func PendingAdminActions(tripData map[string]any) []AdminCheckItem {
	if tripData == nil {
		return nil
	}
	countries := DetectCountries(tripData)
	if len(countries) == 0 {
		return nil
	}
	travelers := extractTravelers(tripData)
	if len(travelers) == 0 {
		return nil
	}
	var out []AdminCheckItem
	for _, t := range travelers {
		if len(t.Nationalities) == 0 {
			continue
		}
		for _, item := range MatchAdminRules(countries, t.Nationalities) {
			if item.Status == "action_required" {
				out = append(out, item)
			}
		}
	}
	return out
}
