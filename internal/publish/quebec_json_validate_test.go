package publish_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestQuebecCanonicalJSONValid(t *testing.T) {
	root := ""
	for _, cand := range []string{
		filepath.Join("..", "..", "..", "tripkit-seeds"),
		"/agent/repos/tripkit-seeds",
	} {
		if _, err := os.Stat(filepath.Join(cand, "quebec-2026.js")); err == nil {
			root = cand
			break
		}
	}
	if root == "" {
		t.Skip("repo tripkit-seeds not found")
	}
	seedB, err := os.ReadFile(filepath.Join(root, "quebec-2026.js"))
	if err != nil {
		t.Fatal(err)
	}
	peopleB, _ := os.ReadFile(filepath.Join(root, "people.js"))
	cfgB, _ := os.ReadFile(filepath.Join(root, "checklist-config.js"))
	seed, err := publish.ParseSeedFile(string(seedB))
	if err != nil {
		t.Fatal(err)
	}
	people, err := publish.ParsePeopleFile(string(peopleB))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := publish.ParseChecklistConfig(string(cfgB))
	if err != nil {
		t.Fatal(err)
	}
	p, err := publish.BuildCanonical(seed, people, cfg.Family, "jullien", "abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	must := func(label string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		if !json.Valid(b) {
			t.Fatalf("invalid %s", label)
		}
		if len(b) == 0 {
			t.Fatalf("empty %s", label)
		}
	}
	must("trip", p.TripData)
	for i, d := range p.Days {
		must("day", d)
		_ = i
	}
	for _, h := range p.Hotels {
		must("hotel", h.Data)
	}
	for id, l := range p.Lists {
		must("list:"+id, l.Data)
	}
}
