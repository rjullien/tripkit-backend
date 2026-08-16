package formalities

import (
	"encoding/json"
	"strings"
)

// ApplyAdminOverrides overlays ops/construction.json formalities.overrides on
// the embedded rules. Keys are "CC.type" (e.g. "CA.eta"). A patch replaces
// matching fields on an existing rule, or appends a new rule when Country and
// Type are set and nothing matches. An empty map is a no-op.
func ApplyAdminOverrides(base []AdminRule, overrides map[string]json.RawMessage) []AdminRule {
	if len(overrides) == 0 {
		return base
	}
	out := append([]AdminRule(nil), base...)
	for key, raw := range overrides {
		if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
			continue
		}
		var patch AdminRule
		if err := json.Unmarshal(raw, &patch); err != nil {
			continue
		}
		country, typ := splitOverrideKey(key, patch)
		if country == "" || typ == "" {
			continue
		}
		patch.Country = country
		patch.Type = typ
		replaced := false
		for i := range out {
			if strings.EqualFold(out[i].Country, country) && strings.EqualFold(out[i].Type, typ) {
				out[i] = mergeAdminRule(out[i], patch)
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, patch)
		}
	}
	return out
}

func splitOverrideKey(key string, patch AdminRule) (country, typ string) {
	country = strings.ToUpper(strings.TrimSpace(patch.Country))
	typ = strings.ToLower(strings.TrimSpace(patch.Type))
	k := strings.TrimSpace(key)
	if i := strings.Index(k, "."); i > 0 {
		if country == "" {
			country = strings.ToUpper(k[:i])
		}
		if typ == "" {
			typ = strings.ToLower(k[i+1:])
		}
	}
	return country, typ
}

func mergeAdminRule(base, patch AdminRule) AdminRule {
	out := base
	if patch.Label != "" {
		out.Label = patch.Label
	}
	if len(patch.AppliesTo) > 0 {
		out.AppliesTo = patch.AppliesTo
	}
	if patch.URL != "" {
		out.URL = patch.URL
	}
	if patch.Cost != "" {
		out.Cost = patch.Cost
	}
	if patch.Delay != "" {
		out.Delay = patch.Delay
	}
	return out
}
