package nuisance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/geocode"
	"github.com/rjullien/tripkit-backend/internal/models"
)

// Address sources reported on a result, so a reader always knows what was
// actually measured.
const (
	SourceHotel   = "hotel"   // hotels[].addr or lat/lon from the seed
	SourceGuessed = "guessed" // Nominatim hit on the hotel name — shown so the user can object
	SourceMissing = "missing" // no usable point: demand hotels[].addr, do not measure the city
	SourceStep    = "step"    // the trip stop (city) coordinates — only when there is no hotel
)

// Construction (SPEC §6, TASKS 5.5) checks *potential* hotels, not only booked
// ones: candidate / to_book / booked all move onto hotels[].addr when it exists.
// Same status vocabulary as the QA extractor (internal/construction/qa.go).
const statusBooked = "booked"

// target is one point to analyse. It carries where the point came from, because
// "trains at 76 m" means something very different at a hotel door and at the
// centre of the city it sits in.
type target struct {
	id         string // storage key: hotel id when a hotel drove the point, else location id
	locationID string
	hotelID    string
	name       string
	lat        float64
	lon        float64
	addr       string // address actually analysed (empty for a step point)
	source     string // SourceHotel | SourceGuessed | SourceMissing | SourceStep
	note       string // why we are not on a seed address, when we should be
}

// hotelInfo is the subset of a seed hotel this package needs.
type hotelInfo struct {
	id      string
	name    string
	addr    string
	city    string
	status  string
	lat     float64
	lon     float64
	hasGeo  bool
	dayNums []int
}

func (h hotelInfo) booked() bool { return strings.EqualFold(h.status, statusBooked) }

// resolveTargets decides, for every place the user can ask about, WHICH address
// the nuisance analysis runs on:
//
//   - hotel with lat/lon or hotels[].addr → that building (geocoded if needed);
//   - hotel without addr → Nominatim search on the name + city, shown as a
//     proposal so the user can object if it is the wrong building;
//   - hotel still unresolved → no measurement (SourceMissing): demand addr;
//   - a stop with no hotel → the trip stop coordinates.
//
// A hotel is never silently scored at the city centre. That is the false green
// that made two hotels in Toulouse share one verdict.
//
// Pre-departure filtering: when all=true, locations and hotels that belong
// exclusively to day < 1 (J-1, preparation) are excluded. They are not part
// of the actual trip and analysing them wastes Overpass quota. An explicit
// locationId request still overrides this filter.
func (s *Service) resolveTargets(ctx context.Context, tripID string, ids []string, all bool) ([]target, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, fmt.Errorf("trip not found: %s", tripID)
	}
	if trip.Data == nil || *trip.Data == "" {
		return nil, nil
	}
	var tripData map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &tripData); err != nil {
		return nil, nil
	}

	locs, _ := tripData["locations"].(map[string]any)
	if locs == nil {
		return nil, nil
	}

	// Load days from the DB table (trip.Data does not contain days in the
	// publish path — they live in the separate Days table). Fall back to
	// trip.Data["days"] for tests and the seed-import.js path.
	dbDays := s.loadDaysFromDB(tripID)
	mergedDays := mergeDays(tripData, dbDays)

	hotels := extractHotelInfos(tripData)
	// Enrich hotels with addr from the Hotels DB table when trip.Data lacks it.
	s.enrichHotelsFromDB(tripID, hotels)

	dayHotelToLocation := hotelLocationLinksFromDays(mergedDays)

	// Pre-departure filter: build sets of locationIds and hotelIds that appear
	// ONLY on day < 1. Only applied when all=true.
	preDepartureLocIDs, preDepartureHotelIDs := preDepartureOnly(mergedDays)

	var out []target
	claimed := map[string]bool{} // location ids already represented by a hotel target

	// 1. One target per hotel referenced by a day, in day order.
	for _, hotelID := range orderedHotelIDsFromDays(mergedDays, hotels) {
		h := hotels[hotelID]
		locID := dayHotelToLocation[hotelID]

		if !matchesRequest(ids, all, hotelID, locID) {
			continue
		}

		// Skip pre-departure hotels when running all (not explicitly requested).
		if all && preDepartureHotelIDs[hotelID] {
			continue
		}

		src, lat, lon, addr, note := s.resolveHotelPoint(ctx, tripID, h, locationName(locs, locID))
		tg := target{
			id:         hotelID,
			locationID: locID,
			hotelID:    hotelID,
			name:       displayName(h.name, hotelID),
			lat:        lat,
			lon:        lon,
			addr:       addr,
			source:     src,
			note:       note,
		}

		if locID != "" {
			claimed[locID] = true
		}
		out = append(out, tg)
	}

	// 2. Remaining stops with no hotel target: unchanged behaviour.
	for id, v := range locs {
		if claimed[id] || !matchesRequest(ids, all, "", id) {
			continue
		}
		// Skip pre-departure locations when running all.
		if all && preDepartureLocIDs[id] {
			continue
		}
		lat, lon, ok := locationPoint(locs, id)
		if !ok {
			continue
		}
		locMap, _ := v.(map[string]any)
		name, _ := locMap["name"].(string)
		out = append(out, target{
			id:         id,
			locationID: id,
			name:       displayName(name, id),
			lat:        lat,
			lon:        lon,
			source:     SourceStep,
		})
	}
	return out, nil
}

