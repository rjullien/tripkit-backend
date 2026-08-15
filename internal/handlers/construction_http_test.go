package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

func constructionRouter(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	h.SetConstruction(&construction.Service{DB: db})

	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/trips/{tripId}/travel-profile/request", h.CreateProfileRequest)
	r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
	return h, r
}

func seedConstructionTrip(t *testing.T, h *Handler) {
	t.Helper()
	start, end := "2026-08-14", "2026-09-01"
	if err := h.db.Create(&models.Trip{
		ID: "trip-constr", Name: "Test Construction",
		StartDate: &start, EndDate: &end,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestCreateProfileRequest_ValidBody(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	body := `{"target":"travelStyle","text":"Nous preferons un rythme lent avec des pauses"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.JobID == "" {
		t.Fatalf("expected non-empty jobId, got: %s", rec.Body.String())
	}

	// Verify DB record was created.
	var rec2 models.ConstructionProfileRequest
	if err := h.db.First(&rec2, "trip_id = ?", "trip-constr").Error; err != nil {
		t.Fatalf("db lookup: %v", err)
	}
	if rec2.Target != "travelStyle" {
		t.Fatalf("target=%q", rec2.Target)
	}
	if rec2.JobID != resp.JobID {
		t.Fatalf("jobId mismatch: db=%q resp=%q", rec2.JobID, resp.JobID)
	}
	if rec2.Status != "running" {
		t.Fatalf("status=%q", rec2.Status)
	}

	// Verify job finishes (emits done).
	deadline := time.Now().Add(2 * time.Second)
	job := h.leoJobs.Get(resp.JobID)
	if job == nil {
		t.Fatal("job not found in hub")
	}
	for time.Now().Before(deadline) {
		if job.Status() != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status() != "done" {
		t.Fatalf("job status=%q", job.Status())
	}
}

func TestCreateProfileRequest_InvalidTarget(t *testing.T) {
	_, r := constructionRouter(t)

	body := `{"target":"invalidSection","text":"some text"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_MissingText(t *testing.T) {
	_, r := constructionRouter(t)

	body := `{"target":"interests","text":""}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_NoAuth(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	// Without the UserIdentity middleware injecting a user, the context has no
	// user at all. But in our test setup we do use UserIdentity, so an empty
	// Remote-User becomes "anonymous". The handler checks for empty string,
	// which mirrors DiscoverySearch behavior: real auth is handled by the Auth
	// middleware layer in main.go. Verify that anonymous still proceeds (the
	// Auth middleware would block unauthenticated requests in production).
	body := `{"target":"interests","text":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Remote-User header - defaults to "anonymous" via UserIdentity middleware
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// With UserIdentity, anonymous is a valid user string (not empty).
	// Real auth enforcement happens in the Auth middleware.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (anonymous passes handler check), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProfileRequest_AllTargets(t *testing.T) {
	h, r := constructionRouter(t)
	seedConstructionTrip(t, h)

	targets := []string{"travelStyle", "budgetRules", "interests", "mealPattern", "lessons"}
	for _, target := range targets {
		body := `{"target":"` + target + `","text":"some modification"}`
		req := httptest.NewRequest(http.MethodPost, "/trips/trip-constr/travel-profile/request", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Remote-User", "rene")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("target %q: expected 202, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}
