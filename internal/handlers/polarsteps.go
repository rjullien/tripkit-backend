package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/polarsteps"
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

// GeneratePolarstepsCaption starts a detached leo.Hub job (same as Discovery /
// Léo Plus). POST returns 202 {jobId} immediately; Bifrost runs off the HTTP
// request so Safari lock / proxy idle cannot kill the generate. Subscribe with
// GET /leo/jobs/{jobId}/stream. The caption is persisted; GET …/caption is the
// store the UI can also poll if the SSE drops.
// POST /trips/{tripId}/polarsteps/caption
func (h *Handler) GeneratePolarstepsCaption(w http.ResponseWriter, r *http.Request) {
	if h.polarsteps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Polarsteps non configuré",
			"code":  "loader_missing",
		})
		return
	}
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	tripID := chi.URLParam(r, "tripId")
	body, err := parsePolarstepsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	note, nowISO := body.UserNote, body.ClientNowISO
	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		type outcome struct {
			res  *polarsteps.Result
			code int
			err  error
		}
		ch := make(chan outcome, 1)
		go func() {
			res, code, err := h.polarsteps.Generate(tripID, note, nowISO)
			ch <- outcome{res: res, code: code, err: err}
		}()
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case o := <-ch:
				return emitPolarstepsOutcome(emit, o.res, o.code, o.err)
			case <-tick.C:
				_ = emit("progress", leo.StreamEvent{Text: "Génération…"})
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

func emitPolarstepsOutcome(emit leo.EmitFunc, res *polarsteps.Result, code int, err error) error {
	if err != nil {
		if code == 0 {
			code = http.StatusInternalServerError
		}
		if code == http.StatusUnprocessableEntity && res != nil {
			_ = emit("error", leo.StreamEvent{
				Error:  err.Error(),
				Code:   "qa_failed",
				Detail: res.QA.Summary,
				Tool:   map[string]any{"qa": res.QA, "day": res.Day, "kind": res.Kind},
			})
			return nil
		}
		_ = emit("error", leo.StreamEvent{
			Error:  err.Error(),
			Code:   polarstepsErrCode(code),
			Detail: err.Error(),
		})
		return nil
	}
	if code == http.StatusUnprocessableEntity {
		sum := ""
		var tool map[string]any
		if res != nil {
			sum = res.QA.Summary
			tool = map[string]any{"qa": res.QA, "day": res.Day, "kind": res.Kind}
		}
		_ = emit("error", leo.StreamEvent{
			Error:  sum,
			Code:   "qa_failed",
			Detail: sum,
			Tool:   tool,
		})
		return nil
	}
	raw, _ := json.Marshal(res)
	_ = emit("done", leo.StreamEvent{Reply: string(raw)})
	return nil
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
