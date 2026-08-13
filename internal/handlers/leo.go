package handlers

import (
	"net/http"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/publish"
)

// LeoStatus returns whether the Hermes proxy is configured (no secrets).
func (h *Handler) LeoStatus(w http.ResponseWriter, r *http.Request) {
	cfg := leo.LoadConfigFromEnv()
	ops := leo.DefaultOpsConfig()
	if h.leoOps != nil {
		ops = h.leoOps.Get()
	}
	writeJSON(w, http.StatusOK, cfg.StatusPayload().WithOps(ops))
}

// LeoChat proxies a short non-streaming chat turn to Hermes-Léo.
// Deprecated for the Plus UI (use LeoChatStream). Kept for curl / fallbacks — do not remove.
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

	admin := isRequestAdmin(r) || config.IsAdmin(user)
	resp, err := h.leoConfig().Chat(leoPromptContext(h.publishReg, user, admin, req.TripID), req)
	if err != nil {
		msg := err.Error()
		status := http.StatusBadGateway
		code := "leo_chat_failed"
		// Short user-facing error — FE shows this alone (no code+hint concat).
		userErr := "Échec Léo. Réessaie."
		if strings.Contains(msg, "not configured") {
			status = http.StatusServiceUnavailable
			code = "missing_hermes_key"
			userErr = "Léo non configuré (clé Hermes)."
		} else if strings.Contains(msg, "required") || strings.Contains(msg, "must be") {
			status = http.StatusBadRequest
			userErr = "Message invalide."
		} else if strings.Contains(msg, "Timeout") || strings.Contains(msg, "deadline exceeded") ||
			strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "context deadline") {
			status = http.StatusGatewayTimeout
			code = "timeout"
			userErr = "Léo met trop longtemps. Demande plus courte, ou Dashboard."
		} else if strings.Contains(msg, "unreachable") {
			userErr = "Hermes injoignable. Réessaie plus tard."
		} else if strings.Contains(msg, "HTML error page") || strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 503") {
			userErr = "Hermes injoignable. Réessaie plus tard."
		} else if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "unauthorized") {
			userErr = "Clé Hermes refusée."
		}
		writeJSON(w, status, map[string]any{
			"error": userErr,
			"code":  code,
			// Keep raw for logs/debug UIs — FE must not dump this by default.
			"detail": msg,
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// leoPromptContext builds the server-side identity/scope for the Hermes system prompt.
func leoPromptContext(reg *publish.Registry, username string, isAdmin bool, tripID string) leo.PromptContext {
	ctx := leo.PromptContext{
		Username: username,
		IsAdmin:  isAdmin,
		TripID:   strings.TrimSpace(tripID),
	}
	if reg == nil {
		// Same dogfood defaults as publish when registry was not attached.
		reg = publish.DefaultDogfoodRegistry()
	}
	for _, src := range reg.ListForUser(username, isAdmin) {
		repo := strings.TrimSpace(src.Repo)
		if repo == "" {
			continue
		}
		ctx.AllowedRepos = append(ctx.AllowedRepos, repo)
	}
	return ctx
}
