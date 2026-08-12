package pluschat

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfigJSON(t *testing.T) {
	raw := []byte(`{"bifrostBaseUrl":"http://bifrost/v1","chatModel":"opencode-go/x"}`)
	c, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled {
		t.Fatal("enabled should default true when omitted")
	}
	if c.ChatModel != "opencode-go/x" {
		t.Fatalf("model=%q", c.ChatModel)
	}

	raw2 := []byte(`{"enabled":false,"bifrostBaseUrl":"http://bifrost/v1","chatModel":"m"}`)
	c2, err := parseConfigJSON(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Enabled || c2.Ready() {
		t.Fatal("expected disabled")
	}
}

func TestPrepareMessages(t *testing.T) {
	_, err := prepareMessages(PromptContext{Username: "rene"}, ChatRequest{})
	if err == nil {
		t.Fatal("expected empty messages error")
	}
	msgs, err := prepareMessages(PromptContext{
		Username: "rene",
		TripID:   "quebec-2026",
		Trip: &TripContext{
			TripID:    "quebec-2026",
			TripName:  "Boucle Québec",
			TodayDate: "2026-08-12",
			Today: &DayFocus{
				Role:      "today",
				DayNumber: -1,
				Date:      "2026-08-12",
				Bookings: map[string]any{
					"hotel": map[string]any{"name": "Test", "pin": "4360", "addr": "20 Côte"},
				},
			},
		},
	}, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "code pin ?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "system" {
		t.Fatalf("got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "quebec-2026") {
		t.Fatal("system prompt should mention trip")
	}
	if !strings.Contains(msgs[0].Content, "4360") {
		t.Fatal("system prompt should include hotel pin from context")
	}
	if !strings.Contains(msgs[0].Content, "CONTEXTE_JSON") {
		t.Fatal("expected CONTEXTE_JSON block")
	}
}

func TestDayNumberForDate(t *testing.T) {
	start, _ := time.Parse("2006-01-02", "2026-08-14")
	if got := dayNumberForDate(start, "2026-08-14"); got != 1 {
		t.Fatalf("day1 got %d", got)
	}
	if got := dayNumberForDate(start, "2026-08-12"); got != -1 {
		t.Fatalf("J0-1 got %d", got)
	}
	if got := dayNumberForDate(start, "2026-08-13"); got != 0 {
		t.Fatalf("J0 got %d", got)
	}
}
