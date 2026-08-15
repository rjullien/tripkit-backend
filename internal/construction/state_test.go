package construction

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupStateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.ConstructionPhaseLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestReadState_NoConstructionData_ReturnsNil(t *testing.T) {
	db := setupStateTestDB(t)

	// Trip with data but no construction field.
	data := `{"people":[{"name":"Alice"}]}`
	trip := models.Trip{ID: "trip-no-construction", Name: "Test", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	state, err := ReadState(db, "trip-no-construction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil state, got %+v", state)
	}
}

func TestReadState_NilData_ReturnsNil(t *testing.T) {
	db := setupStateTestDB(t)

	trip := models.Trip{ID: "trip-nil-data", Name: "Empty"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	state, err := ReadState(db, "trip-nil-data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil state, got %+v", state)
	}
}

func TestReadState_TripNotFound(t *testing.T) {
	db := setupStateTestDB(t)

	_, err := ReadState(db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing trip")
	}
}

func TestWriteState_ReadState_Roundtrip(t *testing.T) {
	db := setupStateTestDB(t)

	// Create trip with some existing data.
	data := `{"people":[{"name":"Alice"}]}`
	trip := models.Trip{ID: "trip-roundtrip", Name: "Roundtrip", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	// Write construction state.
	state := &ConstructionState{
		Phase:          2,
		IdeaRef:        "sicile-2026",
		TransportModes: []string{"car", "train"},
		Dates: &ConstructionDates{
			StartDate: "2026-06-15",
			Window:    "summer",
			Days:      14,
			Flexible:  true,
		},
		LastQA: &QASummary{
			At:       "2025-01-15T10:00:00Z",
			Verdict:  "pass",
			Blockers: []string{},
		},
	}
	if err := WriteState(db, "trip-roundtrip", state); err != nil {
		t.Fatalf("WriteState error: %v", err)
	}

	// Read it back.
	got, err := ReadState(db, "trip-roundtrip")
	if err != nil {
		t.Fatalf("ReadState error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil state after write")
	}

	if got.Phase != 2 {
		t.Errorf("Phase=%d want 2", got.Phase)
	}
	if got.IdeaRef != "sicile-2026" {
		t.Errorf("IdeaRef=%q want %q", got.IdeaRef, "sicile-2026")
	}
	if len(got.TransportModes) != 2 || got.TransportModes[0] != "car" {
		t.Errorf("TransportModes=%v", got.TransportModes)
	}
	if got.Dates == nil {
		t.Fatal("Dates is nil")
	}
	if got.Dates.StartDate != "2026-06-15" {
		t.Errorf("Dates.StartDate=%q", got.Dates.StartDate)
	}
	if got.Dates.Days != 14 {
		t.Errorf("Dates.Days=%d", got.Dates.Days)
	}
	if !got.Dates.Flexible {
		t.Error("Dates.Flexible=false want true")
	}
	if got.LastQA == nil {
		t.Fatal("LastQA is nil")
	}
	if got.LastQA.Verdict != "pass" {
		t.Errorf("LastQA.Verdict=%q", got.LastQA.Verdict)
	}

	// Verify existing data is preserved.
	var updated models.Trip
	db.First(&updated, "id = ?", "trip-roundtrip")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*updated.Data), &raw); err != nil {
		t.Fatalf("unmarshal updated data: %v", err)
	}
	if _, ok := raw["people"]; !ok {
		t.Error("people field was lost after WriteState")
	}
}

func TestWriteState_TouchesTrip(t *testing.T) {
	db := setupStateTestDB(t)

	trip := models.Trip{ID: "trip-touch", Name: "Touch Test"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	// Record original updated_at.
	var before models.Trip
	db.First(&before, "id = ?", "trip-touch")

	state := &ConstructionState{Phase: 1}
	if err := WriteState(db, "trip-touch", state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	var after models.Trip
	db.First(&after, "id = ?", "trip-touch")

	// updated_at should be at or after the original.
	if after.UpdatedAt.Before(before.UpdatedAt) {
		t.Errorf("updated_at went backwards: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestTransitionPhase_LogsToPhaseLog(t *testing.T) {
	db := setupStateTestDB(t)

	trip := models.Trip{ID: "trip-phase-log", Name: "Phase Log Test"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	svc := &Service{DB: db}
	state, code, err := svc.TransitionPhase("trip-phase-log", 3, true, "admin-user")
	if err != nil {
		t.Fatalf("TransitionPhase error: %v", err)
	}
	if code != 200 {
		t.Errorf("code=%d want 200", code)
	}
	if state.Phase != 3 {
		t.Errorf("Phase=%d want 3", state.Phase)
	}

	// Check the log table.
	var logs []models.ConstructionPhaseLog
	db.Where("trip_id = ?", "trip-phase-log").Find(&logs)
	if len(logs) != 1 {
		t.Fatalf("log count=%d want 1", len(logs))
	}
	if logs[0].Phase != 3 {
		t.Errorf("log.Phase=%d want 3", logs[0].Phase)
	}
	if logs[0].ForcedBy != "admin-user" {
		t.Errorf("log.ForcedBy=%q want %q", logs[0].ForcedBy, "admin-user")
	}
	if logs[0].At.IsZero() {
		t.Error("log.At is zero")
	}
}

func TestTransitionPhase_NoForce_ForcedByEmpty(t *testing.T) {
	db := setupStateTestDB(t)

	trip := models.Trip{ID: "trip-no-force", Name: "No Force"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	svc := &Service{DB: db}
	_, _, err := svc.TransitionPhase("trip-no-force", 1, false, "some-user")
	if err != nil {
		t.Fatalf("TransitionPhase error: %v", err)
	}

	var logs []models.ConstructionPhaseLog
	db.Where("trip_id = ?", "trip-no-force").Find(&logs)
	if len(logs) != 1 {
		t.Fatalf("log count=%d want 1", len(logs))
	}
	if logs[0].ForcedBy != "" {
		t.Errorf("ForcedBy=%q want empty", logs[0].ForcedBy)
	}
}

func TestGetConstruction_DefaultPhaseZero(t *testing.T) {
	db := setupStateTestDB(t)

	trip := models.Trip{ID: "trip-default", Name: "Default Phase"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatalf("create trip: %v", err)
	}

	svc := &Service{DB: db}
	state, code, err := svc.GetConstruction("trip-default")
	if err != nil {
		t.Fatalf("GetConstruction error: %v", err)
	}
	if code != 200 {
		t.Errorf("code=%d want 200", code)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Phase != 0 {
		t.Errorf("Phase=%d want 0", state.Phase)
	}
}
