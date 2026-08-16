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
	SourceHotel = "hotel" // the booked hotel's own address
	SourceStep  = "step"  // the trip stop (city) coordinates
)

// statusBooked is the only hotel status that moves the analysis onto the
// hotel's address. Same vocabulary and precedence as the QA extractor
// (internal/construction/qa.go): bookingStatus > status > (bookingRef ⇒ booked).
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
	source     string // SourceHotel | SourceStep
	note       string // why we are not on the hotel address, when we should be
}

// hotelInfo is the subset of a seed hotel this package needs.
type hotelInfo struct {
	id      string
	name    string
	addr    string
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
//   - a hotel marked booked → its own address (explicit lat/lon if the seed has
//     them, otherwise the geocoded hotels[].addr);
//   - anything else → the trip stop coordinates, as before.
//
// A booked hotel whose address cannot be resolved does NOT silently become its
// city: it falls back to the stop *and says so* in note/addressSource, because
// a verdict measured 2 km away is not the verdict of that hotel.
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
	hotels := extractHotelInfos(tripData)
	dayHotelToLocation := hotelLocationLinks(tripData)

	var out []target
	claimed := map[string]bool{} // location ids already represented by a hotel target

	// 1. One target per hotel referenced by a day, in day order.
	for _, hotelID := range orderedHotelIDs(tripData, hotels) {
		h := hotels[hotelID]
		locID := dayHotelToLocation[hotelID]
		stepLat, stepLon, stepOK := locationPoint(locs, locID)

		if !matchesRequest(ids, all, hotelID, locID) {
			continue
		}

		tg := target{
			id:         hotelID,
			locationID: locID,
			hotelID:    hotelID,
			name:       displayName(h.name, hotelID),
			source:     SourceStep,
		}

		switch {
		case !h.booked():
			// Not booked: nothing is committed yet, the stop is the honest
			// reference. Stated explicitly so a green verdict is not read as
			// "this hotel is quiet".
			if !stepOK {
				continue // no usable point at all
			}
			tg.lat, tg.lon = stepLat, stepLon
			tg.note = "hôtel non réservé : analyse au point d'étape, pas à l'adresse de l'hôtel"

		case h.hasGeo:
			tg.lat, tg.lon = h.lat, h.lon
			tg.source = SourceHotel
			tg.addr = firstNonEmpty(h.addr, h.name)

		default:
			pt, err := s.geocodeHotel(ctx, tripID, h)
			if err == nil {
				tg.lat, tg.lon = pt.Lat, pt.Lon
				tg.source = SourceHotel
				// The seed's own address is what the user recognises. The
				// geocoder's display_name is often a POI that happens to sit at
				// that number ("Europcar, 64 Boulevard Pierre Semard…") — right
				// coordinates, confusing label. It stays in the log.
				tg.addr = firstNonEmpty(h.addr, pt.DisplayName)
				log.Printf("nuisance: hotel=%s booked → analysed at its own address %q (resolved: %s)",
					hotelID, h.addr, truncateAddr(pt.DisplayName))
				break
			}
			if !stepOK {
				continue
			}
			tg.lat, tg.lon = stepLat, stepLon
			tg.note = geocodeFallbackNote(h, err)
			log.Printf("nuisance: hotel=%s booked but address not resolved (%v): falling back to step %s", hotelID, err, locID)
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

// geocodeHotel resolves a hotel address, with a long-lived cache: a street
// address does not move, and the public Nominatim instance allows one request
// per second.
func (s *Service) geocodeHotel(ctx context.Context, tripID string, h hotelInfo) (geocode.Point, error) {
	addr := strings.TrimSpace(h.addr)
	if addr == "" {
		return geocode.Point{}, errNoAddress
	}
	if s.Geocoder == nil {
		return geocode.Point{}, errNoGeocoder
	}
	if pt, ok := s.loadGeocodeCache(tripID, addr); ok {
		return pt, nil
	}
	pt, err := s.Geocoder.Geocode(ctx, addr)
	if err != nil {
		return geocode.Point{}, err
	}
	s.saveGeocodeCache(tripID, addr, pt)
	return pt, nil
}

var (
	errNoAddress  = errors.New("hotel has no address")
	errNoGeocoder = errors.New("no geocoder configured")
)

// geocodeFallbackNote explains, in French and in the result, why a booked hotel
// was not analysed at its own address.
func geocodeFallbackNote(h hotelInfo, err error) string {
	switch {
	case errors.Is(err, errNoAddress):
		return "hôtel réservé sans adresse dans le seed : analyse au point d'étape"
	case errors.Is(err, errNoGeocoder):
		return "géocodage indisponible : analyse au point d'étape, pas à l'adresse de l'hôtel"
	case errors.Is(err, geocode.ErrNotFound):
		return fmt.Sprintf("adresse introuvable (%s) : analyse au point d'étape", truncateAddr(h.addr))
	default:
		return "adresse de l'hôtel non résolue : analyse au point d'étape"
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
func hotelLocationLinks(tripData map[string]any) map[string]string {
	out := map[string]string{}
	for _, d := range days(tripData) {
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
func orderedHotelIDs(tripData map[string]any, hotels map[string]hotelInfo) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range days(tripData) {
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
	// Hotels present in hotels{} but referenced by no day: analysed too when
	// booked, rather than silently ignored.
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
