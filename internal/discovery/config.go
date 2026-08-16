package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

const (
	defaultRepo        = "rjullien/tripkit"
	defaultPath        = "ops/discovery-themes.json"
	defaultRef         = "main"
	defaultTTL         = 2 * time.Minute
	defaultOverpassURL = "https://overpass-api.de/api/interpreter"

	// Overpass timeouts. defaultTimeoutSec is the QL budget announced to the
	// server: raised from 25s because a 25s ceiling turned every busy moment on
	// the public instance into "Donnée indisponible". maxTimeoutSec is the hard
	// ceiling for a configured value — beyond ~300s overpass-api.de answers 504
	// anyway, and holding a slot that long makes the rate limiting worse.
	defaultTimeoutSec = 90
	maxTimeoutSec     = 180
	// httpTimeoutMargin keeps our HTTP deadline above the server's QL budget so
	// a server-side timeout arrives as a readable remark instead of a client
	// deadline. Sized for a slow mirror: kumi.systems has been measured taking
	// ~20s just to start answering.
	httpTimeoutMargin = 15 * time.Second

	// Retry budget. Attempts rotate over primary + mirrors, so the default of 3
	// gives each configured endpoint one shot.
	defaultAttempts     = 3
	maxAttempts         = 6
	defaultRetryBase    = 2 * time.Second
	maxRetryDelay       = 20 * time.Second
	maxRetryAfter       = 60 * time.Second
	endpointSwitchDelay = 500 * time.Millisecond

	// defaultConcurrency is deliberately 2, not 4: the public Overpass API
	// allots roughly two slots per IP, and the whole backend shares one egress
	// IP. Fanning out wider is what produced the 429s that the nuisance check
	// then reported as unavailable data.
	defaultConcurrency = 2
)

// defaultMirrors are the fallback Overpass endpoints, tried in order when the
// primary fails. Both were checked to be planet-wide, not regional extracts:
// the same query in Manhattan returns the same 77 elements as the main
// instance. overpass.osm.ch is deliberately absent — it only holds Switzerland
// and would answer "0 element" everywhere else, i.e. a false green verdict.
var defaultMirrors = []string{
	"https://overpass.kumi.systems/api/interpreter",
	"https://overpass.openstreetmap.fr/api/interpreter",
}

// Config is ops/discovery-themes.json (catalogue + Overpass runtime).
type Config struct {
	Version  int            `json:"version"`
	Overpass OverpassConfig `json:"overpass"`
	Themes   []Theme        `json:"themes"`
	Origin   string         `json:"-"`
}

// OverpassConfig is the Overpass runtime: primary endpoint, fallback mirrors and
// the retry budget. Everything here is tunable from ops/discovery-themes.json
// without a redeploy, which is the point — a dedicated instance later only
// changes baseUrl.
type OverpassConfig struct {
	BaseURL        string   `json:"baseUrl"`
	Mirrors        []string `json:"mirrors,omitempty"`
	TimeoutSec     int      `json:"timeoutSec"`
	Concurrency    int      `json:"concurrency"`
	MaxAttempts    int      `json:"maxAttempts,omitempty"`
	RetryBackoffMs int      `json:"retryBackoffMs,omitempty"`
}

// DefaultConfig is compiled-in dogfood (must match ops/discovery-themes.json).
func DefaultConfig() Config {
	return Config{
		Version: 2,
		Overpass: OverpassConfig{
			BaseURL:     defaultOverpassURL,
			Mirrors:     append([]string(nil), defaultMirrors...),
			TimeoutSec:  defaultTimeoutSec,
			Concurrency: defaultConcurrency,
			MaxAttempts: defaultAttempts,
		},
		Themes: []Theme{
			{ID: "magasinage", Label: "Magasinage", Emoji: "🛍️", Engine: engineGeo, Corridor: true, RadiusKm: 15,
				Overpass:     []string{"shop=mall", "shop=clothes", "shop=gift", "shop=jewelry", "shop=shoes", "shop=bag", "shop=craft", "amenity=marketplace"},
				ExcludeNames: []string{"canadian tire", "home depot", "rona", "réno-dépôt", "reno-depot", "walmart", "costco", "ikea", "bureau en gros", "staples", "princess auto"},
				QueryHints:   []string{"boutique", "souvenirs", "artisanat", "mode", "cadeaux", "marché local"}},
			{ID: "outlets", Label: "Outlets & bons plans", Emoji: "🏷️", Engine: engineGeo, Corridor: true, RadiusKm: 25,
				Overpass:   []string{"shop=outlet", "shop=mall"},
				QueryHints: []string{"factory outlet", "village de marques", "premium outlets"}},
			{ID: "rando", Label: "Rando, nature & montagne", Emoji: "🥾", Engine: engineGeo, Corridor: true, RadiusKm: 30,
				Overpass:   []string{"tourism=viewpoint", "leisure=nature_reserve", "natural=peak"},
				QueryHints: []string{"randonnée", "sentier", "parc national", "point de vue"}},
			{ID: "eau", Label: "Mer, lacs & rivières", Emoji: "🌊", Engine: engineGeo, Corridor: true, RadiusKm: 25,
				Overpass:   []string{"natural=beach", "leisure=swimming_area", "waterway=waterfall"},
				QueryHints: []string{"plage", "lac", "cascade", "baignade"}},
			{ID: "parcs", Label: "Parcs d'attractions", Emoji: "🎡", Engine: engineGeo, Corridor: true, RadiusKm: 40,
				Overpass:   []string{"tourism=theme_park", "leisure=water_park"},
				QueryHints: []string{"parc d'attractions", "parc aquatique"}},
			{ID: "spectacles", Label: "Spectacles fixes", Emoji: "🎭", Engine: engineEditorial,
				QueryHints: []string{"spectacle", "théâtre", "concert", "cabaret"}},
			{ID: "festivals", Label: "Festivals & saisonnier", Emoji: "🎪", Engine: engineEditorial, Seasonal: true,
				QueryHints: []string{"festival", "événement saisonnier", "fête locale"}},
		},
		Origin: "dogfood",
	}
}

