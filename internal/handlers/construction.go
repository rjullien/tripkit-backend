package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/nuisance"
	"github.com/rjullien/tripkit-backend/internal/seedgit"
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

	phase := h.constructionPhase(tripID)

	// Run QA
	opts := construction.QAOpts{Phase: phase, Now: time.Now()}
	if h.construction != nil {
		opts = h.construction.QAOpts(phase)
	}
	violations := construction.RunQAWith(tripData, profile, opts)
	if violations == nil {
		violations = []construction.QAViolation{}
	}

	// Store result (upsert: delete old per trip+kind, then insert new).
	resultJSON := construction.QAResultJSON(violations, phase)
	h.db.Where("trip_id = ? AND kind = ?", tripID, "qa").Delete(&models.ConstructionCheck{})
	check := models.ConstructionCheck{
		TripID: tripID,
		Kind:   "qa",
		Data:   resultJSON,
	}
	h.db.Create(&check)
	h.touchTrip(tripID)

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

	violations, storedPhase, phaseOK := construction.ParseStoredQA(check.Data)
	phase := storedPhase
	if !phaseOK {
		phase = h.constructionPhase(tripID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"violations": violations,
		"phase":      phase,
		"count":      len(violations),
		"cached":     true,
		"cachedAt":   check.CreatedAt,
	})
}

func (h *Handler) constructionPhase(tripID string) int {
	if h == nil || h.construction == nil {
		return 0
	}
	state, _, err := h.construction.GetConstruction(tripID)
	if err != nil || state == nil {
		return 0
	}
	return state.Phase
}

// validProfileTargets is the set of allowed targets for profile edit requests.
var validProfileTargets = map[string]bool{
	"travelStyle": true,
	"budgetRules": true,
	"interests":   true,
	"mealPattern": true,
	"lessons":     true,
}

// CreateProfileRequest starts a Léo job in construction:profile-edit that
// edits travel-profile.js. User text always goes through leo.WrapUserRequest.
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

	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	if h.leoRun == nil {
		cfg := leo.LoadConfigFromEnv()
		if !cfg.Ready() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":        "Léo (Hermes) n'est pas configuré côté serveur",
				"code":         "missing_hermes_key",
				"dashboardUrl": cfg.DashboardURL,
				"telegramUrl":  cfg.TelegramURL,
			})
			return
		}
	}

	admin := isRequestAdmin(r) || config.IsAdmin(user)
	pctx := leoPromptContext(h.publishReg, user, admin, tripID)
	pctx.AllowedModes = append(append([]string{}, pctx.AllowedModes...), string(leo.ModeProfileEdit))
	if cc := buildConstructionContext(h.db, tripID); cc != nil {
		pctx.Construction = cc
	}

	repo, file := h.profileEditTarget(tripID)
	req := leo.ChatRequest{
		TripID: tripID,
		Mode:   string(leo.ModeProfileEdit),
		Messages: []leo.ChatMessage{{
			Role:    "user",
			Content: composeProfileEditMessage(repo, file, body.Target, body.Text),
		}},
	}

	row := models.ConstructionProfileRequest{
		ID:     uuid.NewString(),
		TripID: tripID,
		Target: body.Target,
		Text:   body.Text,
		Status: "running",
	}
	if err := h.db.Create(&row).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to record profile request")
		return
	}

	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		err := h.runLeo(ctx, pctx, req, emit)
		status := "done"
		if err != nil {
			status = "error"
			ev := mapLeoStreamErr(err, ctx)
			_ = emit("error", ev)
		}
		h.db.Model(&models.ConstructionProfileRequest{}).Where("id = ?", row.ID).Update("status", status)
		return nil
	})
	h.db.Model(&models.ConstructionProfileRequest{}).Where("id = ?", row.ID).Update("job_id", job.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

func (h *Handler) profileEditTarget(tripID string) (repo, path string) {
	path = "travel-profile.js"
	if h.publishReg == nil {
		return "", path
	}
	target, err := seedgit.LocateTrip(h.publishReg, h.publishManifest, tripID)
	if err != nil {
		return "", path
	}
	return target.Source.Repo, path
}

func composeProfileEditMessage(repo, file, target, text string) string {
	var b strings.Builder
	b.WriteString("Modifie le profil voyageur.\n")
	if strings.TrimSpace(repo) != "" {
		b.WriteString("Dépôt : ")
		b.WriteString(strings.TrimSpace(repo))
		b.WriteString("\n")
	}
	if strings.TrimSpace(file) != "" {
		b.WriteString("Fichier : ")
		b.WriteString(strings.TrimSpace(file))
		b.WriteString("\n")
	}
	b.WriteString("Cible : ")
	b.WriteString(target)
	b.WriteString("\n")
	b.WriteString(leo.WrapUserRequest(text))
	return b.String()
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

// RetainDiscoveryItem writes the item into trip.activities with
// bookingStatus:"candidate" (DB SoT) and best-effort pushes it via seedgit.
// POST /trips/{tripId}/discovery/retain
func (h *Handler) RetainDiscoveryItem(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if h.construction == nil {
		writeError(w, http.StatusServiceUnavailable, "Construction service not configured")
		return
	}

	var body retainItemBody
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	activity, err := construction.BuildCandidateActivity(
		body.Item.ID, body.Item.Name, body.Item.ThemeID,
		body.Item.Lat, body.Item.Lon, body.Item.DistKm,
		body.Item.URL, body.Item.Source,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tripID := chi.URLParam(r, "tripId")
	result, code, err := h.construction.RetainActivity(tripID, user, activity)
	if err != nil {
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, code, result)
}

// ── Nuisance Seed Pin ────────────────────────────────────────────────────────

// PinNuisanceToSeed writes hotels[].nuisance and trip.construction.lastQa
// (DB SoT) and best-effort pushes the same compact summary via seedgit.
// POST /trips/{tripId}/nuisance-check/pin
func (h *Handler) PinNuisanceToSeed(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if h.construction == nil {
		writeError(w, http.StatusServiceUnavailable, "Construction service not configured")
		return
	}

	tripID := chi.URLParam(r, "tripId")
	result, code, err := h.construction.PinNuisance(tripID, user)
	if err != nil {
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, code, result)
}

// ── Nuisance Check ───────────────────────────────────────────────────────────

// RunNuisanceCheck launches a nuisance analysis job for the given locations.
// POST /trips/{tripId}/nuisance-check
// Body: {"locationIds": ["loc1", "loc2"]} or {"all": true} or {"refresh": true}
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
		Refresh     bool     `json:"refresh"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Refresh mode: resolve which locations need re-checking, then run only those.
	if body.Refresh {
		ids, err := h.nuisance.RefreshTargets(tripID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to resolve refresh targets")
			return
		}
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"jobId": "", "message": "Tous les résultats sont à jour."})
			return
		}
		job := h.nuisance.StartCheck(user, nuisance.CheckRequest{
			TripID:      tripID,
			LocationIDs: ids,
			Refresh:     true,
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
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

// AcceptNuisanceCheck marks a nuisance verdict as accepted (user acknowledges the risk).
// POST /trips/{tripId}/nuisance-check/{locationId}/accept
func (h *Handler) AcceptNuisanceCheck(w http.ResponseWriter, r *http.Request) {
	if h.nuisance == nil {
		writeError(w, http.StatusServiceUnavailable, "Nuisance service not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	locationID := chi.URLParam(r, "locationId")

	if err := h.nuisance.AcceptResult(tripID, locationID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
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
