package polarsteps

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Experiment only — not wired into Generate.
// Hypothesis: the system prompt says « ce jour seulement » but alreadyPosted[]
// ships up to 12 full prior steps (700 runes each). Replacing older days with
// one yesterdaySummary + today's texts should shrink the user JSON and maybe
// Bifrost latency.
//
// yesterdaySummary is a deterministic clip (no extra LLM hop). A second
// Bifrost call to "summarize yesterday" would add latency, not remove it.

const slimSystemPrompt = `Tu rédiges un journal Polarsteps (pas un briefing voyage).

Règles :
- Français, 1re personne (on / nous), 2 à 4 paragraphes courts.
- Emojis rares (🍁 🇨🇦 ✈️ OK). Texte brut, pas de markdown, pas de titre.
- N'invente AUCUN lieu, activité, rencontre hors du JSON. userNote prime.
- Aujourd'hui seulement. yesterdaySummary = ce qui a déjà été raconté hier : ne le reliste pas, ne le paraphrase pas.
- alreadyPosted[] = steps déjà générés AUJOURD'HUI. INTERDIT de répéter un fait déjà dans alreadyPosted ou yesterdaySummary.
- INTERDIT : PNR, numéro de vol, horaires exacts, prix, valise, checklist, wifi, codes.
- happened[] = ce qui s'est passé aujourd'hui et n'a pas encore été raconté.
- Réponds UNIQUEMENT avec le texte Polarsteps.`

const maxYesterdaySummaryRunes = 280

// liveJ2PriorSizes matches quebec-2026 polarsteps_steps on 2026-08-15
// (char_length only — no live journal text in the repo).
var liveJ2PriorSizes = []struct {
	day, seq, n int
}{
	{1, 1, 807},
	{1, 2, 572},
	{2, 3, 424},
	{2, 4, 911},
	{2, 5, 591},
	{2, 6, 223},
}

func filler(n int, seed string) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(n)
	for b.Len() < n {
		b.WriteString(seed)
	}
	s := b.String()
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func quebecJ2Input() *Input {
	priors := make([]PriorStep, 0, len(liveJ2PriorSizes))
	for _, p := range liveJ2PriorSizes {
		priors = append(priors, PriorStep{
			Day: p.day,
			Seq: p.seq,
			Text: clipRunes(filler(p.n, fmt.Sprintf(
				"Step %d du jour %d, anecdote distincte sans Vieux-Quebec. ", p.seq, p.day,
			)), maxPriorRunes),
		})
	}
	return &Input{
		Kind:            "daily",
		NowLocal:        "2026-08-15T17:10:00-04:00",
		WindowFromLocal: "2026-08-15T04:00:00-04:00",
		Day:             2,
		Label:           "Québec — Vieux et Château",
		From:            "Québec",
		To:              "Québec",
		Travelers:       []string{"René", "Nicole", "Baptiste"},
		TripName:        "Boucle Québec 2026",
		Nights:          18,
		Phases: []string{
			"Québec & Charlevoix",
			"Fjord du Saguenay",
			"Tadoussac et les baleines",
			"Gaspésie",
			"Bas-Saint-Laurent",
		},
		Happened: []Happened{
			{T: "09:30", D: "Promenade dans le Vieux-Québec"},
			{T: "12:00", D: "Lunch près du Château Frontenac"},
			{T: "15:00", D: "Terrasse Dufferin"},
		},
		AlreadyPosted: priors,
	}
}

// quebecJ10Input is the same day-2 happened, but 12 priors (maxPriors) to
// show how the current prompt grows mid-trip.
func quebecJ10Input() *Input {
	in := quebecJ2Input()
	in.Day = 10
	in.Label = "Percé"
	in.From, in.To = "Percé", "Percé"
	in.NowLocal = "2026-08-23T17:10:00-04:00"
	// 12 priors (cap): 8 older days + 2 yesterday (J9) + 2 today (J10).
	priors := make([]PriorStep, 0, maxPriors)
	for i := 0; i < maxPriors; i++ {
		day := i + 1
		if i >= 8 {
			day = 9
		}
		if i >= 10 {
			day = 10
		}
		priors = append(priors, PriorStep{
			Day:  day,
			Seq:  i + 1,
			Text: clipRunes(filler(640, "Journée sur la côte, route et village. "), maxPriorRunes),
		})
	}
	in.AlreadyPosted = priors
	return in
}

type slimPayload struct {
	Kind               string      `json:"kind"`
	NowLocal           string      `json:"nowLocal"`
	WindowFromLocal    string      `json:"windowFromLocal"`
	UserNote           string      `json:"userNote,omitempty"`
	Day                int         `json:"day"`
	Label              string      `json:"label,omitempty"`
	From               string      `json:"from,omitempty"`
	To                 string      `json:"to,omitempty"`
	Travelers          []string    `json:"travelers,omitempty"`
	TripName           string      `json:"tripName"`
	Nights             int         `json:"nights,omitempty"`
	Phases             []string    `json:"phases,omitempty"`
	Happened           []Happened  `json:"happened,omitempty"`
	YesterdaySummary   string      `json:"yesterdaySummary,omitempty"`
	AlreadyPostedToday []PriorStep `json:"alreadyPosted,omitempty"`
}

