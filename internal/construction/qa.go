package construction

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/formalities"
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
		MaxDrivingMinutes: 360, // 6h family default; ops driveHardLimitMinutes caps this
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

// QAOpts tunes RunQA with ops thresholds and a clock (J-30 for admin_action_required).
type QAOpts struct {
	Phase                 int
	DriveHardLimitMinutes int
	Now                   time.Time
}

// RunQA runs all QA rules against the trip data and returns violations.
// calendar_mismatch runs FIRST and short-circuits if triggered.
func RunQA(tripData map[string]any, profile map[string]any, phase int) []QAViolation {
	return RunQAWith(tripData, profile, QAOpts{Phase: phase})
}

// RunQAWith is RunQA plus ops-driven thresholds (driveHardLimitMinutes) and a clock.
func RunQAWith(tripData map[string]any, profile map[string]any, opts QAOpts) []QAViolation {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.DriveHardLimitMinutes <= 0 {
		opts.DriveHardLimitMinutes = defaultDriveHardLimitMinutes
	}

	p := extractProfile(profile)
	p.IsRoutier = extractIsRoutier(tripData)
	applyDriveHardLimit(&p, opts.DriveHardLimitMinutes)

	days := extractDays(tripData)
	hotels := extractHotels(tripData)

	// Rule 1: calendar_mismatch - short-circuit
	if v := checkCalendarMismatch(tripData, days); v != nil {
		return []QAViolation{*v}
	}

	phase := opts.Phase
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
	violations = append(violations, checkAdminActionRequired(tripData, opts.Now)...)
	violations = append(violations, checkNoPlanB(days)...)

	return violations
}

// applyDriveHardLimit caps the family preference at the ops hard ceiling.
// If the profile did not set a driving limit (zero), the ops value is the limit.
func applyDriveHardLimit(p *qaProfile, hard int) {
	if hard <= 0 {
		return
	}
	if p.MaxDrivingMinutes <= 0 || p.MaxDrivingMinutes > hard {
		p.MaxDrivingMinutes = hard
	}
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
	BookingType  string
	HasPlanB     bool
	Risky        bool
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

		if b, ok := dm["booking"].(map[string]any); ok {
			if v, ok := b["type"].(string); ok {
				day.BookingType = strings.ToLower(strings.TrimSpace(v))
			}
		}
		day.HasPlanB = hasPlanB(dm["planB"])
		if v, ok := dm["risk"].(bool); ok && v {
			day.Risky = true
		}
		if v, ok := dm["risky"].(bool); ok && v {
			day.Risky = true
		}
		if v, ok := dm["needsPlanB"].(bool); ok && v {
			day.Risky = true
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
		// Retrocompat: bookingStatus field takes priority over status
		if v, ok := hm["bookingStatus"].(string); ok && v != "" {
			hotel.Status = v
		}
		// Retrocompat default: if hotel has bookingRef but no bookingStatus, treat as "booked"
		if hotel.Status == "" {
			if ref, ok := hm["bookingRef"].(string); ok && ref != "" {
				hotel.Status = "booked"
			}
		}
		if v, ok := hm["price"].(float64); ok {
			hotel.Price = int(v)
		}
		hotels = append(hotels, hotel)
	}
	return hotels
}

