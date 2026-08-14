package polarsteps

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Happened is one cleaned timeline beat already elapsed today.
type Happened struct {
	T string `json:"t,omitempty"`
	D string `json:"d,omitempty"`
}

// Input is the JSON sent to the LLM (no PNR / packing / wifi).
type Input struct {
	Kind            string     `json:"kind"`
	NowLocal        string     `json:"nowLocal"`
	WindowFromLocal string     `json:"windowFromLocal"`
	UserNote        string     `json:"userNote,omitempty"`
	Day             int        `json:"day"`
	Label           string     `json:"label,omitempty"`
	From            string     `json:"from,omitempty"`
	To              string     `json:"to,omitempty"`
	Travelers       []string   `json:"travelers,omitempty"`
	TripName        string     `json:"tripName"`
	Nights          int        `json:"nights,omitempty"`
	Phases          []string   `json:"phases,omitempty"`
	Happened        []Happened `json:"happened,omitempty"`
}

// SeedGate is polarsteps config from trip.data.
type SeedGate struct {
	Enabled bool
	TripURL string
}

// TripPolarsteps reads trip.data.polarsteps.
func TripPolarsteps(tripData map[string]any) SeedGate {
	raw, ok := tripData["polarsteps"]
	if !ok {
		return SeedGate{}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return SeedGate{}
	}
	g := SeedGate{}
	if v, ok := m["enabled"].(bool); ok {
		g.Enabled = v
	}
	if s, ok := m["tripUrl"].(string); ok {
		g.TripURL = strings.TrimSpace(s)
	}
	return g
}

// TripActive reports startDate ≤ localDate ≤ endDate.
func TripActive(start, end, localDate string) bool {
	if start == "" || localDate == "" {
		return false
	}
	if localDate < start {
		return false
	}
	if end != "" && localDate > end {
		return false
	}
	return true
}

// BuildInput loads today's elapsed programme for Polarsteps.
func BuildInput(db *gorm.DB, tripID string, now time.Time, userNote string) (*Input, *models.Trip, map[string]any, error) {
	tripID = strings.TrimSpace(tripID)
	if db == nil || tripID == "" {
		return nil, nil, nil, fmt.Errorf("tripId required")
	}
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("trip not found: %w", err)
	}
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}

	startStr := ""
	if trip.StartDate != nil {
		startStr = *trip.StartDate
	}
	endStr := ""
	if trip.EndDate != nil {
		endStr = *trip.EndDate
	}
	if startStr == "" {
		return nil, &trip, tripData, fmt.Errorf("trip startDate missing")
	}
	start, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return nil, &trip, tripData, fmt.Errorf("invalid startDate")
	}

	localNow, dayNum, dayData := pickLocalDay(db, tripID, start, tripData, now)

	kind := "daily"
	maxDay := 1
	if endStr != "" {
		if end, err := time.Parse("2006-01-02", endStr); err == nil {
			maxDay = int(end.Sub(start).Hours()/24) + 1
		}
	}
	if dayNum == 1 {
		kind = "opening"
	} else if dayNum == maxDay {
		kind = "closing"
	}

	windowStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 4, 0, 0, 0, localNow.Location())
	label := str(dayData["label"], dayData["title"])
	in := &Input{
		Kind:            kind,
		NowLocal:        localNow.Format(time.RFC3339),
		WindowFromLocal: windowStart.Format(time.RFC3339),
		UserNote:        clipUserNote(userNote),
		Day:             dayNum,
		Label:           cleanLabel(label),
		From:            str(dayData["from"]),
		To:              str(dayData["to"]),
		Travelers:       travelerNames(tripData),
		TripName:        trip.Name,
		Nights:          nightsOf(maxDay),
		Phases:          phaseNames(tripData),
		Happened:        elapsedTimeline(dayData, start.AddDate(0, 0, dayNum-1), windowStart, localNow, resolveDayTZ(tripData, dayData)),
	}
	return in, &trip, tripData, nil
}

func nightsOf(maxDay int) int {
	if maxDay <= 1 {
		return 0
	}
	return maxDay - 1
}

