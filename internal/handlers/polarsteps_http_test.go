package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/polarsteps"
)

type fakeCaption struct {
	text string
	err  error
}

func (f fakeCaption) Complete(system, user string) (string, error) {
	return f.text, f.err
}

const polarstepsGolden = `Décollage depuis Nice Côte d'Azur ce matin pour une grande boucle au Québec.

18 jours, tous les 3 avec Baptiste, un itinéraire en 5 phases : Québec et Charlevoix, le Fjord du Saguenay, Tadoussac et les baleines, la Gaspésie sauvage, et le Bas-Saint-Laurent pour boucler la boucle.

Nice, Genève, Montréal. Escale courte, puis la route commence.

Premier arrêt ce soir : Montréal. La suite s'annonce belle.`

func polarstepsRouter(t *testing.T, c polarsteps.Completer) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	now := func() time.Time {
		tm, _ := time.Parse(time.RFC3339, "2026-08-14T18:00:00-04:00")
		return tm
	}
	h.SetPolarsteps(&polarsteps.Service{DB: db, Completer: c, Now: now})
	r := chi.NewRouter()
	r.Get("/trips/{tripId}/polarsteps/status", h.PolarstepsStatus)
	r.Get("/trips/{tripId}/polarsteps/caption", h.PolarstepsCaption)
	r.Post("/trips/{tripId}/polarsteps/caption", h.GeneratePolarstepsCaption)
	r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
	return h, r
}

func waitPolarstepsJob(t *testing.T, h *Handler, jobID string) {
	t.Helper()
	j := h.LeoHub().Get(jobID)
	if j == nil {
		t.Fatal("job missing")
	}
	select {
	case <-j.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("job timeout")
	}
}

func seedQuebecPolarsteps(t *testing.T, h *Handler) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	data := map[string]any{
		"homeTz":     "Europe/Paris",
		"polarsteps": map[string]any{"enabled": true, "tripUrl": "https://www.polarsteps.com/test/quebec/"},
		"phases":     []any{map[string]any{"name": "Québec & Charlevoix"}},
		"locations":  map[string]any{"montreal": map[string]any{"tz": "America/Toronto"}},
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := h.db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle Québec 2026",
		StartDate: &start, EndDate: &end, Data: &s,
	}).Error; err != nil {
		t.Fatal(err)
	}
	day, _ := json.Marshal(map[string]any{
		"label": "Vol Nice → Montréal", "from": "Nice", "to": "Montréal", "locationId": "montreal",
	})
	if err := h.db.Create(&models.Day{TripID: "quebec-2026", DayNum: 1, Data: string(day)}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPolarstepsStatus_MissingSeedShowsWhenActive(t *testing.T) {
	h, r := polarstepsRouter(t, fakeCaption{text: polarstepsGolden})
	start, end := "2026-08-14", "2026-09-01"
	data := `{"homeTz":"Europe/Paris"}`
	_ = h.db.Create(&models.Trip{
		ID: "quebec-2026", Name: "Boucle",
		StartDate: &start, EndDate: &end, Data: &data,
	}).Error
	req := httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/polarsteps/status?now=2026-08-14T18:00:00-04:00", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("missing seed flag on an active trip should show: %s", w.Body.String())
	}
}

func TestPolarstepsStatus_ReadyWhenActive(t *testing.T) {
	h, r := polarstepsRouter(t, fakeCaption{text: polarstepsGolden})
	seedQuebecPolarsteps(t, h)
	req := httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/polarsteps/status?now=2026-08-14T18:00:00-04:00", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["enabled"] != true || st["ready"] != true {
		t.Fatalf("status=%v", st)
	}
	if st["tripUrl"] != "https://www.polarsteps.com/test/quebec/" {
		t.Fatalf("tripUrl=%v", st["tripUrl"])
	}
}

func TestPolarstepsGenerate_OKAndLast(t *testing.T) {
	h, r := polarstepsRouter(t, fakeCaption{text: polarstepsGolden})
	seedQuebecPolarsteps(t, h)
	body := `{"userNote":"escale longue à Genève","clientNowISO":"2026-08-14T18:00:00-04:00"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/quebec-2026/polarsteps/caption", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var accepted map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	jobID, _ := accepted["jobId"].(string)
	if jobID == "" {
		t.Fatalf("body=%s", w.Body.String())
	}
	waitPolarstepsJob(t, h, jobID)
	if strings.Contains(w.Body.String(), "PNR") {
		t.Fatal("PNR leaked")
	}
	j := h.LeoHub().Get(jobID)
	backlog, _, unsub := j.Subscribe(0)
	unsub()
	var sawDone bool
	for _, ev := range backlog {
		if ev.Event == "done" && strings.Contains(ev.Data.Reply, "Décollage") {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatalf("expected done.reply with caption, events=%d", len(backlog))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/trips/quebec-2026/polarsteps/caption?now=2026-08-14T18:00:00-04:00", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), "Décollage") {
		t.Fatalf("last code=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestPolarstepsGenerate_QAFailNoText(t *testing.T) {
	h, r := polarstepsRouter(t, fakeCaption{text: "trop court PNR 8WQZPY"})
	seedQuebecPolarsteps(t, h)
	req := httptest.NewRequest(http.MethodPost, "/trips/quebec-2026/polarsteps/caption", bytes.NewReader([]byte(`{"clientNowISO":"2026-08-14T18:00:00-04:00"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var accepted map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	jobID, _ := accepted["jobId"].(string)
	waitPolarstepsJob(t, h, jobID)
	j := h.LeoHub().Get(jobID)
	backlog, _, unsub := j.Subscribe(0)
	unsub()
	var sawQA bool
	for _, ev := range backlog {
		if ev.Event == "error" && ev.Data.Code == "qa_failed" {
			sawQA = true
		}
		if ev.Event == "done" && strings.Contains(ev.Data.Reply, `"text"`) {
			t.Fatal("FAILED must not include copyable text on done")
		}
	}
	if !sawQA {
		t.Fatalf("expected qa_failed on job, events=%d", len(backlog))
	}
}

func TestPolarstepsGenerate_NoGoWAImport(t *testing.T) {
	// Guard: this package must not grow a WhatsApp send path.
	src := polarstepsGolden
	if strings.Contains(src, "gowa") {
		t.Fatal("unexpected")
	}
}
