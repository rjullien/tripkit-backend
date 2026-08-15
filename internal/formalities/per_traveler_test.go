package formalities

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Trip{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedTrip(t *testing.T, db *gorm.DB, id string, data map[string]any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal trip data: %v", err)
	}
	s := string(raw)
	// Clear any row left by a previous test on the shared in-memory DB.
	db.Where("id = ?", id).Delete(&models.Trip{})
	if err := db.Create(&models.Trip{ID: id, Data: &s}).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}
}

// tripToUS is a trip to the United States with a mixed-nationality family:
// Rene holds FR only, Dinah holds FR and US.
func tripToUS() map[string]any {
	return map[string]any{
		"locations": map[string]any{
			"nyc": map[string]any{"name": "New York", "country": "US", "lat": 40.7, "lon": -74.0},
		},
		"people": map[string]any{
			"rene":  map[string]any{"name": "Rene", "nationalities": []any{"FR"}},
			"dinah": map[string]any{"name": "Dinah", "nationalities": []any{"FR", "US"}},
		},
	}
}

func findTraveler(t *testing.T, res *AdminCheckResult, name string) TravelerChecklist {
	t.Helper()
	for _, cl := range res.Travelers {
		if cl.Name == name {
			return cl
		}
	}
	t.Fatalf("traveler %q missing from result (got %d travelers)", name, len(res.Travelers))
	return TravelerChecklist{}
}

func hasType(items []AdminCheckItem, typ string) bool {
	for _, it := range items {
		if it.Type == typ {
			return true
		}
	}
	return false
}

// TestAdminCheck_MixedFamilyToUS is the regression test for the bug this change
// fixes: crossing the *union* of the family's passports against the destination
// made Dinah's US passport cancel Rene's ESTA requirement, so a FR-only
// traveler was told he had nothing to file.
func TestAdminCheck_MixedFamilyToUS(t *testing.T) {
	db := newTestDB(t)
	seedTrip(t, db, "trip-mixed-us", tripToUS())
	svc := &Service{DB: db}

	res, err := svc.AdminCheck("trip-mixed-us")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}

	if len(res.Travelers) != 2 {
		t.Fatalf("want 2 traveler checklists, got %d", len(res.Travelers))
	}

	rene := findTraveler(t, res, "Rene")
	if !hasType(rene.Items, "esta") {
		t.Errorf("Rene (FR only) must need an ESTA for the US, got items %+v", rene.Items)
	}
	if rene.Verdict != "action_required" {
		t.Errorf("Rene verdict = %q, want action_required", rene.Verdict)
	}

	dinah := findTraveler(t, res, "Dinah")
	if hasType(dinah.Items, "esta") {
		t.Errorf("Dinah (FR+US bi-national) must NOT need an ESTA, got items %+v", dinah.Items)
	}
	if hasType(dinah.Items, "visa") {
		t.Errorf("Dinah (FR+US bi-national) must NOT need a visa, got items %+v", dinah.Items)
	}

	// The trip-level verdict follows the worst traveler.
	if res.Verdict != "action_required" {
		t.Errorf("trip verdict = %q, want action_required", res.Verdict)
	}
}

// TestAdminCheck_UnknownNationalityIsNotOK guards the direction of failure: a
// traveler with no recorded passport must not be reported as having nothing to do.
func TestAdminCheck_UnknownNationalityIsNotOK(t *testing.T) {
	db := newTestDB(t)
	data := tripToUS()
	data["people"] = map[string]any{
		"ghost": map[string]any{"name": "Ghost"},
	}
	seedTrip(t, db, "trip-unknown-nat", data)
	svc := &Service{DB: db}

	res, err := svc.AdminCheck("trip-unknown-nat")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}
	if res.Verdict == "ok" {
		t.Fatalf("verdict must not be ok when a nationality is unknown, got %+v", res)
	}
	ghost := findTraveler(t, res, "Ghost")
	if !hasType(ghost.Items, "nationality_unknown") {
		t.Errorf("want a nationality_unknown item, got %+v", ghost.Items)
	}
}