func (c Config) Ready() bool {
	return len(c.Themes) > 0 && strings.TrimSpace(c.Overpass.BaseURL) != ""
}

func (c Config) GeoThemes() []Theme {
	var out []Theme
	for _, t := range c.Themes {
		if t.Engine == engineGeo {
			out = append(out, t)
		}
	}
	return out
}

// Loader fetches ops/discovery-themes.json via GitHub PAT + disk cache.
type Loader struct {
	GitHub    *publish.GitHubClient
	Repo      string
	Ref       string
	Path      string
	CachePath string
	TTL       time.Duration

	mu        sync.Mutex
	cfg       Config
	lastFetch time.Time
}

func NewLoaderFromEnv() *Loader {
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_DISCOVERY_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-discovery-themes.json")
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_DISCOVERY_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &Loader{
		GitHub:    publish.NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_DISCOVERY_REPO", defaultRepo),
		Ref:       envOr("TRIPKIT_DISCOVERY_REF", defaultRef),
		Path:      envOr("TRIPKIT_DISCOVERY_PATH", defaultPath),
		CachePath: cache,
		TTL:       ttl,
		cfg:       DefaultConfig(),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (l *Loader) Bootstrap() Config {
	if l == nil {
		return DefaultConfig()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_DISCOVERY_JSON")); raw != "" {
		if c, err := parseConfigJSON([]byte(raw)); err == nil {
			c.Origin = "env"
			l.cfg = c
			l.lastFetch = time.Now()
			return l.cfg
		}
		log.Printf("discovery: TRIPKIT_DISCOVERY_JSON invalid, falling back")
	}

	if raw, err := l.fetchGitHub(); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			_ = os.WriteFile(l.CachePath, raw, 0o600)
			c.Origin = "github"
			l.cfg = c
			l.lastFetch = time.Now()
			return l.cfg
		}
	}

	if raw, err := os.ReadFile(l.CachePath); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			c.Origin = "cache"
			l.cfg = c
			return l.cfg
		}
	}

	l.cfg = DefaultConfig()
	return l.cfg
}

func (l *Loader) Get() Config {
	if l == nil {
		return DefaultConfig()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.lastFetch) >= l.TTL {
		if raw, err := l.fetchGitHub(); err == nil {
			if c, err := parseConfigJSON(raw); err == nil {
				_ = os.WriteFile(l.CachePath, raw, 0o600)
				c.Origin = "github"
				l.cfg = c
				l.lastFetch = time.Now()
			}
		}
	}
	return l.cfg
}

func (l *Loader) fetchGitHub() ([]byte, error) {
	if l.GitHub == nil {
		return nil, fmt.Errorf("no github client")
	}
	return l.GitHub.FetchFile(l.Repo, l.Ref, l.Path)
}

func parseConfigJSON(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, err
	}
	if len(c.Themes) == 0 {
		return Config{}, fmt.Errorf("missing themes")
	}
	ids := map[string]bool{}
	for i, t := range c.Themes {
		t.ID = strings.TrimSpace(t.ID)
		t.Engine = strings.TrimSpace(t.Engine)
		if t.ID == "" || (t.Engine != engineGeo && t.Engine != engineEditorial) {
			return Config{}, fmt.Errorf("themes[%d] invalid", i)
		}
		if ids[t.ID] {
			return Config{}, fmt.Errorf("duplicate theme id %s", t.ID)
		}
		ids[t.ID] = true
		if t.Engine == engineGeo && (len(t.Overpass) == 0 || t.RadiusKm <= 0) {
			return Config{}, fmt.Errorf("themes[%d] geo needs overpass tags and radiusKm", i)
		}
		c.Themes[i] = t
	}
	if strings.TrimSpace(c.Overpass.BaseURL) == "" {
		c.Overpass.BaseURL = defaultOverpassURL
	}
	c.Overpass.BaseURL = strings.TrimRight(strings.TrimSpace(c.Overpass.BaseURL), "/")
	// Mirrors are opt-out, not opt-in: a config written before mirrors existed
	// still gets the fallbacks. An explicit ["none"] disables them.
	if len(c.Overpass.Mirrors) == 0 {
		c.Overpass.Mirrors = append([]string(nil), defaultMirrors...)
	}
	c.Overpass.Mirrors = normalizeEndpoints(c.Overpass.Mirrors)
	if len(c.Overpass.Mirrors) == 1 && strings.EqualFold(c.Overpass.Mirrors[0], "none") {
		c.Overpass.Mirrors = nil
	}
	if c.Overpass.TimeoutSec <= 0 {
		c.Overpass.TimeoutSec = defaultTimeoutSec
	}
	if c.Overpass.TimeoutSec > maxTimeoutSec {
		log.Printf("discovery: overpass.timeoutSec=%d clamped to %d", c.Overpass.TimeoutSec, maxTimeoutSec)
		c.Overpass.TimeoutSec = maxTimeoutSec
	}
	if c.Overpass.Concurrency <= 0 {
		c.Overpass.Concurrency = defaultConcurrency
	}
	if c.Overpass.MaxAttempts <= 0 {
		c.Overpass.MaxAttempts = defaultAttempts
	}
	if c.Overpass.MaxAttempts > maxAttempts {
		c.Overpass.MaxAttempts = maxAttempts
	}
	return c, nil
}
