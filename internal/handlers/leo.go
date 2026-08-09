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

	admin := isRequestAdmin(r) || config.IsAdmin(user)
	resp, err := cfg.Chat(leoPromptContext(h.publishReg, user, admin, req.TripID), req)
	if err != nil {
		msg := err.Error()
		status := http.StatusBadGateway
		hint := "Vérifie Hermes (clé, DNS in-cluster, /v1/chat/completions)."
		if strings.Contains(msg, "not configured") {
			status = http.StatusServiceUnavailable
			hint = "Ajoute hermes-api-key dans Infisical → secret tripkit-secrets."
		} else if strings.Contains(msg, "required") || strings.Contains(msg, "must be") {
			status = http.StatusBadRequest
			hint = "Corps invalide: messages[{role,content}] requis."
		} else if strings.Contains(msg, "unreachable") {
			hint = "Le backend n’atteint pas hermes-leo (service / réseau)."
		} else if strings.Contains(msg, "HTML error page") || strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 503") {
			hint = "Hermes/proxy en 502 — vérifie pod hermes-leo, TRIPKIT_HERMES_BASE_URL et hermes-api-key."
		} else if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "unauthorized") {
			hint = "Clé Hermes refusée (API_SERVER_KEY ≠ hermes-api-key)."
		}
		writeJSON(w, status, map[string]any{
			"error": msg,
			"code":  "leo_chat_failed",
			"hint":  hint,
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
