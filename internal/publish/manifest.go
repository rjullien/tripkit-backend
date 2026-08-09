package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ManifestFileName is the family-owned allowlist at the seed repo root.
const ManifestFileName = "publish-manifest.json"

var tripIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// PublishManifest lists which trips a family exposes to FE Publish.
// Trust boundary: paths/assets only — never publishers / enabled / family.
type PublishManifest struct {
	Version int       `json:"version"`
	Seeds   []SeedRef `json:"seeds"`
}

// ParsePublishManifest decodes and validates a repo publish-manifest.json.
func ParsePublishManifest(raw []byte) (*PublishManifest, error) {
	var m PublishManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("publish-manifest.json: %w", err)
	}
	if m.Version != 0 && m.Version != 1 {
		return nil, fmt.Errorf("publish-manifest.json: unsupported version %d", m.Version)
	}
	out := make([]SeedRef, 0, len(m.Seeds))
	seen := map[string]bool{}
	for _, s := range m.Seeds {
		s.TripID = strings.TrimSpace(s.TripID)
		s.Path = strings.TrimSpace(s.Path)
		s.Title = strings.TrimSpace(s.Title)
		if s.TripID == "" {
			continue
		}
		if !tripIDPattern.MatchString(s.TripID) {
			return nil, fmt.Errorf("publish-manifest.json: invalid tripId %q", s.TripID)
		}
		if seen[s.TripID] {
			return nil, fmt.Errorf("publish-manifest.json: duplicate tripId %q", s.TripID)
		}
		seen[s.TripID] = true
		if s.Path == "" {
			s.Path = s.TripID + ".js"
		}
		if err := validateRepoPath(s.Path); err != nil {
			return nil, fmt.Errorf("publish-manifest.json trip %s: %w", s.TripID, err)
		}
		cleanAssets := make([]string, 0, len(s.Assets))
		for _, a := range s.Assets {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			if err := validateRepoPath(a); err != nil {
				return nil, fmt.Errorf("publish-manifest.json trip %s asset: %w", s.TripID, err)
			}
			cleanAssets = append(cleanAssets, a)
		}
		s.Assets = cleanAssets
		out = append(out, s)
	}
	m.Seeds = out
	return &m, nil
}

func validateRepoPath(p string) error {
	p = strings.TrimPrefix(p, "./")
	if p == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return fmt.Errorf("invalid path %q", p)
	}
	// Keep files at repo root (V1) — no nested paths.
	if strings.Contains(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("path must be top-level file %q", p)
	}
	if path.Base(p) != p {
		return fmt.Errorf("invalid path %q", p)
	}
	return nil
}

// FindSeed returns a seed by trip id.
func (m *PublishManifest) FindSeed(tripID string) (SeedRef, bool) {
	if m == nil {
		return SeedRef{}, false
	}
	return FindSeedRef(m.Seeds, tripID)
}

// FindSeedRef looks up a trip in a seed list.
func FindSeedRef(seeds []SeedRef, tripID string) (SeedRef, bool) {
	for _, s := range seeds {
		if strings.EqualFold(s.TripID, tripID) {
			return s, true
		}
	}
	return SeedRef{}, false
}

type manifestCacheEntry struct {
	seeds   []SeedRef
	fetched time.Time
	err     error
}

// ManifestResolver loads family allowlists from GitHub (cached).
type ManifestResolver struct {
	GitHub *GitHubClient
	TTL    time.Duration
	mu     sync.Mutex
	cache  map[string]manifestCacheEntry
}

// NewManifestResolverFromEnv builds a resolver using TRIPKIT_GITHUB_TOKEN.
func NewManifestResolverFromEnv() *ManifestResolver {
	return &ManifestResolver{
		GitHub: NewGitHubClientFromEnv(),
		TTL:    2 * time.Minute,
		cache:  map[string]manifestCacheEntry{},
	}
}

func (r *ManifestResolver) cacheKey(src Source) string {
	ref := src.Ref
	if ref == "" {
		ref = "main"
	}
	return src.ID + "|" + src.Repo + "|" + ref
}

// SeedsForSource returns the publishable trips for a trusted source.
// Prefer repo publish-manifest.json; fall back to Source.Seeds (tests / emergency).
func (r *ManifestResolver) SeedsForSource(src Source) ([]SeedRef, error) {
	if r == nil {
		return fallbackSeeds(src), nil
	}
	if r.TTL <= 0 {
		r.TTL = 2 * time.Minute
	}
	if r.cache == nil {
		r.cache = map[string]manifestCacheEntry{}
	}

	key := r.cacheKey(src)
	r.mu.Lock()
	if ent, ok := r.cache[key]; ok && time.Since(ent.fetched) < r.TTL {
		r.mu.Unlock()
		if ent.err != nil && len(fallbackSeeds(src)) > 0 {
			return fallbackSeeds(src), nil
		}
		return ent.seeds, ent.err
	}
	r.mu.Unlock()

	seeds, err := r.fetch(src)
	r.mu.Lock()
	r.cache[key] = manifestCacheEntry{seeds: seeds, fetched: time.Now(), err: err}
	r.mu.Unlock()

	if err != nil && len(fallbackSeeds(src)) > 0 {
		// Catalogue must stay usable without Infisical token / when GitHub blips.
		// Worker still re-reads publish-manifest.json from the zip at apply time.
		return fallbackSeeds(src), nil
	}
	return seeds, err
}

func (r *ManifestResolver) fetch(src Source) ([]SeedRef, error) {
	if r.GitHub == nil || r.GitHub.Token == "" {
		return nil, fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	ref := src.Ref
	if ref == "" {
		ref = "main"
	}
	raw, err := r.GitHub.FetchFile(src.Repo, ref, ManifestFileName)
	if err != nil {
		return nil, err
	}
	m, err := ParsePublishManifest(raw)
	if err != nil {
		return nil, err
	}
	return m.Seeds, nil
}

func fallbackSeeds(src Source) []SeedRef {
	return append([]SeedRef(nil), src.Seeds...)
}

// Invalidate clears cached manifests (tests).
func (r *ManifestResolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.cache = map[string]manifestCacheEntry{}
	r.mu.Unlock()
}
