package dailybrief

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// ActuPresence is where Actualité should focus, with the physical stay window
// in that city (half-days from timeline depart/arrive).
type ActuPresence struct {
	LocationID string `json:"locationId,omitempty"`
	PlaceName  string `json:"placeName,omitempty"`
	FromDate   string `json:"fromDate,omitempty"` // YYYY-MM-DD
	FromTime   string `json:"fromTime,omitempty"` // HH:MM
	ToDate     string `json:"toDate,omitempty"`
	ToTime     string `json:"toTime,omitempty"`
	// Focus: "arrival" if morning departure → news for destination; else "on_site".
	Focus string `json:"focus,omitempty"`
}

var (
	reClock      = regexp.MustCompile(`(?i)\b([01]?\d|2[0-3])[:hH]([0-5]\d)\b`)
	reDepartVerb = regexp.MustCompile(`(?i)(départ|depart|route vers|en route|pickup|prendre (la )?route|quitter|route panoramique)`)
	reArriveVerb = regexp.MustCompile(`(?i)(arrivée|arrivee|installation|check-?in)`)
	reDriveEmoji = regexp.MustCompile(`(?i)(🚗|✈|✈️|🚆|🚢|ferry)`)
)

type daySnap struct {
	Num        int
	LocID      string
	From       string
	To         string
	Travel     bool
	Timeline   []map[string]any
	Label      string
	DepartTime string
	ArriveTime string
}

// ResolveActuPresence picks the Actualité city and exact presence window.
// Example: leave Québec 09:30 → Baie-Saint-Paul 11:00 → focus BSP, not Québec shows.
func ResolveActuPresence(db *gorm.DB, tripID string, dayNumber int, startDate string) ActuPresence {
	start, err := time.Parse("2006-01-02", strings.TrimSpace(startDate))
	if err != nil || db == nil {
		return ActuPresence{}
	}
	dayDate := func(n int) string {
		return start.AddDate(0, 0, n-1).Format("2006-01-02")
	}

	snaps := loadDaySnaps(db, tripID)
	cur, ok := snaps[dayNumber]
	if !ok {
		d := dayDate(dayNumber)
		return ActuPresence{FromDate: d, FromTime: "00:00", ToDate: d, ToTime: "23:59", Focus: "on_site"}
	}

	focusLoc := cur.LocID
	focus := "on_site"
	place := preferPlaceName(cur)

	morningLeave := cur.Travel && cur.DepartTime != "" && clockMinutes(cur.DepartTime) < 12*60
	if morningLeave {
		focus = "arrival"
		if cur.To != "" {
			place = cur.To
		}
		// Overnight locationId is the destination on these seeds.
		focusLoc = cur.LocID
	} else if cur.Travel && cur.ArriveTime != "" {
		focus = "arrival"
		if cur.To != "" {
			place = cur.To
		}
		focusLoc = cur.LocID
	}

	if focusLoc == "" {
		d := dayDate(dayNumber)
		return ActuPresence{
			PlaceName: place, FromDate: d, FromTime: "00:00", ToDate: d, ToTime: "23:59", Focus: focus,
		}
	}

	fromDate, fromTime, toDate, toTime := presenceWindowForLoc(snaps, focusLoc, dayDate)
	if place == "" {
		place = humanizeLocID(focusLoc)
	}
	return ActuPresence{
		LocationID: focusLoc,
		PlaceName:  place,
		FromDate:   fromDate,
		FromTime:   fromTime,
		ToDate:     toDate,
		ToTime:     toTime,
		Focus:      focus,
	}
}

// presenceWindowForLoc: first overnight day arrive → morning depart of the day after last overnight.
func presenceWindowForLoc(snaps map[int]daySnap, locID string, dayDate func(int) string) (fromDate, fromTime, toDate, toTime string) {
	nums := sortedDayNums(snaps)
	var block []int
	for _, n := range nums {
		if snaps[n].LocID == locID {
			block = append(block, n)
		}
	}
	if len(block) == 0 {
		return "", "", "", ""
	}
	// Use the contiguous block that contains… any: merge contiguous runs, pick first run
	// that matches — for a given locID usually one run.
	lo, hi := block[0], block[0]
	runs := [][2]int{}
	for i := 1; i < len(block); i++ {
		if block[i] == hi+1 {
			hi = block[i]
			continue
		}
		runs = append(runs, [2]int{lo, hi})
		lo, hi = block[i], block[i]
	}
	runs = append(runs, [2]int{lo, hi})
	lo, hi = runs[0][0], runs[0][1]

	fromDate = dayDate(lo)
	fromTime = "00:00"
	if s := snaps[lo]; s.ArriveTime != "" {
		fromTime = s.ArriveTime
	} else if s.Travel {
		fromTime = "12:00"
	}

	toDate = dayDate(hi)
	toTime = "23:59"
	if next, ok := snaps[hi+1]; ok && next.Travel && next.LocID != locID {
		if next.DepartTime != "" {
			toDate = dayDate(hi + 1)
			toTime = next.DepartTime
		}
	}
	return fromDate, fromTime, toDate, toTime
}

