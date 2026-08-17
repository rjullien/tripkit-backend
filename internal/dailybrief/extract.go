package dailybrief

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// ExtractOpts controls ExtractDay strictness (prod vs admin test).
type ExtractOpts struct {
	// RequireConfigured: dailyBrief+whatsappGroup required (default true for auto send).
	RequireConfigured bool
}

// DayBriefData is the structured extract (+ enrich fields) fed to Bifrost + QA.
type DayBriefData struct {
	TripID     string           `json:"tripId"`
	TripName   string           `json:"tripName"`
	DayNumber  int              `json:"dayNumber"`
	Date       string           `json:"date"`
	Weekday    string           `json:"weekday"`
	Label      string           `json:"label,omitempty"`
	PlaceName  string           `json:"placeName,omitempty"`
	From       string           `json:"from,omitempty"`
	To         string           `json:"to,omitempty"`
	Dist       string           `json:"dist,omitempty"`
	Hotel      map[string]any   `json:"hotel,omitempty"`
	Restaurant map[string]any   `json:"restaurant,omitempty"`
	Timeline   []map[string]any `json:"timeline,omitempty"`
	Highlights []string         `json:"highlights,omitempty"`
	PlaceFacts []string         `json:"placeFacts,omitempty"`
	// PlaceFactsBySegment is filled on travel days: depart / route / arrive.
	PlaceFactsBySegment map[string][]string `json:"placeFactsBySegment,omitempty"`
	Actualites          []ActualiteItem     `json:"actualites,omitempty"`
	CultureExpress      *Tip                `json:"cultureExpress,omitempty"`
	PracticalTip        *Tip                `json:"practicalTip,omitempty"` // mandatory 1-liner
	Tips                []Tip               `json:"tips,omitempty"`         // 0–5 extras pertinents ce jour
	HasKids             bool                `json:"hasKids,omitempty"`
	TravelDay           bool                `json:"travelDay,omitempty"` // on roule / transfert
	MapURL              string              `json:"mapUrl,omitempty"`
	Duration            string              `json:"duration,omitempty"`
	Alerts              []string            `json:"alerts,omitempty"`
	WhatsAppGroup       string              `json:"whatsappGroup,omitempty"`
	Weather             map[string]any      `json:"weather,omitempty"`
	DressCode           string              `json:"dressCode,omitempty"`
	DynamicAlerts       []string            `json:"dynamicAlerts,omitempty"`
	Timezone            string              `json:"timezone,omitempty"`
	// Place stay = physical presence in the Actualité city (half-days from timeline).
	LocationID        string `json:"locationId,omitempty"`
	PlaceStayFrom     string `json:"placeStayFrom,omitempty"`     // YYYY-MM-DD
	PlaceStayFromTime string `json:"placeStayFromTime,omitempty"` // HH:MM
	PlaceStayTo       string `json:"placeStayTo,omitempty"`
	PlaceStayToTime   string `json:"placeStayToTime,omitempty"`
	ActuFocus         string `json:"actuFocus,omitempty"` // on_site | arrival
	// Prep = veille (day 0) or départ last-check (day 1). Built in pipeline.
	Prep *PrepContext `json:"prep,omitempty"`
	// RouteOrder = seed-qa geo_backtracking (Kiro): ordre des étapes vs nearest-neighbour.
	RouteOrder *RouteOrder `json:"routeOrder,omitempty"`
}

