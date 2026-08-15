package discovery

import (
	"encoding/json"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm/clause"
)

func scopeKey(sc Scope) string {
	if sc.LocationID != "" {
		key := "loc:" + sc.LocationID
		if sc.DateISO != "" {
			return key + ":" + sc.DateISO
		}
		return key
	}
	if sc.DayNum != 0 || sc.DateISO != "" {
		return "day:" + itoa(sc.DayNum)
	}
	return "unknown"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return sign + string(b[i:])
}

func (s *Service) cacheThemeKey(themeID string) string {
	v := 1
	if s != nil {
		if n := s.cfg().Version; n > 0 {
			v = n
		}
	}
	return themeID + "@v" + itoa(v)
}

func (s *Service) loadCache(tripID, key, themeID string, ttl time.Duration) ([]Item, bool) {
	if s == nil || s.DB == nil {
		return nil, false
	}
	var row models.DiscoveryCache
	err := s.DB.Where("trip_id = ? AND scope_key = ? AND theme_id = ?", tripID, key, themeID).First(&row).Error
	if err != nil {
		return nil, false
	}
	now := s.now()
	if now.Sub(row.FetchedAt) > ttl {
		return nil, false
	}
	var items []Item
	if err := json.Unmarshal([]byte(row.Payload), &items); err != nil {
		return nil, false
	}
	for i := range items {
		items[i].Cached = true
	}
	return items, true
}

func (s *Service) saveCache(tripID, key, themeID string, items []Item) {
	if s == nil || s.DB == nil {
		return
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return
	}
	row := models.DiscoveryCache{
		TripID:    tripID,
		ScopeKey:  key,
		ThemeID:   themeID,
		Payload:   string(raw),
		FetchedAt: s.now(),
	}
	_ = s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "scope_key"}, {Name: "theme_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"payload", "fetched_at"}),
	}).Create(&row).Error
}

func (s *Service) cachedResults(tripID string, sc Scope, themeIDs []string) *Result {
	key := scopeKey(sc)
	res := &Result{Scope: sc, Themes: themeIDs, ByTheme: map[string][]Item{}}
	effective := s.cfg().Themes
	if themes, err := s.ThemesForTrip(tripID); err == nil {
		effective = themes
	}
	for _, id := range themeIDs {
		ttl := geoTTLHours * time.Hour
		if th, ok := themeByID(effective, id); ok && th.Engine == engineEditorial {
			ttl = editorialTTLHours * time.Hour
		}
		items, ok := s.loadCache(tripID, key, s.cacheThemeKey(id), ttl)
		if !ok {
			continue
		}
		res.ByTheme[id] = items
		res.Items = append(res.Items, items...)
	}
	res.Items = rankItems(res.Items)
	return res
}
