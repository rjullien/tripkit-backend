package handlers

// Golden cross-repo contract fixtures.
//
// Each case drives a real HTTP handler and compares its response body against a
// golden file in testdata/contract/. A byte-identical copy of that directory
// lives in the frontend repo at
// tripkit-frontend/tests/fixtures/construction-contract/ and is asserted by
// tripkit-frontend/tests/construction-contract.test.cjs, so any change to a
// response envelope breaks a test on both sides instead of silently rendering
// nothing.
//
// Regenerate with:
//
//	cd tripkit-backend && go test ./internal/handlers/ -run TestContractFixtures -update
//
// then copy the files over, CHECKSUMS.txt included:
//
//	cp internal/handlers/testdata/contract/*.json \
//	   internal/handlers/testdata/contract/CHECKSUMS.txt \
//	   ../tripkit-frontend/tests/fixtures/construction-contract/
//
// The copy is not on trust: TestContractFixtures_FrontendCopyInSync compares the
// two directories when both repos are checked out side by side, and the frontend
// unit test hashes its own copies against CHECKSUMS.txt.
//
// See testdata/contract/README.md.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/formalities"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/nuisance"
)

var updateContract = flag.Bool("update", false, "regenerate the golden contract fixtures in testdata/contract/")

const contractDir = "testdata/contract"

// contractRouter builds a router with the four construction check endpoints and
// seeds a single trip from the given trip.data JSON.
func contractRouter(t *testing.T, tripID, tripData string) http.Handler {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	h.SetConstruction(&construction.Service{DB: db})
	h.SetFormalities(&formalities.Service{DB: db})

	start, end := "2026-08-14", "2026-08-20"
	trip := models.Trip{ID: tripID, Name: "Contract Fixture", StartDate: &start, EndDate: &end}
	if tripData != "" {
		trip.Data = &tripData
	}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/trips/{tripId}/construction/qa", h.RunConstructionQA)
	r.Put("/trips/{tripId}/construction/phase", h.TransitionPhase)
	r.Post("/trips/{tripId}/admin-check", h.RunAdminCheck)
	r.Post("/trips/{tripId}/health-check", h.RunHealthCheck)
	return r
}

// qaTripData: day 2 is missing (red day_gap) and day 1 transport is a candidate
// (yellow transport_not_booked), so the fixture pins both severities.
const qaTripData = `{
  "startDate": "2026-08-14",
  "construction": {"phase": 2},
  "days": [
    {"dayNum": 1, "date": "2026-08-14", "timezone": "Europe/Paris",
     "transport": {"mode": "train", "status": "candidate"}},
    {"dayNum": 3, "date": "2026-08-16", "timezone": "Europe/Paris",
     "transport": {"mode": "train", "status": "booked"}}
  ],
  "hotels": [
    {"dayNum": 1, "bookingStatus": "booked"},
    {"dayNum": 3, "bookingStatus": "booked"}
  ]
}`

// adminTripData: Dinah is a FR+US bi-national travelling to the US and Canada.
// The US rules must NOT fire (a US passport needs no ESTA) while the Canadian
// eTA must, with appliesTo listing the pooled nationalities.
const adminTripData = `{
  "locations": {
    "loc-nyc": {"country": "US"},
    "loc-mtl": {"country": "CA"}
  },
  "people": {
    "dinah": {"nationalities": ["FR", "US"]}
  }
}`

// healthTripData: Thailand does produce health items.
const healthTripData = `{
  "locations": {"loc-bkk": {"country": "TH"}},
  "people": {"dinah": {"nationalities": ["FR"]}}
}`

// blockedTripData: same red day_gap as qaTripData, used to pin the 409 body.
const blockedTripData = qaTripData

func TestContractFixtures(t *testing.T) {
	cases := []struct {
		file       string
		tripID     string
		tripData   string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			file:       "qa-violations.json",
			tripID:     "trip-contract-qa",
			tripData:   qaTripData,
			method:     http.MethodPost,
			path:       "/trips/trip-contract-qa/construction/qa",
			body:       "{}",
			wantStatus: http.StatusOK,
		},
		{
			file:       "admin-check.json",
			tripID:     "trip-contract-admin",
			tripData:   adminTripData,
			method:     http.MethodPost,
			path:       "/trips/trip-contract-admin/admin-check",
			body:       "{}",
			wantStatus: http.StatusOK,
		},
		{
			file:       "health-check.json",
			tripID:     "trip-contract-health",
			tripData:   healthTripData,
			method:     http.MethodPost,
			path:       "/trips/trip-contract-health/health-check",
			body:       "{}",
			wantStatus: http.StatusOK,
		},
		{
			file:       "phase-transition-blocked.json",
			tripID:     "trip-contract-blocked",
			tripData:   blockedTripData,
			method:     http.MethodPut,
			path:       "/trips/trip-contract-blocked/construction/phase",
			body:       `{"phase":3}`,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			r := contractRouter(t, tc.tripID, tc.tripData)

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Remote-User", "nadia")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			got := canonicalJSON(t, rec.Body.Bytes())
			path := filepath.Join(contractDir, tc.file)

			if *updateContract {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("updated %s", path)
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run `go test ./internal/handlers/ -run TestContractFixtures -update`)", path, err)
			}
			if string(got) != string(want) {
				t.Fatalf("%s is out of date.\n--- want ---\n%s\n--- got ---\n%s\n"+
					"If the change is intentional: regenerate with `-update`, then copy the\n"+
					"fixtures to tripkit-frontend/tests/fixtures/construction-contract/.",
					path, want, got)
			}
		})
	}
}

