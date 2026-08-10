// Package publish implements trusted seed publishing (registry, apply, jobs).
package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Source is one trusted family seed repository mapping (ops/trust config).
// Trip allowlist lives in the seed repo as publish-manifest.json — not here.
// Seeds is an optional fallback for tests / emergency (TRIPKIT_PUBLISH_ALLOW_REGISTRY_SEEDS=1).
type Source struct {
	ID              string    `json:"id"`
	Repo            string    `json:"repo"` // owner/name
	Ref             string    `json:"ref"`
	ExpectedFamily  string    `json:"expectedFamily"`
	PublisherLogins []string  `json:"publisherLogins"`
	OwnerLogins     []string  `json:"ownerLogins"`
	Enabled         bool      `json:"enabled"`
	Seeds           []SeedRef `json:"seeds,omitempty"`
}

// SeedRef maps a trip id to its seed file path in the repo (from publish-manifest.json).
type SeedRef struct {
	TripID string   `json:"tripId"`
	Path   string   `json:"path"`
	Title  string   `json:"title,omitempty"`
	Assets []string `json:"assets,omitempty"`
}

// Registry is the in-memory trusted publish source list.
type Registry struct {
	mu      sync.RWMutex
	sources map[string]Source
}

// ParseSourcesJSON unmarshals a publish-sources.json array.
func ParseSourcesJSON(raw []byte) ([]Source, error) {
	var list []Source
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("publish-sources json: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("publish-sources json: empty array")
	}
	return list, nil
}

// LoadRegistryFromEnv reads TRIPKIT_PUBLISH_SOURCES (JSON array) or returns empty.
// Kept for tests / emergency override — prod prefers GitHub + disk cache.
func LoadRegistryFromEnv() (*Registry, error) {
	raw := strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES"))
	if raw == "" {
		return NewRegistry(nil), nil
	}
	list, err := ParseSourcesJSON([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("TRIPKIT_PUBLISH_SOURCES: %w", err)
	}
	return NewRegistry(list), nil
}

// NewRegistry builds a registry from sources.
func NewRegistry(sources []Source) *Registry {
	r := &Registry{sources: make(map[string]Source)}
	r.ReplaceAll(sources)
	return r
}

// ReplaceAll atomically swaps the source map (used by GitHub refresh / cache load).
func (r *Registry) ReplaceAll(sources []Source) {
	if r == nil {
		return
	}
	next := make(map[string]Source, len(sources))
	for _, s := range sources {
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			continue
		}
		if s.Ref == "" {
			s.Ref = "main"
		}
		next[s.ID] = normalizeSource(s)
	}
	r.mu.Lock()
	r.sources = next
	r.mu.Unlock()
}

// Snapshot returns a copy of all sources (order not stable).
func (r *Registry) Snapshot() []Source {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		out = append(out, s)
	}
	return out
}

// Len returns the number of registered sources.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sources)
}

func normalizeSource(s Source) Source {
	s.PublisherLogins = lowerUnique(s.PublisherLogins)
	s.OwnerLogins = lowerUnique(s.OwnerLogins)
	for i := range s.Seeds {
		if s.Seeds[i].Path == "" && s.Seeds[i].TripID != "" {
			s.Seeds[i].Path = s.Seeds[i].TripID + ".js"
		}
	}
	return s
}

func lowerUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// Get returns a source by id.
func (r *Registry) Get(id string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[id]
	return s, ok
}

// ListForUser returns sources the user may publish (enabled or not — FE filters display).
// Admins see all sources. Others see sources where they are publisher or owner.
func (r *Registry) ListForUser(username string, isAdmin bool) []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user := strings.ToLower(strings.TrimSpace(username))
	out := make([]Source, 0)
	for _, s := range r.sources {
		if isAdmin || containsFold(s.PublisherLogins, user) || containsFold(s.OwnerLogins, user) {
			out = append(out, s)
		}
	}
	return out
}

