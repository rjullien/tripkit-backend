package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// LeoChatStream proxies Hermes SSE (stream:true) as TripKit SSE events.
// Events: delta | tool | done | error  (+ keepalive comments).
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	// Heartbeat so Cloudflare (~100s idle) does not kill silent tool phases.
	hbStop := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
				flusher.Flush()
			}
		}
	}()
	defer close(hbStop)

	emit := func(event string, data leo.StreamEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	admin := isRequestAdmin(r) || config.IsAdmin(user)
	err := cfg.StreamChat(ctx, leoPromptContext(h.publishReg, user, admin, req.TripID), req, emit)
	if err != nil {
		msg := err.Error()
		code := "leo_chat_failed"
		userErr := "Échec Léo. Réessaie."
		if strings.Contains(msg, "not configured") {
			code = "missing_hermes_key"
			userErr = "Léo non configuré (clé Hermes)."
		} else if strings.Contains(msg, "required") || strings.Contains(msg, "must be") {
			userErr = "Message invalide."
		} else if strings.Contains(msg, "Timeout") || strings.Contains(msg, "deadline exceeded") ||
			strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline") {
			code = "timeout"
			userErr = "Léo met trop longtemps. Réessaie ou Dashboard."
		} else if strings.Contains(msg, "unreachable") || strings.Contains(msg, "HTML error page") ||
			strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 503") {
			userErr = "Hermes injoignable. Réessaie plus tard."
		}
		_ = emit("error", leo.StreamEvent{Error: userErr, Code: code, Detail: msg})
	}
}
