// Package pluschat is the Plus-tab Bifrost assistant (direct LLM, no Hermes tools).
// Config SoT: ops/plus-chat.json in rjullien/tripkit (same Loader pattern as dailybrief).
package pluschat

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
	defaultPath       = "ops/plus-chat.json"
	defaultRef        = "main"
	defaultTTL        = 2 * time.Minute
	defaultBifrostURL = "http://bifrost.openclaw.svc.cluster.local:8080/v1"
	defaultModel      = "opencode-go/deepseek-v4-pro"
	maxChatHistory    = 12
)

// Config is non-secret Plus chat runtime config (SoT: ops/plus-chat.json).
type Config struct {
	Enabled        bool   `json:"enabled"`
	BifrostBaseURL string `json:"bifrostBaseUrl"`
	ChatModel      string `json:"chatModel"`
	Notes          string `json:"notes,omitempty"`

	BifrostAPIKey string `json:"-"` // from env only
	Origin        string `json:"-"` // github|cache|dogfood|env
}

// DefaultConfig is compiled-in dogfood.
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		BifrostBaseURL: defaultBifrostURL,
		ChatModel:      defaultModel,
		Origin:         "dogfood",
	}
}

// Ready reports whether the assistant can accept chat (enabled + URL + model).
func (c Config) Ready() bool {
	return c.Enabled &&
		strings.TrimSpace(c.BifrostBaseURL) != "" &&
		strings.TrimSpace(c.ChatModel) != ""
}

// Loader fetches ops/plus-chat.json via GitHub PAT + disk cache.
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
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_PLUS_CHAT_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-plus-chat.json")
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_PLUS_CHAT_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &Loader{
		GitHub:    publish.NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_PLUS_CHAT_REPO", defaultRepo),
		Ref:       envOr("TRIPKIT_PLUS_CHAT_REF", defaultRef),
		Path:      envOr("TRIPKIT_PLUS_CHAT_PATH", defaultPath),
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

	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_PLUS_CHAT_JSON")); raw != "" {
		if c, err := parseConfigJSON([]byte(raw)); err == nil {
			c.Origin = "env"
			l.cfg = withAPIKey(c)
			l.lastFetch = time.Now()
			return l.cfg
		}
		log.Printf("pluschat: TRIPKIT_PLUS_CHAT_JSON invalid, falling back")
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

// Get returns the current config (refreshing GitHub when TTL elapsed).
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
	if !jsonHasEnabled(raw) {
		c.Enabled = true
	}
	if strings.TrimSpace(c.BifrostBaseURL) == "" || strings.TrimSpace(c.ChatModel) == "" {
		return Config{}, fmt.Errorf("missing required plus-chat keys")
	}
	c.BifrostBaseURL = strings.TrimRight(strings.TrimSpace(c.BifrostBaseURL), "/")
	c.ChatModel = strings.TrimSpace(c.ChatModel)
	return c, nil
}

func jsonHasEnabled(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe["enabled"]
	return ok
}
