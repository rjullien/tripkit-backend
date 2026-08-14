package discovery

// Theme is one catalogue entry (ops/discovery-themes.json).
type Theme struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Emoji      string   `json:"emoji,omitempty"`
	Engine     string   `json:"engine"` // geo | editorial
	Corridor   bool     `json:"corridor,omitempty"`
	Seasonal   bool     `json:"seasonal,omitempty"`
	RadiusKm   float64  `json:"radiusKm,omitempty"`
	Overpass   []string `json:"overpass,omitempty"` // key=value tags, never hardcoded in Go
	QueryHints []string `json:"queryHints,omitempty"`
	Origin     string   `json:"origin,omitempty"` // template | added | override
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
}

// Item is one search hit (candidate, not a seed decision).
type Item struct {
	ID      string  `json:"id"`
	ThemeID string  `json:"themeId"`
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	DistKm  float64 `json:"distKm"`
	URL     string  `json:"url,omitempty"`
	When    string  `json:"when,omitempty"`
	Note    string  `json:"note,omitempty"`
	Source  string  `json:"source,omitempty"` // osm | editorial
	Cached  bool    `json:"cached,omitempty"`
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