// matchesRequest reports whether a target was asked for. A request may name
// either the hotel or the stop: the Résa button historically sends the
// locationId, so both must select the same target.
func matchesRequest(ids []string, all bool, hotelID, locationID string) bool {
	if all {
		return true
	}
	for _, want := range ids {
		if want == "" {
			continue
		}
		if want == hotelID || want == locationID {
			return true
		}
	}
	return false
}

// resolveHotelPoint picks the coordinates for one hotel. Precedence:
// seed lat/lon, then hotels[].addr, then a Nominatim search on the name + city
// (shown as a proposal). Never the city centre: if nothing resolves, the
// caller must demand hotels[].addr instead of scoring the stop.
func (s *Service) resolveHotelPoint(ctx context.Context, tripID string, h hotelInfo, city string) (source string, lat, lon float64, addr, note string) {
	if h.hasGeo {
		return SourceHotel, h.lat, h.lon, firstNonEmpty(h.addr, h.name), ""
	}

	if strings.TrimSpace(h.addr) != "" {
		pt, err := s.geocodeQuery(ctx, tripID, h.addr)
		if err == nil {
			// The seed's own address is what the user recognises. Nominatim's
			// display_name is often a POI at that number ("Europcar, 64 Bd…").
			log.Printf("nuisance: hotel=%s status=%s → seed address %q (resolved: %s)",
				h.id, h.status, h.addr, truncateAddr(pt.DisplayName))
			return SourceHotel, pt.Lat, pt.Lon, firstNonEmpty(h.addr, pt.DisplayName), ""
		}
		if src, lat, lon, addr, note, ok := s.guessFromName(ctx, tripID, h, city); ok {
			note = fmt.Sprintf("adresse introuvable (%s) : point proposé via « %s » — dites si ce n'est pas le bon bâtiment",
				truncateAddr(h.addr), hotelSearchQuery(h, city))
			log.Printf("nuisance: hotel=%s seed addr failed (%v): proposing name search %q", h.id, err, addr)
			return src, lat, lon, addr, note
		}
		log.Printf("nuisance: hotel=%s address not resolved (%v): demanding hotels[].addr", h.id, err)
		return SourceMissing, 0, 0, "", demandNote(h, hotelSearchQuery(h, city), err)
	}

	if src, lat, lon, addr, note, ok := s.guessFromName(ctx, tripID, h, city); ok {
		log.Printf("nuisance: hotel=%s no seed addr → proposed %q", h.id, addr)
		return src, lat, lon, addr, note
	}
	q := hotelSearchQuery(h, city)
	if s.Geocoder == nil {
		log.Printf("nuisance: hotel=%s no addr and no geocoder: demanding hotels[].addr", h.id)
		return SourceMissing, 0, 0, "", demandNote(h, q, errNoGeocoder)
	}
	if q != "" {
		log.Printf("nuisance: hotel=%s no addr and name search empty: demanding hotels[].addr", h.id)
		return SourceMissing, 0, 0, "", fmt.Sprintf("pas d'adresse dans le seed, et « %s » n'a rien donné : ajoutez hotels[].addr", q)
	}
	log.Printf("nuisance: hotel=%s no name and no addr: demanding hotels[].addr", h.id)
	return SourceMissing, 0, 0, "", "hôtel sans nom ni adresse : ajoutez hotels[].addr"
}