func sortedDayNums(snaps map[int]daySnap) []int {
	max := 0
	for n := range snaps {
		if n > max {
			max = n
		}
	}
	var out []int
	for n := 0; n <= max; n++ {
		if _, ok := snaps[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

func loadDaySnaps(db *gorm.DB, tripID string) map[int]daySnap {
	var days []models.Day
	_ = db.Where("trip_id = ?", tripID).Order("day_num asc").Find(&days)
	out := map[int]daySnap{}
	for _, d := range days {
		var data map[string]any
		_ = json.Unmarshal([]byte(d.Data), &data)
		label, _ := data["title"].(string)
		if label == "" {
			label, _ = data["label"].(string)
		}
		tl := timelineEntries(data["timeline"])
		snap := daySnap{
			Num:      d.DayNum,
			LocID:    strings.TrimSpace(firstString(data, "locationId")),
			From:     firstString(data, "from"),
			To:       firstString(data, "to"),
			Travel:   isTravelDay(data, label),
			Timeline: tl,
			Label:    label,
		}
		snap.DepartTime, snap.ArriveTime = timelineDepartArrive(tl)
		out[d.DayNum] = snap
	}
	return out
}

func timelineDepartArrive(tl []map[string]any) (depart, arrive string) {
	for _, e := range tl {
		t := normalizeClock(fmt.Sprint(e["time"]))
		label, _ := e["label"].(string)
		if t == "" || label == "" {
			continue
		}
		if depart == "" && (reDepartVerb.MatchString(label) || (reDriveEmoji.MatchString(label) && strings.Contains(label, "→"))) {
			depart = t
		}
		if arrive == "" && reArriveVerb.MatchString(label) {
			arrive = t
		}
	}
	if depart == "" {
		for _, e := range tl {
			t := normalizeClock(fmt.Sprint(e["time"]))
			label, _ := e["label"].(string)
			if t != "" && reDriveEmoji.MatchString(label) {
				depart = t
				break
			}
		}
	}
	return depart, arrive
}

func normalizeClock(s string) string {
	s = strings.TrimSpace(s)
	m := reClock.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if h < 0 || h > 23 || min < 0 || min > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h, min)
}

func clockMinutes(hhmm string) int {
	hhmm = normalizeClock(hhmm)
	if hhmm == "" {
		return 0
	}
	var h, m int
	fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return h*60 + m
}

func contiguousLocDays(snaps map[int]daySnap, dayNumber int, locID string) (lo, hi int) {
	lo, hi = dayNumber, dayNumber
	for n := dayNumber - 1; ; n-- {
		s, ok := snaps[n]
		if !ok || s.LocID != locID {
			break
		}
		lo = n
	}
	for n := dayNumber + 1; ; n++ {
		s, ok := snaps[n]
		if !ok || s.LocID != locID {
			break
		}
		hi = n
	}
	return lo, hi
}

func preferPlaceName(s daySnap) string {
	if s.Travel && s.To != "" {
		return s.To
	}
	if !s.Travel && s.From != "" {
		return s.From
	}
	if s.To != "" {
		return s.To
	}
	return humanizeLocID(s.LocID)
}

func humanizeLocID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "-")
}

// PlaceStayWindow returns date-only bounds for contiguous overnight locationId
// (legacy helper / tests). Prefer ResolveActuPresence for times.
func PlaceStayWindow(db *gorm.DB, tripID string, dayNumber int, locationID, startDate string) (from, to string) {
	start, err := time.Parse("2006-01-02", strings.TrimSpace(startDate))
	if err != nil || db == nil {
		return "", ""
	}
	dayDate := func(n int) string {
		return start.AddDate(0, 0, n-1).Format("2006-01-02")
	}
	snaps := loadDaySnaps(db, tripID)
	loc := strings.TrimSpace(locationID)
	if loc == "" {
		if s, ok := snaps[dayNumber]; ok {
			loc = s.LocID
		}
	}
	if loc == "" {
		d := dayDate(dayNumber)
		return d, d
	}
	lo, hi := contiguousLocDays(snaps, dayNumber, loc)
	return dayDate(lo), dayDate(hi)
}
