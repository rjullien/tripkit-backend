package construction

import (
	"testing"
)

// ── Helper to build synthetic trip data ─────────────────────────────────────

func makeProfile(maxDriving string, majorSites int, accomMax int) map[string]any {
	p := map[string]any{
		"travelStyle": map[string]any{
			"maxDrivingPerDay": maxDriving,
			"majorSitesPerDay": float64(majorSites),
		},
	}
	if accomMax > 0 {
		p["budgetRules"] = map[string]any{
			"accommodation": map[string]any{
				"maxPerNight": float64(accomMax),
			},
		}
	}
	return p
}

func makeTripData(days []map[string]any, hotels []map[string]any, startDate string, transportModes []string) map[string]any {
	td := map[string]any{}
	if days != nil {
		daysAny := make([]any, len(days))
		for i, d := range days {
			daysAny[i] = d
		}
		td["days"] = daysAny
	}
	if hotels != nil {
		hotelsAny := make([]any, len(hotels))
		for i, h := range hotels {
			hotelsAny[i] = h
		}
		td["hotels"] = hotelsAny
	}
	constr := map[string]any{}
	if startDate != "" {
		constr["dates"] = map[string]any{"startDate": startDate}
	}
	if transportModes != nil {
		modes := make([]any, len(transportModes))
		for i, m := range transportModes {
			modes[i] = m
		}
		constr["transportModes"] = modes
	}
	if len(constr) > 0 {
		td["construction"] = constr
	}
	return td
}

// ── Test: calendar_mismatch detected and short-circuits ─────────────────────

func TestRunQA_CalendarMismatch_ShortCircuits(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
		{"dayNum": float64(2), "date": "2026-06-17"}, // mismatch: should be 06-16
		{"dayNum": float64(3), "date": "2026-06-17"},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("4h", 1, 0)

	violations := RunQA(tripData, profile, 1)

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (short-circuit), got %d: %+v", len(violations), violations)
	}
	if violations[0].Code != "calendar_mismatch" {
		t.Errorf("expected code=calendar_mismatch, got %q", violations[0].Code)
	}
	if violations[0].Severity != "red" {
		t.Errorf("expected severity=red, got %q", violations[0].Severity)
	}
	if violations[0].DayNum != 2 {
		t.Errorf("expected DayNum=2, got %d", violations[0].DayNum)
	}
}

// ── Test: drive_too_long with 5h drive and maxDrivingPerDay=4h ──────────────

