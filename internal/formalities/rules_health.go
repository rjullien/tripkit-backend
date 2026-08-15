package formalities

// noAdviceCountries is the list of countries for which no health advisory is shown.
// If ALL destination countries are in this list, verdict is "none" and zero items.
var noAdviceCountries = map[string]bool{
	"US": true, "CA": true, "GB": true, "DE": true, "FR": true,
	"IT": true, "ES": true, "JP": true, "AU": true, "NZ": true,
	"CH": true, "AT": true, "BE": true, "NL": true, "SE": true,
	"NO": true, "DK": true, "FI": true, "IE": true, "PT": true,
}

// HealthRule defines a health advisory for a destination country.
type HealthRule struct {
	Country   string   // destination country code, or "*" for generic
	Type      string   // "vaccins", "paludisme", "eau", "altitude", "trousse"
	Label     string   // human-readable advisory label
	AppliesTo []string // nationalities this applies to; ["*"] = all
	Detail    string
}

// baseHealthRules contains the embedded health rules database.
var baseHealthRules = []HealthRule{
	// Thailand
	{Country: "TH", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A, Typhoïde)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A et Typhoïde recommandées pour tout séjour"},
	{Country: "TH", Type: "paludisme", Label: "Risque de paludisme (zones rurales)",
		AppliesTo: []string{"*"}, Detail: "Traitement préventif recommandé pour les zones rurales et frontalières"},
	{Country: "TH", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet, privilégier l'eau en bouteille"},

	// India
	{Country: "IN", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A/B, Typhoïde, Fièvre jaune si transit)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A, Hépatite B, Typhoïde fortement recommandées"},
	{Country: "IN", Type: "paludisme", Label: "Risque de paludisme",
		AppliesTo: []string{"*"}, Detail: "Traitement préventif recommandé selon la région et la saison"},
	{Country: "IN", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet"},

	// China
	{Country: "CN", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A/B)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A et B recommandées"},
	{Country: "CN", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet dans la plupart des régions"},

	// Morocco
	{Country: "MA", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A recommandée"},
	{Country: "MA", Type: "eau", Label: "Eau - prudence",
		AppliesTo: []string{"*"}, Detail: "Privilégier l'eau en bouteille hors grandes villes"},

	// African countries with yellow fever zones (example: Kenya, Tanzania)
	{Country: "KE", Type: "vaccins", Label: "Fièvre jaune (obligatoire)",
		AppliesTo: []string{"*"}, Detail: "Certificat de vaccination fièvre jaune obligatoire"},
	{Country: "KE", Type: "paludisme", Label: "Risque élevé de paludisme",
		AppliesTo: []string{"*"}, Detail: "Traitement préventif obligatoire"},
	{Country: "KE", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet"},

	{Country: "TZ", Type: "vaccins", Label: "Fièvre jaune (obligatoire)",
		AppliesTo: []string{"*"}, Detail: "Certificat de vaccination fièvre jaune obligatoire"},
	{Country: "TZ", Type: "paludisme", Label: "Risque élevé de paludisme",
		AppliesTo: []string{"*"}, Detail: "Traitement préventif obligatoire"},
	{Country: "TZ", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet"},

	// Brazil
	{Country: "BR", Type: "vaccins", Label: "Fièvre jaune (recommandée, certaines régions)",
		AppliesTo: []string{"*"}, Detail: "Vaccination fièvre jaune recommandée pour certaines régions"},
	{Country: "BR", Type: "paludisme", Label: "Risque de paludisme (Amazonie)",
		AppliesTo: []string{"*"}, Detail: "Traitement préventif pour la région amazonienne"},
	{Country: "BR", Type: "eau", Label: "Eau - prudence",
		AppliesTo: []string{"*"}, Detail: "Privilégier l'eau en bouteille hors grandes villes"},

	// Peru (altitude)
	{Country: "PE", Type: "altitude", Label: "Risque de mal d'altitude (> 2500m)",
		AppliesTo: []string{"*"}, Detail: "Acclimatation progressive recommandée (Cusco, Lac Titicaca)"},
	{Country: "PE", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A, Fièvre jaune zones amazoniennes)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A recommandée, Fièvre jaune pour l'Amazonie"},

	// Nepal (altitude)
	{Country: "NP", Type: "altitude", Label: "Risque de mal d'altitude (treks en haute montagne)",
		AppliesTo: []string{"*"}, Detail: "Acclimatation obligatoire, Diamox recommandé pour les treks > 3000m"},
	{Country: "NP", Type: "vaccins", Label: "Vaccinations recommandées (Hépatite A/B, Typhoïde)",
		AppliesTo: []string{"*"}, Detail: "Hépatite A, Hépatite B, Typhoïde recommandées"},
	{Country: "NP", Type: "eau", Label: "Eau non potable",
		AppliesTo: []string{"*"}, Detail: "Ne pas boire l'eau du robinet"},

	// Generic trousse (first-aid kit) for non-safe countries
	{Country: "*", Type: "trousse", Label: "Trousse de secours recommandée",
		AppliesTo: []string{"*"}, Detail: "Emporter une trousse de premiers secours adaptée à la destination"},
}

// MatchHealthRules crosses detected countries with traveler nationalities.
// If ALL countries are in noAdviceCountries, returns nil (verdict "none").
func MatchHealthRules(countries []string, nationalities []string) []HealthCheckItem {
	// Check if all destinations are in noAdviceCountries
	allSafe := true
	for _, c := range countries {
		if !noAdviceCountries[c] {
			allSafe = false
			break
		}
	}
	if allSafe {
		return nil // verdict "none"
	}

	countrySet := toSet(countries)
	var items []HealthCheckItem

	for _, rule := range baseHealthRules {
		// Generic rules apply if at least one country is not in noAdviceCountries
		if rule.Country == "*" {
			// Only add generic rules once
			items = append(items, HealthCheckItem{
				Country: "*",
				Type:    rule.Type,
				Label:   rule.Label,
				Status:  "warning",
				Detail:  rule.Detail,
			})
			continue
		}

		if !countrySet[rule.Country] {
			continue
		}

		// Skip health rules for safe countries
		if noAdviceCountries[rule.Country] {
			continue
		}

		status := "warning"
		if rule.Type == "vaccins" || rule.Type == "paludisme" {
			status = "action_required"
		}

		items = append(items, HealthCheckItem{
			Country: rule.Country,
			Type:    rule.Type,
			Label:   rule.Label,
			Status:  status,
			Detail:  rule.Detail,
		})
	}

	return items
}
