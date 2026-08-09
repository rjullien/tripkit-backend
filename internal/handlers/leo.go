package handlers

import (
	"net/http"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

// LeoStatus returns whether the Hermes proxy is configured (no secrets).
func (h *Handler) LeoStatus(w http.ResponseWriter, r *http.Request) {
	cfg := leo.LoadConfigFromEnv()
	writeJSON(w, http.StatusOK, cfg.StatusPayload())
}

// LeoChat proxies a short chat turn to Hermes-Léo.
func (h *Handler) LeoChat(w http.ResponseWriter, r *http.Request) {
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

	resp, err := cfg.Chat(user, req)
	if err != nil {
		msg := err.Error()
		status := http.StatusBadGateway
		if strings.Contains(msg, "not configured") {
			status = http.StatusServiceUnavailable
		} else if strings.Contains(msg, "required") || strings.Contains(msg, "must be") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{
			"error": msg,
			"code":  "leo_chat_failed",
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
