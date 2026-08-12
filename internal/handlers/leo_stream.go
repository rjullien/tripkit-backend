package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// LeoChatStream starts a detached Léo job and SSE-subscribes to it.
// While the phone is connected, events (delta | tool | done | error | meta)
// stream live — same UX as before. Disconnect does not cancel Hermes.
func (h *Handler) LeoChatStream(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var req leo.ChatRequest
	if err := parseBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
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
	pctx := leoPromptContext(h.publishReg, user, admin, req.TripID)

	job := h.leoJobs.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		err := h.runLeo(ctx, pctx, req, emit)
		if err == nil {
			return nil
		}
		ev := mapLeoStreamErr(err, ctx)
		_ = emit("error", ev)
		return nil
	})
	streamLeoJob(w, r, job, 0)
}

// LeoJobStream reconnects to an existing job (catch-up after `after` + live).
func (h *Handler) LeoJobStream(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	job, ok := h.leoJobForUser(w, r, user)
	if !ok {
		return
	}
	after, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("after")))
	streamLeoJob(w, r, job, after)
}

// LeoJobCancel stops Hermes. HTTP disconnect must not call this.
func (h *Handler) LeoJobCancel(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	job, ok := h.leoJobForUser(w, r, user)
	if !ok {
		return
	}
	job.Cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobId": job.ID})
}

func (h *Handler) leoJobForUser(w http.ResponseWriter, r *http.Request, user string) (*leo.Job, bool) {
	id := chi.URLParam(r, "jobId")
	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "jobId required")
		return nil, false
	}
	job := h.leoJobs.Get(id)
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "Session Léo expirée. Réessaie.",
			"code":  "job_not_found",
		})
		return nil, false
	}
	if !strings.EqualFold(job.User, user) {
		writeError(w, http.StatusForbidden, "Access denied")
		return nil, false
	}
	return job, true
}

func (h *Handler) runLeo(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
	if h.leoRun != nil {
		return h.leoRun(ctx, pctx, req, emit)
	}
	return leo.LoadConfigFromEnv().StreamChat(ctx, pctx, req, emit)
}

func streamLeoJob(w http.ResponseWriter, r *http.Request, job *leo.Job, after int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var writeMu sync.Mutex
	writeEv := func(ev leo.LoggedEvent) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := r.Context().Err(); err != nil {
			return err
		}
		b, err := json.Marshal(ev.Data)
		if err != nil {
			return err
		}
		if ev.Event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", ev.Event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	hbStop := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-r.Context().Done():
				return
			case <-t.C:
				writeMu.Lock()
				_, _ = fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()
	defer close(hbStop)

	backlog, live, unsub := job.Subscribe(after)
	defer unsub()

	terminal := func(ev leo.LoggedEvent) bool {
		return ev.Event == "done" || ev.Event == "error"
	}

	for _, ev := range backlog {
		if err := writeEv(ev); err != nil {
			return
		}
		if terminal(ev) {
			return
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-live:
			if err := writeEv(ev); err != nil {
				return
			}
			if terminal(ev) {
				return
			}
		case <-job.Done():
			for {
				select {
				case ev := <-live:
					_ = writeEv(ev)
					if terminal(ev) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

func mapLeoStreamErr(err error, ctx context.Context) leo.StreamEvent {
	msg := err.Error()
	code := "leo_chat_failed"
	userErr := "Échec Léo. Réessaie."
	if strings.Contains(msg, "not configured") {
		code = "missing_hermes_key"
		userErr = "Léo non configuré (clé Hermes)."
	} else if strings.Contains(msg, "required") || strings.Contains(msg, "must be") {
		userErr = "Message invalide."
	} else if ctx.Err() == context.Canceled {
		code = "cancelled"
		userErr = "Annulé."
	} else if strings.Contains(msg, "Timeout") || strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline") {
		code = "timeout"
		userErr = "Léo met trop longtemps. Réessaie ou Dashboard."
	} else if strings.Contains(msg, "unreachable") || strings.Contains(msg, "HTML error page") ||
		strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 503") {
		userErr = "Hermes injoignable. Réessaie plus tard."
	}
	return leo.StreamEvent{Error: userErr, Code: code, Detail: msg}
}
