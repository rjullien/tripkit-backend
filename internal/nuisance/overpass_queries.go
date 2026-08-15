package nuisance

import "github.com/rjullien/tripkit-backend/internal/discovery"

// ThemeForCategory converts a NuisanceCategory into a discovery.Theme so that
// the existing discovery.Querier.Search can be reused without duplication.
func ThemeForCategory(cat NuisanceCategory) discovery.Theme {
	return discovery.Theme{
		ID:       "nuisance-" + cat.ID,
		Label:    cat.Label,
		Engine:   "geo",
		RadiusKm: cat.RadiusKm,
		Overpass: cat.Tags,
	}
}
