package dailybrief

import (
	"testing"
	"time"
)

func TestInSendWindow(t *testing.T) {
	loc := time.FixedZone("test", 0)
	at := func(h, m int) time.Time {
		return time.Date(2026, 8, 12, h, m, 30, 0, loc)
	}
	const win = 15
	cases := []struct {
		name    string
		nowH    int
		nowM    int
		wantH   int
		wantM   int
		in      bool
	}{
		{"exact", 8, 45, 8, 45, true},
		{"plus1", 8, 46, 8, 45, true},
		{"plus14", 8, 59, 8, 45, true},
		{"plus15_exclusive", 9, 0, 8, 45, false},
		{"before", 8, 44, 8, 45, false},
		{"hour_earlier", 7, 45, 8, 45, false},
		{"window1_exact_only", 8, 0, 8, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := win
			if tc.name == "window1_exact_only" {
				w = 1
				got := inSendWindow(at(tc.nowH, tc.nowM), tc.wantH, tc.wantM, w)
				if got != tc.in {
					t.Fatalf("got %v want %v", got, tc.in)
				}
				if inSendWindow(at(8, 1), 8, 0, 1) {
					t.Fatal("minute+1 should be outside 1-min window")
				}
				return
			}
			got := inSendWindow(at(tc.nowH, tc.nowM), tc.wantH, tc.wantM, w)
			if got != tc.in {
				t.Fatalf("got %v want %v", got, tc.in)
			}
		})
	}
}

func TestInSendWindow_ClipsMidnight(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 12, 23, 58, 0, 0, loc)
	if !inSendWindow(now, 23, 50, 15) {
		t.Fatal("23:58 should be in [23:50, 24:00)")
	}
	nextDay := time.Date(2026, 8, 13, 0, 2, 0, 0, loc)
	if inSendWindow(nextDay, 23, 50, 15) {
		t.Fatal("next-day 00:02 must not wrap into previous evening window")
	}
}
