package weather

import "testing"

func days(dates ...string) []ForecastDay {
	out := make([]ForecastDay, len(dates))
	for i, d := range dates {
		out[i] = ForecastDay{Date: d, TempMax: float64(20 + i)}
	}
	return out
}

func TestSelectDate_exactMatch(t *testing.T) {
	got := SelectDate(days("2026-08-17", "2026-08-18", "2026-08-19"), "2026-08-18")
	if len(got) != 1 || got[0].Date != "2026-08-18" {
		t.Fatalf("got %#v", got)
	}
}

func TestSelectDate_pastKeepsCurrentDay(t *testing.T) {
	// Requested date is "yesterday" relative to the forecast window.
	got := SelectDate(days("2026-08-17", "2026-08-18"), "2026-08-16")
	if len(got) != 1 || got[0].Date != "2026-08-17" {
		t.Fatalf("past date must keep today, got %#v", got)
	}
}

func TestSelectDate_todayEqualsFirstDay(t *testing.T) {
	got := SelectDate(days("2026-08-17", "2026-08-18"), "2026-08-17")
	if len(got) != 1 || got[0].Date != "2026-08-17" {
		t.Fatalf("today must not be treated as past, got %#v", got)
	}
}

func TestSelectDate_futureBeyondWindow(t *testing.T) {
	got := SelectDate(days("2026-08-17", "2026-08-18"), "2026-09-01")
	if got != nil {
		t.Fatalf("date past the window must be empty, got %#v", got)
	}
}

func TestSelectDate_emptyDateReturnsAll(t *testing.T) {
	in := days("2026-08-17", "2026-08-18")
	got := SelectDate(in, "")
	if len(got) != 2 {
		t.Fatalf("no date filter should keep the window, got %#v", got)
	}
}

func TestSelectDate_emptyWindow(t *testing.T) {
	if got := SelectDate(nil, "2026-08-17"); got != nil {
		t.Fatalf("got %#v", got)
	}
}