// pickLocalDay chooses the calendar day using the day's location TZ, not homeTz.
// J1 evening in America/Toronto must not roll to J2 just because Europe/Paris is already tomorrow.
func pickLocalDay(db *gorm.DB, tripID string, start time.Time, tripData map[string]any, now time.Time) (time.Time, int, map[string]any) {
	homeTZ := str(tripData["homeTz"])
	if homeTZ == "" {
		homeTZ = "Europe/Paris"
	}
	seen := map[string]bool{}
	var tzs []string
	for _, tz := range append([]string{homeTZ}, locationTZs(tripData)...) {
		if tz == "" || seen[tz] {
			continue
		}
		seen[tz] = true
		tzs = append(tzs, tz)
	}

	type cand struct {
		local time.Time
		day   int
		data  map[string]any
	}
	var home, best *cand
	for _, tz := range tzs {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			continue
		}
		local := now.In(loc)
		n := dayNumberForDate(start, local.Format("2006-01-02"))
		data := loadDayData(db, tripID, n)
		c := cand{local: local, day: n, data: data}
		if tz == homeTZ {
			cc := c
			home = &cc
		}
		if dayLoc := dayLocationTZ(tripData, data); dayLoc != "" && dayLoc == tz {
			cc := c
			if best == nil || (best.day < 1 && cc.day >= 1) {
				best = &cc
			}
		}
	}
	if best != nil {
		return best.local, best.day, best.data
	}
	if home != nil {
		return home.local, home.day, home.data
	}
	loc, err := time.LoadLocation(homeTZ)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	n := dayNumberForDate(start, local.Format("2006-01-02"))
	return local, n, loadDayData(db, tripID, n)
}

func loadDayData(db *gorm.DB, tripID string, dayNum int) map[string]any {
	out := map[string]any{}
	if db == nil || tripID == "" {
		return out
	}
	var day models.Day
	if err := db.Where("trip_id = ? AND day_num = ?", tripID, dayNum).First(&day).Error; err != nil {
		return out
	}
	_ = json.Unmarshal([]byte(day.Data), &out)
	return out
}

func locationTZs(tripData map[string]any) []string {
	locs, ok := tripData["locations"].(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range locs {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if tz := str(m["tz"]); tz != "" {
			out = append(out, tz)
		}
	}
	return out
}

func dayLocationTZ(tripData, dayData map[string]any) string {
	locID := str(dayData["locationId"])
	if locID == "" {
		return ""
	}
	locs, ok := tripData["locations"].(map[string]any)
	if !ok {
		return ""
	}
	loc, ok := locs[locID].(map[string]any)
	if !ok {
		return ""
	}
	return str(loc["tz"])
}

func dayNumberForDate(start time.Time, dateStr string) int {
	d, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0
	}
	return int(d.Sub(start).Hours()/24) + 1
}

func resolveDayTZ(tripData, dayData map[string]any) string {
	if locID := str(dayData["locationId"]); locID != "" {
		if locs, ok := tripData["locations"].(map[string]any); ok {
			if loc, ok := locs[locID].(map[string]any); ok {
				if tz := str(loc["tz"]); tz != "" {
					return tz
				}
			}
		}
	}
	if tz := str(tripData["homeTz"]); tz != "" {
		return tz
	}
	return "Europe/Paris"
}

func elapsedTimeline(dayData map[string]any, calDate, windowStart, now time.Time, defaultTZ string) []Happened {
	raw, ok := dayData["timeline"].([]any)
	if !ok {
		return nil
	}
	var out []Happened
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tStr := str(m["t"], m["time"])
		dStr := cleanLabel(str(m["d"], m["label"], m["title"]))
		if dStr == "" {
			continue
		}
		tzName := str(m["tz"])
		if tzName == "" {
			tzName = defaultTZ
		}
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			loc = now.Location()
		}
		when := calDate.In(loc)
		if hh, mm, ok := parseClock(tStr); ok {
			when = time.Date(calDate.Year(), calDate.Month(), calDate.Day(), hh, mm, 0, 0, loc)
		} else {
			when = windowStart
		}
		if when.After(now) {
			continue
		}
		// 04:00 cutoff in the item's own TZ — a 09:10 Nice takeoff must count
		// on J1 even if destination TZ is still "before dawn".
		itemDawn := time.Date(calDate.Year(), calDate.Month(), calDate.Day(), 4, 0, 0, 0, loc)
		if tStr != "" && when.Before(itemDawn) {
			continue
		}
		out = append(out, Happened{T: tStr, D: dStr})
	}
	return out
}

func parseClock(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

func travelerNames(tripData map[string]any) []string {
	people := map[string]string{}
	if p, ok := tripData["people"].(map[string]any); ok {
		for id, v := range p {
			if m, ok := v.(map[string]any); ok {
				if n := str(m["name"], m["displayName"]); n != "" {
					people[id] = n
				}
			}
		}
	}
	var out []string
	switch tv := tripData["travelers"].(type) {
	case []any:
		for _, x := range tv {
			switch t := x.(type) {
			case string:
				out = append(out, displayPerson(t, people))
			case map[string]any:
				id := str(t["personId"], t["id"])
				out = append(out, displayPerson(id, people))
			}
		}
	}
	return out
}

func displayPerson(id string, people map[string]string) string {
	id = strings.TrimSpace(id)
	if n := people[id]; n != "" {
		return n
	}
	if id == "" {
		return ""
	}
	r := []rune(id)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func phaseNames(tripData map[string]any) []string {
	raw, ok := tripData["phases"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range raw {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		if n := str(m["name"]); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func str(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
