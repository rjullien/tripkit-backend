package discovery

import "strings"

// EffectiveThemes = template − disabled + added + overrides.
// Each output theme carries Origin. A disabled id that is not in the template is a no-op.
func EffectiveThemes(template []Theme, prefs ThemePrefs) []Theme {
	disabled := map[string]bool{}
	for _, id := range prefs.Disabled {
		id = strings.TrimSpace(id)
		if id != "" {
			disabled[id] = true
		}
	}

	seen := map[string]int{}
	out := make([]Theme, 0, len(template)+len(prefs.Added))
	for _, t := range template {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			continue
		}
		t.ID = id
		if t.Origin == "" {
			t.Origin = originTemplate
		}
		if ov, ok := prefs.Overrides[id]; ok {
			t = mergeOverride(t, ov)
			t.Origin = originOverride
		}
		if disabled[id] {
			continue
		}
		seen[id] = len(out)
		out = append(out, t)
	}

	for _, t := range prefs.Added {
		id := strings.TrimSpace(t.ID)
		if id == "" || disabled[id] {
			continue
		}
		t.ID = id
		t.Origin = originAdded
		if i, ok := seen[id]; ok {
			out[i] = t
			continue
		}
		seen[id] = len(out)
		out = append(out, t)
	}
	return out
}

func mergeOverride(base, ov Theme) Theme {
	if strings.TrimSpace(ov.Label) != "" {
		base.Label = ov.Label
	}
	if strings.TrimSpace(ov.Emoji) != "" {
		base.Emoji = ov.Emoji
	}
	if strings.TrimSpace(ov.Engine) != "" {
		base.Engine = ov.Engine
	}
	if ov.RadiusKm > 0 {
		base.RadiusKm = ov.RadiusKm
	}
	if len(ov.Overpass) > 0 {
		base.Overpass = ov.Overpass
	}
	if len(ov.QueryHints) > 0 {
		base.QueryHints = ov.QueryHints
	}
	if len(ov.ExcludeNames) > 0 {
		base.ExcludeNames = ov.ExcludeNames
	}
	if ov.Corridor {
		base.Corridor = true
	}
	if ov.Seasonal {
		base.Seasonal = true
	}
	return base
}

func themeByID(themes []Theme, id string) (Theme, bool) {
	for _, t := range themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}
