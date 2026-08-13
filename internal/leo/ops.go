package leo

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
	defaultOpsRepo = "rjullien/tripkit"
	defaultOpsPath = "ops/leo.json"
	defaultOpsRef  = "main"
	defaultOpsTTL  = 2 * time.Minute
	// Matches Hermes-Léo config.yaml `model.provider: custom`.
	// Bare `model` without provider is ignored (Hermes stays on default).
	hermesProvider  = "custom"
	defaultLeoModel = "opencode-go/deepseek-v4-flash"
)

// ModelOption is one allowlisted Bifrost id shown in the Plus UI.
type ModelOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// OpsConfig is non-secret Léo model allowlist (SoT: ops/leo.json).
type OpsConfig struct {
	DefaultModel string        `json:"defaultModel"`
	Models       []ModelOption `json:"models"`
	Notes        string        `json:"notes,omitempty"`
	Origin       string        `json:"-"`
}

// DefaultOpsConfig is compiled-in dogfood (same V1 list as ops/leo.json).
func DefaultOpsConfig() OpsConfig {
	return OpsConfig{
		DefaultModel: defaultLeoModel,
		Models: []ModelOption{
			{ID: "opencode-go/deepseek-v4-flash", Label: "Flash"},
			{ID: "opencode-go/deepseek-v4-pro", Label: "Pro"},
			{ID: "opencode-go/glm-5.2", Label: "GLM 5.2"},
		},
		Origin: "dogfood",
	}
}

// Resolve returns an allowlisted Bifrost id. Unknown / empty → default.
func (c OpsConfig) Resolve(requested string) string {
	c = c.normalized()
	want := strings.TrimSpace(requested)
	for _, m := range c.Models {
		if m.ID == want {
			return m.ID
		}
	}
	return c.DefaultModel
}

func (c OpsConfig) normalized() OpsConfig {
	c.DefaultModel = strings.TrimSpace(c.DefaultModel)
	if c.DefaultModel == "" || len(c.Models) == 0 {
		return DefaultOpsConfig()
	}
	out := make([]ModelOption, 0, len(c.Models))
	seen := map[string]bool{}
	for _, m := range c.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(m.Label)
		if label == "" {
			label = id
		}
		out = append(out, ModelOption{ID: id, Label: label})
	}
	if len(out) == 0 {
		return DefaultOpsConfig()
	}
	if !seen[c.DefaultModel] {
		out = append([]ModelOption{{ID: c.DefaultModel, Label: c.DefaultModel}}, out...)
	}
	c.Models = out
	return c
}

// OpsLoader fetches ops/leo.json via GitHub PAT + disk cache.
type OpsLoader struct {
	GitHub    *publish.GitHubClient
	Repo      string
	Ref       string
	Path      string
	CachePath string
	TTL       time.Duration

	mu        sync.Mutex
	cfg       OpsConfig
	lastFetch time.Time
}

// NewOpsLoaderFromEnv builds a loader with safe defaults.
func NewOpsLoaderFromEnv() *OpsLoader {
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_LEO_OPS_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-leo-ops.json")
	}
	ttl := defaultOpsTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_LEO_OPS_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &OpsLoader{
		GitHub:    publish.NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_LEO_OPS_REPO", defaultOpsRepo),
		Ref:       envOr("TRIPKIT_LEO_OPS_REF", defaultOpsRef),
		Path:      envOr("TRIPKIT_LEO_OPS_PATH", defaultOpsPath),
		CachePath: cache,
		TTL:       ttl,
		cfg:       DefaultOpsConfig(),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Bootstrap loads config once at process start.
func (l *OpsLoader) Bootstrap() OpsConfig {
	if l == nil {
		return DefaultOpsConfig()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_LEO_OPS_JSON")); raw != "" {
		if c, err := parseOpsJSON([]byte(raw)); err == nil {
			c.Origin = "env"
			l.cfg = c
			l.lastFetch = time.Now()
			return l.cfg
		}
		log.Printf("leo ops: TRIPKIT_LEO_OPS_JSON invalid, falling back")
	}

	if raw, err := l.fetchGitHub(); err == nil {
		if c, err := parseOpsJSON(raw); err == nil {
			_ = os.WriteFile(l.CachePath, raw, 0o600)
			c.Origin = "github"
			l.cfg = c
			l.lastFetch = time.Now()
			return l.cfg
		}
	}

	if raw, err := os.ReadFile(l.CachePath); err == nil {
		if c, err := parseOpsJSON(raw); err == nil {
			c.Origin = "cache"
			l.cfg = c
			return l.cfg
		}
	}

	l.cfg = DefaultOpsConfig()
	return l.cfg
}

// Get returns the current config (refreshing GitHub when TTL elapsed).
func (l *OpsLoader) Get() OpsConfig {
	if l == nil {
		return DefaultOpsConfig()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.lastFetch) >= l.TTL {
		if raw, err := l.fetchGitHub(); err == nil {
			if c, err := parseOpsJSON(raw); err == nil {
				_ = os.WriteFile(l.CachePath, raw, 0o600)
				c.Origin = "github"
				l.cfg = c
				l.lastFetch = time.Now()
			}
		}
	}
	return l.cfg
}

func (l *OpsLoader) fetchGitHub() ([]byte, error) {
	if l.GitHub == nil {
		return nil, fmt.Errorf("no github client")
	}
	return l.GitHub.FetchFile(l.Repo, l.Ref, l.Path)
}

func parseOpsJSON(raw []byte) (OpsConfig, error) {
	var c OpsConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return OpsConfig{}, err
	}
	c.DefaultModel = strings.TrimSpace(c.DefaultModel)
	if c.DefaultModel == "" {
		return OpsConfig{}, fmt.Errorf("missing defaultModel")
	}
	out := make([]ModelOption, 0, len(c.Models))
	seen := map[string]bool{}
	for _, m := range c.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(m.Label)
		if label == "" {
			label = id
		}
		out = append(out, ModelOption{ID: id, Label: label})
	}
	if len(out) == 0 {
		return OpsConfig{}, fmt.Errorf("missing models")
	}
	if !seen[c.DefaultModel] {
		out = append([]ModelOption{{ID: c.DefaultModel, Label: c.DefaultModel}}, out...)
	}
	c.Models = out
	return c, nil
}