// CanPublish reports whether username may create jobs for the source.
func (r *Registry) CanPublish(sourceID, username string, isAdmin bool) bool {
	s, ok := r.Get(sourceID)
	if !ok {
		return false
	}
	if !s.Enabled {
		return false
	}
	if isAdmin {
		return true
	}
	user := strings.ToLower(strings.TrimSpace(username))
	return containsFold(s.PublisherLogins, user)
}

// FindSeed looks up a seed path by trip id within a source.
func (s Source) FindSeed(tripID string) (SeedRef, bool) {
	for _, seed := range s.Seeds {
		if strings.EqualFold(seed.TripID, tripID) {
			return seed, true
		}
	}
	return SeedRef{}, false
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}

// DefaultDogfoodRegistry is the compiled-in cold-start FALLBACK when GitHub is
// unreachable and no disk cache exists yet.
//
// Prod trust SoT = rjullien/tripkit/ops/publish-sources.json (fetched via PAT,
// copied to disk; GH down → last cache → this dogfood).
// Trip allowlist = each seed repo's publish-manifest.json (when GitHub token works).
// Seeds below are catalogue FALLBACK only (no token / GitHub down).
// nadia/jihane stay off until their turn; laurine enabled for philippines-2027.
func DefaultDogfoodRegistry() *Registry {
	return NewRegistry([]Source{
		{
			ID:              "jullien",
			Repo:            "rjullien/tripkit-seeds",
			Ref:             "main",
			ExpectedFamily:  "jullien",
			PublisherLogins: []string{"rene", "nicole"},
			OwnerLogins:     []string{"rene", "nicole"},
			Enabled:         true,
			Seeds: []SeedRef{
				{
					TripID: "quebec-2026",
					Path:   "quebec-2026.js",
					Title:  "Québec 2026",
					Assets: []string{"quebec-map.html", "quebec-meteo.html"},
				},
				{
					TripID: "publish-demo-2026",
					Path:   "publish-demo-2026.js",
					Title:  "Démo Publish (test FE)",
				},
			},
		},
		{
			ID:              "nadia",
			Repo:            "rjullien/tripkit-seeds-nadia",
			Ref:             "main",
			ExpectedFamily:  "ramdani",
			PublisherLogins: []string{"nadia"},
			OwnerLogins:     []string{"nadia"},
			Enabled:         false,
		},
		{
			ID:              "laurine",
			Repo:            "rjullien/tripkit-seeds-laurine",
			Ref:             "main",
			ExpectedFamily:  "laurine",
			PublisherLogins: []string{"laurine"},
			OwnerLogins:     []string{"laurine"},
			Enabled:         true,
			Seeds: []SeedRef{
				{
					TripID: "philippines-2027",
					Path:   "philippines-2027.js",
					Title:  "Philippines 2027",
				},
			},
		},
		{
			ID:              "jihane",
			Repo:            "rjullien/tripkit-seeds-jihane",
			Ref:             "main",
			ExpectedFamily:  "zouaoui",
			PublisherLogins: []string{"jihane"},
			OwnerLogins:     []string{"jihane"},
			Enabled:         false,
		},
	})
}

// LoadRegistry loads the trust registry:
//  1. TRIPKIT_PUBLISH_SOURCES env — emergency / test override
//  2. GitHub rjullien/tripkit/ops/publish-sources.json (copied to disk cache)
//  3. Last disk cache if GitHub is down
//  4. DefaultDogfoodRegistry (cold start, no cache yet)
//
// Also returns the SourcesLoader used for periodic refresh (may be nil when env override).
func LoadRegistry() (*Registry, *SourcesLoader, string, error) {
	if raw := strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES")); raw != "" {
		list, err := ParseSourcesJSON([]byte(raw))
		if err != nil {
			return nil, nil, "", fmt.Errorf("TRIPKIT_PUBLISH_SOURCES: %w", err)
		}
		reg := NewRegistry(list)
		return reg, nil, "env", nil
	}
	loader := NewSourcesLoaderFromEnv()
	reg := NewRegistry(nil)
	origin := loader.Bootstrap(reg)
	return reg, loader, origin, nil
}
