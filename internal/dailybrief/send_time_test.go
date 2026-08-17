package dailybrief

import (
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
)

func TestParseBriefSendTime(t *testing.T) {
	cases := []struct {
		in   any
		h, m int
		ok   bool
	}{
		{"07:00", 7, 0, true},
		{"07:30", 7, 30, true},
		{"7:30", 7, 30, true},
		{"08:00", 8, 0, true},
		{"23:59", 23, 59, true},
		{"", 0, 0, false},
		{nil, 0, 0, false},
		{"25:00", 0, 0, false},
		{"8", 0, 0, false},
		{"07:60", 0, 0, false},
	}
	for _, tc := range cases {
		h, m, ok := ParseBriefSendTime(tc.in)
		if ok != tc.ok || h != tc.h || m != tc.m {
			t.Fatalf("%v → got %d:%d ok=%v want %d:%d ok=%v", tc.in, h, m, ok, tc.h, tc.m, tc.ok)
		}
	}
}

func TestTripBriefSendTime_FromTripData(t *testing.T) {
	raw := `{"dailyBrief":true,"whatsappGroup":"g@g.us","briefSendTime":"07:30"}`
	h, m, ok := TripBriefSendTime(models.Trip{Data: &raw})
	if !ok || h != 7 || m != 30 {
		t.Fatalf("got %d:%d ok=%v", h, m, ok)
	}
	raw2 := `{"dailyBrief":true,"whatsappGroup":"g@g.us"}`
	if _, _, ok := TripBriefSendTime(models.Trip{Data: &raw2}); ok {
		t.Fatal("expected no override when briefSendTime absent")
	}
	raw3 := `{"briefSendTime":"bad"}`
	if _, _, ok := TripBriefSendTime(models.Trip{Data: &raw3}); ok {
		t.Fatal("expected invalid briefSendTime to fall back")
	}
}

func TestResolveSendTime_SeedOverridesGlobal(t *testing.T) {
	globalH, globalM := 8, 0
	raw := `{"briefSendTime":"07:30"}`
	h, m, ok := TripBriefSendTime(models.Trip{Data: &raw})
	if !ok {
		t.Fatal("expected seed override")
	}
	wantH, wantM := globalH, globalM
	if ok {
		wantH, wantM = h, m
	}
	if wantH != 7 || wantM != 30 {
		t.Fatalf("want 7:30 got %d:%d", wantH, wantM)
	}
}