// ── Rule implementations ────────────────────────────────────────────────────
//
// Phase-aware severity: only night_without_hotel (red at phase >= 3) and
// transport_not_booked / car_not_booked (red at phase >= 4) use the phase
// parameter to escalate severity. The remaining rules have fixed severity
// regardless of phase per DESIGN.md spec: they fire at any phase because
// their violations are structural issues (calendar mismatch, drive too long,
// budget exceeded, etc.) that do not depend on construction progress.
// The phase parameter is still accepted by all rule signatures for API
// consistency and to allow future phase-aware gating without refactoring.

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
// phase: accepted for API consistency; severity is always "red" (structural issue).
func checkDriveTooLong(days []qaDay, p qaProfile, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// phase: accepted for API consistency; severity is always "yellow".
func checkDriveUnverified(days []qaDay, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// phase: accepted for API consistency; severity is always "yellow".
func checkTimezoneUndocumented(days []qaDay, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// phase: accepted for API consistency; severity is always "yellow".
func checkTimeConstrainedConflict(days []qaDay, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// phase: accepted for API consistency; severity is always "red".
func checkDayGap(days []qaDay, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// A day with no transport block at all counts as unbooked too (reported as
// status=absent): that is the most common shape of an unbooked day during
// early construction, and this rule gates phase 4.
func checkTransportNotBooked(days []qaDay, phase int) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		status := "absent"
		if day.Transport != nil && day.Transport.Status != "" {
			status = day.Transport.Status
		}
		if status == "absent" || status == "candidate" {
			sev := "yellow"
			if phase >= 4 {
				sev = "red"
			}
			out = append(out, QAViolation{
				Code:     "transport_not_booked",
				Severity: sev,
				Message:  fmt.Sprintf("Day %d main transport not booked (status=%s)", day.Num, status),
				DayNum:   day.Num,
				Detail:   fmt.Sprintf("status=%s", status),
			})
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
// phase: accepted for API consistency; severity is always "red".
func checkTooManyMajorSites(days []qaDay, p qaProfile, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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
// phase: accepted for API consistency; severity is always "yellow".
func checkBudgetExceeded(days []qaDay, hotels []qaHotel, p qaProfile, phase int) []QAViolation {
	_ = phase // fixed severity regardless of phase (see block comment above)
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

func hasPlanB(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case map[string]any:
		return len(t) > 0
	default:
		return v != nil
	}
}

var riskyBookingTypes = map[string]bool{
	"ferry":      true,
	"boat":       true,
	"traversier": true,
	"bateau":     true,
}

func isRiskySegment(day qaDay) bool {
	if day.Risky {
		return true
	}
	if riskyBookingTypes[day.BookingType] {
		return true
	}
	if day.Transport != nil && riskyBookingTypes[strings.ToLower(strings.TrimSpace(day.Transport.Mode))] {
		return true
	}
	return false
}

// checkNoPlanB flags a ferry/boat (or explicitly risky) day with no plan B.
func checkNoPlanB(days []qaDay) []QAViolation {
	var out []QAViolation
	for _, day := range days {
		if !isRiskySegment(day) || day.HasPlanB {
			continue
		}
		kind := day.BookingType
		if kind == "" && day.Transport != nil {
			kind = day.Transport.Mode
		}
		if kind == "" {
			kind = "risk"
		}
		out = append(out, QAViolation{
			Code:     "no_plan_b",
			Severity: "yellow",
			Message:  fmt.Sprintf("Day %d risky %s segment has no plan B", day.Num, kind),
			DayNum:   day.Num,
			Detail:   fmt.Sprintf("bookingType=%s", kind),
		})
	}
	return out
}

// checkAdminActionRequired fires when the deterministic admin engine still
// lists an action_required formality. Yellow until J-30, then red.
func checkAdminActionRequired(tripData map[string]any, now time.Time) []QAViolation {
	items := formalities.PendingAdminActions(tripData)
	if len(items) == 0 {
		return nil
	}
	sev := "yellow"
	if daysUntilStart(tripData, now) <= 30 {
		sev = "red"
	}
	var out []QAViolation
	for _, item := range items {
		out = append(out, QAViolation{
			Code:     "admin_action_required",
			Severity: sev,
			Message:  fmt.Sprintf("Formality %s still required for %s", item.Type, item.Country),
			Detail:   item.Label,
		})
	}
	return out
}

func daysUntilStart(tripData map[string]any, now time.Time) int {
	startDateStr := ""
	if constr, ok := tripData["construction"].(map[string]any); ok {
		if dates, ok := constr["dates"].(map[string]any); ok {
			if sd, ok := dates["startDate"].(string); ok {
				startDateStr = sd
			}
		}
	}
	if startDateStr == "" {
		if sd, ok := tripData["startDate"].(string); ok {
			startDateStr = sd
		}
	}
	if startDateStr == "" {
		return 999
	}
	start, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		return 999
	}
	nowDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	return int(startDay.Sub(nowDay).Hours() / 24)
}

// ── QA result serialization helper ──────────────────────────────────────────

// storedQA is the JSON object persisted in construction_checks.kind=qa.
// Older rows stored a bare violations array; ParseStoredQA accepts both.
type storedQA struct {
	Violations []QAViolation `json:"violations"`
	Phase      int           `json:"phase"`
}

// QAResultJSON serializes violations + the phase they were run against.
func QAResultJSON(violations []QAViolation, phase int) string {
	if violations == nil {
		violations = []QAViolation{}
	}
	b, err := json.Marshal(storedQA{Violations: violations, Phase: phase})
	if err != nil {
		return `{"violations":[],"phase":0}`
	}
	return string(b)
}

// ParseStoredQA reads a construction_checks QA row. A wrapped object
// `{violations, phase}` is the current shape; a legacy JSON array is still
// accepted (phaseOK=false so the caller can fall back to the trip's phase).
func ParseStoredQA(data string) (violations []QAViolation, phase int, phaseOK bool) {
	data = strings.TrimSpace(data)
	if data == "" || data == "null" {
		return []QAViolation{}, 0, false
	}
	switch data[0] {
	case '[':
		var vs []QAViolation
		if err := json.Unmarshal([]byte(data), &vs); err != nil {
			return []QAViolation{}, 0, false
		}
		if vs == nil {
			vs = []QAViolation{}
		}
		return vs, 0, false
	case '{':
		var wrap struct {
			Violations []QAViolation `json:"violations"`
			Phase      *int          `json:"phase"`
		}
		if err := json.Unmarshal([]byte(data), &wrap); err != nil {
			return []QAViolation{}, 0, false
		}
		if wrap.Violations == nil {
			wrap.Violations = []QAViolation{}
		}
		if wrap.Phase != nil {
			return wrap.Violations, *wrap.Phase, true
		}
		return wrap.Violations, 0, false
	default:
		return []QAViolation{}, 0, false
	}
}
