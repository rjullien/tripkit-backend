package construction

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupWritebackDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.ConstructionCheck{}, &models.ConstructionPhaseLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestRetainActivity_WritesCandidateAndTouchesTrip(t *testing.T) {
	db := setupWritebackDB(t)
	data := `{"people":[{"name":"Alice"}]}`
	trip := models.Trip{ID: "trip-retain", Name: "Retain", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	var before models.Trip
	db.First(&before, "id = ?", "trip-retain")

	stub := &stubSeedGit{res: &SeedPushResult{OK: true, SHA: "abc"}}
	svc := &Service{DB: db, SeedGit: stub}
	act, err := BuildCandidateActivity("osm:node:1", "Musée", "musees", 48.8, 2.3, 1.2, "https://maps", "osm")
	if err != nil {
		t.Fatal(err)
	}
	got, code, err := svc.RetainActivity("trip-retain", "rene", act)
	if err != nil || code != http.StatusOK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got.Activity["bookingStatus"] != "candidate" {
		t.Fatalf("activity=%v", got.Activity)
	}
	if got.SeedPush == nil || !got.SeedPush.OK {
		t.Fatalf("seedPush=%+v", got.SeedPush)
	}

	var after models.Trip
	db.First(&after, "id = ?", "trip-retain")
	if after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatalf("updated_at not bumped")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(*after.Data), &raw); err != nil {
		t.Fatal(err)
	}
	acts := raw["activities"].(map[string]any)
	item := acts["osm:node:1"].(map[string]any)
	if item["name"] != "Musée" || item["bookingStatus"] != "candidate" {
		t.Fatalf("persisted=%v", item)
	}
	if _, ok := raw["people"]; !ok {
		t.Fatal("people lost")
	}
}

