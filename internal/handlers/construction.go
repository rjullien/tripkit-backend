package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/middleware"
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
