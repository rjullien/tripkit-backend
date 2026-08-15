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
// then copy the files over:
//
//	cp internal/handlers/testdata/contract/*.json \
//	   ../tripkit-frontend/tests/fixtures/construction-contract/
//
// See testdata/contract/README.md.

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/formalities"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
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
