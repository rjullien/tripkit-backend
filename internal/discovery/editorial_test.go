package discovery

import (
	"context"
	"strings"
	"testing"
)

func TestParseEditorialJSON_FenceEmptyDedupCap(t *testing.T) {
	theme := Theme{ID: "festivals"}

	got, err := parseEditorialJSON("```json\n[{\"name\":\"Festifoule\",\"when\":\"2026-08-21\",\"url\":\"https://festifoule.ca\",\"note\":\"Tadoussac\"}]\n```", theme)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Festifoule" || got[0].When != "2026-08-21" {
		t.Fatalf("%+v", got)
	}
	if got[0].Source != "editorial" || !strings.Contains(got[0].ID, "festifoule") {
		t.Fatalf("id/source %+v", got[0])
	}

	empty, err := parseEditorialJSON("[]", theme)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %+v", err, empty)
	}

	if _, err := parseEditorialJSON("sorry, no events", theme); err == nil {
		t.Fatal("garbage should error")
	}

	raw := `[
		{"name":"Alpha"},{"name":"alpha"},{"name":"Beta"},{"name":"Gamma"},
		{"name":"Delta"},{"name":"Epsilon"},{"name":"Zeta"},{"name":"Eta"},
		{"name":"Theta"},{"name":"Iota"}
	]`
	capped, err := parseEditorialJSON(raw, theme)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != maxEditorialItems {
		t.Fatalf("len=%d want %d", len(capped), maxEditorialItems)
	}
	if capped[0].Name != "Alpha" || capped[1].Name != "Beta" {
		t.Fatalf("dedup %+v", capped)
	}
}

func TestEditorialUserPrompt_DateAndSeason(t *testing.T) {
	fest := editorialUserPrompt(EditorialQuery{
		Theme:    Theme{ID: "festivals", Label: "Festivals", Seasonal: true, QueryHints: []string{"festival"}},
		Place:    "Tadoussac",
		TripName: "Boucle Québec",
		DateISO:  "2026-08-21",
		Lat:      48.1454,
		Lon:      -69.7173,
	})
	for _, needle := range []string{"2026-08-21", "Tadoussac", "Boucle Québec", "week-end", "festival"} {
		if !strings.Contains(fest, needle) {
			t.Fatalf("fest missing %q\n%s", needle, fest)
		}
	}
	if strings.Contains(fest, "aujourd") {
		t.Fatal("must not mention today")
	}

	show := editorialUserPrompt(EditorialQuery{
		Theme:   Theme{ID: "spectacles", Label: "Spectacles"},
		Place:   "Québec",
		DateISO: "2026-08-14",
	})
	if !strings.Contains(show, "Spectacles") || !strings.Contains(show, "2026-08-14") {
		t.Fatalf("%s", show)
	}
	if strings.Contains(show, "week-end") {
		t.Fatal("spectacles is not seasonal")
	}
}

func TestLeoEditorial_SearchParsesReply(t *testing.T) {
	l := &LeoEditorial{Complete: func(_ context.Context, system, user string) (string, error) {
		if !strings.Contains(system, "JSON array") || strings.Contains(system, "Écriture git") {
			t.Fatalf("system=%s", system)
		}
		if !strings.Contains(user, "2026-08-21") {
			t.Fatalf("user=%s", user)
		}
		return `[{"name":"Festifoule","when":"2026-08-21","url":"https://ex","note":"soir"}]`, nil
	}}
	got, err := l.Search(context.Background(), EditorialQuery{
		Theme:   Theme{ID: "festivals", Label: "Festivals", Seasonal: true},
		Place:   "Tadoussac",
		DateISO: "2026-08-21",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Festifoule" {
		t.Fatalf("%+v", got)
	}
}

func TestSlug(t *testing.T) {
	if got := slug("Village de marques"); got != "village-de-marques" {
		t.Fatalf("%q", got)
	}
	if slug("   ") != "item" {
		t.Fatal("empty")
	}
}