func TestRetainActivity_SeedGitFailureStill200(t *testing.T) {
	db := setupWritebackDB(t)
	trip := models.Trip{ID: "trip-retain-fail", Name: "Retain"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	stub := &stubSeedGit{res: &SeedPushResult{OK: false, Error: "SHA conflict"}}
	svc := &Service{DB: db, SeedGit: stub}
	act, _ := BuildCandidateActivity("a1", "Café", "cafes", 0, 0, 0, "", "")
	got, code, err := svc.RetainActivity("trip-retain-fail", "rene", act)
	if err != nil || code != 200 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got.SeedPush == nil || got.SeedPush.OK || got.SeedPush.Error != "SHA conflict" {
		t.Fatalf("seedPush=%+v", got.SeedPush)
	}
	var persisted models.Trip
	db.First(&persisted, "id = ?", "trip-retain-fail")
	var raw map[string]any
	json.Unmarshal([]byte(*persisted.Data), &raw)
	if raw["activities"].(map[string]any)["a1"] == nil {
		t.Fatal("activity must persist even when git fails")
	}
}

func TestPinNuisance_WritesHotelsAndLastQa(t *testing.T) {
	db := setupWritebackDB(t)
	data := `{"hotels":{"montreal":{"name":"Hotel Test","addr":"1 rue"}}}`
	trip := models.Trip{ID: "trip-pin", Name: "Pin", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	nui, _ := json.Marshal(map[string]any{
		"locationId":     "montreal",
		"hotelId":        "montreal",
		"verdict":        "MODERE",
		"recommendation": "Changer de quartier.",
		"analyzedAt":     time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"categories": []any{
			map[string]any{"category": "highways", "level": "MODERE", "detail": "A-40 à 250 m"},
			map[string]any{"category": "security", "level": "FAIBLE", "detail": "ok"},
		},
	})
	if err := db.Create(&models.ConstructionCheck{
		TripID: "trip-pin", Kind: "nuisance", TargetID: "montreal", Data: string(nui),
	}).Error; err != nil {
		t.Fatal(err)
	}

	stub := &stubSeedGit{res: &SeedPushResult{OK: true, SHA: "pin"}}
	svc := &Service{DB: db, SeedGit: stub}
	got, code, err := svc.PinNuisance("trip-pin", "rene")
	if err != nil || code != 200 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got.LastQa["verdict"] != "WARNING" {
		t.Fatalf("lastQa=%v", got.LastQa)
	}
	if got.Hotels["montreal"]["verdict"] != "MODERE" || got.Hotels["montreal"]["mainIssue"] != "highways" {
		t.Fatalf("hotels=%v", got.Hotels)
	}

	var persisted models.Trip
	db.First(&persisted, "id = ?", "trip-pin")
	var raw map[string]any
	json.Unmarshal([]byte(*persisted.Data), &raw)
	hotel := raw["hotels"].(map[string]any)["montreal"].(map[string]any)
	n := hotel["nuisance"].(map[string]any)
	if n["verdict"] != "MODERE" {
		t.Fatalf("persisted nuisance=%v", n)
	}
	cons := raw["construction"].(map[string]any)
	if cons["lastQA"].(map[string]any)["verdict"] != "WARNING" {
		t.Fatalf("lastQA=%v", cons["lastQA"])
	}
}

func TestPinNuisance_NothingToPin(t *testing.T) {
	db := setupWritebackDB(t)
	trip := models.Trip{ID: "trip-pin-empty", Name: "Empty"}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	svc := &Service{DB: db}
	_, code, err := svc.PinNuisance("trip-pin-empty", "rene")
	if err == nil || code != http.StatusBadRequest {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "nothing to pin") {
		t.Fatalf("err=%v", err)
	}
}

func TestPinNuisance_SkipsUnknownHotels(t *testing.T) {
	db := setupWritebackDB(t)
	data := `{"hotels":{"quebec":{"name":"Known"}}}`
	trip := models.Trip{ID: "trip-pin-skip", Name: "Skip", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	nui, _ := json.Marshal(map[string]any{
		"locationId": "ghost", "hotelId": "ghost", "verdict": "ELEVE",
	})
	db.Create(&models.ConstructionCheck{TripID: "trip-pin-skip", Kind: "nuisance", TargetID: "ghost", Data: string(nui)})
	svc := &Service{DB: db}
	got, code, err := svc.PinNuisance("trip-pin-skip", "rene")
	if err != nil || code != 200 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(got.Hotels) != 0 {
		t.Fatalf("must not invent hotels: %v", got.Hotels)
	}
	if got.LastQa["verdict"] != "FAIL" {
		t.Fatalf("lastQa=%v", got.LastQa)
	}
}

func TestSanitizeActivityID(t *testing.T) {
	act, err := BuildCandidateActivity("osm:node:9!", "Village de marques", "outlets", 0, 0, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if act["id"] != "osm:node:9" {
		t.Fatalf("id=%v", act["id"])
	}
	act, err = BuildCandidateActivity("", "Café du coin", "", 0, 0, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if act["id"] != "Café_du_coin" && act["id"] != "Cafe_du_coin" {
		// letters (including accented) are kept; space → underscore
		if !strings.Contains(act["id"].(string), "du_coin") {
			t.Fatalf("id=%v", act["id"])
		}
	}
}

func TestPinNuisance_ReadsWrappedQA(t *testing.T) {
	db := setupWritebackDB(t)
	data := `{"hotels":{"montreal":{"name":"H"}}}`
	trip := models.Trip{ID: "trip-pin-wrap", Name: "Wrap", Data: &data}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	wrapped := QAResultJSON([]QAViolation{{Code: "day_gap", Severity: "red", DayNum: 2}}, 3)
	if err := db.Create(&models.ConstructionCheck{
		TripID: "trip-pin-wrap", Kind: "qa", Data: wrapped,
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &Service{DB: db}
	got, code, err := svc.PinNuisance("trip-pin-wrap", "rene")
	if err != nil || code != 200 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got.LastQa["verdict"] != "FAIL" {
		t.Fatalf("lastQa=%v", got.LastQa)
	}
	blockers, _ := got.LastQa["blockers"].([]string)
	if len(blockers) != 1 || blockers[0] != "day_gap:2" {
		t.Fatalf("blockers=%v", got.LastQa["blockers"])
	}
}
