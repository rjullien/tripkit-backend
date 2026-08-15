package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/nuisance"
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

// RunConstructionQA runs QA rules synchronously and stores the result.
// POST /trips/{tripId}/construction/qa
func (h *Handler) RunConstructionQA(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	// Load trip data
	var trip struct {
		Data *string
	}
	if err := h.db.Table("trips").Select("data").Where("id = ?", tripID).Scan(&trip).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	// Parse trip data
	var tripData map[string]any
	if trip.Data != nil && *trip.Data != "" {
		if err := json.Unmarshal([]byte(*trip.Data), &tripData); err != nil {
			tripData = make(map[string]any)
		}
	} else {
		tripData = make(map[string]any)
	}

	// Extract travel profile
	var profile map[string]any
	if tp, ok := tripData["travelProfile"].(map[string]any); ok {
		profile = tp
	}

	// Get current phase
	phase := 0
	if h.construction != nil {
		state, _, _ := h.construction.GetConstruction(tripID)
		if state != nil {
			phase = state.Phase
		}
	}

	// Run QA
	violations := construction.RunQA(tripData, profile, phase)
	if violations == nil {
		violations = []construction.QAViolation{}
	}

	// Store result
	resultJSON := construction.QAResultJSON(violations)
	check := models.ConstructionCheck{
		TripID: tripID,
		Kind:   "qa",
		Data:   resultJSON,
	}
	h.db.Create(&check)

	writeJSON(w, http.StatusOK, map[string]any{
		"violations": violations,
		"phase":      phase,
		"count":      len(violations),
	})
}

// GetConstructionQA returns the last cached QA result for a trip.
// GET /trips/{tripId}/construction/qa
func (h *Handler) GetConstructionQA(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	var check models.ConstructionCheck
	if err := h.db.Where("trip_id = ? AND kind = ?", tripID, "qa").Order("created_at DESC").First(&check).Error; err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"violations": []any{},
			"phase":      0,
			"count":      0,
			"cached":     false,
		})
		return
	}

	var violations []construction.QAViolation
	if err := json.Unmarshal([]byte(check.Data), &violations); err != nil {
		violations = []construction.QAViolation{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"violations": violations,
		"count":      len(violations),
		"cached":     true,
		"cachedAt":   check.CreatedAt,
	})
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

// ── Discovery Retain ──────────────────────────────────────────────────────────

// retainItemBody is the payload for POST /trips/{tripId}/discovery/retain.
type retainItemBody struct {
	Item retainItem `json:"item"`
}

