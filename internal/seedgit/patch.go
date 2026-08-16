package seedgit

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

var varDecl = regexp.MustCompile(`(?m)^var\s+(\w+)\s*=`)

const allowedPhasePath = "trip.construction.phase"

// PatchPhase rewrites only trip.construction.phase in a voyage seed JS file.
// Comments and formatting outside that number are preserved.
func PatchPhase(src string, phase int) (string, error) {
	objStart, objEnd, err := seedObjectSpan(src)
	if err != nil {
		return "", err
	}
	root := src[objStart:objEnd]
	trip, err := findDepth1Object(root, "trip")
	if err != nil {
		return "", fmt.Errorf("seed trip: %w", err)
	}

	cons, consErr := findDepth1Object(trip.value, "construction")
	if consErr != nil {
		inserted, err := insertConstruction(trip.value, phase)
		if err != nil {
			return "", err
		}
		newRoot := splice(root, trip.start, trip.end, inserted)
		return splice(src, objStart, objEnd, newRoot), nil
	}

	updatedCons, err := setPhaseInObject(cons.value, phase)
	if err != nil {
		return "", err
	}
	newTrip := splice(trip.value, cons.start, cons.end, updatedCons)
	newRoot := splice(root, trip.start, trip.end, newTrip)
	return splice(src, objStart, objEnd, newRoot), nil
}

type span struct {
	start int
	end   int
	value string
}

func splice(src string, start, end int, repl string) string {
	return src[:start] + repl + src[end:]
}

func seedObjectSpan(src string) (start, end int, err error) {
	m := varDecl.FindStringSubmatchIndex(src)
	if m == nil {
		return 0, 0, fmt.Errorf("no var declaration found")
	}
	rest := src[m[1]:]
	rel := strings.IndexByte(rest, '{')
	if rel < 0 {
		return 0, 0, fmt.Errorf("object start not found")
	}
	start = m[1] + rel
	obj, err := publish.ExtractBalanced(src[start:])
	if err != nil {
		return 0, 0, err
	}
	return start, start + len(obj), nil
}

func findDepth1Object(obj, key string) (span, error) {
	loc, err := findDepth1Key(obj, key)
	if err != nil {
		return span{}, err
	}
	if loc.valueStart >= len(obj) || obj[loc.valueStart] != '{' {
		return span{}, fmt.Errorf("%s is not an object", key)
	}
	raw, err := publish.ExtractBalanced(obj[loc.valueStart:])
	if err != nil {
		return span{}, err
	}
	return span{start: loc.valueStart, end: loc.valueStart + len(raw), value: raw}, nil
}

type keyValue struct {
	key        string
	valueStart int
}

func findDepth1Key(obj, want string) (keyValue, error) {
	if len(obj) < 2 || (obj[0] != '{' && obj[0] != '[') {
		return keyValue{}, fmt.Errorf("expected object")
	}
	i := 1
	depth := 1
	expectKey := obj[0] == '{'
	for i < len(obj) {
		i = skipSpaceAndComments(obj, i)
		if i >= len(obj) {
			break
		}
		c := obj[i]
		if depth == 1 && expectKey && c != '}' && c != ']' {
			key, next, err := readKey(obj, i)
			if err != nil {
				return keyValue{}, err
			}
			i = skipSpaceAndComments(obj, next)
			if i >= len(obj) || obj[i] != ':' {
				return keyValue{}, fmt.Errorf("expected ':' after key %q", key)
			}
			i = skipSpaceAndComments(obj, i+1)
			if key == want {
				return keyValue{key: key, valueStart: i}, nil
			}
			end, err := skipValue(obj, i)
			if err != nil {
				return keyValue{}, err
			}
			i = end
			expectKey = false
			continue
		}
		switch c {
		case '"', '\'':
			i = skipString(obj, i)
		case '{', '[':
			depth++
			i++
			expectKey = c == '{'
		case '}', ']':
			depth--
			i++
			expectKey = false
		case ',':
			i++
			expectKey = depth == 1 && obj[0] == '{'
		default:
			i++
		}
	}
	return keyValue{}, fmt.Errorf("key %q not found", want)
}

func setPhaseInObject(obj string, phase int) (string, error) {
	loc, err := findDepth1Key(obj, "phase")
	if err != nil {
		return insertPhase(obj, phase)
	}
	end, err := skipValue(obj, loc.valueStart)
	if err != nil {
		return "", err
	}
	return splice(obj, loc.valueStart, end, strconv.Itoa(phase)), nil
}

func insertPhase(obj string, phase int) (string, error) {
	if len(obj) < 2 || obj[0] != '{' {
		return "", fmt.Errorf("construction is not an object")
	}
	inner := obj[1:]
	return "{" + "\n      \"phase\": " + strconv.Itoa(phase) + "," + inner, nil
}

