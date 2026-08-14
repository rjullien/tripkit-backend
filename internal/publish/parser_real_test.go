package publish_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func repoRoot(t *testing.T, name string) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "..", name),
		filepath.Join("/agent/repos", name),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	t.Skipf("repo %s not found", name)
	return ""
}

func TestParseRealJullienSeedFiles(t *testing.T) {
	root := repoRoot(t, "tripkit-seeds")
	seedB, err := os.ReadFile(filepath.Join(root, "quebec-2026.js"))
	if err != nil {
		t.Fatal(err)
	}
	seed, err := publish.ParseSeedFile(string(seedB))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seed.Trip["id"] != "quebec-2026" {
		t.Fatalf("id=%v", seed.Trip["id"])
	}
	peopleB, err := os.ReadFile(filepath.Join(root, "people.js"))
	if err != nil {
		t.Fatal(err)
	}
	people, err := publish.ParsePeopleFile(string(peopleB))
	if err != nil {
		t.Fatalf("people: %v", err)
	}
	if people["rene"].Name == "" {
		t.Fatal("rene missing")
	}
	cfgB, err := os.ReadFile(filepath.Join(root, "checklist-config.js"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := publish.ParseChecklistConfig(string(cfgB))
	if err != nil {
		t.Fatalf("cfg: %v", err)
	}
	if cfg.Family != "jullien" {
		t.Fatalf("family=%q", cfg.Family)
	}
}

func TestParseRealNadiaPeople(t *testing.T) {
	root := repoRoot(t, "tripkit-seeds-nadia")
	b, err := os.ReadFile(filepath.Join(root, "people.js"))
	if err != nil {
		t.Fatal(err)
	}
	people, err := publish.ParsePeopleFile(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := people["nadia"]; !ok {
		t.Fatal("nadia missing")
	}
}

func TestParseTravelProfile(t *testing.T) {
	code := `var TRAVEL_PROFILE = {
  family: "jullien",
  themes: { disabled: ["eau"], added: [], overrides: {} }
};`
	m, err := publish.ParseTravelProfile(code)
	if err != nil {
		t.Fatal(err)
	}
	if m["family"] != "jullien" {
		t.Fatalf("%v", m["family"])
	}
	themes, _ := m["themes"].(map[string]any)
	disabled, _ := themes["disabled"].([]any)
	if len(disabled) != 1 || disabled[0] != "eau" {
		t.Fatalf("disabled=%v", disabled)
	}
}
