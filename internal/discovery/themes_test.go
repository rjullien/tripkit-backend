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
