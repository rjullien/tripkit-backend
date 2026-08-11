// Package dailybrief implements the Daily Brief pipeline:
// extract → enrich → Bifrost format → QA → GoWA send.
package dailybrief

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
	defaultRepo      = "rjullien/tripkit"
	defaultPath      = "ops/daily-brief.json"
	defaultRef       = "main"
	defaultTTL       = 2 * time.Minute
	defaultGowaURL    = "http://gowa.gowa.svc.cluster.local:3000"
	defaultBifrostURL = "http://bifrost.openclaw.svc.cluster.local:8080/v1"
	defaultModel      = "opencode-go/deepseek-v4-pro"
	defaultCron       = "30 6 * * *"
)

// Config is non-secret Daily Brief runtime config (SoT: ops/daily-brief.json).
type Config struct {
	GowaBaseURL    string `json:"gowaBaseUrl"`
	BifrostBaseURL string `json:"bifrostBaseUrl"`
	BriefModel     string `json:"briefModel"`
	AdminPhone     string `json:"adminPhone"`
	// SendLocalHour / SendLocalMinute: wall-clock in the *day's* IANA TZ (not homeTz).
	// Example: 8, 0 → fire when it is 08:00 in America/Toronto on that day's location.
	SendLocalHour   int    `json:"sendLocalHour"`
	SendLocalMinute int    `json:"sendLocalMinute"`
	// Cron is legacy "M H * * *" — used only if sendLocalHour is unset (0) AND cron set.
	Cron  string `json:"cron,omitempty"`
	Notes string `json:"notes,omitempty"`

	BifrostAPIKey string `json:"-"` // from env only
	Origin        string `json:"-"` // github|cache|dogfood|env
}

// DefaultConfig is compiled-in dogfood (cluster DNS + model only).
// No PII: adminPhone and WhatsApp destinations live in private ops/seeds (rjullien/tripkit).
func DefaultConfig() Config {
	return Config{
		GowaBaseURL:     defaultGowaURL,
		BifrostBaseURL:  defaultBifrostURL,
		BriefModel:      defaultModel,
		AdminPhone:      "", // from ops/daily-brief.json only
		SendLocalHour:   8,
		SendLocalMinute: 0,
		Cron:            defaultCron,
		Origin:          "dogfood",
	}
}

// SendHourMinute returns the local wall-clock to fire (hour, minute).
// Prefer sendLocalHour/Minute from ops JSON; fall back to legacy cron "M H * * *".
func (c Config) SendHourMinute() (hour, minute int) {
	if c.Cron != "" && c.SendLocalHour == 0 && c.SendLocalMinute == 0 {
		m, h := parseCronHM(c.Cron)
		return h, m
	}
	if c.SendLocalHour == 0 && c.SendLocalMinute == 0 {
		return 8, 0
	}
	return c.SendLocalHour, c.SendLocalMinute
}

// Loader fetches ops/daily-brief.json via GitHub PAT + disk cache (Publish pattern).
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
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_DAILY_BRIEF_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-daily-brief.json")
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_DAILY_BRIEF_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &Loader{
		GitHub:    publish.NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_DAILY_BRIEF_REPO", defaultRepo),
		Ref:       envOr("TRIPKIT_DAILY_BRIEF_REF", defaultRef),
		Path:      envOr("TRIPKIT_DAILY_BRIEF_PATH", defaultPath),
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

// Bootstrap loads config once at process start.
func (l *Loader) Bootstrap() Config {
	if l == nil {
		c := DefaultConfig()
		c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
		return c
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_DAILY_BRIEF_JSON")); raw != "" {
		if c, err := parseConfigJSON([]byte(raw)); err == nil {
			c.Origin = "env"
			c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
			l.cfg = c
			l.lastFetch = time.Now() // keep env override for TTL window
			return l.cfg
		}
		log.Printf("dailybrief: TRIPKIT_DAILY_BRIEF_JSON invalid, falling back")
	}

	if raw, err := l.fetchGitHub(); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			_ = os.WriteFile(l.CachePath, raw, 0o600)
			c.Origin = "github"
			c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
			l.cfg = c
			l.lastFetch = time.Now()
			return l.cfg
		}
	}

	if raw, err := os.ReadFile(l.CachePath); err == nil {
		if c, err := parseConfigJSON(raw); err == nil {
			c.Origin = "cache"
			c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
			l.cfg = c
			return l.cfg
		}
	}

	c := DefaultConfig()
	c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
	l.cfg = c
	return l.cfg
}

// Get returns the current config (refreshing GitHub when TTL elapsed).
func (l *Loader) Get() Config {
	if l == nil {
		c := DefaultConfig()
		c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
		return c
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.lastFetch) >= l.TTL {
		if raw, err := l.fetchGitHub(); err == nil {
			if c, err := parseConfigJSON(raw); err == nil {
				_ = os.WriteFile(l.CachePath, raw, 0o600)
				c.Origin = "github"
				c.BifrostAPIKey = strings.TrimSpace(os.Getenv("TRIPKIT_BIFROST_API_KEY"))
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
	if strings.TrimSpace(c.GowaBaseURL) == "" || strings.TrimSpace(c.BifrostBaseURL) == "" || strings.TrimSpace(c.BriefModel) == "" {
		return Config{}, fmt.Errorf("missing required daily-brief keys")
	}
	c.GowaBaseURL = strings.TrimRight(strings.TrimSpace(c.GowaBaseURL), "/")
	c.BifrostBaseURL = strings.TrimRight(strings.TrimSpace(c.BifrostBaseURL), "/")
	c.AdminPhone = strings.TrimSpace(c.AdminPhone)
	// Default 08:00 local if neither sendLocal* nor cron provided.
	if c.SendLocalHour == 0 && c.SendLocalMinute == 0 && c.Cron == "" {
		c.SendLocalHour = 8
		c.SendLocalMinute = 0
	}
	return c, nil
}
