package seedgit

import (
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