// guessFromName looks the hotel up by "Name, City" so a missing seed address
// still yields a building to measure — and a display_name the user can reject.
func (s *Service) guessFromName(ctx context.Context, tripID string, h hotelInfo, city string) (source string, lat, lon float64, addr, note string, ok bool) {
	q := hotelSearchQuery(h, city)
	if q == "" || strings.EqualFold(q, strings.TrimSpace(h.addr)) {
		return "", 0, 0, "", "", false
	}
	pt, err := s.geocodeQuery(ctx, tripID, q)
	if err != nil {
		return "", 0, 0, "", "", false
	}
	note = fmt.Sprintf("pas d'adresse dans le seed : point proposé via « %s » — dites si ce n'est pas le bon bâtiment", q)
	return SourceGuessed, pt.Lat, pt.Lon, firstNonEmpty(pt.DisplayName, q), note, true
}

func hotelSearchQuery(h hotelInfo, city string) string {
	name := strings.TrimSpace(h.name)
	city = firstNonEmpty(strings.TrimSpace(h.city), strings.TrimSpace(city))
	switch {
	case name != "" && city != "":
		return name + ", " + city
	case name != "":
		return name
	default:
		return ""
	}
}

// geocodeQuery resolves a free-text query with the 30-day success-only cache.
func (s *Service) geocodeQuery(ctx context.Context, tripID, query string) (geocode.Point, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return geocode.Point{}, errNoAddress
	}
	if s.Geocoder == nil {
		return geocode.Point{}, errNoGeocoder
	}
	if pt, ok := s.loadGeocodeCache(tripID, query); ok {
		return pt, nil
	}
	pt, err := s.Geocoder.Geocode(ctx, query)
	if err != nil {
		return geocode.Point{}, err
	}
	s.saveGeocodeCache(tripID, query, pt)
	return pt, nil
}

var (
	errNoAddress  = errors.New("hotel has no address")
	errNoGeocoder = errors.New("no geocoder configured")
)

// demandNote tells the user to add hotels[].addr. Used when we have no building
// to measure: scoring the city instead would be a false green.
func demandNote(h hotelInfo, query string, err error) string {
	hasAddr := strings.TrimSpace(h.addr) != ""
	switch {
	case errors.Is(err, errNoGeocoder):
		if hasAddr {
			return "géocodage indisponible : ajoutez une adresse utilisable dans hotels[].addr"
		}
		return "géocodage indisponible et pas d'adresse dans le seed : ajoutez hotels[].addr"
	case hasAddr && errors.Is(err, geocode.ErrNotFound):
		return fmt.Sprintf("adresse introuvable (%s) : ajoutez une adresse utilisable dans hotels[].addr", truncateAddr(h.addr))
	case query != "" && errors.Is(err, geocode.ErrNotFound):
		return fmt.Sprintf("pas d'adresse dans le seed, et « %s » n'a rien donné : ajoutez hotels[].addr", query)
	case hasAddr:
		return "adresse de l'hôtel non résolue : ajoutez hotels[].addr"
	default:
		return "hôtel sans adresse : ajoutez hotels[].addr"
	}
}

func truncateAddr(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 60 {
		return s
	}
	return s[:60] + "…"
}

// extractHotelInfos reads trip.data.hotels, which the seed import may write as a
// dict keyed by hotel id or as an array of objects carrying their own id.
func extractHotelInfos(tripData map[string]any) map[string]hotelInfo {
	out := map[string]hotelInfo{}

	add := func(id string, hm map[string]any) {
		id = strings.TrimSpace(id)
		if id == "" {
			id, _ = hm["hotelId"].(string)
		}
		if id == "" {
			id, _ = hm["id"].(string)
		}
		if strings.TrimSpace(id) == "" {
			return
		}
		h := hotelInfo{id: id}
		h.name, _ = hm["name"].(string)
		// addr is the documented field; address is the alias the card also reads.
		h.addr = firstNonEmpty(str(hm["addr"]), str(hm["address"]))
		h.city = str(hm["city"])
		h.status = hotelStatus(hm)
		if lat, okLat := asFloat(hm["lat"]); okLat {
			if lon, okLon := asFloat(hm["lon"]); okLon && !(lat == 0 && lon == 0) {
				h.lat, h.lon, h.hasGeo = lat, lon, true
			}
		}
		if nums, ok := hm["dayNums"].([]any); ok {
			for _, n := range nums {
				if f, ok := asFloat(n); ok {
					h.dayNums = append(h.dayNums, int(f))
				}
			}
		}
		out[id] = h
	}

	switch raw := tripData["hotels"].(type) {
	case map[string]any:
		for id, v := range raw {
			if hm, ok := v.(map[string]any); ok {
				add(id, hm)
			}
		}
	case []any:
		for _, v := range raw {
			if hm, ok := v.(map[string]any); ok {
				add("", hm)
			}
		}
	}
	return out
}

