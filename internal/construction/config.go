package construction

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
	defaultRepo       = "rjullien/tripkit"
	defaultPath       = "ops/construction.json"
	defaultRef        = "main"
	defaultTTL        = 2 * time.Minute
	defaultBifrostURL = "http://bifrost.openclaw.svc.cluster.local:8080/v1"

	defaultAdminModel     = "opencode-go/deepseek-v4-flash"
	defaultHealthModel    = "opencode-go/deepseek-v4-flash"
	defaultNuisanceModel  = "opencode-go/deepseek-v4-pro"
	defaultDiscoveryModel = "opencode-go/deepseek-v4-flash"

	defaultDriveHardLimitMinutes = 480
	defaultCorridorSampleKm      = 40
)

// Models holds the per-sub-feature LLM model allowlist (SoT: ops/construction.json).
// Each check picks its own model so a cheap flash model can format admin results
// while a stronger model writes the nuisance synthesis.
type Models struct {
	AdminCheck    string `json:"adminCheck"`
	HealthCheck   string `json:"healthCheck"`
	Nuisance      string `json:"nuisance"`
	DiscoveryRank string `json:"discoveryRank"`
}

// QAThresholds are the QA seuils read from ops, never hardcoded in Go.
type QAThresholds struct {
	DriveHardLimitMinutes int     `json:"driveHardLimitMinutes"`
	CorridorSampleKm      float64 `json:"corridorSampleKm"`
}

// FormalitiesConfig carries the ops overlay on top of the embedded rules base.
type FormalitiesConfig struct {
	Overrides map[string]json.RawMessage `json:"overrides"`
}

// Config is non-secret construction runtime config (SoT: ops/construction.json).
type Config struct {
	Enabled        bool              `json:"enabled"`
	BifrostBaseURL string            `json:"bifrostBaseUrl"`
	Models         Models            `json:"models"`
	LeoModes       []string          `json:"leoModes"`
	QA             QAThresholds      `json:"qa"`
	Formalities    FormalitiesConfig `json:"formalities"`
	Notes          string            `json:"notes,omitempty"`

	BifrostAPIKey string `json:"-"` // from env only
	Origin        string `json:"-"` // github|cache|dogfood|env
}

// DefaultConfig is compiled-in dogfood (must match ops/construction.json).
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		BifrostBaseURL: defaultBifrostURL,
		Models: Models{
			AdminCheck:    defaultAdminModel,
			HealthCheck:   defaultHealthModel,
			Nuisance:      defaultNuisanceModel,
			DiscoveryRank: defaultDiscoveryModel,
		},
		LeoModes: []string{
			"construction:ideation",
			"construction:route",
			"construction:activities",
			"construction:profile-edit",
		},
		QA: QAThresholds{
			DriveHardLimitMinutes: defaultDriveHardLimitMinutes,
			CorridorSampleKm:      defaultCorridorSampleKm,
		},
		Origin: "dogfood",
	}
}

// Ready reports whether the LLM formatting layer can be used at all
// (enabled + a Bifrost base URL). Individual checks additionally need a model.
func (c Config) Ready() bool {
	return c.Enabled && strings.TrimSpace(c.BifrostBaseURL) != ""
}

// ModelFor returns the model for a sub-feature, falling back to the dogfood
// default so a partially filled ops file never silences a check.
func (c Config) ModelFor(feature string) string {
	var m string
	switch feature {
	case "adminCheck":
		m = c.Models.AdminCheck
	case "healthCheck":
		m = c.Models.HealthCheck
	case "nuisance":
		m = c.Models.Nuisance
	case "discoveryRank":
		m = c.Models.DiscoveryRank
	}
	if strings.TrimSpace(m) != "" {
		return strings.TrimSpace(m)
	}
	d := DefaultConfig()
	switch feature {
	case "adminCheck":
		return d.Models.AdminCheck
	case "healthCheck":
		return d.Models.HealthCheck
	case "nuisance":
		return d.Models.Nuisance
	case "discoveryRank":
		return d.Models.DiscoveryRank
	}
	return ""
}

// Loader fetches ops/construction.json via GitHub PAT + disk cache.
// Same discipline as pluschat/discovery: env JSON → GitHub → disk cache → dogfood.
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

// NewLoaderFromEnv builds a loader with safe defaults.
func NewLoaderFromEnv() *Loader {
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_CONSTRUCTION_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-construction.json")
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_CONSTRUCTION_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &Loader{
		GitHub:    publish.NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_CONSTRUCTION_REPO", defaultRepo),
		Ref:       envOr("TRIPKIT_CONSTRUCTION_REF", defaultRef),
		Path:      envOr("TRIPKIT_CONSTRUCTION_PATH", defaultPath),
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

func withAPIKey(c Config) Config {
	c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
	return c
}

// Bootstrap loads config once at process start.
func (l *Loader) Bootstrap() Config {
	if l == nil {
		return withAPIKey(DefaultConfig())
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_CONSTRUCTION_JSON")); raw != "" {
		if c, err := parseConfigJSON([]byte(raw)); err == nil {
			c.Origin = "env"
			l.cfg = withAPIKey(c)
			l.lastFetch = time.Now()
			return l.cfg
		}
		log.Printf("construction: TRIPKIT_CONSTRUCTION_JSON invalid, falling back")
	}

	if raw, err := l.fetchGitHub(); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			_ = os.WriteFile(l.CachePath, raw, 0o600)
			c.Origin = "github"
			l.cfg = withAPIKey(c)
			l.lastFetch = time.Now()
			return l.cfg
		}
	}

	if raw, err := os.ReadFile(l.CachePath); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			c.Origin = "cache"
			l.cfg = withAPIKey(c)
			return l.cfg
		}
	}

	l.cfg = withAPIKey(DefaultConfig())
	return l.cfg
}

// Get returns the current config (refreshing from GitHub when the TTL elapsed).
func (l *Loader) Get() Config {
	if l == nil {
		return withAPIKey(DefaultConfig())
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.lastFetch) >= l.TTL {
		if raw, err := l.fetchGitHub(); err == nil {
			if c, err := parseConfigJSON(raw); err == nil {
				_ = os.WriteFile(l.CachePath, raw, 0o600)
				c.Origin = "github"
				l.cfg = withAPIKey(c)
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
	// enabled defaults to true when omitted (JSON false is explicit).
	if !jsonHasKey(raw, "enabled") {
		c.Enabled = true
	}
	if strings.TrimSpace(c.BifrostBaseURL) == "" {
		return Config{}, fmt.Errorf("construction: missing bifrostBaseUrl")
	}
	c.BifrostBaseURL = strings.TrimRight(strings.TrimSpace(c.BifrostBaseURL), "/")

	// Thresholds: fall back to dogfood rather than to a zero value, because a
	// zero driveHardLimitMinutes would make every day a QA violation.
	d := DefaultConfig()
	if c.QA.DriveHardLimitMinutes <= 0 {
		c.QA.DriveHardLimitMinutes = d.QA.DriveHardLimitMinutes
	}
	if c.QA.CorridorSampleKm <= 0 {
		c.QA.CorridorSampleKm = d.QA.CorridorSampleKm
	}
	if len(c.LeoModes) == 0 {
		c.LeoModes = d.LeoModes
	}
	return c, nil
}

func jsonHasKey(raw []byte, key string) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe[key]
	return ok
}