// nuisanceTripData: one located stop, coordinates fixed so the scored
// distances in the fixture are deterministic.
const nuisanceTripData = `{
  "locations": {
    "loc-mtl": {"name": "Montréal Vieux-Port", "lat": 45.5, "lon": -73.5}
  }
}`

// contractQuerier is a deterministic Overpass stub: the trains query fails
// (Overpass unreachable) while nightlife returns three bars. The public
// Overpass API is not reachable from the test environment, so the failure path
// can only be proven with a stub.
type contractQuerier struct{}

func (contractQuerier) Search(_ context.Context, lat, lon float64, theme discovery.Theme) ([]discovery.Item, error) {
	switch theme.ID {
	case "nuisance-trains":
		return nil, errors.New("overpass HTTP 429")
	case "nuisance-nightlife":
		return []discovery.Item{
			{ID: "osm:node:1", Name: "Bar A", Lat: lat + 0.0005, Lon: lon},
			{ID: "osm:node:2", Name: "Bar B", Lat: lat - 0.0005, Lon: lon},
			{ID: "osm:node:3", Name: "Bar C", Lat: lat, Lon: lon + 0.0005},
		}, nil
	default:
		return nil, nil
	}
}

// TestContractFixtures_Nuisance pins the {results:[...]} nuisance envelope,
// including the INDETERMINE level, the per-category `unavailable` flag and the
// self-describing `incomplete` / `failedCategories` fields. The completer is nil,
// as in a deployment without Bifrost configuration.
func TestContractFixtures_Nuisance(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	fixed := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	svc := &nuisance.Service{
		DB:       db,
		Overpass: contractQuerier{},
		Hub:      h.LeoHub(),
		Now:      func() time.Time { return fixed },
	}
	h.SetNuisance(svc)

	tripData := nuisanceTripData
	start, end := "2026-08-14", "2026-08-20"
	trip := models.Trip{ID: "trip-contract-nuisance", Name: "Contract Fixture", StartDate: &start, EndDate: &end, Data: &tripData}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}

	job := svc.StartCheck("nadia", nuisance.CheckRequest{TripID: "trip-contract-nuisance", All: true})
	<-job.Done()

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Get("/trips/{tripId}/nuisance-check", h.GetNuisanceCheck)

	req := httptest.NewRequest(http.MethodGet, "/trips/trip-contract-nuisance/nuisance-check", nil)
	req.Header.Set("Remote-User", "nadia")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}

	got := canonicalJSON(t, rec.Body.Bytes())
	path := filepath.Join(contractDir, "nuisance-check.json")

	if *updateContract {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `go test ./internal/handlers/ -run TestContractFixtures -update`)", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is out of date.\n--- want ---\n%s\n--- got ---\n%s\n"+
			"If the change is intentional: regenerate with `-update`, then copy the\n"+
			"fixtures to tripkit-frontend/tests/fixtures/construction-contract/.",
			path, want, got)
	}
}

// The optional LLM `summary` must be absent when no completer is configured, so
// the admin-check/health-check golden fixtures stay valid.
func TestContractFixtures_SummaryOmittedWithoutCompleter(t *testing.T) {
	for _, tc := range []struct {
		path     string
		tripData string
		tripID   string
	}{
		{"/trips/trip-summary-admin/admin-check", adminTripData, "trip-summary-admin"},
		{"/trips/trip-summary-health/health-check", healthTripData, "trip-summary-health"},
	} {
		r := contractRouter(t, tc.tripID, tc.tripData)
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Remote-User", "nadia")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d: %s", tc.path, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["summary"]; ok {
			t.Errorf("%s must omit `summary` when Completer is nil, got %v", tc.path, body["summary"])
		}
	}
}

// ── Cross-repo sync guards ───────────────────────────────────────────────────
//
// The fixtures exist so an envelope change fails a test on both sides of the
// wire. Until now the copy step itself was documented in two READMEs and checked
// by nothing: regenerate, commit, forget the cp, and the frontend suite stayed
// green against a stale fixture — structurally the same blind spot the fixtures
// were introduced to close. Two guards, both dependency-free:
//
//  1. checksumFile is committed next to the fixtures and copied over with them.
//     The frontend unit test hashes its own copies against it, so a JSON copied
//     without the manifest (or the reverse) fails there.
//  2. When both repos are checked out side by side — the layout in which the cp
//     happens — TestContractFixtures_FrontendCopyInSync compares the two
//     directories byte for byte, so a forgotten cp fails here.
//
// Guard 1 alone does NOT close the drift: the manifest is committed inside each
// repo and hashes only its own directory, so a stale frontend copy shipped with
// its own stale manifest satisfies both suites. Guard 2 is the one that bites,
// and it needs both checkouts — hence the `fixtures-cross-repo` job in
// .github/workflows/ci.yaml, which checks the frontend out beside this repo and
// sets requireFrontendEnv so the skip below cannot hide the check.

