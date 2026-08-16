package nuisance

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/geocode"
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

// geocodeTTL is how long a resolved hotel address stays valid. A street address
// does not move, and the public Nominatim instance allows one request per
// second: re-resolving the same hotel on every run would be both slow and rude.
const geocodeTTL = 30 * 24 * time.Hour

// geocodeScopeKey identifies a geocoded address. The address is normalised
// (collapsed whitespace, lowercase) so a cosmetic seed edit still hits.
func geocodeScopeKey(addr string) string {
	return "geocode:" + strings.ToLower(strings.Join(strings.Fields(addr), " "))
}

type cachedPoint struct {
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	DisplayName string  `json:"displayName,omitempty"`
}

func (s *Service) loadGeocodeCache(tripID, addr string) (geocode.Point, bool) {
	if s == nil || s.DB == nil {
		return geocode.Point{}, false
	}
	var row models.DiscoveryCache
	err := s.DB.Where("trip_id = ? AND scope_key = ? AND theme_id = ?",
		tripID, geocodeScopeKey(addr), "geocode").First(&row).Error
	if err != nil {
		return geocode.Point{}, false
	}
	if s.now().Sub(row.FetchedAt) > geocodeTTL {
		return geocode.Point{}, false
	}
	var p cachedPoint
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		return geocode.Point{}, false
	}
	if p.Lat == 0 && p.Lon == 0 {
		return geocode.Point{}, false
	}
	return geocode.Point{Lat: p.Lat, Lon: p.Lon, DisplayName: p.DisplayName}, true
}

// saveGeocodeCache stores a SUCCESSFUL resolution only. Caching a failure would
// pin a hotel (candidate or booked) to its city centre for a month.
func (s *Service) saveGeocodeCache(tripID, addr string, pt geocode.Point) {
	if s == nil || s.DB == nil || (pt.Lat == 0 && pt.Lon == 0) {
		return
	}
	raw, err := json.Marshal(cachedPoint{Lat: pt.Lat, Lon: pt.Lon, DisplayName: pt.DisplayName})
	if err != nil {
		return
	}
	row := models.DiscoveryCache{
		TripID:    tripID,
		ScopeKey:  geocodeScopeKey(addr),
		ThemeID:   "geocode",
		Payload:   string(raw),
		FetchedAt: s.now(),
	}
	_ = s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "scope_key"}, {Name: "theme_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"payload", "fetched_at"}),
	}).Create(&row).Error
}
