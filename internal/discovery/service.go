package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// ProgressFunc reports per-theme progress (SSE).
type ProgressFunc func(themeID, label string, items []Item, cached bool)

// Service runs point searches (vue Jour) against Overpass + DB cache.
type Service struct {
	DB        *gorm.DB
	Loader    *Loader
	Overpass  Querier
	Editorial EditorialSearcher
	Now       func() time.Time
	cacheMu   sync.Mutex
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) cfg() Config {
	if s != nil && s.Loader != nil {
		return s.Loader.Get()
	}
	return DefaultConfig()
}

func (s *Service) querier() Querier {
	if s != nil && s.Overpass != nil {
		return s.Overpass
	}
	return NewClient(s.cfg().Overpass)
}

// ThemesForTrip returns EffectiveThemes(template, travel-profile.themes).
func (s *Service) ThemesForTrip(tripID string) ([]Theme, error) {
	cfg := s.cfg()
	prefs, err := s.themePrefs(tripID)
	if err != nil {
		return nil, err
	}
	return EffectiveThemes(cfg.Themes, prefs), nil
}

func (s *Service) themePrefs(tripID string) (ThemePrefs, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return ThemePrefs{}, err
	}
	if trip.Data == nil || *trip.Data == "" {
		return ThemePrefs{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return ThemePrefs{}, nil
	}
	raw, ok := data["travelProfile"]
	if !ok || raw == nil {
		return ThemePrefs{}, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return ThemePrefs{}, nil
	}
	var profile struct {
		Themes ThemePrefs `json:"themes"`
	}
	if err := json.Unmarshal(b, &profile); err != nil {
		return ThemePrefs{}, nil
	}
	return profile.Themes, nil
}

// ResolvePoint maps a Jour scope to lat/lon + a place label.
func (s *Service) ResolvePoint(tripID string, sc Scope) (lat, lon float64, place string, dateISO string, err error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return 0, 0, "", "", err
	}
	var tripData map[string]any
	if trip.Data != nil && *trip.Data != "" {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}
	dayNum := sc.DayNum
	var day models.Day
	dayData := map[string]any{}
	if sc.LocationID == "" {
		if err := s.DB.Where("trip_id = ? AND day_num = ?", tripID, dayNum).First(&day).Error; err != nil {
			return 0, 0, "", "", fmt.Errorf("day %d not found", dayNum)
		}
		_ = json.Unmarshal([]byte(day.Data), &dayData)
	} else {
		dayData["locationId"] = sc.LocationID
	}
	lat, lon, ok := coordsFromTrip(tripData, dayData)
	if !ok {
		return 0, 0, "", "", fmt.Errorf("no coordinates for this day")
	}
	place = placeLabel(dayData, sc.LocationID)
	dateISO = sc.DateISO
	if dateISO == "" && trip.StartDate != nil {
		dateISO = dateForDay(*trip.StartDate, dayNum)
	}
	return lat, lon, place, dateISO, nil
}

func coordsFromTrip(tripData, dayData map[string]any) (float64, float64, bool) {
	locID, _ := dayData["locationId"].(string)
	if locID == "" {
		return 0, 0, false
	}
	locs, _ := tripData["locations"].(map[string]any)
	if locs == nil {
		return 0, 0, false
	}
	loc, _ := locs[locID].(map[string]any)
	if loc == nil {
		return 0, 0, false
	}
	return asFloatPair(loc["lat"], loc["lon"])
}

