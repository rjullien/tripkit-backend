package formalities

import "strings"

// AdminRule defines an entry requirement for a destination country.
type AdminRule struct {
	Country   string   // destination country code
	Type      string   // "esta", "eta", "visa", "visa_waiver", "free_movement"
	Label     string   // human-readable label
	AppliesTo []string // nationalities this applies to; ["*"] = all except destination nationals
	URL       string
	Cost      string
	Delay     string
}

// EU countries for Schengen free-movement and various visa-waiver programs.
var euCountries = []string{
	"FR", "DE", "IT", "ES", "BE", "NL", "PT", "AT", "CH", "SE",
	"NO", "DK", "FI", "IE", "LU", "GR", "PL", "CZ", "HU", "RO",
	"BG", "HR", "SK", "SI", "EE", "LV", "LT", "MT", "CY",
}

// VWP (Visa Waiver Program) countries eligible for ESTA.
var vwpCountries = []string{
	"FR", "DE", "IT", "ES", "BE", "NL", "PT", "AT", "CH", "SE",
	"NO", "DK", "FI", "IE", "LU", "GR", "GB", "JP", "AU", "NZ",
	"KR", "SG", "TW",
}

// Countries eligible for Canada eTA.
var etaCanadaCountries = []string{
	"FR", "DE", "IT", "ES", "BE", "NL", "PT", "AT", "CH", "SE",
	"NO", "DK", "FI", "IE", "LU", "GR", "GB", "JP", "AU", "NZ",
	"KR", "SG",
}

// Countries requiring UK ETA.
var ukETACountries = []string{
	"US", "CA", "AU", "NZ", "JP", "KR", "SG", "BR", "MX", "CO",
	"AE", "SA", "QA", "KW", "BH", "OM",
}

// Countries eligible for Australia eVisitor (subclass 651).
var auEVisitorCountries = []string{
	"FR", "DE", "IT", "ES", "BE", "NL", "PT", "AT", "CH", "SE",
	"NO", "DK", "FI", "IE", "LU", "GR", "GB",
}

// Schengen zone countries (for free-movement detection).
var schengenCountries = []string{
	"FR", "DE", "IT", "ES", "BE", "NL", "PT", "AT", "CH", "SE",
	"NO", "DK", "FI", "LU", "GR", "PL", "CZ", "HU", "SK", "SI",
	"EE", "LV", "LT", "MT", "IS", "LI", "HR",
}

// baseAdminRules is the embedded rules database.
var baseAdminRules = []AdminRule{
	// United States - ESTA for VWP countries
	{Country: "US", Type: "esta", Label: "ESTA (Electronic System for Travel Authorization)",
		AppliesTo: vwpCountries, URL: "https://esta.cbp.dhs.gov", Cost: "21 USD", Delay: "72h"},
	// United States - B1/B2 visa for non-VWP
	{Country: "US", Type: "visa", Label: "Visa B1/B2",
		AppliesTo: []string{"*"}, Cost: "185 USD", Delay: "Variable"},

	// Canada - eTA
	{Country: "CA", Type: "eta", Label: "eTA / AVE (Autorisation de Voyage Électronique)",
		AppliesTo: etaCanadaCountries, URL: "https://www.canada.ca/eta", Cost: "7 CAD", Delay: "minutes"},

	// United Kingdom - ETA
	{Country: "GB", Type: "eta", Label: "UK ETA (Electronic Travel Authorisation)",
		AppliesTo: ukETACountries, URL: "https://www.gov.uk/get-eta", Cost: "10 GBP", Delay: "3 jours"},
	// UK - EU nationals do not need visa for <6 months
	{Country: "GB", Type: "visa_waiver", Label: "Pas de visa requis (séjour < 6 mois)",
		AppliesTo: euCountries},

	// Australia - eVisitor
	{Country: "AU", Type: "evisitor", Label: "eVisitor (subclass 651)",
		AppliesTo: auEVisitorCountries, URL: "https://immi.homeaffairs.gov.au", Cost: "Gratuit", Delay: "quelques jours"},

	// Japan - visa waiver 90 days
	{Country: "JP", Type: "visa_waiver", Label: "Exemption de visa (90 jours)",
		AppliesTo: append(euCountries, "US", "CA", "AU", "NZ", "GB")},

	// Thailand - visa exemption 30 days
	{Country: "TH", Type: "visa_waiver", Label: "Exemption de visa (30 jours)",
		AppliesTo: append(euCountries, "US", "CA", "AU", "NZ", "GB", "JP", "KR")},

	// India - e-Visa required
	{Country: "IN", Type: "evisa", Label: "e-Visa obligatoire",
		AppliesTo: []string{"*"}, URL: "https://indianvisaonline.gov.in", Cost: "25-80 USD", Delay: "3-5 jours"},

	// China - visa required
	{Country: "CN", Type: "visa", Label: "Visa obligatoire",
		AppliesTo: []string{"*"}, Cost: "Variable", Delay: "5-10 jours ouvrables"},

	// Morocco - visa exempt for EU
	{Country: "MA", Type: "visa_waiver", Label: "Pas de visa requis (séjour < 90 jours)",
		AppliesTo: append(euCountries, "US", "CA", "GB", "AU", "NZ", "JP")},

	// Schengen - EU free movement
	{Country: "SCHENGEN", Type: "free_movement", Label: "Libre circulation UE",
		AppliesTo: euCountries},
}

