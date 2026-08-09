// Package publish implements trusted seed publishing (registry, apply, jobs).
package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Source is one trusted family seed repository mapping (ops-only config).
type Source struct {
	ID               string   `json:"id"`
	Repo             string   `json:"repo"` // owner/name
	Ref              string   `json:"ref"`
	ExpectedFamily   string   `json:"expectedFamily"`
	PublisherLogins  []string `json:"publisherLogins"`
	OwnerLogins      []string `json:"ownerLogins"`
	Enabled          bool     `json:"enabled"`
	Seeds            []SeedRef `json:"seeds"` // explicit allowlist
}

// SeedRef maps a trip id to its seed file path in the repo.
type SeedRef struct {
	TripID   string   `json:"tripId"`
	Path     string   `json:"path"`
	Assets   []string `json:"assets"`
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

// DefaultDogfoodRegistry returns the progressive dogfood config (all disabled).
// Used when TRIPKIT_PUBLISH_SOURCES is unset so the API still exposes the shape.
func DefaultDogfoodRegistry() *Registry {
	return NewRegistry([]Source{
		{
			ID:              "jullien",
			Repo:            "rjullien/tripkit-seeds",
			Ref:             "main",
			ExpectedFamily:  "jullien",
			PublisherLogins: []string{"rene", "nicole"},
			OwnerLogins:     []string{"rene", "nicole"},
			Enabled:         false,
			Seeds: []SeedRef{{
				TripID: "quebec-2026",
				Path:   "quebec-2026.js",
				Assets: []string{"quebec-map.html", "quebec-meteo.html"},
			}},
		},
		{
			ID:              "nadia",
			Repo:            "rjullien/tripkit-seeds-nadia",
			Ref:             "main",
			ExpectedFamily:  "ramdani",
			PublisherLogins: []string{"nadia"},
			OwnerLogins:     []string{"nadia"},
			Enabled:         false,
			Seeds: []SeedRef{{
				TripID: "balears-2026",
				Path:   "balears-2026.js",
			}},
		},
		{
			ID:              "laurine",
			Repo:            "rjullien/tripkit-seeds-laurine",
			Ref:             "main",
			ExpectedFamily:  "laurine",
			PublisherLogins: []string{"laurine"},
			OwnerLogins:     []string{"laurine"},
			Enabled:         false,
			Seeds:           nil,
		},
	})
}

// LoadRegistry loads env JSON, or falls back to DefaultDogfoodRegistry.
func LoadRegistry() (*Registry, error) {
	if strings.TrimSpace(os.Getenv("TRIPKIT_PUBLISH_SOURCES")) == "" {
		return DefaultDogfoodRegistry(), nil
	}
	return LoadRegistryFromEnv()
}