// TestAdminCheck_NoPeopleIsWarning covers a trip with a destination but no
// people recorded at all.
func TestAdminCheck_NoPeopleIsWarning(t *testing.T) {
	db := newTestDB(t)
	data := tripToUS()
	delete(data, "people")
	seedTrip(t, db, "trip-no-people", data)
	svc := &Service{DB: db}

	res, err := svc.AdminCheck("trip-no-people")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}
	if res.Verdict != "warning" {
		t.Errorf("verdict = %q, want warning", res.Verdict)
	}
}

// TestAdminCheck_DeadlineAndURLPropagated covers SPEC §7.1: each item carries
// its lead time and its official link.
func TestAdminCheck_DeadlineAndURLPropagated(t *testing.T) {
	db := newTestDB(t)
	seedTrip(t, db, "trip-deadline", tripToUS())
	svc := &Service{DB: db}

	res, err := svc.AdminCheck("trip-deadline")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}
	rene := findTraveler(t, res, "Rene")
	for _, it := range rene.Items {
		if it.Type != "esta" {
			continue
		}
		if it.Deadline == "" {
			t.Errorf("ESTA item has no deadline: %+v", it)
		}
		if it.URL == "" {
			t.Errorf("ESTA item has no official URL: %+v", it)
		}
		return
	}
	t.Fatal("no ESTA item found for Rene")
}

// TestAdminCheck_SummaryUsesCompleter checks the LLM wording layer is actually
// invoked — it was dead code before this change.
func TestAdminCheck_SummaryUsesCompleter(t *testing.T) {
	db := newTestDB(t)
	seedTrip(t, db, "trip-summary", tripToUS())

	called := false
	svc := &Service{
		DB: db,
		Completer: bifrost.CompleteFn(func(system, user string) (string, error) {
			called = true
			return "Resume LLM", nil
		}),
	}

	res, err := svc.AdminCheck("trip-summary")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}
	if !called {
		t.Fatal("Completer was never called: the LLM wording step is still dead code")
	}
	if res.Summary != "Resume LLM" {
		t.Errorf("Summary = %q, want the completer output", res.Summary)
	}
}

// TestAdminCheck_SummaryFallsBackWithoutCompleter checks the structured result
// still stands alone when Bifrost is not configured.
func TestAdminCheck_SummaryFallsBackWithoutCompleter(t *testing.T) {
	db := newTestDB(t)
	seedTrip(t, db, "trip-nollm", tripToUS())
	svc := &Service{DB: db}

	res, err := svc.AdminCheck("trip-nollm")
	if err != nil {
		t.Fatalf("AdminCheck: %v", err)
	}
	if len(res.Travelers) == 0 {
		t.Fatal("travelers must be present without a completer")
	}
	if res.Summary != "" {
		t.Errorf("Summary = %q, want empty without a completer (omitempty, pas de prose de repli présentée comme un LLM)", res.Summary)
	}
}

// TestHealthCheck_SilenceRule covers SPEC §7.2: on a destination that asks for
// nothing, the verdict is "none", there are no items, and no summary is
// generated — the frontend must be able to render nothing at all.
func TestHealthCheck_SilenceRule(t *testing.T) {
	db := newTestDB(t)
	seedTrip(t, db, "trip-health-silent", tripToUS())

	called := false
	svc := &Service{
		DB: db,
		HealthCompleter: bifrost.CompleteFn(func(system, user string) (string, error) {
			called = true
			return "ne devrait pas etre appele", nil
		}),
	}

	res, err := svc.HealthCheck("trip-health-silent")
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if res.Verdict != "none" {
		t.Errorf("verdict = %q, want none for the US", res.Verdict)
	}
	if len(res.Items) != 0 {
		t.Errorf("want no items, got %+v", res.Items)
	}
	if res.Summary != "" {
		t.Errorf("want no summary under the silence rule, got %q", res.Summary)
	}
	if called {
		t.Error("the LLM must not be called when there is nothing to say")
	}
}

// TestExtractTravelers_Deterministic guards against map-iteration flakiness in
// the API response ordering.
func TestExtractTravelers_Deterministic(t *testing.T) {
	data := tripToUS()
	first := extractTravelers(data)
	for i := 0; i < 20; i++ {
		got := extractTravelers(data)
		if len(got) != len(first) {
			t.Fatalf("unstable traveler count: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j].ID != first[j].ID {
				t.Fatalf("unstable traveler order at %d: %q vs %q", j, got[j].ID, first[j].ID)
			}
		}
	}
}