// MatchAdminRules crosses detected countries with traveler nationalities to produce check items.
// CRITICAL: a person with a nationality matching the destination country does NOT need
// entry documents for that country (e.g., FR+US bi-national does NOT need ESTA for US).
func MatchAdminRules(countries []string, nationalities []string) []AdminCheckItem {
	var items []AdminCheckItem

	countrySet := toSet(countries)
	natSet := toSet(nationalities)

	for _, rule := range baseAdminRules {
		// Check if this rule's country (or a Schengen member) is in our destination list.
		matchedCountry := ""
		if rule.Country == "SCHENGEN" {
			for _, sc := range schengenCountries {
				if countrySet[sc] {
					matchedCountry = sc
					break
				}
			}
		} else {
			if countrySet[rule.Country] {
				matchedCountry = rule.Country
			}
		}
		if matchedCountry == "" {
			continue
		}

		// CRITICAL: if the traveler holds a passport from the destination country,
		// they do NOT need entry formalities for that country.
		if natSet[matchedCountry] {
			continue
		}

		// For Schengen free_movement: only applies if the traveler IS an EU national.
		if rule.Type == "free_movement" {
			if !hasAny(natSet, rule.AppliesTo) {
				continue
			}
			// EU national traveling to Schengen: no action needed.
			items = append(items, AdminCheckItem{
				Country:   matchedCountry,
				Type:      rule.Type,
				Label:     rule.Label,
				Status:    "ok",
				AppliesTo: matchedNationalities(rule, nationalities),
				Detail:    "Libre circulation UE - aucune formalité requise",
			})
			continue
		}

		// Check if rule applies to the traveler's nationalities.
		applies := false
		if isWildcard(rule.AppliesTo) {
			// Wildcard: applies to everyone except nationals of the destination.
			// We already filtered destination nationals above.
			applies = true
		} else {
			applies = hasAny(natSet, rule.AppliesTo)
		}

		if !applies {
			continue
		}

		// If a more specific rule (ESTA/eTA) matches, skip the generic visa rule.
		// The logic: if we already added a specific rule for this country, skip "*" visa.
		if isWildcard(rule.AppliesTo) && rule.Type == "visa" {
			// Check if a more specific rule already matched for this country.
			alreadyHasSpecific := false
			for _, item := range items {
				if item.Country == matchedCountry && item.Type != "visa" {
					alreadyHasSpecific = true
					break
				}
			}
			if alreadyHasSpecific {
				continue
			}
		}

		status := "action_required"
		if rule.Type == "visa_waiver" || rule.Type == "free_movement" {
			status = "ok"
		}

		items = append(items, AdminCheckItem{
			Country:   matchedCountry,
			Type:      rule.Type,
			Label:     rule.Label,
			Status:    status,
			AppliesTo: matchedNationalities(rule, nationalities),
			Detail:    adminDetail(rule),
			URL:       rule.URL,
			Cost:      rule.Cost,
			Deadline:  rule.Delay,
		})
	}

	return items
}

// isWildcard reports whether a rule's nationality list is the ["*"] catch-all.
func isWildcard(list []string) bool {
	return len(list) == 1 && list[0] == "*"
}

// matchedNationalities narrows a rule's nationality list to the nationalities
// actually present in the trip.
//
// AdminCheckItem.AppliesTo used to carry EVERY trip nationality, which made the
// per-traveler checklist of construction/SPEC.md §7 decorative: the frontend
// groups items by intersecting AppliesTo with each person's nationalities, so a
// US-only traveler was told to obtain a Canadian eTA their passport does not
// require. Only the nationalities that triggered the rule belong here.
//
// The order of `nationalities` is preserved (Service sorts it), so the field
// stays deterministic. A wildcard rule keeps ["*"]: it really does apply to
// everyone, and that is the bucket the frontend renders as such.
func matchedNationalities(rule AdminRule, nationalities []string) []string {
	if isWildcard(rule.AppliesTo) {
		return []string{"*"}
	}
	ruleSet := toSet(rule.AppliesTo)
	out := make([]string, 0, len(nationalities))
	for _, n := range nationalities {
		if ruleSet[n] {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		// Unreachable: callers only build an item once hasAny() matched. Kept
		// explicit because an empty list is read as "applies to everyone"
		// downstream, which would silently widen the item back.
		return []string{"*"}
	}
	return out
}

// toSet converts a string slice to a set map.
func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// hasAny returns true if any element in the list is in the set.
func hasAny(set map[string]bool, list []string) bool {
	for _, v := range list {
		if set[v] {
			return true
		}
	}
	return false
}

// adminDetail builds the human-readable detail line for a rule: cost and lead
// time when known. Previously this field carried the bare cost string, which
// read as "21 USD" with no indication of what it referred to.
func adminDetail(rule AdminRule) string {
	var parts []string
	if c := strings.TrimSpace(rule.Cost); c != "" {
		parts = append(parts, "Coût : "+c)
	}
	if d := strings.TrimSpace(rule.Delay); d != "" {
		parts = append(parts, "Délai : "+d)
	}
	if len(parts) == 0 {
		return "Aucune démarche payante identifiée."
	}
	return strings.Join(parts, " · ")
}
