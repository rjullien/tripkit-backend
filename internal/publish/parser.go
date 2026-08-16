package publish

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var varDecl = regexp.MustCompile(`(?m)^var\s+(\w+)\s*=`)

// ParseJSObject extracts the first top-level `var NAME = {…}` object without
// executing JavaScript. Supports JSON plus common seed literals: unquoted keys,
// trailing commas, and // line comments.
func ParseJSObject(code string) (json.RawMessage, string, error) {
	m := varDecl.FindStringSubmatchIndex(code)
	if m == nil {
		return nil, "", fmt.Errorf("no var declaration found")
	}
	name := code[m[2]:m[3]]
	rest := code[m[1]:]
	start := strings.IndexByte(rest, '{')
	if start < 0 {
		return nil, name, fmt.Errorf("object start not found for %s", name)
	}
	obj, err := ExtractBalanced(rest[start:])
	if err != nil {
		return nil, name, err
	}
	jsonish := jsObjectToJSON(obj)
	if !json.Valid([]byte(jsonish)) {
		return nil, name, fmt.Errorf("converted object is not valid JSON for %s", name)
	}
	return json.RawMessage(jsonish), name, nil
}

// ExtractBalanced returns the leading balanced `{…}` or `[…]` slice of s.
func ExtractBalanced(s string) (string, error) {
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return "", fmt.Errorf("expected '{' or '['")
	}
	depth := 0
	inStr := false
	escape := false
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = true
			quote = c
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return s[:i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced braces")
}

// ParseSeedFile parses a voyage seed JS into SeedFile.
func ParseSeedFile(code string) (SeedFile, error) {
	raw, name, err := ParseJSObject(code)
	if err != nil {
		return SeedFile{}, err
	}
	if !strings.HasPrefix(name, "SEED_") && name != "TRIP_DATA" {
		return SeedFile{}, fmt.Errorf("expected SEED_* variable, got %s", name)
	}
	var seed SeedFile
	if err := json.Unmarshal(raw, &seed); err != nil {
		return SeedFile{}, fmt.Errorf("seed json: %w", err)
	}
	return seed, nil
}

// ParsePeopleFile parses people.js into personId → Person.
func ParsePeopleFile(code string) (map[string]Person, error) {
	raw, _, err := ParseJSObject(code)
	if err != nil {
		return nil, err
	}
	var asMap map[string]map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, fmt.Errorf("people json: %w", err)
	}
	out := make(map[string]Person, len(asMap))
	for id, m := range asMap {
		p := Person{Raw: m, ID: id}
		if v, ok := m["id"].(string); ok {
			p.ID = v
		}
		if v, ok := m["name"].(string); ok {
			p.Name = v
		}
		if v, ok := m["emoji"].(string); ok {
			p.Emoji = v
		}
		if v, ok := m["login"].(string); ok {
			p.Login = v
		}
		if v, ok := m["note"].(string); ok {
			p.Note = v
		}
		out[id] = p
	}
	return out, nil
}

// ParseChecklistConfig parses checklist-config.js for family coherence check.
func ParseChecklistConfig(code string) (ChecklistConfig, error) {
	raw, _, err := ParseJSObject(code)
	if err != nil {
		return ChecklistConfig{}, err
	}
	var cfg ChecklistConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ChecklistConfig{}, err
	}
	return cfg, nil
}

// ParseTravelProfile parses travel-profile.js (family tastes). Optional at publish time.
func ParseTravelProfile(code string) (map[string]any, error) {
	raw, _, err := ParseJSObject(code)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("travel-profile json: %w", err)
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

// StructuralValidate performs hard gates (not full seed-qa).
func StructuralValidate(seed SeedFile, expectedTripID, expectedFamily string, cfgFamily string) []string {
	var errs []string
	if seed.Trip == nil {
		return []string{"trip missing"}
	}
	id, _ := seed.Trip["id"].(string)
	if id == "" {
		errs = append(errs, "trip.id missing")
	} else if expectedTripID != "" && id != expectedTripID {
		errs = append(errs, fmt.Sprintf("trip.id %q != expected %q", id, expectedTripID))
	}
	if name, _ := seed.Trip["name"].(string); name == "" {
		errs = append(errs, "trip.name missing")
	}
	if len(seed.Days) == 0 {
		errs = append(errs, "days empty")
	}
	for i, d := range seed.Days {
		if _, err := dayNumOf(d); err != nil {
			errs = append(errs, fmt.Sprintf("days[%d]: %v", i, err))
		}
	}
	if expectedFamily != "" && cfgFamily != "" && !strings.EqualFold(expectedFamily, cfgFamily) {
		errs = append(errs, fmt.Sprintf("checklist-config.family %q != expectedFamily %q", cfgFamily, expectedFamily))
	}
	return errs
}
