package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type polarstepsCaptionBody struct {
	UserNote     string `json:"userNote"`
	ClientNowISO string `json:"clientNowISO"`
}

func clientNowFromQuery(r *http.Request) time.Time {
	now := time.Now()
	q := strings.TrimSpace(r.URL.Query().Get("now"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("clientNowISO"))
	}
	if t, err := time.Parse(time.RFC3339, q); err == nil {
		return t
	}
	return now
}

func parsePolarstepsBody(r *http.Request) (polarstepsCaptionBody, error) {
	var body polarstepsCaptionBody
	if r.Body == nil {
		return body, nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil && err != io.EOF {
		return body, err
	}
	return body, nil
}

// PolarstepsStatus reports whether the Plus Polarsteps box should show.
// GET /trips/{tripId}/polarsteps/status
func (h *Handler) PolarstepsStatus(w http.ResponseWriter, r *http.Request) {
	if h.polarsteps == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"ready":   false,
			"reason":  "loader_missing",
		})
		return
	}
	tripID := chi.URLParam(r, "tripId")
	st, err := h.polarsteps.Status(tripID, clientNowFromQuery(r))
	if err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// PolarstepsCaption returns the last saved draft for the current local day.
// GET /trips/{tripId}/polarsteps/caption
func (h *Handler) PolarstepsCaption(w http.ResponseWriter, r *http.Request) {
	if h.polarsteps == nil {
		writeError(w, http.StatusServiceUnavailable, "Polarsteps non configuré")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	nowQ := strings.TrimSpace(r.URL.Query().Get("now"))
	if nowQ == "" {
		nowQ = strings.TrimSpace(r.URL.Query().Get("clientNowISO"))
	}
	res, err := h.polarsteps.Last(tripID, nowQ)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GeneratePolarstepsCaption runs extract → Bifrost → QA → persist.
// POST /trips/{tripId}/polarsteps/caption
func (h *Handler) GeneratePolarstepsCaption(w http.ResponseWriter, r *http.Request) {
	if h.polarsteps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Polarsteps non configuré",
			"code":  "loader_missing",
		})
		return
	}
	tripID := chi.URLParam(r, "tripId")
	body, err := parsePolarstepsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	res, code, err := h.polarsteps.Generate(tripID, body.UserNote, body.ClientNowISO)
	if err != nil {
		if code == 0 {
			code = http.StatusInternalServerError
		}
		if code == http.StatusUnprocessableEntity && res != nil {
			writeJSON(w, code, map[string]any{
				"error": err.Error(),
				"code":  "qa_failed",
				"qa":    res.QA,
				"day":   res.Day,
				"kind":  res.Kind,
			})
			return
		}
		writeJSON(w, code, map[string]any{
			"error": err.Error(),
			"code":  polarstepsErrCode(code),
		})
		return
	}
	if code == http.StatusUnprocessableEntity {
		writeJSON(w, code, map[string]any{
			"error": res.QA.Summary,
			"code":  "qa_failed",
			"qa":    res.QA,
			"day":   res.Day,
			"kind":  res.Kind,
		})
		return
	}
	if code == 0 {
		code = http.StatusOK
	}
	writeJSON(w, code, res)
}

func polarstepsErrCode(httpCode int) string {
	switch httpCode {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusServiceUnavailable:
		return "not_ready"
	case http.StatusBadGateway:
		return "llm_error"
	default:
		return "error"
	}
}
