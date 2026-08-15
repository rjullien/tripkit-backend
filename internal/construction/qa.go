package construction

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QAViolation represents a single rule violation found during QA.
type QAViolation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // "red" or "yellow"
	Message  string `json:"message"`
	DayNum   int    `json:"dayNum,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// qaProfile holds thresholds extracted from the travel profile.
type qaProfile struct {
	MaxDrivingMinutes int
	MajorSitesPerDay  int
	BudgetAccomMax    int
	BudgetRestMax     int
	BudgetActMax      int
	IsRoutier         bool // trip uses car as main transport
}

// defaultProfile returns sensible defaults when no travel profile is provided.
func defaultProfile() qaProfile {
	return qaProfile{
		MaxDrivingMinutes: 360, // 6h
		MajorSitesPerDay:  2,
		BudgetAccomMax:    0, // 0 = not checked
		BudgetRestMax:     0,
		BudgetActMax:      0,
		IsRoutier:         false,
	}
}

// parseDuration parses a duration string like "4h", "4h30", "4h30m", "270m", "270"
// into minutes.
func parseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Try standard Go duration parsing first (handles "4h", "4h30m", "30m" etc.)
	if d, err := time.ParseDuration(s); err == nil {
		return int(d.Minutes()), nil
	}

	// Try "4h30" format (hours + optional minutes without 'm' suffix)
	if idx := strings.Index(s, "h"); idx >= 0 {
		hours, err := strconv.Atoi(s[:idx])
		if err != nil {
			return 0, fmt.Errorf("invalid hours in %q", s)
		}
		mins := 0
		rest := s[idx+1:]
		rest = strings.TrimSuffix(rest, "m")
		if rest != "" {
			mins, err = strconv.Atoi(rest)
			if err != nil {
				return 0, fmt.Errorf("invalid minutes in %q", s)
			}
		}
		return hours*60 + mins, nil
	}

	// Try plain number (minutes)
	if m, err := strconv.Atoi(s); err == nil {
		return m, nil
	}

	return 0, fmt.Errorf("unparseable duration: %q", s)
}

// extractProfile builds a qaProfile from the travelProfile map.
func extractProfile(profile map[string]any) qaProfile {
	p := defaultProfile()
	if profile == nil {
		return p
	}

	// travelStyle
	if ts, ok := profile["travelStyle"].(map[string]any); ok {
		if v, ok := ts["maxDrivingPerDay"].(string); ok {
			if mins, err := parseDuration(v); err == nil && mins > 0 {
				p.MaxDrivingMinutes = mins
			}
		}
		if v, ok := ts["majorSitesPerDay"].(float64); ok && v > 0 {
			p.MajorSitesPerDay = int(v)
		}
	}

	// budgetRules
	if br, ok := profile["budgetRules"].(map[string]any); ok {
		if acc, ok := br["accommodation"].(map[string]any); ok {
			if v, ok := acc["maxPerNight"].(float64); ok {
				p.BudgetAccomMax = int(v)
			}
		}
		if rest, ok := br["restaurant"].(map[string]any); ok {
			if v, ok := rest["maxPerPerson"].(float64); ok {
				p.BudgetRestMax = int(v)
			}
		}
		if act, ok := br["activities"].(map[string]any); ok {
			if v, ok := act["maxPerPerson"].(float64); ok {
				p.BudgetActMax = int(v)
			}
		}
	}

	return p
}

// extractIsRoutier checks if the trip uses a car as the main transport mode.
func extractIsRoutier(tripData map[string]any) bool {
	constr, ok := tripData["construction"].(map[string]any)
	if !ok {
		return false
	}
	modes, ok := constr["transportModes"].([]any)
	if !ok {
		return false
	}
	for _, m := range modes {
		if s, ok := m.(string); ok && (s == "voiture" || s == "car" || s == "location") {
			return true
		}
	}
	return false
}

// RunQA runs all QA rules against the trip data and returns violations.
// calendar_mismatch runs FIRST and short-circuits if triggered.
func RunQA(tripData map[string]any, profile map[string]any, phase int) []QAViolation {
	p := extractProfile(profile)
	p.IsRoutier = extractIsRoutier(tripData)

	days := extractDays(tripData)
	hotels := extractHotels(tripData)

	// Rule 1: calendar_mismatch - short-circuit
	if v := checkCalendarMismatch(tripData, days); v != nil {
		return []QAViolation{*v}
	}

	// Remaining rules
	var violations []QAViolation

	violations = append(violations, checkDriveTooLong(days, p, phase)...)
	violations = append(violations, checkDriveUnverified(days, phase)...)
	violations = append(violations, checkTimezoneUndocumented(days, phase)...)
	violations = append(violations, checkTimeConstrainedConflict(days, phase)...)
	violations = append(violations, checkNightWithoutHotel(days, hotels, phase)...)
	violations = append(violations, checkDayGap(days, phase)...)
	violations = append(violations, checkTransportNotBooked(days, phase)...)
	violations = append(violations, checkCarNotBooked(days, p, phase)...)
	violations = append(violations, checkTooManyMajorSites(days, p, phase)...)
	violations = append(violations, checkBudgetExceeded(days, hotels, p, phase)...)

	return violations
}

// ── Day/Hotel extraction from tripData ──────────────────────────────────────

type qaDay struct {
	Num          int
	Date         string
	Drive        *qaDrive
	Timezone     string
	Activities   []qaActivity
	Transport    *qaTransport
	MajorSites   int
	HasCarBooked bool
}

type qaDrive struct {
	DurationMin int
	Source      string
	VerifiedAt  string
}

type qaActivity struct {
	Name      string
	FixedTime string
	Type      string // "major", "minor", etc.
}

type qaTransport struct {
	Mode   string
	Status string // "booked", "candidate", "absent"
}

type qaHotel struct {
	DayNum int
	Status string // "booked", "to_book", "candidate", etc.
	Price  int
}

func extractDays(tripData map[string]any) []qaDay {
	raw, ok := tripData["days"].([]any)
	if !ok {
		return nil
	}

	var days []qaDay
	for _, d := range raw {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		day := qaDay{}

		if v, ok := dm["dayNum"].(float64); ok {
			day.Num = int(v)
		}
		if v, ok := dm["date"].(string); ok {
			day.Date = v
		}
		if v, ok := dm["timezone"].(string); ok {
			day.Timezone = v
		}

		// Drive
		if drv, ok := dm["drive"].(map[string]any); ok {
			drive := &qaDrive{}
			if v, ok := drv["durationMin"].(float64); ok {
				drive.DurationMin = int(v)
			} else if v, ok := drv["duration"].(string); ok {
				if mins, err := parseDuration(v); err == nil {
					drive.DurationMin = mins
				}
			}
			if v, ok := drv["source"].(string); ok {
				drive.Source = v
			}
			if v, ok := drv["verifiedAt"].(string); ok {
				drive.VerifiedAt = v
			}
			day.Drive = drive
		}

		// Activities
		if acts, ok := dm["activities"].([]any); ok {
			for _, a := range acts {
				am, ok := a.(map[string]any)
				if !ok {
					continue
				}
				act := qaActivity{}
				if v, ok := am["name"].(string); ok {
					act.Name = v
				}
				if v, ok := am["fixedTime"].(string); ok {
					act.FixedTime = v
				}
				if v, ok := am["type"].(string); ok {
					act.Type = v
				}
				day.Activities = append(day.Activities, act)

				if act.Type == "major" {
					day.MajorSites++
				}
			}
		}

		// Transport
		if tr, ok := dm["transport"].(map[string]any); ok {
			transport := &qaTransport{}
			if v, ok := tr["mode"].(string); ok {
				transport.Mode = v
			}
			if v, ok := tr["status"].(string); ok {
				transport.Status = v
			}
			day.Transport = transport
		}

		// Car booking status
		if v, ok := dm["carBooked"].(bool); ok {
			day.HasCarBooked = v
		}

		days = append(days, day)
	}
	return days
}

func extractHotels(tripData map[string]any) []qaHotel {
	raw, ok := tripData["hotels"].([]any)
	if !ok {
		return nil
	}

	var hotels []qaHotel
	for _, h := range raw {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		hotel := qaHotel{}
		if v, ok := hm["dayNum"].(float64); ok {
			hotel.DayNum = int(v)
		}
		if v, ok := hm["status"].(string); ok {
			hotel.Status = v
		}
		if v, ok := hm["price"].(float64); ok {
			hotel.Price = int(v)
		}
		hotels = append(hotels, hotel)
	}
	return hotels
}

// ── Rule implementations ────────────────────────────────────────────────────

// checkCalendarMismatch verifies that startDate + dayNum matches each day's date.
func checkCalendarMismatch(tripData map[string]any, days []qaDay) *QAViolation {
	// Need a startDate to validate against
	startDateStr := ""
	if constr, ok := tripData["construction"].(map[string]any); ok {
		if dates, ok := constr["dates"].(map[string]any); ok {
			if sd, ok := dates["startDate"].(string); ok {
				startDateStr = sd
			}
		}
	}
	// Fallback: top-level startDate
	if startDateStr == "" {
		if sd, ok := tripData["startDate"].(string); ok {
			startDateStr = sd
		}
	}
	if startDateStr == "" || len(days) == 0 {
		return nil
	}

	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		return nil
	}

	for _, day := range days {
		if day.Date == "" || day.Num <= 0 {
			continue
		}
		expected := startDate.AddDate(0, 0, day.Num-1).Format("2006-01-02")
		if day.Date != expected {
			return &QAViolation{
				Code:     "calendar_mismatch",
				Severity: "red",
				Message:  fmt.Sprintf("Day %d date %s does not match expected %s (startDate=%s)", day.Num, day.Date, expected, startDateStr),
				DayNum:   day.Num,
				Detail:   fmt.Sprintf("expected=%s actual=%s", expected, day.Date),
			}
		}
	}
	return nil
}

// checkDriveTooLong checks if any day exceeds maxDrivingPerDay.
func checkDriveTooLong(days []qaDay, p qaProfile, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if day.Drive == nil || day.Drive.DurationMin <= 0 {
			continue
		}
		if day.Drive.DurationMin > p.MaxDrivingMinutes {
			out = append(out, QAViolation{
				Code:     "drive_too_long",
				Severity: "red",
				Message:  fmt.Sprintf("Day %d drive %dmin exceeds max %dmin", day.Num, day.Drive.DurationMin, p.MaxDrivingMinutes),
				DayNum:   day.Num,
				Detail:   fmt.Sprintf("actual=%dmin max=%dmin", day.Drive.DurationMin, p.MaxDrivingMinutes),
			})
		}
	}
	return out
}

// checkDriveUnverified checks for drives without source or verifiedAt.
func checkDriveUnverified(days []qaDay, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if day.Drive == nil || day.Drive.DurationMin <= 0 {
			continue
		}
		if day.Drive.Source == "" && day.Drive.VerifiedAt == "" {
			out = append(out, QAViolation{
				Code:     "drive_unverified",
				Severity: "yellow",
				Message:  fmt.Sprintf("Day %d drive distance has no source or verification date", day.Num),
				DayNum:   day.Num,
			})
		}
	}
	return out
}

// checkTimezoneUndocumented checks for timezone changes between consecutive days.
func checkTimezoneUndocumented(days []qaDay, phase int) []QAViolation {
	var out []QAViolation
	for i := 1; i < len(days); i++ {
		prev := days[i-1]
		curr := days[i]
		if prev.Timezone != "" && curr.Timezone != "" && prev.Timezone != curr.Timezone {
			out = append(out, QAViolation{
				Code:     "timezone_undocumented",
				Severity: "yellow",
				Message:  fmt.Sprintf("Timezone change between day %d (%s) and day %d (%s)", prev.Num, prev.Timezone, curr.Num, curr.Timezone),
				DayNum:   curr.Num,
				Detail:   fmt.Sprintf("from=%s to=%s", prev.Timezone, curr.Timezone),
			})
		}
	}
	return out
}

// checkTimeConstrainedConflict checks for fixed-time activities incompatible with arrival.
func checkTimeConstrainedConflict(days []qaDay, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if day.Drive == nil || day.Drive.DurationMin == 0 {
			continue
		}
		for _, act := range day.Activities {
			if act.FixedTime == "" {
				continue
			}
			// If there is a fixed-time activity and significant drive, flag it
			// A proper check would compare arrival time vs activity time;
			// for now, flag if drive > 2h and a fixed-time activity exists on same day.
			if day.Drive.DurationMin > 120 {
				out = append(out, QAViolation{
					Code:     "time_constrained_conflict",
					Severity: "yellow",
					Message:  fmt.Sprintf("Day %d has fixed-time activity %q but %dmin drive", day.Num, act.Name, day.Drive.DurationMin),
					DayNum:   day.Num,
					Detail:   fmt.Sprintf("activity=%s fixedTime=%s driveMin=%d", act.Name, act.FixedTime, day.Drive.DurationMin),
				})
			}
		}
	}
	return out
}

// checkNightWithoutHotel checks days that have no hotel with booked/to_book status.
func checkNightWithoutHotel(days []qaDay, hotels []qaHotel, phase int) []QAViolation {
	hotelMap := make(map[int]string) // dayNum -> status
	for _, h := range hotels {
		hotelMap[h.DayNum] = h.Status
	}

	var out []QAViolation
	for _, day := range days {
		status, has := hotelMap[day.Num]
		if !has || (status != "booked" && status != "to_book") {
			sev := "yellow"
			if phase >= 3 {
				sev = "red"
			}
			out = append(out, QAViolation{
				Code:     "night_without_hotel",
				Severity: sev,
				Message:  fmt.Sprintf("Day %d has no hotel with booked/to_book status", day.Num),
				DayNum:   day.Num,
			})
		}
	}
	return out
}

// checkDayGap checks for gaps in day numbering.
func checkDayGap(days []qaDay, phase int) []QAViolation {
	if len(days) == 0 {
		return nil
	}

	numSet := make(map[int]bool, len(days))
	minDay := days[0].Num
	maxDay := days[0].Num
	for _, d := range days {
		numSet[d.Num] = true
		if d.Num < minDay {
			minDay = d.Num
		}
		if d.Num > maxDay {
			maxDay = d.Num
		}
	}

	var out []QAViolation
	for i := minDay; i <= maxDay; i++ {
		if !numSet[i] {
			out = append(out, QAViolation{
				Code:     "day_gap",
				Severity: "red",
				Message:  fmt.Sprintf("Day %d is missing (gap in day numbering)", i),
				DayNum:   i,
			})
		}
	}
	return out
}

// checkTransportNotBooked checks if main transport is absent or candidate.
func checkTransportNotBooked(days []qaDay, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if day.Transport == nil || day.Transport.Status == "" || day.Transport.Status == "absent" || day.Transport.Status == "candidate" {
			sev := "yellow"
			if phase >= 4 {
				sev = "red"
			}
			// Only flag if transport section exists but is not booked,
			// or if absent entirely
			if day.Transport != nil {
				out = append(out, QAViolation{
					Code:     "transport_not_booked",
					Severity: sev,
					Message:  fmt.Sprintf("Day %d main transport not booked (status=%s)", day.Num, day.Transport.Status),
					DayNum:   day.Num,
					Detail:   fmt.Sprintf("status=%s", day.Transport.Status),
				})
			}
		}
	}
	return out
}

// checkCarNotBooked checks if a routier trip has no car booking.
func checkCarNotBooked(days []qaDay, p qaProfile, phase int) []QAViolation {
	if !p.IsRoutier {
		return nil
	}

	var out []QAViolation
	for _, day := range days {
		if !day.HasCarBooked {
			sev := "yellow"
			if phase >= 4 {
				sev = "red"
			}
			out = append(out, QAViolation{
				Code:     "car_not_booked",
				Severity: sev,
				Message:  fmt.Sprintf("Day %d: routier trip but car not booked", day.Num),
				DayNum:   day.Num,
			})
		}
	}
	return out
}

// checkTooManyMajorSites checks if any day exceeds the majorSitesPerDay limit.
func checkTooManyMajorSites(days []qaDay, p qaProfile, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if day.MajorSites > p.MajorSitesPerDay {
			out = append(out, QAViolation{
				Code:     "too_many_major_sites",
				Severity: "red",
				Message:  fmt.Sprintf("Day %d has %d major sites (max %d)", day.Num, day.MajorSites, p.MajorSitesPerDay),
				DayNum:   day.Num,
				Detail:   fmt.Sprintf("count=%d max=%d", day.MajorSites, p.MajorSitesPerDay),
			})
		}
	}
	return out
}

// checkBudgetExceeded checks if hotels or restaurants exceed budget limits.
func checkBudgetExceeded(days []qaDay, hotels []qaHotel, p qaProfile, phase int) []QAViolation {
	var out []QAViolation
	if p.BudgetAccomMax > 0 {
		for _, h := range hotels {
			if h.Price > p.BudgetAccomMax {
				out = append(out, QAViolation{
					Code:     "budget_exceeded",
					Severity: "yellow",
					Message:  fmt.Sprintf("Day %d hotel price %d exceeds budget max %d", h.DayNum, h.Price, p.BudgetAccomMax),
					DayNum:   h.DayNum,
					Detail:   fmt.Sprintf("type=accommodation price=%d max=%d", h.Price, p.BudgetAccomMax),
				})
			}
		}
	}
	return out
}

// ── QA result serialization helper ──────────────────────────────────────────

// QAResultJSON serializes violations to JSON string for storage.
func QAResultJSON(violations []QAViolation) string {
	b, err := json.Marshal(violations)
	if err != nil {
		return "[]"
	}
	return string(b)
}
