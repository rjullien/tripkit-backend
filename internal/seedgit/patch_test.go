package seedgit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

const miniSeed = `// header comment must survive
var SEED_TEST_2026 = {
  // trip comment
  trip: {
    id: "test-2026",
    name: "Test",
    construction: {
      phase: 1, // keep this
      dates: { startDate: "2026-01-01", days: 3 }
    }
  },
  days: [{ day: 1, title: "A" }]
};
`

func TestPatchPhase_PreservesCommentsAndOtherFields(t *testing.T) {
	out, err := PatchPhase(miniSeed, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// header comment must survive") {
		t.Fatalf("lost header comment:\n%s", out)
	}
	if !strings.Contains(out, "// trip comment") {
		t.Fatalf("lost trip comment:\n%s", out)
	}
	if !strings.Contains(out, "// keep this") {
		t.Fatalf("lost construction comment:\n%s", out)
	}
	if !strings.Contains(out, "phase: 3") && !strings.Contains(out, `"phase": 3`) && !strings.Contains(out, "phase:3") {
		t.Fatalf("phase not updated:\n%s", out)
	}
	if strings.Contains(out, "phase: 1") {
		t.Fatalf("old phase still present:\n%s", out)
	}

	seed, err := publish.ParseSeedFile(out)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if int(c["phase"].(float64)) != 3 {
		t.Fatalf("phase=%v", c["phase"])
	}
	dates := c["dates"].(map[string]any)
	if dates["startDate"] != "2026-01-01" {
		t.Fatalf("dates mutated: %v", dates)
	}
	if err := allowlistPhaseOnly(miniSeed, out); err != nil {
		t.Fatal(err)
	}
}

func TestPatchPhase_InsertsConstructionWhenMissing(t *testing.T) {
	src := `var SEED_TEST_2026 = {
  trip: { id: "test-2026", name: "Test" },
  days: [{ day: 1, title: "A" }]
};`
	out, err := PatchPhase(src, 2)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := publish.ParseSeedFile(out)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if int(c["phase"].(float64)) != 2 {
		t.Fatalf("phase=%v", c["phase"])
	}
	if err := allowlistPhaseOnly(src, out); err != nil {
		t.Fatal(err)
	}
}

func TestAllowlist_RefusesOtherKeyChange(t *testing.T) {
	before := miniSeed
	after := strings.Replace(miniSeed, `"Test"`, `"Hacked"`, 1)
	err := allowlistPhaseOnly(before, after)
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("err=%v", err)
	}
}

func TestPatchPhase_QuotedConstructionKeys(t *testing.T) {
	src := `var SEED_TEST_2026 = {
  "trip": {
    "id": "test-2026",
    "name": "Test",
    "construction": {
      "phase": 5,
      "dates": { "days": 19 }
    }
  },
  "days": [{ "day": 1 }]
};`
	out, err := PatchPhase(src, 4)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := publish.ParseSeedFile(out)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if int(c["phase"].(float64)) != 4 {
		t.Fatalf("phase=%v want 4\n%s", c["phase"], out)
	}
	dates := c["dates"].(map[string]any)
	if int(dates["days"].(float64)) != 19 {
		t.Fatalf("dates mutated: %v", dates)
	}
}

