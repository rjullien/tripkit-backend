package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

type discoverySearchBody struct {
	Themes []string        `json:"themes"`
	Scope  discovery.Scope `json:"scope"`
}

// DiscoveryThemes returns the effective catalogue for this trip's family profile.
// GET /trips/{tripId}/discovery/themes
func (h *Handler) DiscoveryThemes(w http.ResponseWriter, r *http.Request) {
	if h.discovery == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Discovery non configuré", "code": "not_ready"})
		return
	}
	tripID := chi.URLParam(r, "tripId")
	themes, err := h.discovery.ThemesForTrip(tripID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Trip not found", "code": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"themes": themes,
		"origin": h.discoveryOrigin(),
	})
}

// DiscoveryCatalog is the template (no family overlay).
// GET /discovery/themes
func (h *Handler) DiscoveryCatalog(w http.ResponseWriter, r *http.Request) {
	cfg := discovery.DefaultConfig()
	if h.discovery != nil && h.discovery.Loader != nil {
		cfg = h.discovery.Loader.Get()
	}
	themes := discovery.EffectiveThemes(cfg.Themes, discovery.ThemePrefs{})
	writeJSON(w, http.StatusOK, map[string]any{
		"themes": themes,
		"origin": cfg.Origin,
	})
}

func (h *Handler) discoveryOrigin() string {
	if h.discovery == nil || h.discovery.Loader == nil {
		return "dogfood"
	}
	return h.discovery.Loader.Get().Origin
}

// DiscoverySearch starts a discovery job (Overpass geo + Léo editorial) and returns {jobId}.
// Subscribe with GET /leo/jobs/{jobId}/stream. Events: theme | result | done | error.
// POST /trips/{tripId}/discovery/search
func (h *Handler) DiscoverySearch(w http.ResponseWriter, r *http.Request) {
	if h.discovery == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Discovery non configuré", "code": "not_ready"})
		return
	}
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	var body discoverySearchBody
	if err := parseBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body", "code": "bad_request"})
		return
	}
	if len(body.Scope.Corridor) == 2 {
		from := strings.TrimSpace(body.Scope.Corridor[0])
		to := strings.TrimSpace(body.Scope.Corridor[1])
		if from == "" || to == "" || from == to {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scope.corridor needs two different location ids", "code": "bad_request"})
			return
		}
		body.Scope.Corridor = []string{from, to}
	} else if body.Scope.DayNum == 0 && strings.TrimSpace(body.Scope.LocationID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scope.dayNum or scope.corridor required", "code": "bad_request"})
		return
	}

	scope := body.Scope
	themes := append([]string(nil), body.Themes...)
	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		res, err := h.discovery.Search(ctx, tripID, scope, themes, func(themeID, label string, items []discovery.Item, cached bool) {
			_ = emit("theme", leo.StreamEvent{
				Text:   label,
				Detail: themeID,
				Tool: map[string]any{
					"themeId": themeID,
					"label":   label,
					"count":   len(items),
					"cached":  cached,
					"items":   items,
				},
			})
		})
		if err != nil {
			code := "overpass_error"
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no coordinates") {
				code = "bad_request"
			}
			_ = emit("error", leo.StreamEvent{Error: err.Error(), Code: code, Detail: err.Error()})
			return nil
		}
		raw, _ := json.Marshal(res)
		_ = emit("result", leo.StreamEvent{Reply: string(raw)})
		return nil
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

// DiscoveryResults returns cached hits (no Overpass).
// GET /trips/{tripId}/discovery/results?dayNum=8
func (h *Handler) DiscoveryResults(w http.ResponseWriter, r *http.Request) {
	if h.discovery == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Discovery non configuré", "code": "not_ready"})
		return
	}
	tripID := chi.URLParam(r, "tripId")
	q := r.URL.Query()
	dayNum, _ := strconv.Atoi(strings.TrimSpace(q.Get("dayNum")))
	loc := strings.TrimSpace(q.Get("locationId"))
	fromLoc := strings.TrimSpace(q.Get("fromLoc"))
	toLoc := strings.TrimSpace(q.Get("toLoc"))
	dateISO := strings.TrimSpace(q.Get("dateISO"))
	if fromLoc == "" || toLoc == "" {
		if raw := strings.TrimSpace(q.Get("corridor")); raw != "" {
			parts := strings.Split(raw, ",")
			if len(parts) == 2 {
				fromLoc = strings.TrimSpace(parts[0])
				toLoc = strings.TrimSpace(parts[1])
			}
		}
	}
	if fromLoc == "" && toLoc == "" && dayNum == 0 && loc == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "dayNum or fromLoc+toLoc required", "code": "bad_request"})
		return
	}
	var themeIDs []string
	if raw := strings.TrimSpace(q.Get("themes")); raw != "" {
		themeIDs = strings.Split(raw, ",")
	}
	sc := discovery.Scope{DayNum: dayNum, LocationID: loc, DateISO: dateISO}
	if fromLoc != "" && toLoc != "" {
		sc.Corridor = []string{fromLoc, toLoc}
	}
	res, err := h.discovery.Results(tripID, sc, themeIDs)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
