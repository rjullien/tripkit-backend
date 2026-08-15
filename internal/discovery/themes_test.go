package discovery

import "testing"

func TestEffectiveThemes_TemplateMinusDisabledPlusAdded(t *testing.T) {
	tpl := []Theme{
		{ID: "outlets", Label: "Outlets", Engine: engineGeo},
		{ID: "eau", Label: "Eau", Engine: engineGeo},
		{ID: "rando", Label: "Rando", Engine: engineGeo},
	}
	got := EffectiveThemes(tpl, ThemePrefs{
		Disabled: []string{"eau", "missing-id"},
		Added:    []Theme{{ID: "maison", Label: "Chez nous", Engine: engineGeo}},
		Overrides: map[string]Theme{
			"outlets": {Label: "Bons plans", RadiusKm: 40},
		},
	})
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (outlets, rando, maison)", len(got))
	}
	if got[0].ID != "outlets" || got[0].Origin != originOverride || got[0].Label != "Bons plans" || got[0].RadiusKm != 40 {
		t.Fatalf("outlets override: %+v", got[0])
	}
	if got[0].Engine != engineGeo {
		t.Fatal("override must keep engine")
	}
	if got[1].ID != "rando" || got[1].Origin != originTemplate {
		t.Fatalf("rando: %+v", got[1])
	}
	if got[2].ID != "maison" || got[2].Origin != originAdded {
		t.Fatalf("added: %+v", got[2])
	}
}

func TestEffectiveThemes_DisabledUnknownIsNoop(t *testing.T) {
	tpl := []Theme{{ID: "outlets", Label: "Outlets", Engine: engineGeo}}
	got := EffectiveThemes(tpl, ThemePrefs{Disabled: []string{"nope", ""}})
	if len(got) != 1 || got[0].ID != "outlets" {
		t.Fatalf("got %+v", got)
	}
}

func TestEffectiveThemes_JullienFamilyProfile(t *testing.T) {
	// Uses the ACTUAL Jullien family theme prefs from tripkit-seeds/travel-profile.js:
	//   disabled: ["parcs"]
	//   added: [{id:"trains-touristiques", label:"Trains touristiques", emoji:"🚂", engine:"editorial", corridor:false, seasonal:false, queryHints:[...]}]
	//   overrides: {outlets: {radiusKm: 40}}
	tpl := DefaultConfig().Themes // full catalogue including "parcs"
	prefs := ThemePrefs{
		Disabled: []string{"parcs"},
		Added: []Theme{
			{
				ID:         "trains-touristiques",
				Label:      "Trains touristiques",
				Emoji:      "🚂",
				Engine:     engineEditorial,
				Corridor:   false,
				Seasonal:   false,
				QueryHints: []string{"train touristique", "chemin de fer historique", "scenic railway"},
			},
		},
		Overrides: map[string]Theme{
			"outlets": {RadiusKm: 40},
		},
	}

	got := EffectiveThemes(tpl, prefs)

	// (1) "parcs" must be absent from results
	for _, th := range got {
		if th.ID == "parcs" {
			t.Fatal("disabled theme 'parcs' must not appear in effective themes")
		}
	}

	// (2) "trains-touristiques" must be present with engine=editorial
	trainsTh, found := findTheme(got, "trains-touristiques")
	if !found {
		t.Fatal("added theme 'trains-touristiques' not found in effective themes")
	}
	if trainsTh.Engine != engineEditorial {
		t.Fatalf("trains-touristiques engine=%q want %q", trainsTh.Engine, engineEditorial)
	}
	if trainsTh.Label != "Trains touristiques" {
		t.Fatalf("trains-touristiques label=%q want 'Trains touristiques'", trainsTh.Label)
	}
	if trainsTh.Emoji != "🚂" {
		t.Fatalf("trains-touristiques emoji=%q want '🚂'", trainsTh.Emoji)
	}
	if trainsTh.Origin != originAdded {
		t.Fatalf("trains-touristiques origin=%q want %q", trainsTh.Origin, originAdded)
	}
	if len(trainsTh.QueryHints) != 3 {
		t.Fatalf("trains-touristiques queryHints len=%d want 3", len(trainsTh.QueryHints))
	}

	// (3) "outlets" must have radiusKm=40 (override from default 25)
	outletsTh, found := findTheme(got, "outlets")
	if !found {
		t.Fatal("theme 'outlets' not found in effective themes")
	}
	if outletsTh.RadiusKm != 40 {
		t.Fatalf("outlets radiusKm=%v want 40", outletsTh.RadiusKm)
	}
	if outletsTh.Origin != originOverride {
		t.Fatalf("outlets origin=%q want %q", outletsTh.Origin, originOverride)
	}
	// Override should preserve other fields from template
	if outletsTh.Engine != engineGeo {
		t.Fatalf("outlets engine=%q want %q (override must keep engine)", outletsTh.Engine, engineGeo)
	}
	if outletsTh.Label != "Outlets & bons plans" {
		t.Fatalf("outlets label=%q want 'Outlets & bons plans' (override with empty label keeps original)", outletsTh.Label)
	}

	// Verify total count: original 7 themes - 1 disabled + 1 added = 7
	if len(got) != 7 {
		t.Fatalf("effective themes len=%d want 7 (7 template - 1 disabled + 1 added)", len(got))
	}
}

func findTheme(themes []Theme, id string) (Theme, bool) {
	for _, t := range themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

func TestEffectiveThemes_EmptyPrefsIsTemplate(t *testing.T) {
	tpl := DefaultConfig().Themes
	got := EffectiveThemes(tpl, ThemePrefs{})
	if len(got) != len(tpl) {
		t.Fatalf("len=%d want %d", len(got), len(tpl))
	}
	for _, t0 := range got {
		if t0.Origin != originTemplate {
			t.Fatalf("origin=%q for %s", t0.Origin, t0.ID)
		}
	}
}
