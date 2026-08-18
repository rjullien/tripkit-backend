package dailybrief

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Worker tick tests use sendFn — no Bifrost, no GoWA, no LLM.

func setupTickDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.DailyBriefSend{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedQuebecDay4(t *testing.T, db *gorm.DB, tripData map[string]any) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	raw, err := json.Marshal(tripData)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if err := db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle", StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dayRaw, _ := json.Marshal(map[string]any{"title": "J4", "locationId": "quebec"})
	if err := db.Create(&models.Day{TripID: "quebec-2026", DayNum: 4, Data: string(dayRaw)}).Error; err != nil {
		t.Fatal(err)
	}
}

func quebecEnabledData() map[string]any {
	return map[string]any{
		"dailyBrief":    true,
		"briefSendTime": "07:00",
		"whatsappGroup": "120363000000000001@g.us",
		"homeTz":        "Europe/Paris",
		"locations": map[string]any{
			"quebec": map[string]any{"tz": "America/Toronto"},
		},
	}
}

func mustToronto(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("need tzdata America/Toronto: %v", err)
	}
	return loc
}

func quebecLocal(t *testing.T, day int, hour, min int) time.Time {
	t.Helper()
	return time.Date(2026, 8, day, hour, min, 0, 0, mustToronto(t))
}

type sendCall struct {
	tripID    string
	dayNumber int
}

func tickingWorker(db *gorm.DB, now time.Time, calls *[]sendCall, mu *sync.Mutex) *Worker {
	return &Worker{
		DB:    db,
		nowFn: func() time.Time { return now },
		sendFn: func(tripID string, dayNumber int) (*SendResult, error) {
			mu.Lock()
			*calls = append(*calls, sendCall{tripID, dayNumber})
			mu.Unlock()
			return &SendResult{Sent: true, MessageID: "fake-1", QAVerdict: QAPassed}, nil
		},
	}
}

func TestWorkerTick_SendsAtBriefSendTimeLocalTZ(t *testing.T) {
	db := setupTickDB(t, "tick_0700")
	seedQuebecDay4(t, db, quebecEnabledData())
	now := quebecLocal(t, 17, 7, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 1 || calls[0].tripID != "quebec-2026" || calls[0].dayNumber != 4 {
		t.Fatalf("want one send day=4, got %+v", calls)
	}
}

func TestWorkerTick_BeforeTargetDoesNotSend(t *testing.T) {
	db := setupTickDB(t, "tick_0659")
	seedQuebecDay4(t, db, quebecEnabledData())
	now := quebecLocal(t, 17, 6, 59)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 0 {
		t.Fatalf("06:59 must not send, got %+v", calls)
	}
}

func TestWorkerTick_SameDayCatchUpAfterWindow(t *testing.T) {
	db := setupTickDB(t, "tick_noon")
	seedQuebecDay4(t, db, quebecEnabledData())
	now := quebecLocal(t, 17, 12, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 1 || calls[0].dayNumber != 4 {
		t.Fatalf("noon catch-up should send J4, got %+v", calls)
	}
}

func TestWorkerTick_NextDayDoesNotSendYesterday(t *testing.T) {
	db := setupTickDB(t, "tick_next")
	seedQuebecDay4(t, db, quebecEnabledData())
	now := quebecLocal(t, 18, 0, 2)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	for _, c := range calls {
		if c.dayNumber == 4 {
			t.Fatalf("must not send yesterday J4 after midnight, got %+v", calls)
		}
	}
}

func TestWorkerTick_SkipsWhenFlagsMissing(t *testing.T) {
	db := setupTickDB(t, "tick_stripped")
	// Shape seen in live Québec when auto-send died: itinerary keys, no dailyBrief.
	seedQuebecDay4(t, db, map[string]any{
		"locations":   map[string]any{"quebec": map[string]any{"tz": "America/Toronto"}},
		"hotels":      map[string]any{},
		"restaurants": map[string]any{},
		"travelers":   []any{},
		"users":       map[string]any{},
		"phases":      []any{},
	})
	now := quebecLocal(t, 17, 7, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 0 {
		t.Fatalf("stripped trip.data must skip worker, got %+v", calls)
	}
}

func TestWorkerTick_SkipsWhenGroupEmpty(t *testing.T) {
	db := setupTickDB(t, "tick_nogroup")
	data := quebecEnabledData()
	data["whatsappGroup"] = ""
	seedQuebecDay4(t, db, data)
	now := quebecLocal(t, 17, 7, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 0 {
		t.Fatalf("empty whatsappGroup must skip, got %+v", calls)
	}
}

func TestWorkerTick_SkipsAlreadySent(t *testing.T) {
	db := setupTickDB(t, "tick_sent")
	seedQuebecDay4(t, db, quebecEnabledData())
	if err := db.Create(&models.DailyBriefSend{
		TripID: "quebec-2026", DayNumber: 4, LocalDate: "2026-08-17",
		QAVerdict: string(QAPassed), Sent: true, MessageID: "already",
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := quebecLocal(t, 17, 12, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 0 {
		t.Fatalf("already_sent must not call LLM/send, got %+v", calls)
	}
}

func TestWorkerTick_SendsWhenColumnsSetAndJSONStripped(t *testing.T) {
	db := setupTickDB(t, "tick_cols")
	stripped := map[string]any{
		"locations": map[string]any{"quebec": map[string]any{"tz": "America/Toronto"}},
		"hotels":    map[string]any{},
	}
	seedQuebecDay4(t, db, stripped)
	on := true
	group := "120363000000000001@g.us"
	sendAt := "07:00"
	if err := db.Model(&models.Trip{}).Where("id = ?", "quebec-2026").Updates(map[string]any{
		"daily_brief":     on,
		"whatsapp_group":  group,
		"brief_send_time": sendAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := quebecLocal(t, 17, 7, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 1 || calls[0].dayNumber != 4 {
		t.Fatalf("columns must send J4 after JSON wipe, got %+v", calls)
	}
}

func TestBackfillFlagColumns_CopiesJSONIntoNullColumns(t *testing.T) {
	db := setupTickDB(t, "tick_backfill")
	seedQuebecDay4(t, db, quebecEnabledData())
	n := BackfillFlagColumns(db)
	if n != 1 {
		t.Fatalf("expected 1 backfill, got %d", n)
	}
	var trip models.Trip
	if err := db.First(&trip, "id = ?", "quebec-2026").Error; err != nil {
		t.Fatal(err)
	}
	if trip.DailyBrief == nil || !*trip.DailyBrief {
		t.Fatal("daily_brief not backfilled")
	}
	if trip.WhatsappGroup == nil || *trip.WhatsappGroup == "" {
		t.Fatal("whatsapp_group not backfilled")
	}
	if n2 := BackfillFlagColumns(db); n2 != 0 {
		t.Fatalf("second backfill must be no-op, got %d", n2)
	}
}

func TestWorkerTick_SkipsSameDayQAFailed(t *testing.T) {
	db := setupTickDB(t, "tick_qa")
	seedQuebecDay4(t, db, quebecEnabledData())
	if err := db.Create(&models.DailyBriefSend{
		TripID: "quebec-2026", DayNumber: 4, LocalDate: "2026-08-17",
		QAVerdict: string(QAFailed), Sent: false, Error: "QA FAILED",
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := quebecLocal(t, 17, 12, 0)
	var calls []sendCall
	var mu sync.Mutex
	tickingWorker(db, now, &calls, &mu).tick()
	if len(calls) != 0 {
		t.Fatalf("QA FAILED must not retry all afternoon, got %+v", calls)
	}
}
