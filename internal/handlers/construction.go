package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/construction"
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
// Body: {"phase": <int>} — required; a body without a `phase` key answers 400.
// Query: ?force=1 skips the QA gate; reserved for admins (403 otherwise).
// A refused transition answers 409 with a structured `blockers` array.
func (h *Handler) TransitionPhase(w http.ResponseWriter, r *http.Request) {
	if h.construction == nil {
		writeError(w, http.StatusServiceUnavailable, "Construction service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")

	// The target is decoded as a pointer on purpose: `phase` is optional as far
	// as encoding/json is concerned, and phase 0 ("not started") is a valid
	// target, so an `int` cannot tell an absent key from a deliberate 0. A body
	// with a typo'd field name would then rewind the trip to "not started" and
	// write an audit row for it. Presence is checked here, range in the service.
	var body struct {
		Phase *int `json:"phase"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Phase == nil {
		writeError(w, http.StatusBadRequest, "phase is required")
		return
	}

	force := r.URL.Query().Get("force") == "1"
	user := middleware.EffectiveUser(r)

	// A QA gate that any authenticated user can skip is not a gate: forcing is
	// an admin override, and the audit record only means something if the
	// override was authorized in the first place.
	if force && !(config.IsAdmin(user) || isRequestAdmin(r)) {
		writeError(w, http.StatusForbidden, "admin_required")
		return
	}

	state, code, err := h.construction.TransitionPhase(tripID, *body.Phase, force, user)
	if err != nil {
		var blocked *construction.TransitionBlockedError
		if errors.As(err, &blocked) {
			blockers := blocked.Blockers
			if blockers == nil {
				blockers = []construction.QAViolation{}
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    "transition_blocked",
				"blockers": blockers,
			})
			return
		}
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

	// Store result (upsert: delete old per trip+kind, then insert new).
	resultJSON := construction.QAResultJSON(violations)
	h.db.Where("trip_id = ? AND kind = ?", tripID, "qa").Delete(&models.ConstructionCheck{})
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

// CreateProfileRequest validates a travel-profile edit request. The write path
// (a Leo job editing the profile section in the seed) is not wired yet, so the
// endpoint answers 501 not_implemented instead of faking a completed job.
// POST /trips/{tripId}/travel-profile/request
// Body: {"target": "travelStyle|budgetRules|interests|mealPattern|lessons", "text": "..."}
func (h *Handler) CreateProfileRequest(w http.ResponseWriter, r *http.Request) {
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

	// TODO(hermes): wire a real Leo/Hermes job that edits the travel profile
	// section in the seed, then persist a models.ConstructionProfileRequest row
	// and answer 202 with its jobId. Until then the endpoint must not claim
	// success: it used to start a canned job and store a row stuck at
	// status "running" forever.
	//
	// When the LLM call is wired, body.Text MUST travel through
	// leo.WrapUserRequest: it delimits user-supplied text as data and neutralizes
	// a smuggled </user_request>, so the text can never be read as instructions.
	// The helper and its tests exist (internal/leo/prompts.go,
	// TestWrapUserRequest) precisely so this requirement is not a comment.
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error":  "not_implemented",
		"detail": "La modification du profil voyageur n'est pas encore branchée : aucune écriture n'a été effectuée.",
	})
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

// RetainDiscoveryItem validates a "retain this discovery item" request. The
// write path (a Leo job adding the item to trip.activities with
// bookingStatus:"candidate") is not wired yet, so the endpoint answers 501
// not_implemented rather than a 202 no client can tell apart from real work.
// POST /trips/{tripId}/discovery/retain
func (h *Handler) RetainDiscoveryItem(w http.ResponseWriter, r *http.Request) {
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

	// TODO(hermes): wire a real Leo/Hermes job that writes body.Item into
	// trip.activities with bookingStatus:"candidate", then answer 202 with its
	// jobId. Until then the endpoint must not claim success: a 202 followed by
	// delta/done frames is indistinguishable from real work, so the UI painted
	// "Retenu ✓" for a write that never happened.
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error":  "not_implemented",
		"detail": "L'ajout d'une activité dans le seed n'est pas encore branché : aucune écriture n'a été effectuée.",
	})
}

// ── Nuisance Seed Pin ────────────────────────────────────────────────────────

// PinNuisanceToSeed handles the "Épingler dans le seed" button of the nuisance
// results view. The write path (a Leo job writing hotels[].nuisance and
// trip.construction.lastQa into the seed) is not wired yet, so the endpoint
// answers 501 not_implemented instead of a fake 202.
// POST /trips/{tripId}/nuisance-check/pin
func (h *Handler) PinNuisanceToSeed(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	// TODO(hermes): wire a real Leo/Hermes job that writes hotels[].nuisance and
	// trip.construction.lastQa into the seed, then answer 202 with its jobId.
	// Until then the endpoint must not claim success: it only emitted a canned
	// stream, which no client could tell apart from a completed write.
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error":  "not_implemented",
		"detail": "L'épinglage des nuisances dans le seed n'est pas encore branché : aucune écriture n'a été effectuée.",
	})
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

	// Store result in construction_checks (upsert: delete old per trip+kind, then insert new).
	h.db.Where("trip_id = ? AND kind = ?", tripID, "admin").Delete(&models.ConstructionCheck{})
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

	// Store result in construction_checks (upsert: delete old per trip+kind, then insert new).
	h.db.Where("trip_id = ? AND kind = ?", tripID, "health").Delete(&models.ConstructionCheck{})
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
