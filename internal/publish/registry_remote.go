package publish

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultPublishSourcesRepo = "rjullien/tripkit"
	defaultPublishSourcesPath = "ops/publish-sources.json"
	defaultPublishSourcesRef  = "main"
	defaultPublishSourcesTTL  = 2 * time.Minute
)

// SourcesLoader fetches ops/publish-sources.json from the private tripkit repo,
// copies it to a local cache when GitHub is available, and falls back to that
// cache (then DefaultDogfoodRegistry) when GitHub is down.
//
// No Infisical publish-sources key and no vps-infra JSON — only the existing
// TRIPKIT_GITHUB_TOKEN (Contents:read on rjullien/tripkit + seed repos).
type SourcesLoader struct {
	GitHub    *GitHubClient
	Repo      string
	Ref       string
	Path      string
	CachePath string
	TTL       time.Duration

	mu        sync.Mutex
	lastFetch time.Time // last successful GitHub fetch
	origin    string    // github | cache | dogfood | env
}

// NewSourcesLoaderFromEnv builds a loader from env (safe defaults).
func NewSourcesLoaderFromEnv() *SourcesLoader {
	cache := strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES_CACHE"))
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "tripkit-publish-sources.json")
	}
	ttl := defaultPublishSourcesTTL
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	return &SourcesLoader{
		GitHub:    NewGitHubClientFromEnv(),
		Repo:      envOr("TRIPKIT_PUBLISH_SOURCES_REPO", defaultPublishSourcesRepo),
		Ref:       envOr("TRIPKIT_PUBLISH_SOURCES_REF", defaultPublishSourcesRef),
		Path:      envOr("TRIPKIT_PUBLISH_SOURCES_PATH", defaultPublishSourcesPath),
		CachePath: cache,
		TTL:       ttl,
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Origin returns how the registry was last filled (github/cache/dogfood/env).
func (l *SourcesLoader) Origin() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.origin
}

// Bootstrap fills reg once at process start.
// Order: GitHub → disk cache → compiled-in dogfood. Always succeeds with sources.
func (l *SourcesLoader) Bootstrap(reg *Registry) string {
	if reg == nil {
		return ""
	}
	if l == nil {
		reg.ReplaceAll(DefaultDogfoodRegistry().Snapshot())
		return "dogfood"
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if raw, err := l.fetchGitHub(); err == nil {
		if list, err := ParseSourcesJSON(raw); err == nil {
			l.writeCache(raw)
			reg.ReplaceAll(list)
			l.lastFetch = time.Now()
			l.origin = "github"
			return l.origin
		}
	}

	if raw, err := os.ReadFile(l.CachePath); err == nil {
		if list, err := ParseSourcesJSON(raw); err == nil {
			reg.ReplaceAll(list)
			l.origin = "cache"
			return l.origin
		}
	}

	reg.ReplaceAll(DefaultDogfoodRegistry().Snapshot())
	l.origin = "dogfood"
	return l.origin
}

// TryRefresh updates reg from GitHub when the TTL elapsed.
// On GitHub failure, keeps the in-memory previous version (no wipe to dogfood).
func (l *SourcesLoader) TryRefresh(reg *Registry) {
	if l == nil || reg == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.TTL > 0 && !l.lastFetch.IsZero() && time.Since(l.lastFetch) < l.TTL {
		return
	}

	raw, err := l.fetchGitHub()
	if err != nil {
		return
	}
	list, err := ParseSourcesJSON(raw)
	if err != nil {
		log.Printf("publish-sources: github payload invalid: %v", err)
		return
	}
	l.writeCache(raw)
	reg.ReplaceAll(list)
	l.lastFetch = time.Now()
	l.origin = "github"
}

// StartRegistryRefresh periodically re-fetches from GitHub into reg.
// No-op when TRIPKIT_PUBLISH_SOURCES env override is active (loader.origin == env).
func StartRegistryRefresh(reg *Registry, loader *SourcesLoader) {
	if reg == nil || loader == nil {
		return
	}
	if strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES")) != "" {
		return
	}
	ttl := loader.TTL
	if ttl <= 0 {
		ttl = defaultPublishSourcesTTL
	}
	go func() {
		t := time.NewTicker(ttl)
		defer t.Stop()
		for range t.C {
			loader.TryRefresh(reg)
		}
	}()
}

func (l *SourcesLoader) fetchGitHub() ([]byte, error) {
	if l.GitHub == nil || l.GitHub.Token == "" {
		return nil, fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	repo := l.Repo
	if repo == "" {
		repo = defaultPublishSourcesRepo
	}
	ref := l.Ref
	if ref == "" {
		ref = defaultPublishSourcesRef
	}
	path := l.Path
	if path == "" {
		path = defaultPublishSourcesPath
	}
	return l.GitHub.FetchFile(repo, ref, path)
}

func (l *SourcesLoader) writeCache(raw []byte) {
	if l.CachePath == "" {
		return
	}
	dir := filepath.Dir(l.CachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("publish-sources: cache mkdir: %v", err)
		return
	}
	tmp := l.CachePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("publish-sources: cache write: %v", err)
		return
	}
	if err := os.Rename(tmp, l.CachePath); err != nil {
		log.Printf("publish-sources: cache rename: %v", err)
		_ = os.Remove(tmp)
	}
}