const checksumFile = "CHECKSUMS.txt"

// frontendContractDir is the frontend copy, relative to this package. It is also
// the layout the CI job creates, so the default needs no override there.
const frontendContractDir = "../../../tripkit-frontend/tests/fixtures/construction-contract"

const (
	// frontendDirEnv overrides frontendContractDir for a checkout laid out
	// differently.
	frontendDirEnv = "TRIPKIT_FRONTEND_CONTRACT_DIR"
	// requireFrontendEnv turns the "frontend not checked out" skip into a
	// failure. A skipped guard is indistinguishable from a passing one in a log,
	// so the job that exists to run it says so explicitly.
	requireFrontendEnv = "TRIPKIT_REQUIRE_FRONTEND_FIXTURES"
)

// contractChecksums renders the sha256 manifest of the golden fixtures, in the
// `sha256sum` format (hash, two spaces, file name), sorted by file name.
func contractChecksums(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no golden fixture found in %s", dir)
	}
	var b strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fmt.Fprintf(&b, "%x  %s\n", sha256.Sum256(raw), name)
	}
	return []byte(b.String())
}

// TestContractFixtures_Checksums pins the manifest the frontend asserts against.
// It must run after the fixtures themselves have been regenerated, hence its
// position in this file (Go runs tests in source order).
func TestContractFixtures_Checksums(t *testing.T) {
	got := contractChecksums(t, contractDir)
	path := filepath.Join(contractDir, checksumFile)

	if *updateContract {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `go test ./internal/handlers/ -run TestContractFixtures -update`)", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is out of date.\n--- want ---\n%s\n--- got ---\n%s\n"+
			"Regenerate with `-update`, then copy the fixtures AND %s to\n"+
			"tripkit-frontend/tests/fixtures/construction-contract/.",
			path, want, got, checksumFile)
	}
}

// TestContractFixtures_FrontendCopyInSync fails when the frontend copy has
// drifted, which is what makes the cross-repo contract real rather than
// documented. It is skipped when the frontend repo is not checked out beside this
// one, which is why CI runs it in a job that does check both out and sets
// requireFrontendEnv: the committed manifest cannot catch a self-consistent stale
// copy on its own.
func TestContractFixtures_FrontendCopyInSync(t *testing.T) {
	if *updateContract {
		t.Skipf("fixtures just regenerated: copy them to %s, then re-run", frontendContractDir)
	}
	frontendDir := frontendContractDir
	if override := os.Getenv(frontendDirEnv); override != "" {
		frontendDir = override
	}
	if _, err := os.Stat(frontendDir); err != nil {
		if os.Getenv(requireFrontendEnv) == "1" {
			t.Fatalf("%s=1 but the frontend fixtures are not at %s: %v\n"+
				"This job exists to run the cross-repo comparison: check tripkit-frontend out beside this repo "+
				"or point %s at its tests/fixtures/construction-contract/.",
				requireFrontendEnv, frontendDir, err, frontendDirEnv)
		}
		t.Skipf("frontend repo not checked out beside this one (%s): only the committed %s applies here, "+
			"and it cannot catch a stale copy carrying its own manifest — the %s=1 CI job can",
			frontendDir, checksumFile, requireFrontendEnv)
	}

	entries, err := os.ReadDir(contractDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".json") && name != checksumFile) {
			continue
		}
		mine, err := os.ReadFile(filepath.Join(contractDir, name))
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := os.ReadFile(filepath.Join(frontendDir, name))
		if err != nil {
			t.Errorf("%s is missing from the frontend copy: %v\n"+
				"cp %s/%s %s/", name, err, contractDir, name, frontendDir)
			continue
		}
		if !bytes.Equal(mine, theirs) {
			t.Errorf("%s differs between the two repos.\ncp %s/%s %s/",
				name, contractDir, name, frontendDir)
		}
	}

	// A fixture only present on the frontend side is drift too: it means a file
	// was renamed or removed here without the copy following.
	feEntries, err := os.ReadDir(frontendDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range feEntries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".json") && name != checksumFile) {
			continue
		}
		if _, err := os.Stat(filepath.Join(contractDir, name)); err != nil {
			t.Errorf("%s exists in the frontend copy but not in %s: stale fixture", name, contractDir)
		}
	}
}

// canonicalJSON re-serializes a response body with sorted keys and two-space
// indentation so the golden files stay diffable and comparison is insensitive to
// handler-side key ordering.
func canonicalJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(out, '\n')
}
