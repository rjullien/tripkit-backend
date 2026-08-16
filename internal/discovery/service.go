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
	// CorridorSampleKm, when set, overrides defaultSampleKm (ops qa.corridorSampleKm).
	CorridorSampleKm func() float64
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

// interests loads the interests section from the trip's travel-profile.
func (s *Service) interests(tripID string) (map[string]InterestPref, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, err
	}
	if trip.Data == nil || *trip.Data == "" {
		return nil, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return nil, nil
	}
	raw, ok := data["travelProfile"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, nil
	}
	var profile struct {
		Interests map[string]InterestPref `json:"interests"`
	}
	if err := json.Unmarshal(b, &profile); err != nil {
		return nil, nil
	}
	return profile.Interests, nil
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
// When scope.corridor is [fromLoc, toLoc], it searches along that drive instead.
func (s *Service) Search(ctx context.Context, tripID string, sc Scope, themeIDs []string, progress ProgressFunc) (*Result, error) {
	if _, _, ok := corridorPair(sc); ok {
		return s.searchCorridor(ctx, tripID, sc, themeIDs, progress)
	}
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
			ck := s.cacheThemeKey(theme.ID)
			items, cached := s.loadCache(tripID, key, ck, geoTTLHours*time.Hour)
			if !cached {
				q := s.querier()
				got, qerr := q.Search(ctx, lat, lon, theme)
				if qerr != nil {
					log.Printf("discovery: overpass theme=%s: %v (soft-fail)", theme.ID, qerr)
					got = nil
				} else {
					s.saveCache(tripID, key, ck, got)
				}
				items = got
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
			ck := s.cacheThemeKey(theme.ID)
			items, cached := s.loadCache(tripID, key, ck, editorialTTLHours*time.Hour)
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
				} else {
					s.saveCache(tripID, key, ck, got)
				}
				items = got
			}
			res.ByTheme[theme.ID] = items
			res.Items = append(res.Items, items...)
			if progress != nil {
				progress(theme.ID, theme.Label, items, cached)
			}
		}
	}

	res.Items = rankItems(res.Items)

	// Apply interest-based ranking if preferences are available.
	interests, _ := s.interests(tripID)
	if len(interests) > 0 {
		cfg := RankConfig{Interests: interests}
		res.Items = RankItems(res.Items, cfg)
	}

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
	if from, to, ok := corridorPair(sc); ok {
		return s.corridorCached(tripID, sc, from, to, themeIDs)
	}
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

func (s *Service) tripDataMap(tripID string) (map[string]any, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, err
	}
	if trip.Data == nil || *trip.Data == "" {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return map[string]any{}, nil
	}
	return data, nil
}

func coordsForLoc(tripData map[string]any, locID string) (float64, float64, bool) {
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

func locDisplayName(tripData map[string]any, locID string) string {
	locs, _ := tripData["locations"].(map[string]any)
	if loc, _ := locs[locID].(map[string]any); loc != nil {
		if n, _ := loc["name"].(string); strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
		if n, _ := loc["label"].(string); strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
	}
	return strings.ReplaceAll(locID, "-", " ")
}

func (s *Service) searchCorridor(ctx context.Context, tripID string, sc Scope, themeIDs []string, progress ProgressFunc) (*Result, error) {
	fromID, toID, ok := corridorPair(sc)
	if !ok {
		return nil, fmt.Errorf("corridor requires two different location ids")
	}
	tripData, err := s.tripDataMap(tripID)
	if err != nil {
		return nil, err
	}
	fromLat, fromLon, okFrom := coordsForLoc(tripData, fromID)
	toLat, toLon, okTo := coordsForLoc(tripData, toID)
	if !okFrom || !okTo {
		return nil, fmt.Errorf("no coordinates for corridor %s → %s", fromID, toID)
	}
	fromName := locDisplayName(tripData, fromID)
	toName := locDisplayName(tripData, toID)
	place := fromName + " → " + toName

	effective, err := s.ThemesForTrip(tripID)
	if err != nil {
		return nil, err
	}
	wanted := pickThemes(effective, themeIDs)
	key := scopeKey(sc)

	res := &Result{
		Scope:   sc,
		Place:   place,
		Lat:     fromLat,
		Lon:     fromLon,
		ByTheme: map[string][]Item{},
	}

	var corridorThemes []Theme
	allCached := true
	for _, theme := range wanted {
		if theme.Engine != engineGeo || !theme.Corridor {
			continue
		}
		corridorThemes = append(corridorThemes, theme)
		res.Themes = append(res.Themes, theme.ID)
		ck := s.cacheThemeKey(theme.ID)
		if items, cached := s.loadCache(tripID, key, ck, geoTTLHours*time.Hour); cached {
			res.ByTheme[theme.ID] = items
			res.Items = append(res.Items, items...)
			if progress != nil {
				progress(theme.ID, theme.Label, items, true)
			}
		} else {
			allCached = false
		}
	}

	if len(corridorThemes) == 0 {
		return res, nil
	}
	if allCached {
		res.Items = rankByDetour(res.Items)
		return res, nil
	}

	got, err := s.CorridorSearch(ctx, fromLat, fromLon, toLat, toLon, corridorThemes, nil)
	if err != nil {
		return res, err
	}
	res.Items = nil
	res.ByTheme = map[string][]Item{}
	if got != nil {
		for _, theme := range corridorThemes {
			items := got.ByTheme[theme.ID]
			if items == nil {
				items = []Item{}
			}
			s.saveCache(tripID, key, s.cacheThemeKey(theme.ID), items)
			res.ByTheme[theme.ID] = items
			res.Items = append(res.Items, items...)
			if progress != nil {
				progress(theme.ID, theme.Label, items, false)
			}
		}
	}
	res.Items = rankByDetour(res.Items)
	return res, nil
}

func (s *Service) corridorCached(tripID string, sc Scope, fromID, toID string, themeIDs []string) (*Result, error) {
	tripData, err := s.tripDataMap(tripID)
	if err != nil {
		return nil, err
	}
	fromLat, fromLon, okFrom := coordsForLoc(tripData, fromID)
	if !okFrom {
		return nil, fmt.Errorf("no coordinates for corridor %s → %s", fromID, toID)
	}
	if len(themeIDs) == 0 {
		themes, err := s.ThemesForTrip(tripID)
		if err != nil {
			return nil, err
		}
		for _, t := range themes {
			if t.Engine == engineGeo && t.Corridor {
				themeIDs = append(themeIDs, t.ID)
			}
		}
	}
	res := s.cachedResults(tripID, sc, themeIDs)
	res.Place = locDisplayName(tripData, fromID) + " → " + locDisplayName(tripData, toID)
	res.Lat = fromLat
	res.Lon = fromLon
	return res, nil
}