type retainItem struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	ThemeID string  `json:"themeId"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	DistKm  float64 `json:"distKm"`
	URL     string  `json:"url"`
	Source  string  `json:"source"`
}

// RetainDiscoveryItem starts a Leo job to add a discovery item to trip.activities
// with bookingStatus:"candidate". The write goes through Leo (never direct FE write).
// POST /trips/{tripId}/discovery/retain
func (h *Handler) RetainDiscoveryItem(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var body retainItemBody
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(body.Item.Name) == "" {
		writeError(w, http.StatusBadRequest, "item.name is required")
		return
	}

	item := body.Item
	message := fmt.Sprintf(
		"Ajoute cette activite dans trip.activities avec bookingStatus:'candidate':\n"+
			"- id: %s\n- nom: %s\n- theme: %s\n- lat: %f, lon: %f\n- distance: %.1f km\n- url: %s\n- source: %s",
		item.ID, item.Name, item.ThemeID, item.Lat, item.Lon, item.DistKm, item.URL, item.Source,
	)

	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		_ = emit("meta", leo.StreamEvent{
			Text:   "construction:activities",
			Detail: "retain-discovery-item",
			Tool: map[string]any{
				"tripId": tripID,
				"item":   item,
			},
		})
		_ = emit("delta", leo.StreamEvent{Text: message})
		_ = emit("done", leo.StreamEvent{})
		return nil
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

// ── Nuisance Seed Pin ────────────────────────────────────────────────────────

// PinNuisanceToSeed starts a Leo job to write the nuisance summary into the
// seed (hotels[].nuisance + trip.construction.lastQa). Triggered from the
// "Epingler dans le seed" button in the nuisance results view.
// POST /trips/{tripId}/nuisance-check/pin
func (h *Handler) PinNuisanceToSeed(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	message := fmt.Sprintf(
		"Ecris le resume des nuisances dans le seed pour le voyage %s:\n"+
			"- Pour chaque hotel, ajoute ou mets a jour hotels[].nuisance avec le resume de l'analyse.\n"+
			"- Mets a jour trip.construction.lastQa avec la date et le resume global.",
		tripID,
	)

	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		_ = emit("meta", leo.StreamEvent{
			Text:   "construction:activities",
			Detail: "pin-nuisance-to-seed",
			Tool: map[string]any{
				"tripId": tripID,
			},
		})
		_ = emit("delta", leo.StreamEvent{Text: message})
		_ = emit("done", leo.StreamEvent{})
		return nil
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

// ── Nuisance Check ───────────────────────────────────────────────────────────

// RunNuisanceCheck launches a nuisance analysis job for the given locations.
// POST /trips/{tripId}/nuisance-check
// Body: {"locationIds": ["loc1", "loc2"]} or {"all": true}
func (h *Handler) RunNuisanceCheck(w http.ResponseWriter, r *http.Request) {
	if h.nuisance == nil {
		writeError(w, http.StatusServiceUnavailable, "Nuisance service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	user := middleware.EffectiveUser(r)

	var body struct {
		LocationIDs []string `json:"locationIds"`
		All         bool     `json:"all"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !body.All && len(body.LocationIDs) == 0 {
		writeError(w, http.StatusBadRequest, "locationIds or all:true required")
		return
	}

	job := h.nuisance.StartCheck(user, nuisance.CheckRequest{
		TripID:      tripID,
		LocationIDs: body.LocationIDs,
		All:         body.All,
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

// GetNuisanceCheck returns stored nuisance check results.
// GET /trips/{tripId}/nuisance-check - all locations
// GET /trips/{tripId}/nuisance-check/{locationId} - single location
func (h *Handler) GetNuisanceCheck(w http.ResponseWriter, r *http.Request) {
	if h.nuisance == nil {
		writeError(w, http.StatusServiceUnavailable, "Nuisance service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	locationID := chi.URLParam(r, "locationId")

	if locationID != "" {
		result, err := h.nuisance.GetResult(tripID, locationID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"result": nil})
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}

	results, err := h.nuisance.GetResults(tripID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// ── Admin-check & Health-check ───────────────────────────────────────────────

// RunAdminCheck runs the formalities admin-check pipeline, stores results, and returns them.
// POST /trips/{tripId}/admin-check
func (h *Handler) RunAdminCheck(w http.ResponseWriter, r *http.Request) {
	if h.formalities == nil {
		writeError(w, http.StatusServiceUnavailable, "Formalities service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	result, err := h.formalities.AdminCheck(tripID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Store result in construction_checks
	dataBytes, _ := json.Marshal(result)
	check := models.ConstructionCheck{
		TripID: tripID,
		Kind:   "admin",
		Data:   string(dataBytes),
	}
	h.db.Create(&check)
	h.touchTrip(tripID)

	writeJSON(w, http.StatusOK, result)
}

// GetAdminCheck returns the last cached admin-check result for a trip.
// GET /trips/{tripId}/admin-check
func (h *Handler) GetAdminCheck(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	var check models.ConstructionCheck
	if err := h.db.Where("trip_id = ? AND kind = ?", tripID, "admin").Order("created_at DESC").First(&check).Error; err != nil {
		writeError(w, http.StatusNotFound, "No admin-check result found")
		return
	}

	var result any
	if err := json.Unmarshal([]byte(check.Data), &result); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to parse stored result")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"result":   result,
		"cached":   true,
		"cachedAt": check.CreatedAt,
	})
}

// RunHealthCheck runs the formalities health-check pipeline, stores results, and returns them.
// POST /trips/{tripId}/health-check
func (h *Handler) RunHealthCheck(w http.ResponseWriter, r *http.Request) {
	if h.formalities == nil {
		writeError(w, http.StatusServiceUnavailable, "Formalities service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	result, err := h.formalities.HealthCheck(tripID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Store result in construction_checks
	dataBytes, _ := json.Marshal(result)
	check := models.ConstructionCheck{
		TripID: tripID,
		Kind:   "health",
		Data:   string(dataBytes),
	}
	h.db.Create(&check)
	h.touchTrip(tripID)

	writeJSON(w, http.StatusOK, result)
}

// GetHealthCheck returns the last cached health-check result for a trip.
// GET /trips/{tripId}/health-check
func (h *Handler) GetHealthCheck(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	var check models.ConstructionCheck
	if err := h.db.Where("trip_id = ? AND kind = ?", tripID, "health").Order("created_at DESC").First(&check).Error; err != nil {
		writeError(w, http.StatusNotFound, "No health-check result found")
		return
	}

	var result any
	if err := json.Unmarshal([]byte(check.Data), &result); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to parse stored result")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"result":   result,
		"cached":   true,
		"cachedAt": check.CreatedAt,
	})
}
