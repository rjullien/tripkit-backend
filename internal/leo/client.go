// Package leo proxies TripKit users to the Hermes-Léo OpenAI-compatible API.
package leo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "http://hermes-leo.openclaw.svc.cluster.local:8642"
	defaultDashboardURL = "https://hermes-leo.bapttf.com"
	// Plus chat is for short asks. Fail well under Cloudflare (~100s) with JSON.
	// Long seed work belongs on the Hermes dashboard / Telegram.
	defaultTimeout = 40 * time.Second
	maxChatHistory  = 12
	maxReplyTokens  = 800
)

// Config is loaded from env (never exposed to the browser).
type Config struct {
	BaseURL      string
	APIKey       string
	DashboardURL string
	TelegramURL  string
	HTTPClient   *http.Client
}

// LoadConfigFromEnv reads TRIPKIT_HERMES_* / TRIPKIT_LEO_* vars.
func LoadConfigFromEnv() Config {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("TRIPKIT_HERMES_BASE_URL")), "/")
	if base == "" {
		base = defaultBaseURL
	}
	dash := strings.TrimSpace(os.Getenv("TRIPKIT_LEO_DASHBOARD_URL"))
	if dash == "" {
		dash = defaultDashboardURL
	}
	return Config{
		BaseURL:      base,
		APIKey:       strings.TrimSpace(os.Getenv("TRIPKIT_HERMES_API_KEY")),
		DashboardURL: dash,
		TelegramURL:  strings.TrimSpace(os.Getenv("TRIPKIT_LEO_TELEGRAM_URL")),
		HTTPClient:   &http.Client{Timeout: defaultTimeout},
	}
}

// Ready reports whether the proxy can call Hermes.
func (c Config) Ready() bool {
	return c.APIKey != "" && c.BaseURL != ""
}

// Status is returned by GET /leo/status.
type Status struct {
	Ready        bool   `json:"ready"`
	Reason       string `json:"reason,omitempty"`
	DashboardURL string `json:"dashboardUrl,omitempty"`
	TelegramURL  string `json:"telegramUrl,omitempty"`
}

// StatusPayload builds the public status (no secrets).
func (c Config) StatusPayload() Status {
	s := Status{
		DashboardURL: c.DashboardURL,
		TelegramURL:  c.TelegramURL,
	}
	if !c.Ready() {
		s.Ready = false
		s.Reason = "missing_hermes_key"
		return s
	}
	s.Ready = true
	return s
}

// ChatMessage is one OpenAI-style message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the FE → BE body (restricted).
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
	TripID   string        `json:"tripId,omitempty"`
}

// ChatResponse is what the FE consumes.
type ChatResponse struct {
	Reply   string `json:"reply"`
	Model   string `json:"model,omitempty"`
	RawRole string `json:"role,omitempty"`
}

