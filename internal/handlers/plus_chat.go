package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/pluschat"
)

// PlusChatStatus reports whether the Bifrost assistant is ready (ops/plus-chat.json).
func (h *Handler) PlusChatStatus(w http.ResponseWriter, r *http.Request) {
	if h.plusChat == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":  false,
			"reason": "loader_missing",
		})
		return
	}
	cfg := h.plusChat.Get()
	if !cfg.Ready() {
		reason := "disabled"
		if !cfg.Enabled {
			reason = "disabled"
		} else if strings.TrimSpace(cfg.BifrostBaseURL) == "" || strings.TrimSpace(cfg.ChatModel) == "" {
			reason = "missing_config"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":  false,
			"reason": reason,
			"origin": cfg.Origin,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":  true,
		"model":  cfg.ChatModel,
		"origin": cfg.Origin,
	})
}

// PlusChatStream proxies Bifrost SSE as TripKit SSE (delta | done | error).
func (h *Handler) PlusChatStream(w http.ResponseWriter, r *http.Request) {
	user := middleware.EffectiveUser(r)
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if h.plusChat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Assistant non configuré",
			"code":  "loader_missing",
		})
		return
	}

	var req pluschat.ChatRequest
	if err := parseBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	cfg := h.plusChat.Get()
	if !cfg.Ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Assistant désactivé ou mal configuré",
			"code":  "not_ready",
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

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

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

	emit := func(event string, data pluschat.StreamEvent) error {
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

	pctx := pluschat.PromptContext{
		Username: user,
		TripID:   strings.TrimSpace(req.TripID),
	}
	err := cfg.StreamChatDB(ctx, h.db, pctx, req, emit, h.weatherProvider())
	if err != nil {
		msg := err.Error()
		code := "plus_chat_failed"
		if ctx.Err() != nil {
			code = "cancelled"
			msg = "Annulé."
		}
		_ = emit("error", pluschat.StreamEvent{Error: msg, Code: code, Detail: err.Error()})
	}
}
