package nuisance

// NuisanceCategory defines one axis of nuisance analysis: which OSM tags to
// query, the search radius in km, and the distance/count thresholds that
// determine the scoring level.
type NuisanceCategory struct {
	ID       string   // machine name
	Label    string   // human-readable (French)
	Emoji    string   // display icon
	Tags     []string // Overpass key=value pairs
	RadiusKm float64  // search radius for Overpass query

	// Distance-based thresholds (meters).
	// Red if distance < RedBelow, Yellow if < YellowBelow, else Green.
	RedBelow    float64
	YellowBelow float64

	// Count-based (nightlife): Red if count > RedAbove, Yellow if >= YellowAbove.
	CountBased bool
	RedAbove   int
	YellowAbove int
}

// Categories is the authoritative list of nuisance axes.
// Thresholds come from SPEC-nuisance-check.md section 4.1.
var Categories = []NuisanceCategory{
	{
		ID:          "trains",
		Label:       "Trains",
		Emoji:       "🚂",
		Tags:        []string{"railway=rail"},
		RadiusKm:    0.5,
		RedBelow:    200,
		YellowBelow: 500,
	},
	{
		ID:          "airports",
		Label:       "Aéroports",
		Emoji:       "✈️",
		Tags:        []string{"aeroway=aerodrome"},
		RadiusKm:    8,
		RedBelow:    3000,
		YellowBelow: 8000,
	},
	{
		ID:          "highways",
		Label:       "Autoroutes",
		Emoji:       "🛣️",
		Tags:        []string{"highway=motorway"},
		RadiusKm:    0.3,
		RedBelow:    100,
		YellowBelow: 300,
	},
	{
		ID:         "nightlife",
		Label:      "Vie nocturne",
		Emoji:      "🎵",
		Tags:       []string{"amenity=nightclub", "amenity=bar"},
		RadiusKm:   0.2,
		CountBased: true,
		RedAbove:   5,
		YellowAbove: 2,
	},
	{
		ID:          "industrial",
		Label:       "Industriel",
		Emoji:       "🏭",
		Tags:        []string{"landuse=industrial"},
		RadiusKm:    0.8,
		RedBelow:    300,
		YellowBelow: 800,
	},
	{
		ID:       "security",
		Label:    "Sécurité",
		Emoji:    "🔒",
		Tags:     nil, // placeholder: web reviews, not OSM-queryable
		RadiusKm: 0,
	},
}

// CategoryByID returns the category with the given ID, or nil if not found.
func CategoryByID(id string) *NuisanceCategory {
	for i := range Categories {
		if Categories[i].ID == id {
			return &Categories[i]
		}
	}
	return nil
}
