package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

// GetConstruction returns the current construction state for a trip.
// GET /trips/{tripId}/construction
func (h *Handler) GetConstruction(w http.ResponseWriter, r *http.Request) {
	if h.construction == nil {
		writeError(w, http.StatusServiceUnavailable, "Construction service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	state, code, err := h.construction.GetConstruction(tripID)
	if err != nil {
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, code, state)
}

// TransitionPhase transitions the construction phase for a trip.
// PUT /trips/{tripId}/construction/phase
// Body: {"phase": <int>}
// Query: ?force=1 to force (admin override)
func (h *Handler) TransitionPhase(w http.ResponseWriter, r *http.Request) {
	if h.construction == nil {
		writeError(w, http.StatusServiceUnavailable, "Construction service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")

	var body struct {
		Phase int `json:"phase"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	force := r.URL.Query().Get("force") == "1"
	user := middleware.EffectiveUser(r)

	state, code, err := h.construction.TransitionPhase(tripID, body.Phase, force, user)
	if err != nil {
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, code, state)
}

// GetTravelProfile returns a fused view of people + travelersContext + travelProfile
// from the trip's data JSON. This provides a unified read for construction UIs.
// GET /trips/{tripId}/travel-profile
func (h *Handler) GetTravelProfile(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	var trip struct {
		Data *string
	}
	if err := h.db.Table("trips").Select("data").Where("id = ?", tripID).Scan(&trip).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	if trip.Data == nil || *trip.Data == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"people":           nil,
			"travelersContext": nil,
			"travelProfile":    nil,
		})
		return
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*trip.Data), &raw); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"people":           nil,
			"travelersContext": nil,
			"travelProfile":    nil,
		})
		return
	}

	// Extract the three relevant fields, preserving their raw JSON structure.
	result := map[string]any{
		"people":           rawToAny(raw["people"]),
		"travelersContext": rawToAny(raw["travelersContext"]),
		"travelProfile":    rawToAny(raw["travelProfile"]),
	}

	writeJSON(w, http.StatusOK, result)
}

// rawToAny converts a json.RawMessage to any (nil if empty or null).
func rawToAny(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// validProfileTargets is the set of allowed targets for profile edit requests.
var validProfileTargets = map[string]bool{
	"travelStyle":  true,
	"budgetRules":  true,
	"interests":    true,
	"mealPattern":  true,
	"lessons":      true,
}

// CreateProfileRequest starts a Leo job to edit a section of the travel profile.
// POST /trips/{tripId}/travel-profile/request
// Body: {"target": "travelStyle|budgetRules|interests|mealPattern|lessons", "text": "..."}
func (h *Handler) CreateProfileRequest(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var body struct {
		Target string `json:"target"`
		Text   string `json:"text"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	body.Target = strings.TrimSpace(body.Target)
	body.Text = strings.TrimSpace(body.Text)

	if !validProfileTargets[body.Target] {
		writeError(w, http.StatusBadRequest, "Invalid target: must be one of travelStyle, budgetRules, interests, mealPattern, lessons")
		return
	}
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	// Create the profile request record.
	reqID := uuid.NewString()
	rec := models.ConstructionProfileRequest{
		ID:     reqID,
		TripID: tripID,
		Target: body.Target,
		Text:   body.Text,
		Status: "pending",
	}
	if err := h.db.Create(&rec).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create profile request")
		return
	}

	// Start a Leo job in mode "construction:profile-edit".
	message := fmt.Sprintf("Modifier la section '%s' du profil voyageur.\nDemande : %s", body.Target, body.Text)
	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		_ = emit("meta", leo.StreamEvent{
			Text:   "profile-edit",
			Detail: body.Target,
			Tool:   map[string]any{"target": body.Target, "text": body.Text, "tripId": tripID},
		})
		// The actual LLM call will be wired in a subsequent feature.
		// For now, emit the request context so subscribers see the intent.
		_ = emit("delta", leo.StreamEvent{Text: message})
		_ = emit("done", leo.StreamEvent{})
		return nil
	})

	// Update record with jobId and running status.
	h.db.Model(&rec).Updates(map[string]any{
		"job_id": job.ID,
		"status": "running",
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}