func TestRunQA_DriveTooLong(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15", "drive": map[string]any{"durationMin": float64(300), "source": "google"}},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("4h", 2, 0) // 4h = 240min, drive = 300min

	violations := RunQA(tripData, profile, 2)

	found := false
	for _, v := range violations {
		if v.Code == "drive_too_long" {
			found = true
			if v.DayNum != 1 {
				t.Errorf("expected DayNum=1, got %d", v.DayNum)
			}
			if v.Severity != "red" {
				t.Errorf("expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected drive_too_long violation, got %+v", violations)
	}
}

// ── Test: night_without_hotel with no hotel ─────────────────────────────────

func TestRunQA_NightWithoutHotel(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
		{"dayNum": float64(2), "date": "2026-06-16"},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "status": "booked"},
		// Day 2 has no hotel
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 3)

	found := false
	for _, v := range violations {
		if v.Code == "night_without_hotel" && v.DayNum == 2 {
			found = true
			// Phase 3: should be red
			if v.Severity != "red" {
				t.Errorf("at phase 3, expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected night_without_hotel for day 2, got %+v", violations)
	}
}

// ── Test: night_without_hotel is yellow in phase 2 ──────────────────────────

func TestRunQA_NightWithoutHotel_YellowInPhase2(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 2)

	found := false
	for _, v := range violations {
		if v.Code == "night_without_hotel" {
			found = true
			if v.Severity != "yellow" {
				t.Errorf("at phase 2, expected severity=yellow, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected night_without_hotel violation, got %+v", violations)
	}
}

// ── Test: day_gap with missing day number ───────────────────────────────────

func TestRunQA_DayGap(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
		{"dayNum": float64(3), "date": "2026-06-17"}, // day 2 is missing
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 1)

	found := false
	for _, v := range violations {
		if v.Code == "day_gap" && v.DayNum == 2 {
			found = true
			if v.Severity != "red" {
				t.Errorf("expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected day_gap for day 2, got %+v", violations)
	}
}

// ── Test: too_many_major_sites with 3 sites and limit 1 ─────────────────────

func TestRunQA_TooManyMajorSites(t *testing.T) {
	days := []map[string]any{
		{
			"dayNum": float64(1),
			"date":   "2026-06-15",
			"activities": []any{
				map[string]any{"name": "Museum", "type": "major"},
				map[string]any{"name": "Castle", "type": "major"},
				map[string]any{"name": "Cathedral", "type": "major"},
			},
		},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("6h", 1, 0) // majorSitesPerDay = 1

	violations := RunQA(tripData, profile, 2)

	found := false
	for _, v := range violations {
		if v.Code == "too_many_major_sites" && v.DayNum == 1 {
			found = true
			if v.Severity != "red" {
				t.Errorf("expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected too_many_major_sites violation, got %+v", violations)
	}
}

// ── Test: transport_not_booked severity changes between Ph1 and Ph4 ─────────

func TestRunQA_TransportNotBooked_SeverityByPhase(t *testing.T) {
	days := []map[string]any{
		{
			"dayNum":    float64(1),
			"date":      "2026-06-15",
			"transport": map[string]any{"mode": "train", "status": "candidate"},
		},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	// Phase 1: should be yellow
	violationsPh1 := RunQA(tripData, profile, 1)
	var sevPh1 string
	for _, v := range violationsPh1 {
		if v.Code == "transport_not_booked" {
			sevPh1 = v.Severity
			break
		}
	}
	if sevPh1 != "yellow" {
		t.Errorf("Phase 1: expected severity=yellow, got %q", sevPh1)
	}

	// Phase 4: should be red
	violationsPh4 := RunQA(tripData, profile, 4)
	var sevPh4 string
	for _, v := range violationsPh4 {
		if v.Code == "transport_not_booked" {
			sevPh4 = v.Severity
			break
		}
	}
	if sevPh4 != "red" {
		t.Errorf("Phase 4: expected severity=red, got %q", sevPh4)
	}
}

// ── Test: CanTransition refuses when red blockers exist ─────────────────────

func TestCanTransition_RefusesWithBlockers(t *testing.T) {
	violations := []QAViolation{
		{Code: "drive_too_long", Severity: "red", Message: "too long", DayNum: 1},
		{Code: "drive_unverified", Severity: "yellow", Message: "unverified", DayNum: 2},
	}

	allowed, blockers := CanTransition(violations, 3, false)
	if allowed {
		t.Error("expected transition to be refused")
	}
	if len(blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blockers))
	}
	if blockers[0].Code != "drive_too_long" {
		t.Errorf("expected blocker code=drive_too_long, got %q", blockers[0].Code)
	}
}

// ── Test: CanTransition allows with force=true ──────────────────────────────

func TestCanTransition_AllowsWithForce(t *testing.T) {
	violations := []QAViolation{
		{Code: "drive_too_long", Severity: "red", Message: "too long", DayNum: 1},
		{Code: "day_gap", Severity: "red", Message: "gap", DayNum: 3},
	}

	allowed, blockers := CanTransition(violations, 3, true)
	if !allowed {
		t.Error("expected transition to be allowed with force=true")
	}
	if blockers != nil {
		t.Errorf("expected nil blockers with force, got %+v", blockers)
	}
}

// ── Test: CanTransition allows when no red violations ───────────────────────

func TestCanTransition_AllowsWhenNoRed(t *testing.T) {
	violations := []QAViolation{
		{Code: "drive_unverified", Severity: "yellow", Message: "unverified", DayNum: 1},
		{Code: "night_without_hotel", Severity: "yellow", Message: "no hotel", DayNum: 2},
	}

	allowed, blockers := CanTransition(violations, 2, false)
	if !allowed {
		t.Error("expected transition to be allowed with only yellow violations")
	}
	if len(blockers) != 0 {
		t.Errorf("expected no blockers, got %+v", blockers)
	}
}

// ── Test: drive_unverified ──────────────────────────────────────────────────

func TestRunQA_DriveUnverified(t *testing.T) {
	days := []map[string]any{
		{
			"dayNum": float64(1),
			"date":   "2026-06-15",
			"drive":  map[string]any{"durationMin": float64(120)},
			// no source, no verifiedAt
		},
	}
	tripData := makeTripData(days, nil, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 2)

	found := false
	for _, v := range violations {
		if v.Code == "drive_unverified" {
			found = true
			if v.Severity != "yellow" {
				t.Errorf("expected severity=yellow, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected drive_unverified violation, got %+v", violations)
	}
}

// ── Test: car_not_booked for routier trip ───────────────────────────────────

func TestRunQA_CarNotBooked_Routier(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15", "carBooked": false},
	}
	tripData := makeTripData(days, nil, "2026-06-15", []string{"voiture"})
	profile := makeProfile("6h", 2, 0)

	// Phase 1: yellow
	violationsPh1 := RunQA(tripData, profile, 1)
	var found1 bool
	for _, v := range violationsPh1 {
		if v.Code == "car_not_booked" {
			found1 = true
			if v.Severity != "yellow" {
				t.Errorf("Phase 1: expected severity=yellow, got %q", v.Severity)
			}
		}
	}
	if !found1 {
		t.Errorf("Phase 1: expected car_not_booked, got %+v", violationsPh1)
	}

	// Phase 4: red
	violationsPh4 := RunQA(tripData, profile, 4)
	var found4 bool
	for _, v := range violationsPh4 {
		if v.Code == "car_not_booked" {
			found4 = true
			if v.Severity != "red" {
				t.Errorf("Phase 4: expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found4 {
		t.Errorf("Phase 4: expected car_not_booked, got %+v", violationsPh4)
	}
}

// ── Test: budget_exceeded ───────────────────────────────────────────────────

func TestRunQA_BudgetExceeded(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "status": "booked", "price": float64(250)},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 200) // accomMax = 200, hotel = 250

	violations := RunQA(tripData, profile, 2)

	found := false
	for _, v := range violations {
		if v.Code == "budget_exceeded" && v.DayNum == 1 {
			found = true
			if v.Severity != "yellow" {
				t.Errorf("expected severity=yellow, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected budget_exceeded violation, got %+v", violations)
	}
}

// ── Test: parseDuration helper ──────────────────────────────────────────────

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"4h", 240, false},
		{"4h30m", 270, false},
		{"30m", 30, false},
		{"4h30", 270, false},
		{"270", 270, false},
		{"", 0, true},
		{"abc", 0, true},
	}

	for _, tc := range tests {
		got, err := parseDuration(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q): expected error, got %d", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDuration(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// ── Test: no violations for clean trip ──────────────────────────────────────

func TestRunQA_CleanTrip_NoViolations(t *testing.T) {
	// A clean trip needs a booked transport block on every day: a missing block
	// counts as unbooked (see TestRunQA_TransportNotBooked_MissingBlock).
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15", "drive": map[string]any{"durationMin": float64(120), "source": "google", "verifiedAt": "2026-01-01"}, "transport": map[string]any{"mode": "train", "status": "booked"}},
		{"dayNum": float64(2), "date": "2026-06-16", "transport": map[string]any{"mode": "train", "status": "booked"}},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "status": "booked"},
		{"dayNum": float64(2), "status": "to_book"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("4h", 2, 0)

	violations := RunQA(tripData, profile, 1)

	if len(violations) != 0 {
		t.Errorf("expected 0 violations for clean trip, got %d: %+v", len(violations), violations)
	}
}

// ── Test: default profile used when nil ─────────────────────────────────────

func TestRunQA_NilProfile_UsesDefaults(t *testing.T) {
	// Drive of 400min should exceed default 360min (6h)
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15", "drive": map[string]any{"durationMin": float64(400), "source": "maps"}},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "status": "booked"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)

	violations := RunQA(tripData, nil, 1)

	found := false
	for _, v := range violations {
		if v.Code == "drive_too_long" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected drive_too_long with nil profile (default 6h), got %+v", violations)
	}
}

// ── Test: hotel with bookingRef but no bookingStatus defaults to booked ─────

func TestRunQA_HotelBookingRefRetrocompat(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
		{"dayNum": float64(2), "date": "2026-06-16"},
	}
	hotels := []map[string]any{
		// Has bookingRef but no status/bookingStatus -> should default to "booked"
		{"dayNum": float64(1), "bookingRef": "ABC123"},
		// Has explicit bookingStatus
		{"dayNum": float64(2), "bookingStatus": "to_book"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 3)

	// Neither day should trigger night_without_hotel
	for _, v := range violations {
		if v.Code == "night_without_hotel" {
			t.Errorf("unexpected night_without_hotel for day %d (retrocompat should apply): %+v", v.DayNum, v)
		}
	}
}

// ── Test: hotel with bookingStatus "candidate" triggers night_without_hotel ─

func TestRunQA_HotelCandidate_TriggersViolation(t *testing.T) {
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "bookingStatus": "candidate"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	// Phase 3: candidate hotel should trigger night_without_hotel as red
	violations := RunQA(tripData, profile, 3)

	found := false
	for _, v := range violations {
		if v.Code == "night_without_hotel" && v.DayNum == 1 {
			found = true
			if v.Severity != "red" {
				t.Errorf("expected severity=red at phase 3, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected night_without_hotel for candidate hotel, got %+v", violations)
	}
}

// ── Test: transport_not_booked fires only when transport section present ────

func TestRunQA_TransportNotBooked_FiresInPh4(t *testing.T) {
	days := []map[string]any{
		{
			"dayNum":    float64(1),
			"date":      "2026-06-15",
			"transport": map[string]any{"mode": "flight", "status": "candidate"},
		},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "bookingStatus": "booked"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	// Phase 4: should be red
	violations := RunQA(tripData, profile, 4)
	found := false
	for _, v := range violations {
		if v.Code == "transport_not_booked" && v.DayNum == 1 {
			found = true
			if v.Severity != "red" {
				t.Errorf("Phase 4: expected severity=red, got %q", v.Severity)
			}
		}
	}
	if !found {
		t.Errorf("Phase 4: expected transport_not_booked for candidate flight, got %+v", violations)
	}
}

// ── Test: transport_not_booked also fires when the block is absent ──────────

func TestRunQA_TransportNotBooked_MissingBlock(t *testing.T) {
	// A day with no transport block at all is the most common shape of an
	// unbooked day during early construction: it must be flagged too.
	days := []map[string]any{
		{"dayNum": float64(1), "date": "2026-06-15"},
	}
	hotels := []map[string]any{
		{"dayNum": float64(1), "bookingStatus": "booked"},
	}
	tripData := makeTripData(days, hotels, "2026-06-15", nil)
	profile := makeProfile("6h", 2, 0)

	violations := RunQA(tripData, profile, 2)
	var got *QAViolation
	for i := range violations {
		if violations[i].Code == "transport_not_booked" && violations[i].DayNum == 1 {
			got = &violations[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected transport_not_booked for a day without transport block, got %+v", violations)
	}
	if got.Severity != "yellow" {
		t.Errorf("phase 2: expected severity=yellow, got %q", got.Severity)
	}
	if got.Detail != "status=absent" {
		t.Errorf("expected detail=status=absent, got %q", got.Detail)
	}

	// Phase 4 escalates the same violation to red.
	for _, v := range RunQA(tripData, profile, 4) {
		if v.Code == "transport_not_booked" && v.Severity != "red" {
			t.Errorf("phase 4: expected severity=red, got %q", v.Severity)
		}
	}
}

func TestParseStoredQA_ArrayAndObject(t *testing.T) {
	vs := []QAViolation{{Code: "day_gap", Severity: "red", Message: "gap", DayNum: 2}}
	wrapped := QAResultJSON(vs, 3)
	got, phase, ok := ParseStoredQA(wrapped)
	if !ok || phase != 3 || len(got) != 1 || got[0].Code != "day_gap" {
		t.Fatalf("wrapped: got=%+v phase=%d ok=%v json=%s", got, phase, ok, wrapped)
	}

	got, phase, ok = ParseStoredQA(`[{"code":"day_gap","severity":"red","dayNum":2}]`)
	if ok || phase != 0 || len(got) != 1 || got[0].Code != "day_gap" {
		t.Fatalf("legacy array: got=%+v phase=%d ok=%v", got, phase, ok)
	}

	empty, phase, ok := ParseStoredQA("[]")
	if ok || phase != 0 || empty == nil || len(empty) != 0 {
		t.Fatalf("empty array: %+v phase=%d ok=%v", empty, phase, ok)
	}

	got, phase, ok = ParseStoredQA(`{"violations":[],"phase":1}`)
	if !ok || phase != 1 || got == nil || len(got) != 0 {
		t.Fatalf("empty object: %+v phase=%d ok=%v", got, phase, ok)
	}
}
