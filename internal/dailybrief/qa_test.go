package dailybrief

import (
	"testing"
	"time"
)

func TestRunQA_Passed(t *testing.T) {
	src := &DayBriefData{
		Date:    "2026-04-15",
		Weekday: "mercredi",
		Hotel:   map[string]any{"name": "SpringHill"},
		Timeline: []map[string]any{
			{"time": "08:00", "label": "Depart"},
		},
	}
	text := `📅 *mercredi 15 avril*
🏨 SpringHill check-in
• 08:00 - Depart
⭐ *À savoir*
• Ville test au bord de l'eau
📰 *Actualité*
• Expo locale ce week-end
💡 *Astuce pratique*
Cash utile pour le parking`
	qa := RunQA(text, src)
	if qa.Verdict != QAPassed && qa.Verdict != QAWarning {
		t.Fatalf("verdict=%s summary=%s %#v", qa.Verdict, qa.Summary, qa)
	}
}

func TestRunQA_FailedPlaceholder(t *testing.T) {
	src := &DayBriefData{Date: "2026-04-15", Weekday: "mercredi"}
	qa := RunQA("TODO fix this mercredi 2026-04-15", src)
	if qa.Verdict != QAFailed {
		t.Fatalf("expected FAILED, got %s", qa.Verdict)
	}
}

func TestRunQA_FrenchHotelAndDate(t *testing.T) {
	src := &DayBriefData{
		Date:    "2026-08-16",
		Weekday: "dimanche",
		Hotel:   map[string]any{"name": "Les Lofts ilewa — Chute-Montmorency"},
		Timeline: []map[string]any{
			{"time": "09:00", "label": "Petit-Champlain"},
			{"time": "14:30", "label": "Chutes"},
		},
		Weather: map[string]any{"summary": "pluie"},
	}
	text := `_Bon dimanche les amis !_ 🌧️
Aujourd'hui 16 août.
*09h00* Petit-Champlain
*14h30* Chutes
🔑 Votre logement : Les Lofts ilewa — Chute-Montmorency
⭐ *À savoir*
• Capitale du Québec
📰 *Actualité*
• Festival d'été en ville
💡 *Astuce pratique*
Parapluie dans le sac`
	qa := RunQA(text, src)
	if qa.Verdict == QAFailed {
		t.Fatalf("expected pass/warn, got %s %#v", qa.Verdict, qa)
	}
}

func TestTimelineTimePresent(t *testing.T) {
	cases := []struct {
		text, t string
		want    bool
	}{
		{"*09:00* go", "09:00", true},
		{"*09h00* go", "09:00", true},
		{"*9h00* go", "09:00", true},
		{"*14h30* go", "14:30", true},
		{"🚶 *9h* — go", "09:00", true},
		{"*11h* — quartier", "11:00", true},
		{"no times", "09:00", false},
	}
	for _, c := range cases {
		if got := timelineTimePresent(c.text, c.t); got != c.want {
			t.Fatalf("%q in %q: got %v want %v", c.t, c.text, got, c.want)
		}
	}
}

func TestParseConfigJSON(t *testing.T) {
	raw := []byte(`{
	  "gowaBaseUrl": "http://gowa.gowa.svc.cluster.local:3000",
	  "bifrostBaseUrl": "http://bifrost.openclaw.svc.cluster.local:8080/v1",
	  "briefModel": "opencode-go/deepseek-v4-pro",
	  "adminPhone": "15555550100"
	}`)
	c, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.BriefModel != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("model=%s", c.BriefModel)
	}
}

func TestParseCronHM(t *testing.T) {
	m, h := parseCronHM("30 6 * * *")
	if m != 30 || h != 6 {
		t.Fatalf("got %d %d", m, h)
	}
}

func TestSendHourMinute(t *testing.T) {
	c := Config{SendLocalHour: 8, SendLocalMinute: 0}
	h, m := c.SendHourMinute()
	if h != 8 || m != 0 {
		t.Fatalf("got %d:%d", h, m)
	}
	c2 := Config{Cron: "30 6 * * *"}
	h, m = c2.SendHourMinute()
	if h != 6 || m != 30 {
		t.Fatalf("cron fallback got %d:%d", h, m)
	}
}

func TestCandidateDayNumbers(t *testing.T) {
	start, _ := time.Parse("2006-01-02", "2026-08-14")
	end, _ := time.Parse("2006-01-02", "2026-09-01")
	now, _ := time.Parse(time.RFC3339, "2026-08-16T12:00:00Z")
	days := candidateDayNumbers(start, end, now)
	if len(days) == 0 {
		t.Fatal("expected candidates")
	}
	found := false
	for _, d := range days {
		if d == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected day 3 in %v", days)
	}
}

func TestHumanDateFR(t *testing.T) {
	if got := humanDateFR("2026-04-15"); got != "15 avril" {
		t.Fatalf("got %q", got)
	}
}

func TestTextContainsLoose(t *testing.T) {
	name := "Les Lofts ilewa — Chute-Montmorency"
	text := "🏨 *Les Lofts ilewa — Chute‑Montmorency*" // nb hyphen in Chute‑
	if !textContainsLoose(text, name) {
		t.Fatal("expected loose match on unicode dashes")
	}
}