// hotelStatus applies the precedence already used by the QA extractor:
// bookingStatus wins over status, and a booking reference alone means booked.
func hotelStatus(hm map[string]any) string {
	status := str(hm["status"])
	if v := str(hm["bookingStatus"]); v != "" {
		status = v
	}
	if status == "" {
		if ref := firstNonEmpty(str(hm["bookingRef"]), str(hm["confirmationNumber"])); ref != "" {
			status = statusBooked
		}
	}
	return strings.TrimSpace(status)
}

// hotelLocationLinks maps a hotel to its stop. A hotel carries no locationId:
// the link only exists through the day that references both.
// Retained for backward compatibility with tests that put days in trip.Data.
func hotelLocationLinks(tripData map[string]any) map[string]string {
	return hotelLocationLinksFromDays(days(tripData))
}

// hotelLocationLinksFromDays builds the hotel→location mapping from a slice of
// day maps (sourced from the DB or from trip.Data["days"]).
func hotelLocationLinksFromDays(daySlice []map[string]any) map[string]string {
	out := map[string]string{}
	for _, d := range daySlice {
		hotelID := str(d["hotelId"])
		if hotelID == "" {
			continue
		}
		if _, seen := out[hotelID]; seen {
			continue
		}
		if locID := str(d["locationId"]); locID != "" {
			out[hotelID] = locID
		}
	}
	return out
}

// orderedHotelIDs lists hotels in day order (stable output, stable progress
// frames), then any hotel no day references.
// Retained for backward compatibility with tests that put days in trip.Data.
func orderedHotelIDs(tripData map[string]any, hotels map[string]hotelInfo) []string {
	return orderedHotelIDsFromDays(days(tripData), hotels)
}

// orderedHotelIDsFromDays lists hotels in day order from a merged day slice,
// then any hotel no day references (orphans).
func orderedHotelIDsFromDays(daySlice []map[string]any, hotels map[string]hotelInfo) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range daySlice {
		id := str(d["hotelId"])
		if id == "" || seen[id] {
			continue
		}
		if _, ok := hotels[id]; !ok {
			continue // day references a hotel that is not in hotels{}
		}
		seen[id] = true
		out = append(out, id)
	}
	// Hotels present in hotels{} but referenced by no day: still analysed
	// (construction alternatives / candidates not yet assigned to a night).
	var orphans []string
	for id := range hotels {
		if !seen[id] {
			orphans = append(orphans, id)
		}
	}
	sortStrings(orphans)
	return append(out, orphans...)
}