// ActualiteItem is a traveler news item (actionable: detail + link when available).
type ActualiteItem struct {
	Title   string `json:"title"`
	Source  string `json:"source,omitempty"`
	URL     string `json:"url,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Detail  string `json:"detail,omitempty"` // dug by LLM from title+snippet
}

// Tip is a short traveler tip selected for this day (deterministic).
type Tip struct {
	Kind  string `json:"kind"` // culture_express|pratique|food|photo|plan_b|timing|transport|budget|famille|securite
	Title string `json:"title"`
	Text  string `json:"text"`
	Key   string `json:"key,omitempty"` // anti-redite id (not shown in WhatsApp)
}

// ExtractDay loads trip + day from DB into DayBriefData (no LLM).
func ExtractDay(db *gorm.DB, tripID string, dayNumber int) (*DayBriefData, error) {
	return ExtractDayOpts(db, tripID, dayNumber, ExtractOpts{RequireConfigured: true})
}

// ExtractDayOpts is ExtractDay with options (admin test can skip config gate).
func ExtractDayOpts(db *gorm.DB, tripID string, dayNumber int, opts ExtractOpts) (*DayBriefData, error) {
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, fmt.Errorf("trip not found: %w", err)
	}
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}

	dailyBrief, _ := tripData["dailyBrief"].(bool)
	waGroup, _ := tripData["whatsappGroup"].(string)
	waGroup = strings.TrimSpace(waGroup)
	if opts.RequireConfigured && (!dailyBrief || waGroup == "") {
		return nil, fmt.Errorf("daily brief not configured for trip %s", tripID)
	}

	var day models.Day
	dayData := map[string]any{}
	if dayNumber == -1 {
		// Virtual J0-1 prep brief (startDate−2): NOT a seed day.
		// Frontier with teasers (SPEC-teasers endOffset=J-2) → practical listes brief.
		// Always synthesize from day 0 template + adjusted calendar date.
		if err := db.Where("trip_id = ? AND day_num = ?", tripID, 0).First(&day).Error; err != nil {
			return nil, fmt.Errorf("J0-1 prep brief needs day 0 template: %w", err)
		}
		_ = json.Unmarshal([]byte(day.Data), &dayData)
		applySyntheticJ0m1(dayData)
	} else {
		if err := db.Where("trip_id = ? AND day_num = ?", tripID, dayNumber).First(&day).Error; err != nil {
			return nil, fmt.Errorf("day %d not found: %w", dayNumber, err)
		}
		_ = json.Unmarshal([]byte(day.Data), &dayData)
	}

	dateStr := ""
	if trip.StartDate != nil && *trip.StartDate != "" {
		if t0, err := time.Parse("2006-01-02", *trip.StartDate); err == nil {
			d := t0.AddDate(0, 0, dayNumber-1)
			dateStr = d.Format("2006-01-02")
		}
	}
	weekday := ""
	if dateStr != "" {
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			weekday = frenchWeekday(t.Weekday())
		}
	}

	label, _ := dayData["title"].(string)
	if label == "" {
		label, _ = dayData["label"].(string)
	}

	out := &DayBriefData{
		TripID:        tripID,
		TripName:      trip.Name,
		DayNumber:     dayNumber,
		Date:          dateStr,
		Weekday:       weekday,
		Label:         label,
		PlaceName:     placeNameFromDay(label, dayData),
		From:          firstString(dayData, "from"),
		To:            firstString(dayData, "to"),
		Dist:          firstString(dayData, "dist"),
		WhatsAppGroup: waGroup,
		Timezone:      resolveTZ(tripData, dayData),
		HasKids:       tripHasKids(tripData),
		TravelDay:     isTravelDay(dayData, label),
	}
	if locID, _ := dayData["locationId"].(string); locID != "" {
		out.LocationID = strings.TrimSpace(locID)
	}
	if trip.StartDate != nil {
		pres := ResolveActuPresence(db, tripID, dayNumber, *trip.StartDate)
		if pres.LocationID != "" {
			out.LocationID = pres.LocationID
		}
		if pres.PlaceName != "" {
			out.PlaceName = pres.PlaceName
		}
		out.PlaceStayFrom = pres.FromDate
		out.PlaceStayFromTime = pres.FromTime
		out.PlaceStayTo = pres.ToDate
		out.PlaceStayToTime = pres.ToTime
		out.ActuFocus = pres.Focus
	}

	if hotelID, ok := dayData["hotelId"].(string); ok && hotelID != "" {
		if hotels, ok := tripData["hotels"].(map[string]any); ok {
			if h, ok := hotels[hotelID].(map[string]any); ok {
				out.Hotel = copyHotelAccess(hotelID, h)
			}
		}
	}
	if out.Hotel == nil {
		var hotelRow models.Hotel
		if err := db.Where("trip_id = ? AND day_num = ?", tripID, dayNumber).First(&hotelRow).Error; err == nil {
			hm := map[string]any{}
			_ = json.Unmarshal([]byte(hotelRow.Data), &hm)
			if id, _ := hm["id"].(string); id == "" {
				hm = copyHotelAccess("", hm)
			} else {
				hm = copyHotelAccess(id, hm)
			}
			out.Hotel = hm
		}
	}

	if rid, ok := dayData["restaurant"].(string); ok && rid != "" {
		if restos, ok := tripData["restaurants"].(map[string]any); ok {
			if r, ok := restos[rid].(map[string]any); ok {
				out.Restaurant = map[string]any{
					"id":      rid,
					"name":    r["name"],
					"address": firstString(r, "addr", "address"),
					"booking": r["booking"],
				}
			}
		}
	}

	out.Timeline = timelineEntries(dayData["timeline"])
	out.RouteOrder = AssessTimelineOrder(out.Timeline)
	out.Highlights = stringList(dayData["highlights"])
	out.MapURL = firstString(dayData, "mapUrl", "routeUrl")
	out.Duration = firstString(dayData, "dur", "duration")
	out.Alerts = stringList(dayData["alerts"])

	return out, nil
}

func placeNameFromDay(label string, dayData map[string]any) string {
	label = strings.TrimSpace(label)
	if label == "" {
		if id, _ := dayData["locationId"].(string); id != "" {
			return strings.ReplaceAll(id, "-", " ")
		}
		return ""
	}
	for _, sep := range []string{" — ", " – ", " - ", " → ", "→"} {
		if i := strings.Index(label, sep); i > 0 {
			left := strings.TrimSpace(label[:i])
			left = strings.TrimLeftFunc(left, func(r rune) bool {
				return r == '🚗' || r == '✈' || r == '️' || r == '🚆' || r == '🚢' || r == ' '
			})
			// Also strip common transport prefixes without relying on all emoji
			left = strings.TrimSpace(strings.TrimPrefix(left, "🚗"))
			if left != "" {
				return left
			}
		}
	}
	return strings.TrimSpace(strings.TrimPrefix(label, "🚗"))
}

func tripHasKids(tripData map[string]any) bool {
	people, _ := tripData["people"].(map[string]any)
	travelers, _ := tripData["travelers"].([]any)
	checkPerson := func(p map[string]any) bool {
		if p == nil {
			return false
		}
		if child, ok := p["child"].(bool); ok && child {
			return true
		}
		if isChild, ok := p["isChild"].(bool); ok && isChild {
			return true
		}
		if age, ok := asFloat(p["age"]); ok && age > 0 && age < 18 {
			return true
		}
		emoji, _ := p["emoji"].(string)
		for _, e := range []string{"🧒", "👧", "👦", "👶"} {
			if strings.Contains(emoji, e) {
				return true
			}
		}
		return false
	}
	for _, t := range travelers {
		tm, _ := t.(map[string]any)
		if tm == nil {
			continue
		}
		if checkPerson(tm) {
			return true
		}
		pid, _ := tm["personId"].(string)
		if pid != "" && people != nil {
			if p, _ := people[pid].(map[string]any); checkPerson(p) {
				return true
			}
		}
	}
	return false
}

func isTravelDay(dayData map[string]any, label string) bool {
	dist := strings.ToLower(strings.TrimSpace(firstString(dayData, "dist")))
	dur := strings.ToLower(strings.TrimSpace(firstString(dayData, "dur", "duration")))
	if strings.Contains(label, "🚗") || strings.Contains(label, "✈") {
		return true
	}
	// dist "Local" / "-" = journée sur place
	if dist == "" || dist == "-" || dist == "local" {
		// Only duration with hours counts as travel when dist is local/empty
		if dur != "" && dur != "-" && (strings.Contains(dur, "h") || strings.Contains(dur, "min")) {
			// e.g. ferry day without km — still travel-ish; require from≠empty
			if strings.TrimSpace(firstString(dayData, "from")) != "" && strings.Contains(strings.ToLower(label), "→") {
				return true
			}
		}
		return false
	}
	if strings.Contains(dist, "km") || strings.Contains(dist, "mi") {
		return true
	}
	return false
}

// DayTimezone returns IANA TZ for a trip day (locationId → locations[].tz → homeTz → Paris).
func DayTimezone(db *gorm.DB, trip models.Trip, dayNumber int) string {
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}
	dayData := map[string]any{}
	var day models.Day
	lookup := dayNumber
	if dayNumber == -1 {
		lookup = 0 // virtual J0-1 uses day 0 location TZ
	}
	if err := db.Where("trip_id = ? AND day_num = ?", trip.ID, lookup).First(&day).Error; err == nil {
		_ = json.Unmarshal([]byte(day.Data), &dayData)
	}
	return resolveTZ(tripData, dayData)
}

// applySyntheticJ0m1 rewrites day-0 template into a J0-1 (J-2) prep brief payload.
// No seed row: teasers own mystery/excitement until J-2; this message is operational.
func applySyntheticJ0m1(dayData map[string]any) {
	if dayData == nil {
		return
	}
	dayData["label"] = "J0-1 — démarrer valises & avant de partir"
	dayData["emoji"] = "🧳"
	// Drop veille-only items (coucher tôt / vol demain) — keep house + packing + downloads.
	raw := dayData["timeline"]
	arr, ok := raw.([]any)
	if !ok {
		return
	}
	var kept []any
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		d := strings.ToLower(firstString(m, "d", "label", "title"))
		if strings.Contains(d, "coucher") || strings.Contains(d, "vol demain") {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) > 0 {
		dayData["timeline"] = kept
	}
}

func resolveTZ(tripData, dayData map[string]any) string {
	if locID, ok := dayData["locationId"].(string); ok && locID != "" {
		if locs, ok := tripData["locations"].(map[string]any); ok {
			if loc, ok := locs[locID].(map[string]any); ok {
				if tz, ok := loc["tz"].(string); ok && tz != "" {
					return tz
				}
			}
		}
	}
	if tz, ok := tripData["homeTz"].(string); ok && tz != "" {
		return tz
	}
	return "Europe/Paris"
}

func timelineEntries(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := map[string]any{}
		if t := firstString(m, "t", "time"); t != "" {
			entry["time"] = t
		}
		if d := firstString(m, "d", "label", "title"); d != "" {
			entry["label"] = d
		}
		if typ, ok := m["type"].(string); ok {
			entry["type"] = typ
		}
		if p := firstString(m, "place"); p != "" {
			entry["place"] = p
		}
		if lat, ok := asFloat(m["lat"]); ok {
			entry["lat"] = lat
		}
		if lon, ok := asFloat(m["lon"]); ok {
			entry["lon"] = lon
		}
		if len(entry) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

func stringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		switch t := x.(type) {
		case string:
			if t != "" {
				out = append(out, t)
			}
		case map[string]any:
			if s := firstString(t, "text", "label", "d"); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func frenchWeekday(w time.Weekday) string {
	switch w {
	case time.Monday:
		return "lundi"
	case time.Tuesday:
		return "mardi"
	case time.Wednesday:
		return "mercredi"
	case time.Thursday:
		return "jeudi"
	case time.Friday:
		return "vendredi"
	case time.Saturday:
		return "samedi"
	default:
		return "dimanche"
	}
}

// TripFlags reads dailyBrief / whatsappGroup from trip.data.
func TripFlags(trip models.Trip) (enabled bool, group string) {
	if trip.Data == nil {
		return false, ""
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return false, ""
	}
	enabled, _ = data["dailyBrief"].(bool)
	group, _ = data["whatsappGroup"].(string)
	return enabled, strings.TrimSpace(group)
}

// TripBriefSendTime reads optional seed trip.briefSendTime ("HH:MM" or "H:MM").
// ok=false → worker must use ops/daily-brief.json sendLocalHour/Minute.
func TripBriefSendTime(trip models.Trip) (hour, minute int, ok bool) {
	if trip.Data == nil {
		return 0, 0, false
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return 0, 0, false
	}
	return ParseBriefSendTime(data["briefSendTime"])
}

// ParseBriefSendTime accepts "07:30", "7:30". Empty / invalid → ok=false.
func ParseBriefSendTime(v any) (hour, minute int, ok bool) {
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return 0, 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, errH := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, errM := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// hotelAccessKeys are fields travelers ask about (codes, wifi, address, refs).
var hotelAccessKeys = []string{
	"name", "addr", "address", "phone", "pin", "wifi", "access", "ssid", "password",
	"checkin", "checkout", "confirmationNumber", "pnr", "ref", "booking", "note",
	"directions", "contact", "parking", "room", "bookingUrl", "payment", "cancellation",
	"dates", "breakfast", "amenities", "host", "notes", "price",
}

// copyHotelAccess builds a hotel map with access/booking fields (for brief + Plus assistant).
func copyHotelAccess(id string, h map[string]any) map[string]any {
	if h == nil {
		return nil
	}
	out := map[string]any{}
	if id != "" {
		out["id"] = id
	}
	for _, k := range hotelAccessKeys {
		if v, ok := h[k]; ok && v != nil {
			out[k] = v
		}
	}
	// Nested wifi object {ssid, password} or flat fields.
	if out["wifi"] == nil {
		if ssid, ok := h["ssid"]; ok {
			out["wifi"] = map[string]any{"ssid": ssid, "password": h["password"]}
		}
	}
	if len(out) == 0 || (len(out) == 1 && out["id"] != nil) {
		// Fallback: shallow copy all string/number fields if keys missing.
		for k, v := range h {
			out[k] = v
		}
		if id != "" {
			out["id"] = id
		}
	}
	return out
}
