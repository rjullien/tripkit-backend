package pluschat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/dailybrief"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// DayFocus is today or tomorrow context for the assistant.
type DayFocus struct {
	Role       string                `json:"role"` // today | tomorrow
	DayNumber  int                   `json:"dayNumber"`
	Date       string                `json:"date"`
	Weekday    string                `json:"weekday,omitempty"`
	Label      string                `json:"label,omitempty"`
	PlaceName  string                `json:"placeName,omitempty"`
	Timezone   string                `json:"timezone,omitempty"`
	Timeline   []map[string]any      `json:"timeline,omitempty"`
	Highlights []string              `json:"highlights,omitempty"`
	Weather    map[string]any        `json:"weather,omitempty"`
	DressCode  string                `json:"dressCode,omitempty"`
	Bookings   map[string]any        `json:"bookings,omitempty"` // hotel, restaurants, ferry, flights, car, events, dayBooking
	Raw        *dailybrief.DayBriefData `json:"-"`
}

// TripContext is injected into the system prompt (read-only facts).
type TripContext struct {
	TripID    string         `json:"tripId"`
	TripName  string         `json:"tripName"`
	StartDate string         `json:"startDate,omitempty"`
	EndDate   string         `json:"endDate,omitempty"`
	HomeTZ    string         `json:"homeTz,omitempty"`
	NowLocal  string         `json:"nowLocal"` // RFC3339 in trip TZ
	TodayDate string         `json:"todayDate"`
	Calendar  []CalendarDay  `json:"calendar,omitempty"`
	Today     *DayFocus      `json:"today,omitempty"`
	Tomorrow  *DayFocus      `json:"tomorrow,omitempty"`
	Notes     []string       `json:"notes,omitempty"`
}

// CalendarDay is a short day → date → place row.
type CalendarDay struct {
	DayNumber int    `json:"day"`
	Date      string `json:"date"`
	Weekday   string `json:"weekday,omitempty"`
	Label     string `json:"label,omitempty"`
	Place     string `json:"place,omitempty"`
}

// BuildTripContext loads today + tomorrow (full day + all bookings + weather).
func BuildTripContext(db *gorm.DB, tripID string, now time.Time) (*TripContext, error) {
	tripID = strings.TrimSpace(tripID)
	if db == nil || tripID == "" {
		return nil, fmt.Errorf("tripId required")
	}
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, fmt.Errorf("trip not found: %w", err)
	}
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}

	homeTZ := firstStr(tripData["homeTz"])
	if homeTZ == "" {
		homeTZ = "Europe/Paris"
	}
	loc, err := time.LoadLocation(homeTZ)
	if err != nil {
		loc = time.UTC
		homeTZ = "UTC"
	}
	localNow := now.In(loc)
	todayDate := localNow.Format("2006-01-02")
	tomorrowDate := localNow.AddDate(0, 0, 1).Format("2006-01-02")

	startStr := ""
	if trip.StartDate != nil {
		startStr = *trip.StartDate
	}
	endStr := ""
	if trip.EndDate != nil {
		endStr = *trip.EndDate
	}

	ctx := &TripContext{
		TripID:    tripID,
		TripName:  trip.Name,
		StartDate: startStr,
		EndDate:   endStr,
		HomeTZ:    homeTZ,
		NowLocal:  localNow.Format(time.RFC3339),
		TodayDate: todayDate,
		Notes: []string{
			"Réponds UNIQUEMENT à partir de ce contexte (et de l'historique chat).",
			"Codes pin / wifi / confirmation / adresses : cite-les tels quels depuis bookings.hotel.",
			"Pas de modification de seed — renvoyer vers Léo pour écrire.",
			"« Lundi / demain / J3 » = calendrier ci-dessous + today/tomorrow.",
		},
	}

	if startStr != "" {
		start, err := time.Parse("2006-01-02", startStr)
		if err == nil {
			maxDay := 1
			if endStr != "" {
				if end, err2 := time.Parse("2006-01-02", endStr); err2 == nil {
					maxDay = int(end.Sub(start).Hours()/24) + 1
				}
			}
			ctx.Calendar = buildCalendar(db, tripID, start, maxDay)
			todayDN := dayNumberForDate(start, todayDate)
			tomorrowDN := dayNumberForDate(start, tomorrowDate)
			ctx.Today = buildDayFocus(db, trip, tripData, "today", todayDN, todayDate)
			ctx.Tomorrow = buildDayFocus(db, trip, tripData, "tomorrow", tomorrowDN, tomorrowDate)
		}
	}

	return ctx, nil
}

func dayNumberForDate(start time.Time, dateStr string) int {
	d, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0
	}
	// day 1 = startDate → dayNumber = days since start + 1
	return int(d.Sub(start).Hours()/24) + 1
}

