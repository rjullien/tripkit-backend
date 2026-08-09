package publish_test

import (
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestParsePublishManifest(t *testing.T) {
	raw := []byte(`{
	  "version": 1,
	  "seeds": [
	    {"tripId":"quebec-2026","path":"quebec-2026.js","title":"Québec","assets":["quebec-map.html"]},
	    {"tripId":"usa-2026"}
	  ]
	}`)
	m, err := publish.ParsePublishManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Seeds) != 2 {
		t.Fatalf("want 2 seeds, got %d", len(m.Seeds))
	}
	usa, ok := m.FindSeed("usa-2026")
	if !ok || usa.Path != "usa-2026.js" {
		t.Fatalf("default path: %+v", usa)
	}
}

func TestParsePublishManifest_RejectsTraversal(t *testing.T) {
	_, err := publish.ParsePublishManifest([]byte(`{"seeds":[{"tripId":"x","path":"../secret.js"}]}`))
	if err == nil {
		t.Fatal("expected path error")
	}
	_, err = publish.ParsePublishManifest([]byte(`{"seeds":[{"tripId":"x","path":"nested/x.js"}]}`))
	if err == nil {
		t.Fatal("expected top-level-only error")
	}
}

func TestManifestResolver_FallbackRegistrySeeds(t *testing.T) {
	t.Setenv("TRIPKIT_GITHUB_TOKEN", "")
	src := publish.Source{
		ID: "jullien", Repo: "rjullien/tripkit-seeds", Ref: "main",
		Seeds: []publish.SeedRef{{TripID: "quebec-2026", Path: "quebec-2026.js"}},
	}
	r := publish.NewManifestResolverFromEnv()
	seeds, err := r.SeedsForSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 1 || seeds[0].TripID != "quebec-2026" {
		t.Fatalf("fallback seeds: %+v", seeds)
	}
}
