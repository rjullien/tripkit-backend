package seedgit

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

type memFile struct {
	content string
	sha     string
}

type memFiles struct {
	mu      sync.Mutex
	files   map[string]memFile
	puts    int
	failGet error
	failPut error
}

func (m *memFiles) key(repo, path string) string { return repo + "|" + path }

func (m *memFiles) GetFile(repo, ref, path string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet != nil {
		return nil, "", m.failGet
	}
	f, ok := m.files[m.key(repo, path)]
	if !ok {
		return nil, "", fmt.Errorf("missing %s/%s", repo, path)
	}
	return []byte(f.content), f.sha, nil
}

func (m *memFiles) PutFile(repo, branch, path, message, sha string, content []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut != nil {
		return "", m.failPut
	}
	k := m.key(repo, path)
	f, ok := m.files[k]
	if !ok {
		return "", fmt.Errorf("missing %s/%s", repo, path)
	}
	if f.sha != sha {
		return "", fmt.Errorf("SHA conflict")
	}
	m.puts++
	next := "sha-" + fmt.Sprint(m.puts)
	m.files[k] = memFile{content: string(content), sha: next}
	if !strings.Contains(message, "feat(construction):") {
		return "", fmt.Errorf("unexpected message %q", message)
	}
	_ = branch
	return next, nil
}

func testRegistry() *publish.Registry {
	return publish.NewRegistry([]publish.Source{{
		ID:             "jullien",
		Repo:           "rjullien/tripkit-seeds",
		Ref:            "main",
		ExpectedFamily: "jullien",
		Enabled:        true,
		Seeds:          []publish.SeedRef{{TripID: "test-2026", Path: "test-2026.js"}},
	}})
}

func TestPushPhase_HappyPath(t *testing.T) {
	files := &memFiles{files: map[string]memFile{
		"rjullien/tripkit-seeds|test-2026.js": {content: miniSeed, sha: "abc"},
	}}
	svc := &Service{Registry: testRegistry(), Files: files}
	res, err := svc.PushPhase("test-2026", 3, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.OK {
		t.Fatalf("res=%+v", res)
	}
	if res.SHA != "sha-1" || files.puts != 1 {
		t.Fatalf("sha=%s puts=%d", res.SHA, files.puts)
	}
	got := files.files["rjullien/tripkit-seeds|test-2026.js"].content
	seed, err := publish.ParseSeedFile(got)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if int(c["phase"].(float64)) != 3 {
		t.Fatalf("phase=%v", c["phase"])
	}
}

func TestPushPhase_IdempotentSkipsPut(t *testing.T) {
	files := &memFiles{files: map[string]memFile{
		"rjullien/tripkit-seeds|test-2026.js": {content: miniSeed, sha: "abc"},
	}}
	svc := &Service{Registry: testRegistry(), Files: files}
	res, err := svc.PushPhase("test-2026", 1, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.OK || !res.Unchanged {
		t.Fatalf("res=%+v", res)
	}
	if files.puts != 0 {
		t.Fatalf("puts=%d", files.puts)
	}
}

func TestPushPhase_ParseFailureDoesNotPut(t *testing.T) {
	files := &memFiles{files: map[string]memFile{
		"rjullien/tripkit-seeds|test-2026.js": {content: "not a seed", sha: "abc"},
	}}
	svc := &Service{Registry: testRegistry(), Files: files}
	res, err := svc.PushPhase("test-2026", 2, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected failure, got %+v", res)
	}
	if files.puts != 0 {
		t.Fatalf("PUT must not run on parse failure, puts=%d", files.puts)
	}
}

func TestPushPhase_SHAConflict(t *testing.T) {
	files := &memFiles{
		files: map[string]memFile{
			"rjullien/tripkit-seeds|test-2026.js": {content: miniSeed, sha: "abc"},
		},
		failPut: fmt.Errorf("SHA conflict"),
	}
	svc := &Service{Registry: testRegistry(), Files: files}
	res, err := svc.PushPhase("test-2026", 4, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "SHA conflict") {
		t.Fatalf("res=%+v", res)
	}
}

func TestPushPhase_UnknownTrip(t *testing.T) {
	svc := &Service{Registry: testRegistry(), Files: &memFiles{files: map[string]memFile{}}}
	res, err := svc.PushPhase("missing-2026", 1, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "allowlist") {
		t.Fatalf("res=%+v", res)
	}
}

func TestPushActivity_HappyPath(t *testing.T) {
	files := &memFiles{files: map[string]memFile{
		"rjullien/tripkit-seeds|test-2026.js": {content: miniSeed, sha: "abc"},
	}}
	svc := &Service{Registry: testRegistry(), Files: files}
	act := map[string]any{"id": "osm:node:1", "name": "Musée", "bookingStatus": "candidate"}
	res, err := svc.PushActivity("test-2026", act, "rene")
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.OK {
		t.Fatalf("res=%+v", res)
	}
	if files.puts != 1 {
		t.Fatalf("puts=%d", files.puts)
	}
	got := files.files["rjullien/tripkit-seeds|test-2026.js"].content
	seed, err := publish.ParseSeedFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if seed.Activities["osm:node:1"].(map[string]any)["name"] != "Musée" {
		t.Fatalf("activities=%v", seed.Activities)
	}
}

func TestPushPin_HappyPath(t *testing.T) {
	files := &memFiles{files: map[string]memFile{
		"rjullien/tripkit-seeds|test-2026.js": {content: miniSeedWithHotels, sha: "abc"},
	}}
	svc := &Service{Registry: testRegistry(), Files: files}
	res, err := svc.PushPin("test-2026",
		map[string]any{"at": "2026-08-16T10:00:00Z", "verdict": "PASS", "blockers": []any{}},
		map[string]map[string]any{"montreal": {"verdict": "FAIBLE", "at": "2026-08-16T10:00:00Z"}},
		"rene")
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.OK {
		t.Fatalf("res=%+v", res)
	}
	got := files.files["rjullien/tripkit-seeds|test-2026.js"].content
	seed, err := publish.ParseSeedFile(got)
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Trip["construction"].(map[string]any)
	if c["lastQa"].(map[string]any)["verdict"] != "PASS" {
		t.Fatalf("lastQa=%v", c["lastQa"])
	}
}
