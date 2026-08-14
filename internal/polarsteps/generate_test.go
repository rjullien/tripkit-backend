package polarsteps

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
)

type fakeComplete struct {
	text string
	err  error
	last string
}

func (f *fakeComplete) Complete(system, user string) (string, error) {
	f.last = user
	return f.text, f.err
}

func TestGenerate_SavesOnPass(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"homeTz":     "Europe/Paris",
		"polarsteps": map[string]any{"enabled": true},
		"phases":     []any{map[string]any{"name": "Québec & Charlevoix"}},
		"locations":  map[string]any{"montreal": map[string]any{"tz": "America/Toronto"}},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	_ = db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec 2026",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error
	day, _ := json.Marshal(map[string]any{
		"label": "Vol Nice → Montréal", "from": "Nice", "to": "Montréal", "locationId": "montreal",
	})
	_ = db.Create(&models.Day{TripID: "quebec-2026", DayNum: 1, Data: string(day)}).Error

	fake := &fakeComplete{text: golden}
	svc := &Service{
		DB:        db,
		Completer: fake,
		Now: func() time.Time {
			tm, _ := time.Parse(time.RFC3339, "2026-08-14T18:00:00-04:00")
			return tm
		},
	}
	res, code, err := svc.Generate("quebec-2026", "escale longue à Genève", "")
	if err != nil {
		t.Fatal(err)
	}
	if code != 200 {
		t.Fatalf("code=%d qa=%+v", code, res.QA)
	}
	if res.Text != golden {
		t.Fatalf("text=%q", res.Text)
	}
	if !strings.Contains(fake.last, "escale longue") {
		t.Fatalf("LLM user payload missing note: %s", fake.last)
	}
	last, err := svc.Last("quebec-2026", "2026-08-14T18:00:00-04:00")
	if err != nil || last.Text != golden {
		t.Fatalf("last=%+v err=%v", last, err)
	}
}

func TestGenerate_NoSaveOnQAFail(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"homeTz":     "Europe/Paris",
		"polarsteps": map[string]any{"enabled": true},
		"locations":  map[string]any{"montreal": map[string]any{"tz": "America/Toronto"}},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	_ = db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec 2026",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error
	day, _ := json.Marshal(map[string]any{
		"from": "Nice", "to": "Montréal", "locationId": "montreal",
	})
	_ = db.Create(&models.Day{TripID: "quebec-2026", DayNum: 1, Data: string(day)}).Error

	svc := &Service{
		DB:        db,
		Completer: &fakeComplete{text: "trop court PNR 8WQZPY"},
		Now: func() time.Time {
			tm, _ := time.Parse(time.RFC3339, "2026-08-14T18:00:00-04:00")
			return tm
		},
	}
	res, code, err := svc.Generate("quebec-2026", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if code != 422 || res.QA.Verdict != QAFailed {
		t.Fatalf("code=%d qa=%+v", code, res.QA)
	}
	if res.Text != "" {
		t.Fatal("FAILED must not return copyable text")
	}
	last, err := svc.Last("quebec-2026", "2026-08-14T18:00:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	if last.Text != "" {
		t.Fatalf("should not persist failed caption: %q", last.Text)
	}
}

func TestStatus_MissingSeedFlagShowsWhenActive(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{"homeTz": "Europe/Paris"}
	raw, _ := json.Marshal(data)
	s := string(raw)
	_ = db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error
	svc := &Service{DB: db, Now: func() time.Time {
		tm, _ := time.Parse(time.RFC3339, "2026-08-14T12:00:00Z")
		return tm
	}}
	st, err := svc.Status("quebec-2026", svc.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st["enabled"] != true || st["seedEnabled"] != true {
		t.Fatalf("missing flag on an active trip should show: %v", st)
	}
}

func TestStatus_ExplicitFalseHides(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"homeTz":     "Europe/Paris",
		"polarsteps": map[string]any{"enabled": false},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	_ = db.Create(&models.Trip{
		ID: "usa-2026", Name: "USA",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error
	svc := &Service{DB: db, Now: func() time.Time {
		tm, _ := time.Parse(time.RFC3339, "2026-08-14T12:00:00Z")
		return tm
	}}
	st, err := svc.Status("usa-2026", svc.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st["enabled"] != false {
		t.Fatalf("explicit false must hide: %v", st)
	}
	_, code, err := svc.Generate("usa-2026", "", "")
	if err == nil || code != 404 {
		t.Fatalf("disabled generate code=%d err=%v", code, err)
	}
}
