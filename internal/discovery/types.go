package discovery

// Theme is one catalogue entry (ops/discovery-themes.json).
type Theme struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Emoji        string   `json:"emoji,omitempty"`
	Engine       string   `json:"engine"` // geo | editorial
	Corridor     bool     `json:"corridor,omitempty"`
	Seasonal     bool     `json:"seasonal,omitempty"`
	RadiusKm     float64  `json:"radiusKm,omitempty"`
	Overpass     []string `json:"overpass,omitempty"` // key=value tags, never hardcoded in Go
	ExcludeNames []string `json:"excludeNames,omitempty"`
	QueryHints   []string `json:"queryHints,omitempty"`
	Origin       string   `json:"origin,omitempty"` // template | added | override

	// KeepUnnamed keeps OSM features that carry no name. Discovery drops them
	// (an unnamed shop is not a suggestion), but a nuisance is a nuisance
	// whether or not it is named: measured against the live API, 22 of the 60
	// railway=rail ways around Toulouse Matabiau and *both* landuse=industrial
	// polygons near a hotel have no name tag. Dropping them scored the category
	// on zero items, i.e. a green "Aucun élément détecté" next to a railway.
	KeepUnnamed bool `json:"keepUnnamed,omitempty"`
}

// ThemePrefs is travel-profile.js → themes (family personalization).
type ThemePrefs struct {
	Disabled  []string         `json:"disabled"`
	Added     []Theme          `json:"added"`
	Overrides map[string]Theme `json:"overrides"`
}

// Scope is the geographic + calendar window of a search.
type Scope struct {
	DayNum     int    `json:"dayNum,omitempty"`
	LocationID string `json:"locationId,omitempty"`
	DateISO    string `json:"dateISO,omitempty"`
	// Corridor is [fromLocId, toLocId] — search along the drive, not around a point.
	Corridor []string `json:"corridor,omitempty"`
}

// Item is one search hit (candidate, not a seed decision).
type Item struct {
	ID              string  `json:"id"`
	ThemeID         string  `json:"themeId"`
	Name            string  `json:"name"`
	Lat             float64 `json:"lat"`
	Lon             float64 `json:"lon"`
	DistKm          float64 `json:"distKm"`
	DetourKm        float64 `json:"detourKm,omitempty"`
	DetourEstimated bool    `json:"detourEstimated,omitempty"`
	URL             string  `json:"url,omitempty"`
	When            string  `json:"when,omitempty"`
	Note            string  `json:"note,omitempty"`
	Source          string  `json:"source,omitempty"` // osm | editorial
	Cached          bool    `json:"cached,omitempty"`
}

// Result is the payload of one search (all requested themes).
type Result struct {
	Scope   Scope             `json:"scope"`
	Place   string            `json:"place,omitempty"`
	Lat     float64           `json:"lat,omitempty"`
	Lon     float64           `json:"lon,omitempty"`
	Themes  []string          `json:"themes"`
	Items   []Item            `json:"items"`
	ByTheme map[string][]Item `json:"byTheme,omitempty"`
}

const (
	engineGeo         = "geo"
	engineEditorial   = "editorial"
	originTemplate    = "template"
	originAdded       = "added"
	originOverride    = "override"
	geoTTLHours       = 24
	editorialTTLHours = 6
)