func buildCalendar(db *gorm.DB, tripID string, start time.Time, maxDay int) []CalendarDay {
	var days []models.Day
	_ = db.Where("trip_id = ?", tripID).Order("day_num").Find(&days).Error
	byNum := map[int]models.Day{}
	for _, d := range days {
		byNum[d.DayNum] = d
	}
	var out []CalendarDay
	// Include prep days -1,0 if present or within window around today
	lo, hi := -1, maxDay
	for n := lo; n <= hi; n++ {
		date := start.AddDate(0, 0, n-1).Format("2006-01-02")
		wd := frenchWeekdayShort(start.AddDate(0, 0, n-1).Weekday())
		label, place := "", ""
		if d, ok := byNum[n]; ok {
			dm := map[string]any{}
			_ = json.Unmarshal([]byte(d.Data), &dm)
			label = firstStr(dm["label"], dm["title"])
			place = firstStr(dm["locationId"])
			if place == "" {
				place = placeFromLabel(label)
			}
		} else if n == -1 {
			label = "J0-1 (préparation)"
		} else if n == 0 {
			label = "J0 (veille)"
		} else {
			continue
		}
		out = append(out, CalendarDay{
			DayNumber: n,
			Date:      date,
			Weekday:   wd,
			Label:     label,
			Place:     place,
		})
	}
	return out
}

func buildDayFocus(db *gorm.DB, trip models.Trip, tripData map[string]any, role string, dayNumber int, dateStr string) *DayFocus {
	focus := &DayFocus{
		Role:      role,
		DayNumber: dayNumber,
		Date:      dateStr,
	}
	if wd, err := time.Parse("2006-01-02", dateStr); err == nil {
		focus.Weekday = frenchWeekdayShort(wd.Weekday())
	}

	src, err := dailybrief.ExtractDayOpts(db, trip.ID, dayNumber, dailybrief.ExtractOpts{RequireConfigured: false})
	if err != nil {
		// Day may not exist (outside trip) — still attach trip-level bookings for the date.
		focus.Bookings = collectBookingsForDate(tripData, nil, dateStr)
		if len(focus.Bookings) == 0 {
			focus.Label = "(hors itinéraire seed)"
		}
		return focus
	}
	focus.Raw = src
	focus.Label = src.Label
	focus.PlaceName = src.PlaceName
	focus.Timezone = src.Timezone
	focus.Timeline = src.Timeline
	focus.Highlights = src.Highlights
	focus.Weekday = src.Weekday

	// Weather for this calendar date at day coords.
	dayData := map[string]any{}
	var dayRow models.Day
	if err := db.Where("trip_id = ? AND day_num = ?", trip.ID, dayNumber).First(&dayRow).Error; err == nil {
		_ = json.Unmarshal([]byte(dayRow.Data), &dayData)
	}
	if lat, lon, ok := dailybrief.CoordsFromTripData(tripData, dayData); ok {
		_ = dailybrief.EnrichDayOnDate(src, lat, lon, dateStr)
		focus.Weather = src.Weather
		focus.DressCode = src.DressCode
	}

	focus.Bookings = collectBookingsForDate(tripData, src, dateStr)
	attachDayBooking(focus.Bookings, dayData)
	if focus.Bookings == nil {
		focus.Bookings = map[string]any{}
		attachDayBooking(focus.Bookings, dayData)
	}
	return focus
}

func collectBookingsForDate(tripData map[string]any, src *dailybrief.DayBriefData, dateStr string) map[string]any {
	out := map[string]any{}

	if src != nil && src.Hotel != nil {
		out["hotel"] = src.Hotel
	}

	// Day-embedded booking (ferry etc.)
	if src != nil {
		var dayRow map[string]any
		// Timeline restaurants already partially in src.Restaurant — expand all refs below.
		_ = dayRow
	}

	restaurants := collectDayRestaurants(tripData, src)
	if len(restaurants) > 0 {
		out["restaurants"] = restaurants
	}

	if flights := filterFlightsOnDate(tripData["flights"], dateStr); len(flights) > 0 {
		out["flights"] = flights
	}
	if car := filterCarOnDate(tripData["carRental"], dateStr); car != nil {
		out["carRental"] = car
	}
	if ferries := filterFerriesOnDate(tripData, dateStr); len(ferries) > 0 {
		out["ferries"] = ferries
	}
	if events := filterEventsOnDate(tripData["events"], dateStr); len(events) > 0 {
		out["events"] = events
	}

	// day.booking from extract: re-load via hotel path already done; get booking from day data via src timeline only.
	// Attach day.booking if we can find it on the day's raw — ExtractDay doesn't expose it.
	// Caller may have left it in Bookings via extractDayBooking — do that in buildDayFocus via dayData.
	return out
}

// attachDayBooking merges day["booking"] into bookings map.
func attachDayBooking(bookings map[string]any, dayData map[string]any) {
	if bookings == nil || dayData == nil {
		return
	}
	if b, ok := dayData["booking"]; ok && b != nil {
		bookings["dayBooking"] = b
	}
}

