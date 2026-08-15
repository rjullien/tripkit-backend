package nuisance

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm/clause"
)

// cacheTTL is how long an Overpass answer stays fresh for a nuisance category.
// 24h matches the discovery geo cache (internal/discovery/types.go geoTTLHours):
// railways, airports, motorways and industrial land do not move, and the point
// of the cache is to keep a re-run of the same trip from re-hitting the public
// Overpass API, which allots roughly two slots per IP.
const cacheTTL = 24 * time.Hour

// cacheScopeKey identifies a nuisance query location. Coordinates are rounded
// to 4 decimals (~11 m) so a jitter in the seed data still hits the cache, and
// the "nuisance:" prefix keeps these rows from colliding with the discovery
// scope keys, which share the construction_discovery table.
func cacheScopeKey(lat, lon float64) string {
	return fmt.Sprintf("nuisance:%.4f,%.4f", lat, lon)
}

// loadCache returns the cached items for a location/theme when a fresh entry
// exists. Same idiom as discovery.Service.loadCache (internal/discovery/store.go).
func (s *Service) loadCache(tripID string, lat, lon float64, themeID string) ([]discovery.Item, bool) {
	if s == nil || s.DB == nil {
		return nil, false
	}
	var row models.DiscoveryCache
	err := s.DB.Where("trip_id = ? AND scope_key = ? AND theme_id = ?",
		tripID, cacheScopeKey(lat, lon), themeID).First(&row).Error
	if err != nil {
		return nil, false
	}
	if s.now().Sub(row.FetchedAt) > cacheTTL {
		return nil, false
	}
	var items []discovery.Item
	if err := json.Unmarshal([]byte(row.Payload), &items); err != nil {
		return nil, false
	}
	return items, true
}

// saveCache stores a SUCCESSFUL Overpass answer. Callers must never call it for
// a failed query: caching an empty-because-failed result would turn a transient
// Overpass outage into a day-long "nothing nearby" verdict (the negative-cache
// bug already fixed for discovery in PR #60).
func (s *Service) saveCache(tripID string, lat, lon float64, themeID string, items []discovery.Item) {
	if s == nil || s.DB == nil {
		return
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return
	}
	row := models.DiscoveryCache{
		TripID:    tripID,
		ScopeKey:  cacheScopeKey(lat, lon),
		ThemeID:   themeID,
		Payload:   string(raw),
		FetchedAt: s.now(),
	}
	_ = s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "scope_key"}, {Name: "theme_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"payload", "fetched_at"}),
	}).Create(&row).Error
}

// now is the injectable clock (tests set Now to exercise TTL expiry).
func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