func slimForLLM(in *Input) slimPayload {
	var today []PriorStep
	var yesterday []string
	for _, p := range in.AlreadyPosted {
		if p.Day == in.Day {
			today = append(today, p)
			continue
		}
		if p.Day == in.Day-1 {
			yesterday = append(yesterday, strings.TrimSpace(p.Text))
		}
	}
	return slimPayload{
		Kind:               in.Kind,
		NowLocal:           in.NowLocal,
		WindowFromLocal:    in.WindowFromLocal,
		UserNote:           in.UserNote,
		Day:                in.Day,
		Label:              in.Label,
		From:               in.From,
		To:                 in.To,
		Travelers:          in.Travelers,
		TripName:           in.TripName,
		Nights:             in.Nights,
		Phases:             in.Phases,
		Happened:           in.Happened,
		YesterdaySummary:   clipRunes(strings.Join(yesterday, " / "), maxYesterdaySummaryRunes),
		AlreadyPostedToday: today,
	}
}

func jsonBytes(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

func TestExperiment_SlimPayloadSmaller(t *testing.T) {
	j2 := quebecJ2Input()
	j2slim := slimForLLM(j2)
	j2cur, j2s := jsonBytes(j2), jsonBytes(j2slim)
	if j2s >= j2cur {
		t.Fatalf("J2 slim %d >= current %d", j2s, j2cur)
	}
	if j2slim.YesterdaySummary == "" {
		t.Fatal("expected yesterdaySummary on J2")
	}
	if len(j2slim.AlreadyPostedToday) != 4 {
		t.Fatalf("today steps=%d want 4", len(j2slim.AlreadyPostedToday))
	}
	if utf8.RuneCountInString(j2slim.YesterdaySummary) > maxYesterdaySummaryRunes {
		t.Fatalf("yesterdaySummary too long")
	}

	j10 := quebecJ10Input()
	j10slim := slimForLLM(j10)
	j10cur, j10s := jsonBytes(j10), jsonBytes(j10slim)
	if j10s >= j10cur {
		t.Fatalf("J10 slim %d >= current %d", j10s, j10cur)
	}

	t.Logf("J2  current=%d slim=%d delta=%d (%.0f%%)", j2cur, j2s, j2cur-j2s, 100*float64(j2cur-j2s)/float64(j2cur))
	t.Logf("J10 current=%d slim=%d delta=%d (%.0f%%)", j10cur, j10s, j10cur-j10s, 100*float64(j10cur-j10s)/float64(j10cur))
	t.Logf("system current=%d slim=%d", len(systemPrompt), len(slimSystemPrompt))
}

func TestExperiment_SlimKeepsTodayDropsOlderDays(t *testing.T) {
	in := quebecJ2Input()
	s := slimForLLM(in)
	for _, p := range s.AlreadyPostedToday {
		if p.Day != 2 {
			t.Fatalf("leaked day %d into alreadyPosted", p.Day)
		}
	}
	if s.YesterdaySummary == "" {
		t.Fatal("yesterday summary empty")
	}
}

func TestExperiment_BifrostTiming(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("POLARSTEPS_SLIM_HARNESS"))
	if base == "" {
		t.Skip("set POLARSTEPS_SLIM_HARNESS=http://127.0.0.1:18080/v1 to run live Bifrost timing")
	}
	rounds := 2
	if os.Getenv("POLARSTEPS_SLIM_ROUNDS") == "1" {
		rounds = 1
	}
	c := NewBifrostCompleter(Config{
		BifrostBaseURL: base,
		CaptionModel:   defaultModel,
	})
	c.HTTPClient.Timeout = 180 * time.Second

	type row struct {
		name string
		sys  string
		user string
	}
	in := quebecJ2Input()
	curUser, _ := json.Marshal(in)
	slimUser, _ := json.Marshal(slimForLLM(in))
	jobs := []row{
		{name: "current", sys: systemPrompt, user: string(curUser)},
		{name: "slim", sys: slimSystemPrompt, user: string(slimUser)},
	}

	t.Logf("payload current=%d slim=%d", len(curUser), len(slimUser))
	for r := 1; r <= rounds; r++ {
		for _, job := range jobs {
			start := time.Now()
			text, err := c.Complete(job.sys, job.user)
			ms := time.Since(start).Milliseconds()
			if err != nil {
				t.Errorf("round %d %s: %v after %dms", r, job.name, err, ms)
				continue
			}
			qa := RunQA(text, in) // QA still sees full priors (prod-shaped)
			t.Logf("round %d %s: %dms text_runes=%d qa=%s issues=%v",
				r, job.name, ms, utf8.RuneCountInString(text), qa.Verdict, qa.Issues)
			fmt.Fprintf(os.Stderr, "HARNESS r%d %s %dms runes=%d qa=%s\n",
				r, job.name, ms, utf8.RuneCountInString(text), qa.Verdict)
		}
	}
}