func collectDayRestaurants(tripData map[string]any, src *dailybrief.DayBriefData) []map[string]any {
	restosRoot, _ := tripData["restaurants"].(map[string]any)
	if restosRoot == nil || src == nil {
		if src != nil && src.Restaurant != nil {
			return []map[string]any{src.Restaurant}
		}
		return nil
	}
	seen := map[string]bool{}
	var out []map[string]any
	addRef := func(ref any) {
		key := strings.TrimSpace(fmt.Sprint(ref))
		if key == "" || key == "<nil>" || seen[key] {
			return
		}
		seen[key] = true
		entry, _ := restosRoot[key].(map[string]any)
		if entry == nil {
			// numeric keys sometimes stored without string cast issues
			return
		}
		item := map[string]any{"ref": key}
		if main, ok := entry["main"].(map[string]any); ok {
			item["name"] = main["name"]
			item["note"] = main["note"]
			item["price"] = main["price"]
			item["mapUrl"] = main["mapUrl"]
			item["addr"] = firstStr(main["addr"], main["address"])
			item["booking"] = main["booking"]
			item["phone"] = main["phone"]
		} else {
			item["name"] = entry["name"]
			item["note"] = entry["note"]
			item["mapUrl"] = entry["mapUrl"]
			item["booking"] = entry["booking"]
		}
		out = append(out, item)
	}
	for _, ev := range src.Timeline {
		if ref, ok := ev["restaurantRef"]; ok {
			addRef(ref)
		}
	}
	if src.Restaurant != nil {
		if id, _ := src.Restaurant["id"].(string); id != "" && !seen[id] {
			out = append(out, src.Restaurant)
			seen[id] = true
		}
	}
	return out
}

func filterFlightsOnDate(raw any, dateStr string) []map[string]any {
	fm, _ := raw.(map[string]any)
	if fm == nil {
		return nil
	}
	var out []map[string]any
	for _, key := range []string{"outbound", "aller", "inbound", "return", "retour"} {
		leg, _ := fm[key].(map[string]any)
		if leg == nil {
			continue
		}
		if flightTouchesDate(leg, dateStr) {
			cp := map[string]any{"leg": key}
			for k, v := range leg {
				cp[k] = v
			}
			out = append(out, cp)
		}
	}
	return out
}

func flightTouchesDate(leg map[string]any, dateStr string) bool {
	if segs, ok := leg["segments"].([]any); ok && len(segs) > 0 {
		for _, s := range segs {
			sm, _ := s.(map[string]any)
			if sm == nil {
				continue
			}
			if datePrefix(firstStr(sm["dep"], sm["arr"])) == dateStr {
				return true
			}
		}
	}
	return datePrefix(firstStr(leg["dep"], leg["arr"], leg["date"])) == dateStr
}

func filterCarOnDate(raw any, dateStr string) map[string]any {
	car, _ := raw.(map[string]any)
	if car == nil {
		return nil
	}
	pickup, _ := car["pickup"].(map[string]any)
	ret, _ := car["return"].(map[string]any)
	pickDate := ""
	retDate := ""
	if pickup != nil {
		pickDate = datePrefix(firstStr(pickup["date"]))
	}
	if ret != nil {
		retDate = datePrefix(firstStr(ret["date"]))
	}
	if pickDate != dateStr && retDate != dateStr {
		return nil
	}
	out := map[string]any{}
	for k, v := range car {
		out[k] = v
	}
	out["relevant"] = map[string]any{"pickupDate": pickDate, "returnDate": retDate, "focusDate": dateStr}
	return out
}

func filterFerriesOnDate(tripData map[string]any, dateStr string) []map[string]any {
	var list []any
	switch v := tripData["ferries"].(type) {
	case []any:
		list = v
	case map[string]any:
		list = []any{v}
	}
	if f := tripData["ferry"]; f != nil {
		list = append(list, f)
	}
	var out []map[string]any
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if datePrefix(firstStr(m["date"], m["depDate"])) == dateStr || datePrefix(firstStr(m["arrDate"], m["arr"])) == dateStr {
			out = append(out, m)
		}
	}
	return out
}

func filterEventsOnDate(raw any, dateStr string) []map[string]any {
	em, _ := raw.(map[string]any)
	if em == nil {
		return nil
	}
	var out []map[string]any
	for id, v := range em {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		if datePrefix(firstStr(m["date"])) == dateStr {
			cp := map[string]any{"id": id}
			for k, val := range m {
				cp[k] = val
			}
			out = append(out, cp)
		}
	}
	return out
}

func datePrefix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func firstStr(vals ...any) string {
	for _, v := range vals {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func placeFromLabel(label string) string {
	label = strings.TrimSpace(label)
	for _, sep := range []string{" — ", " – ", " - ", " → "} {
		if i := strings.Index(label, sep); i > 0 {
			return strings.TrimSpace(label[:i])
		}
	}
	return label
}

func frenchWeekdayShort(d time.Weekday) string {
	switch d {
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

// FormatContextJSON returns compact JSON for the system prompt.
func FormatContextJSON(ctx *TripContext) string {
	if ctx == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