func insertConstruction(tripObj string, phase int) (string, error) {
	if len(tripObj) < 2 || tripObj[0] != '{' {
		return "", fmt.Errorf("trip is not an object")
	}
	block := "\n    \"construction\": {\n      \"phase\": " + strconv.Itoa(phase) + "\n    },"
	return "{" + block + tripObj[1:], nil
}

func skipSpaceAndComments(s string, i int) int {
	for i < len(s) {
		if unicode.IsSpace(rune(s[i])) {
			i++
			continue
		}
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			continue
		}
		break
	}
	return i
}

func readKey(s string, i int) (string, int, error) {
	if i >= len(s) {
		return "", i, fmt.Errorf("expected key")
	}
	if s[i] == '"' || s[i] == '\'' {
		end := skipString(s, i)
		raw := s[i+1 : end-1]
		return raw, end, nil
	}
	if !isIdentStart(s[i]) {
		return "", i, fmt.Errorf("expected key at %d", i)
	}
	j := i + 1
	for j < len(s) && isIdentPart(s[j]) {
		j++
	}
	return s[i:j], j, nil
}

func skipString(s string, i int) int {
	quote := s[i]
	i++
	escape := false
	for i < len(s) {
		c := s[i]
		if escape {
			escape = false
			i++
			continue
		}
		if c == '\\' {
			escape = true
			i++
			continue
		}
		if c == quote {
			return i + 1
		}
		i++
	}
	return len(s)
}

func skipValue(s string, i int) (int, error) {
	i = skipSpaceAndComments(s, i)
	if i >= len(s) {
		return i, fmt.Errorf("expected value")
	}
	switch s[i] {
	case '{', '[':
		raw, err := publish.ExtractBalanced(s[i:])
		if err != nil {
			return i, err
		}
		return i + len(raw), nil
	case '"', '\'':
		return skipString(s, i), nil
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		j := i
		if s[j] == '-' {
			j++
		}
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		return j, nil
	default:
		if strings.HasPrefix(s[i:], "true") {
			return i + 4, nil
		}
		if strings.HasPrefix(s[i:], "false") {
			return i + 5, nil
		}
		if strings.HasPrefix(s[i:], "null") {
			return i + 4, nil
		}
		return i, fmt.Errorf("unsupported value at %d", i)
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func parseGeneric(code string) (map[string]any, error) {
	raw, _, err := publish.ParseJSObject(code)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func allowlistPhaseOnly(before, after string) error {
	return allowlistPaths(before, after, func(p string) bool {
		return p == allowedPhasePath
	})
}

func allowlistPaths(before, after string, ok func(string) bool) error {
	a, err := parseGeneric(before)
	if err != nil {
		return fmt.Errorf("parse original seed: %w", err)
	}
	b, err := parseGeneric(after)
	if err != nil {
		return fmt.Errorf("parse patched seed: %w", err)
	}
	var leaves []string
	diffLeaves(a, b, "", &leaves)
	for _, p := range leaves {
		if !ok(p) {
			return fmt.Errorf("refusing patch: change outside allowlist at %s", p)
		}
	}
	return nil
}

func pathHasPrefix(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+".") || strings.HasPrefix(p, prefix+"[")
}

func diffLeaves(a, b any, path string, out *[]string) {
	if jsonEqual(a, b) {
		return
	}
	am, aOK := asMap(a)
	bm, bOK := asMap(b)
	if aOK || bOK {
		if !aOK {
			am = map[string]any{}
		}
		if !bOK {
			bm = map[string]any{}
		}
		seen := map[string]bool{}
		for k := range am {
			seen[k] = true
		}
		for k := range bm {
			seen[k] = true
		}
		for k := range seen {
			diffLeaves(am[k], bm[k], joinPath(path, k), out)
		}
		return
	}
	aa, aArr := asSlice(a)
	ba, bArr := asSlice(b)
	if aArr && bArr {
		n := len(aa)
		if len(ba) > n {
			n = len(ba)
		}
		if n != len(aa) || n != len(ba) {
			*out = append(*out, path)
			return
		}
		for i := 0; i < n; i++ {
			diffLeaves(aa[i], ba[i], fmt.Sprintf("%s[%d]", path, i), out)
		}
		return
	}
	*out = append(*out, path)
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if af, ok := asFloat(a); ok {
		bf, ok := asFloat(b)
		return ok && af == bf
	}
	as, aOK := a.(string)
	bs, bOK := b.(string)
	if aOK && bOK {
		return as == bs
	}
	ab, aOK := a.(bool)
	bb, bOK := b.(bool)
	if aOK && bOK {
		return ab == bb
	}
	am, aOK := asMap(a)
	bm, bOK := asMap(b)
	if aOK && bOK {
		if len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			if !jsonEqual(av, bm[k]) {
				return false
			}
		}
		return true
	}
	aa, aOK := asSlice(a)
	ba, bOK := asSlice(b)
	if aOK && bOK {
		if len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if !jsonEqual(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