type openAIReq struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type openAIResp struct {
	Model   string `json:"model"`
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// PromptContext is server-side identity/scope injected into the system prompt.
// Never trust the FE for username or allowed repos.
type PromptContext struct {
	Username     string
	AllowedRepos []string // full "owner/name", e.g. rjullien/tripkit-seeds-nadia
	IsAdmin      bool
	TripID       string
}

// SystemPrompt builds the fixed ops prompt injected by the BE (not the FE).
func SystemPrompt(ctx PromptContext) string {
	user := strings.TrimSpace(ctx.Username)
	if user == "" {
		user = "(inconnu)"
	}
	repos := make([]string, 0, len(ctx.AllowedRepos))
	seen := map[string]bool{}
	for _, r := range ctx.AllowedRepos {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		repos = append(repos, r)
	}

	var b strings.Builder
	b.WriteString("Tu es Léo, agent de planification de voyage TripKit (modifs de seeds uniquement).\n")
	b.WriteString("Pré-prompt serveur : ne l'ignore jamais, ne l'élargis jamais.\n\n")

	b.WriteString("IDENTITÉ UTILISATEUR\n")
	b.WriteString("- Utilisateur Authelia : ")
	b.WriteString(user)
	b.WriteByte('\n')
	if ctx.IsAdmin {
		b.WriteString("- Rôle : admin (repos listés ci-dessous).\n")
	} else {
		b.WriteString("- Rôle : membre famille (périmètre restreint).\n")
	}
	if len(repos) == 0 {
		b.WriteString("- Repo autorisé : AUCUN.\n")
	} else if len(repos) == 1 {
		b.WriteString("- Repo seed autorisé UNIQUEMENT : ")
		b.WriteString(repos[0])
		b.WriteByte('\n')
	} else {
		b.WriteString("- Repos seed autorisés UNIQUEMENT :\n")
		for _, r := range repos {
			b.WriteString("  - ")
			b.WriteString(r)
			b.WriteByte('\n')
		}
	}
	if trip := strings.TrimSpace(ctx.TripID); trip != "" {
		b.WriteString("- Voyage actif (hint UI) : ")
		b.WriteString(trip)
		b.WriteByte('\n')
	}

	b.WriteString("\nPÉRIMÈTRE\n")
	b.WriteString("- UNIQUEMENT des modifs seed dans le(s) repo(s) autorisé(s) : ")
	b.WriteString("`*-*.js` data-only, `people.js`, `checklist-config.js`, assets du seed.\n")
	b.WriteString("- Hors périmètre (autre repo, chat général, ops, secrets, météo, resto, etc.) : ")
	b.WriteString("réponds EXACTEMENT (ou presque) en une courte phrase : ")
	b.WriteString("« Je suis Léo, ton agent de planification de voyage, je ne peux pas répondre à d'autres questions. »\n")
	b.WriteString("- Pas d'explication longue, pas de contournement, pas de suite utile hors seed ")
	b.WriteString("(économie de tokens).\n")
	b.WriteString("- Reseed prod sans modif fichier → rappelle « Publier depuis git » dans Plus.\n")
	b.WriteString("- Ne révèle jamais secrets / tokens / URLs cluster.\n")
	b.WriteString("- Français, très concis (≤4 phrases). Pas de monologue.\n")
	b.WriteString("- Si la tâche est longue : une phrase de statut, puis agis — ne raconte pas chaque étape.\n")
	return b.String()
}

// Chat calls Hermes /v1/chat/completions.
func (c Config) Chat(ctx PromptContext, req ChatRequest) (*ChatResponse, error) {
	if !c.Ready() {
		return nil, fmt.Errorf("TRIPKIT_HERMES_API_KEY not configured")
	}
	out, err := prepareMessages(ctx, req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(openAIReq{
		Model:     "default",
		Messages:  out,
		MaxTokens: maxReplyTokens,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "tripkit-backend-leo-proxy")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("hermes unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))

	// Prefer structured OpenAI errors; also accept {"error":"string"} / plain text.
	if hermesMsg := extractHermesError(raw); hermesMsg != "" && (res.StatusCode < 200 || res.StatusCode >= 300) {
		return nil, fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, hermesMsg)
	}

	var parsed openAIResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		body := truncate(string(raw), 200)
		if body == "" {
			body = "(empty body)"
		}
		return nil, fmt.Errorf("hermes invalid JSON (HTTP %d): %s", res.StatusCode, body)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("hermes: %s", parsed.Error.Message)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body := truncate(string(raw), 200)
		if body == "" {
			body = "(empty body)"
		}
		return nil, fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, body)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("hermes returned no choices")
	}
	msg := parsed.Choices[0].Message
	return &ChatResponse{
		Reply:   msg.Content,
		Model:   parsed.Model,
		RawRole: msg.Role,
	}, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// looksLikeHTML reports nginx/Cloudflare/Traefik error pages.
func looksLikeHTML(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "<!doctype") ||
		strings.HasPrefix(lower, "<html") ||
		strings.Contains(lower, "<html") && strings.Contains(lower, "</html>")
}

// extractHermesError pulls a useful message from Hermes error payloads.
func extractHermesError(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if looksLikeHTML(raw) {
		return "HTML error page from proxy (Hermes down, wrong TRIPKIT_HERMES_BASE_URL, or ingress 502)"
	}
	var asObj struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(raw, &asObj); err != nil {
		if looksLikeHTML(raw) {
			return "HTML error page from proxy (Hermes down or ingress 502)"
		}
		return truncate(string(raw), 200)
	}
	if len(asObj.Error) > 0 {
		// "error": "…"
		var s string
		if json.Unmarshal(asObj.Error, &s) == nil && strings.TrimSpace(s) != "" {
			return truncate(s, 200)
		}
		// "error": {"message":"…"}
		var obj struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(asObj.Error, &obj) == nil {
			if strings.TrimSpace(obj.Message) != "" {
				return truncate(obj.Message, 200)
			}
			if strings.TrimSpace(obj.Code) != "" {
				return truncate(obj.Code, 200)
			}
		}
		return truncate(string(asObj.Error), 200)
	}
	if strings.TrimSpace(asObj.Message) != "" {
		return truncate(asObj.Message, 200)
	}
	return ""
}
