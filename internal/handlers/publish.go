package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/publish"
)

// SourceCatalogItem is one entry from GET /publish/sources.
type SourceCatalogItem struct {
	SourceID   string   `json:"sourceId"`
	Repo       string   `json:"repo"`
	Ref        string   `json:"ref"`
	Enabled    bool     `json:"enabled"`
	Family     string   `json:"family"`
	TripID     string   `json:"tripId"`
	SeedPath   string   `json:"seedPath"`
	Title      string   `json:"title,omitempty"`
	Operation  string   `json:"operation"` // create | update
	InProd     bool     `json:"inProd"`
	Assets     []string `json:"assets,omitempty"`
}

// ListPublishSources returns trusted seeds the caller may see.
func (h *Handler) ListPublishSources(w http.ResponseWriter, r *http.Request) {
	if h.publishReg == nil {
		writeJSON(w, http.StatusOK, []SourceCatalogItem{})
		return
	}
	user := middleware.EffectiveUser(r)
	admin := isRequestAdmin(r)
	sources := h.publishReg.ListForUser(user, admin)
	out := make([]SourceCatalogItem, 0)
	for _, src := range sources {
		for _, seed := range src.Seeds {
			inProd := false
			var trip models.Trip
			if h.db.First(&trip, "id = ?", seed.TripID).Error == nil {
				inProd = true
			}
			op := "create"
			if inProd {
				op = "update"
			}
			title := ""
			if trip.Name != "" {
				title = trip.Name
			}
			out = append(out, SourceCatalogItem{
				SourceID:  src.ID,
				Repo:      src.Repo,
				Ref:       src.Ref,
				Enabled:   src.Enabled,
				Family:    src.ExpectedFamily,
				TripID:    seed.TripID,
				SeedPath:  seed.Path,
				Title:     title,
				Operation: op,
				InProd:    inProd,
				Assets:    seed.Assets,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// CreatePublishJob enqueues a publish job (202).
func (h *Handler) CreatePublishJob(w http.ResponseWriter, r *http.Request) {
	if h.publishReg == nil {
		writeError(w, http.StatusServiceUnavailable, "Publish registry not configured")
		return
	}
	var req publish.CreateJobRequest
	if err := parseBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	user := middleware.EffectiveUser(r)
	admin := isRequestAdmin(r) || config.IsAdmin(user)

	job, err := publish.CreateJob(h.db, h.publishReg, req, user, admin)
	if err != nil {
		switch err {
		case publish.ErrForbidden:
			writeError(w, http.StatusForbidden, "Not allowed to publish this source")
		case publish.ErrAlreadyRunning:
			writeJSON(w, http.StatusConflict, map[string]any{"error": "already_running", "code": "already_running"})
		case publish.ErrConfirmCreate:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "confirmCreate required for new trips", "code": "confirm_create_required"})
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, publish.ToView(job))
}

// GetPublishJob returns job status for polling.
func (h *Handler) GetPublishJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "jobId")
	job, err := publish.GetJob(h.db, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}
	user := middleware.EffectiveUser(r)
	admin := isRequestAdmin(r) || config.IsAdmin(user)
	if !admin && !strings.EqualFold(job.RequestedBy, user) {
		allowed := false
		if h.publishReg != nil {
			if src, ok := h.publishReg.Get(job.SourceID); ok {
				for _, u := range append(src.PublisherLogins, src.OwnerLogins...) {
					if strings.EqualFold(u, user) {
						allowed = true
						break
					}
				}
			}
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
	}
	writeJSON(w, http.StatusOK, publish.ToView(job))
}