func days(tripData map[string]any) []map[string]any {
	raw, _ := tripData["days"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, d := range raw {
		if dm, ok := d.(map[string]any); ok {
			out = append(out, dm)
		}
	}
	return out
}

func locationName(locs map[string]any, id string) string {
	if id == "" {
		return ""
	}
	locMap, _ := locs[id].(map[string]any)
	if locMap == nil {
		return ""
	}
	name, _ := locMap["name"].(string)
	return strings.TrimSpace(name)
}

func locationPoint(locs map[string]any, id string) (lat, lon float64, ok bool) {
	if id == "" {
		return 0, 0, false
	}
	locMap, _ := locs[id].(map[string]any)
	if locMap == nil {
		return 0, 0, false
	}
	lat, latOK := asFloat(locMap["lat"])
	lon, lonOK := asFloat(locMap["lon"])
	if !latOK || !lonOK || (lat == 0 && lon == 0) {
		return 0, 0, false
	}
	return lat, lon, true
}

func displayName(name, fallbackID string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return fallbackID
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}


// ── Pre-departure filtering & DB enrichment ─────────────────────────────────

// loadDaysFromDB reads the Days table for a trip and returns them as generic
// maps (same shape as trip.Data["days"] entries). Returns nil if the query fails
// or no rows exist — the caller falls back to trip.Data["days"].
func (s *Service) loadDaysFromDB(tripID string) []map[string]any {
	var rows []models.Day
	if err := s.DB.Where("trip_id = ?", tripID).Order("day_num").Find(&rows).Error; err != nil {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var m map[string]any
		if err := json.Unmarshal([]byte(r.Data), &m); err != nil {
			continue
		}
		// Ensure "day" key is present (some stores omit it from the JSON).
		if _, ok := m["day"]; !ok {
			m["day"] = float64(r.DayNum)
		}
		out = append(out, m)
	}
	return out
}

// mergeDays prefers DB days when available, otherwise falls back to
// trip.Data["days"] (tests put everything in trip.Data).
func mergeDays(tripData map[string]any, dbDays []map[string]any) []map[string]any {
	if len(dbDays) > 0 {
		return dbDays
	}
	return days(tripData)
}

// preDepartureOnly returns two sets of IDs that appear ONLY on day < 1.
// An ID that appears on both day 0 and day 3 is NOT pre-departure-only.
// An ID that appears on NO day at all is NOT pre-departure-only (it's an orphan).
func preDepartureOnly(daySlice []map[string]any) (locOnly, hotelOnly map[string]bool) {
	// Track which IDs appear at all, and which appear on a trip day.
	locSeen := map[string]bool{}
	hotelSeen := map[string]bool{}
	locOnTrip := map[string]bool{}
	hotelOnTrip := map[string]bool{}

	for _, d := range daySlice {
		dayNum := dayNumFromMap(d)
		locID := str(d["locationId"])
		hotelID := str(d["hotelId"])

		if locID != "" {
			locSeen[locID] = true
			if dayNum >= 1 {
				locOnTrip[locID] = true
			}
		}
		if hotelID != "" {
			hotelSeen[hotelID] = true
			if dayNum >= 1 {
				hotelOnTrip[hotelID] = true
			}
		}
	}

	locOnly = map[string]bool{}
	hotelOnly = map[string]bool{}
	for id := range locSeen {
		if !locOnTrip[id] {
			locOnly[id] = true
		}
	}
	for id := range hotelSeen {
		if !hotelOnTrip[id] {
			hotelOnly[id] = true
		}
	}
	return locOnly, hotelOnly
}

// dayNumFromMap extracts the day number from a day map.
func dayNumFromMap(d map[string]any) int {
	switch n := d["day"].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// enrichHotelsFromDB loads hotel data from the Hotels DB table and fills in
// any missing addr or city fields. In the publish path, trip.Data.hotels has
// the full data, but in the seed-import.js path or after certain write-back
// operations the dict may lose fields. The Hotels table always has the full
// seed payload.
func (s *Service) enrichHotelsFromDB(tripID string, hotels map[string]hotelInfo) {
	var rows []models.Hotel
	if err := s.DB.Where("trip_id = ?", tripID).Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		var hm map[string]any
		if err := json.Unmarshal([]byte(row.Data), &hm); err != nil {
			continue
		}
		hotelID := str(hm["hotelId"])
		if hotelID == "" {
			hotelID = str(hm["id"])
		}
		if hotelID == "" {
			continue
		}
		h, exists := hotels[hotelID]
		if !exists {
			// Hotel in DB but not in trip.Data.hotels — add it.
			h = hotelInfo{id: hotelID}
			h.name, _ = hm["name"].(string)
			h.addr = firstNonEmpty(str(hm["addr"]), str(hm["address"]))
			h.city = str(hm["city"])
			h.status = hotelStatus(hm)
			if lat, okLat := asFloat(hm["lat"]); okLat {
				if lon, okLon := asFloat(hm["lon"]); okLon && !(lat == 0 && lon == 0) {
					h.lat, h.lon, h.hasGeo = lat, lon, true
				}
			}
			if nums, ok := hm["dayNums"].([]any); ok {
				for _, n := range nums {
					if f, ok := asFloat(n); ok {
						h.dayNums = append(h.dayNums, int(f))
					}
				}
			}
			hotels[hotelID] = h
			continue
		}
		// Enrich missing fields from DB row.
		if strings.TrimSpace(h.addr) == "" {
			if addr := firstNonEmpty(str(hm["addr"]), str(hm["address"])); addr != "" {
				h.addr = addr
				hotels[hotelID] = h
			}
		}
		if strings.TrimSpace(h.city) == "" {
			if city := str(hm["city"]); city != "" {
				h.city = city
				hotels[hotelID] = h
			}
		}
		if !h.hasGeo {
			if lat, okLat := asFloat(hm["lat"]); okLat {
				if lon, okLon := asFloat(hm["lon"]); okLon && !(lat == 0 && lon == 0) {
					h.lat, h.lon, h.hasGeo = lat, lon, true
					hotels[hotelID] = h
				}
			}
		}
	}
}