func TestPatchPhase_QuebecSeed(t *testing.T) {
	root := filepath.Join("/agent/repos", "tripkit-seeds")
	raw, err := os.ReadFile(filepath.Join(root, "quebec-2026.js"))
	if err != nil {
		t.Skip(err)
	}
	src := string(raw)
	out, err := PatchPhase(src, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePhasePatch(src, out, "quebec-2026", 3, "jullien"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"phase": 3`) && !strings.Contains(out, `"phase":3`) {
		t.Fatalf("phase not 3")
	}
	if strings.Count(out, `"startDate": "2026-08-14"`) == 0 {
		t.Fatal("dates.startDate lost")
	}
}

const miniSeedWithHotels = `// header comment must survive
var SEED_TEST_2026 = {
  // trip comment
  trip: {
    id: "test-2026",
    name: "Test",
    construction: {
      phase: 1, // keep this
      dates: { startDate: "2026-01-01", days: 3 }
    }
  },
  days: [{ day: 1, title: "A" }],
  hotels: {
    montreal: { name: "Hotel Test", addr: "1 rue" }
  }
};
`

func TestPatchActivity_PreservesCommentsAndAllowlist(t *testing.T) {
	act := map[string]any{
		"id":            "osm:node:1",
		"name":          "Musée",
		"theme":         "musees",
		"bookingStatus": "candidate",
		"lat":           48.8,
		"lon":           2.3,
	}
	out, err := PatchActivity(miniSeed, act)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// header comment must survive") {
		t.Fatalf("lost header:\n%s", out)
	}
	if !strings.Contains(out, "osm:node:1") {
		t.Fatalf("activity id missing:\n%s", out)
	}
	seed, err := publish.ParseSeedFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := seed.Activities["osm:node:1"].(map[string]any)
	if !ok {
		t.Fatalf("activities=%v", seed.Activities)
	}
	if got["bookingStatus"] != "candidate" || got["name"] != "Musée" {
		t.Fatalf("activity=%v", got)
	}
	if err := allowlistPaths(miniSeed, out, func(p string) bool {
		return pathHasPrefix(p, "activities")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPatchActivity_AllowlistRejectsExtraKey(t *testing.T) {
	act := map[string]any{"id": "a1", "name": "X", "bookingStatus": "candidate"}
	out, err := PatchActivity(miniSeed, act)
	if err != nil {
		t.Fatal(err)
	}
	hacked := strings.Replace(out, `"Test"`, `"Hacked"`, 1)
	err = allowlistPaths(miniSeed, hacked, func(p string) bool {
		return pathHasPrefix(p, "activities")
	})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("err=%v", err)
	}
}

func TestPatchPin_WritesLastQaAndHotelNuisance(t *testing.T) {
	lastQa := map[string]any{"at": "2026-08-16T10:00:00Z", "verdict": "WARNING", "blockers": []string{"nuisance:montreal"}}
	nui := map[string]map[string]any{
		"montreal": {"verdict": "MODERE", "at": "2026-08-16T10:00:00Z", "mainIssue": "highways", "detail": "A-40"},
		"missing":  {"verdict": "ELEVE"},
	}
	out, err := PatchPin(miniSeedWithHotels, lastQa, nui)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// header comment must survive") || !strings.Contains(out, "// keep this") {
		t.Fatalf("lost comments:\n%s", out)
	}
	seed, err := publish.ParseSeedFile(out)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if int(c["phase"].(float64)) != 1 {
		t.Fatalf("phase mutated: %v", c["phase"])
	}
	lq := c["lastQa"].(map[string]any)
	if lq["verdict"] != "WARNING" {
		t.Fatalf("lastQa=%v", lq)
	}
	hotels := map[string]any{}
	if err := json.Unmarshal(seed.Hotels, &hotels); err != nil {
		t.Fatal(err)
	}
	montreal := hotels["montreal"].(map[string]any)
	n := montreal["nuisance"].(map[string]any)
	if n["verdict"] != "MODERE" || n["mainIssue"] != "highways" {
		t.Fatalf("nuisance=%v", n)
	}
	if _, ok := hotels["missing"]; ok {
		t.Fatal("must not invent hotels")
	}
	if err := allowlistPaths(miniSeedWithHotels, out, func(p string) bool {
		if pathHasPrefix(p, "trip.construction.lastQa") {
			return true
		}
		return strings.HasPrefix(p, "hotels.") && strings.Contains(p, ".nuisance")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPatchPin_AllowlistRejectsTripNameChange(t *testing.T) {
	out, err := PatchPin(miniSeedWithHotels, map[string]any{"verdict": "PASS", "blockers": []any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hacked := strings.Replace(out, `"Test"`, `"Hacked"`, 1)
	err = allowlistPaths(miniSeedWithHotels, hacked, func(p string) bool {
		return pathHasPrefix(p, "trip.construction.lastQa")
	})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("err=%v", err)
	}
}