func asFloatPair(a, b any) (float64, float64, bool) {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	return af, bf, aok && bok
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func placeLabel(dayData map[string]any, locID string) string {
	for _, k := range []string{"to", "from", "label"} {
		if s, _ := dayData[k].(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return strings.ReplaceAll(locID, "-", " ")
}

func dateForDay(startISO string, dayNum int) string {
	t, err := time.Parse("2006-01-02", startISO)
	if err != nil {
		return ""
	}
	// startDate = Day 1; Day 0 = startDate-1
	return t.AddDate(0, 0, dayNum-1).Format("2006-01-02")
}

// Search runs geo themes around the day's location, then editorial themes via Léo.
// Overpass / Léo failures are soft: the theme contributes zero items, the rest continue.
func (s *Service) Search(ctx context.Context, tripID string, sc Scope, themeIDs []string, progress ProgressFunc) (*Result, error) {
	lat, lon, place, dateISO, err := s.ResolvePoint(tripID, sc)
	if err != nil {
		return nil, err
	}
	sc.DateISO = dateISO
	effective, err := s.ThemesForTrip(tripID)
	if err != nil {
		return nil, err
	}
	wanted := pickThemes(effective, themeIDs)
	key := scopeKey(sc)
	cfg := s.cfg()
	conc := cfg.Overpass.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}
	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	res := &Result{
		Scope:   sc,
		Place:   place,
		Lat:     lat,
		Lon:     lon,
		ByTheme: map[string][]Item{},
	}

	var geo []Theme
	var editorial []Theme
	for _, theme := range wanted {
		switch theme.Engine {
		case engineGeo:
			geo = append(geo, theme)
			res.Themes = append(res.Themes, theme.ID)
		case engineEditorial:
			editorial = append(editorial, theme)
			res.Themes = append(res.Themes, theme.ID)
		}
	}

	var wg sync.WaitGroup
	for _, theme := range geo {
		theme := theme
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			items, cached := s.loadCache(tripID, key, theme.ID, geoTTLHours*time.Hour)
			if !cached {
				q := s.querier()
				got, qerr := q.Search(ctx, lat, lon, theme)
				if qerr != nil {
					log.Printf("discovery: overpass theme=%s: %v (soft-fail)", theme.ID, qerr)
					got = nil
				}
				items = got
				s.saveCache(tripID, key, theme.ID, items)
			}
			mu.Lock()
			res.ByTheme[theme.ID] = items
			res.Items = append(res.Items, items...)
			mu.Unlock()
			if progress != nil {
				progress(theme.ID, theme.Label, items, cached)
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	if len(editorial) > 0 {
		tripName := s.lookupTripName(tripID)
		for _, theme := range editorial {
			if ctx.Err() != nil {
				break
			}
			items, cached := s.loadCache(tripID, key, theme.ID, editorialTTLHours*time.Hour)
			if !cached {
				got, qerr := s.searchEditorial(ctx, EditorialQuery{
					Theme:    theme,
					Place:    place,
					TripName: tripName,
					DateISO:  dateISO,
					Lat:      lat,
					Lon:      lon,
				})
				if qerr != nil {
					log.Printf("discovery: editorial theme=%s: %v (soft-fail)", theme.ID, qerr)
					got = nil
				}
				items = got
				s.saveCache(tripID, key, theme.ID, items)
			}
			res.ByTheme[theme.ID] = items
			res.Items = append(res.Items, items...)
			if progress != nil {
				progress(theme.ID, theme.Label, items, cached)
			}
		}
	}

	res.Items = rankItems(res.Items)
	return res, nil
}

func (s *Service) searchEditorial(ctx context.Context, q EditorialQuery) ([]Item, error) {
	if s == nil || s.Editorial == nil {
		return nil, fmt.Errorf("leo editorial not configured")
	}
	return s.Editorial.Search(ctx, q)
}

func (s *Service) lookupTripName(tripID string) string {
	if s == nil || s.DB == nil {
		return ""
	}
	var trip models.Trip
	if err := s.DB.Session(&gorm.Session{}).Select("name").First(&trip, "id = ?", tripID).Error; err != nil {
		return ""
	}
	return trip.Name
}

func pickThemes(all []Theme, ids []string) []Theme {
	if len(ids) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []Theme
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// Results reads the cache only (no Overpass).
func (s *Service) Results(tripID string, sc Scope, themeIDs []string) (*Result, error) {
	lat, lon, place, dateISO, err := s.ResolvePoint(tripID, sc)
	if err != nil {
		return nil, err
	}
	sc.DateISO = dateISO
	if len(themeIDs) == 0 {
		themes, err := s.ThemesForTrip(tripID)
		if err != nil {
			return nil, err
		}
		for _, t := range themes {
			themeIDs = append(themeIDs, t.ID)
		}
	}
	res := s.cachedResults(tripID, sc, themeIDs)
	res.Place = place
	res.Lat = lat
	res.Lon = lon
	return res, nil
}
