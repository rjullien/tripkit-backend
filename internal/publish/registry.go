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

// LoadRegistryFromEnv reads TRIPKIT_PUBLISH_SOURCES (JSON array) or returns empty.
func LoadRegistryFromEnv() (*Registry, error) {
	raw := strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES"))
	if raw == "" {
		return NewRegistry(nil), nil
	}
	var list []Source
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("TRIPKIT_PUBLISH_SOURCES: %w", err)
	}
	return NewRegistry(list), nil
}

// NewRegistry builds a registry from sources.
func NewRegistry(sources []Source) *Registry {
	r := &Registry{sources: make(map[string]Source)}
	for _, s := range sources {
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			continue
		}
		if s.Ref == "" {
			s.Ref = "main"
		}
		r.sources[s.ID] = normalizeSource(s)
	}
	return r
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

// DefaultDogfoodRegistry is the in-code FALLBACK when TRIPKIT_PUBLISH_SOURCES is unset.
// Prod trust SoT = rjullien/tripkit/ops/publish-sources.json → Infisical publish-sources
// → env TRIPKIT_PUBLISH_SOURCES (flip enabled without a BE release).
// Trip allowlist = each seed repo's publish-manifest.json (when GitHub token works).
// Seeds below are catalogue FALLBACK only (no token / GitHub down).
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
			Enabled:         false,
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

// LoadRegistry prefers TRIPKIT_PUBLISH_SOURCES (Infisical / ops JSON).
// Empty env → DefaultDogfoodRegistry (local/CI / before cluster wire).
func LoadRegistry() (*Registry, error) {
	if strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES")) == "" {
		return DefaultDogfoodRegistry(), nil
	}
	return LoadRegistryFromEnv()
}
