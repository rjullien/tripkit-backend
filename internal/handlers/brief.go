package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/dailybrief"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// GetDayBrief generates a WhatsApp-format daily brief (no send).
// GET /trips/{tripId}/days/{dayNum}/brief?format=whatsapp
// Admin: ?skipConfig=1 to preview even if trip.dailyBrief not yet in DB.
func (h *Handler) GetDayBrief(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil {
		writeError(w, http.StatusServiceUnavailable, "Daily brief not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil || dayNum < 1 {
		writeError(w, http.StatusBadRequest, "invalid dayNum")
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format != "" && format != "whatsapp" {
		writeError(w, http.StatusBadRequest, "only format=whatsapp supported")
		return
	}

	opts := dailybrief.ExtractOpts{RequireConfigured: true}
	if r.URL.Query().Get("skipConfig") == "1" {
		user := middleware.EffectiveUser(r)
		if !config.IsAdmin(user) && !isRequestAdmin(r) {
			writeError(w, http.StatusForbidden, "skipConfig requires admin")
			return
		}
		opts.RequireConfigured = false
	}

	res, err := h.brief.GenerateOpts(tripID, dayNum, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"text":        res.Text,
		"dayNumber":   res.DayNumber,
		"generatedAt": res.GeneratedAt,
		"weather":     res.Weather,
		"qa":          res.QA,
		"qaLoopUsed":  res.QALoopUsed,
		"timezone":    res.Timezone,
		"sent":        false,
	})
}

type sendBriefBody struct {
	To    string `json:"to"`
	Force bool   `json:"force"`
}

// SendDayBrief generates + QA + GoWA send.
// POST /trips/{tripId}/days/{dayNum}/brief/send
//
// Query: force=1  body optional {"to":"<e164-no-plus>","force":true}
// Admin-only when overriding destination (to=) or skipConfig=1.
// Real phone numbers / group JIDs live in private ops (not this repo).
func (h *Handler) SendDayBrief(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil {
		writeError(w, http.StatusServiceUnavailable, "Daily brief not configured")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil || dayNum < 1 {
		writeError(w, http.StatusBadRequest, "invalid dayNum")
		return
	}

	opt := dailybrief.SendOptions{
		Force: r.URL.Query().Get("force") == "1",
	}
	if r.Body != nil && r.ContentLength != 0 {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body sendBriefBody
		if len(raw) > 0 && json.Unmarshal(raw, &body) == nil {
			opt.To = strings.TrimSpace(body.To)
			if body.Force {
				opt.Force = true
			}
		}
	}
	if to := strings.TrimSpace(r.URL.Query().Get("to")); to != "" {
		opt.To = to
	}
	if r.URL.Query().Get("skipConfig") == "1" {
		opt.SkipConfigGate = true
	}

	user := middleware.EffectiveUser(r)
	admin := config.IsAdmin(user) || isRequestAdmin(r)
	if (opt.To != "" || opt.SkipConfigGate) && !admin {
		writeError(w, http.StatusForbidden, "to= / skipConfig require admin")
		return
	}

	res, err := h.brief.GenerateAndSendOpts(tripID, dayNum, opt)
	if err != nil {
		status := http.StatusBadGateway
		if res != nil && res.QAVerdict == dailybrief.QAFailed {
			status = http.StatusConflict
		}
		if res != nil {
			writeJSON(w, status, res)
			return
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
