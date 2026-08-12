package pluschat

import (
	"fmt"
	"strings"
)

// ChatMessage is one turn in the Plus assistant thread.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the FE → BE body (same shape as Leo for UX parity).
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
	TripID   string        `json:"tripId,omitempty"`
}

// PromptContext is server-side identity injected into the system prompt.
type PromptContext struct {
	Username string
	TripID   string
}

// SystemPrompt builds the fixed assistant prompt (not seed-editing — that's Léo).
func SystemPrompt(ctx PromptContext) string {
	user := strings.TrimSpace(ctx.Username)
	if user == "" {
		user = "(inconnu)"
	}
	var b strings.Builder
	b.WriteString("Tu es l'assistant voyage TripKit (réponses conversationnelles).\n")
	b.WriteString("Tu n'as PAS d'outils : tu ne modifies pas les seeds, pas GitHub, pas WhatsApp.\n")
	b.WriteString("Pour créer/modifier un seed → renvoyer l'utilisateur vers Léo (box Plus).\n\n")
	b.WriteString("IDENTITÉ\n")
	b.WriteString("- Utilisateur Authelia : ")
	b.WriteString(user)
	b.WriteByte('\n')
	if trip := strings.TrimSpace(ctx.TripID); trip != "" {
		b.WriteString("- Voyage actif (hint UI) : ")
		b.WriteString(trip)
		b.WriteByte('\n')
	}
	b.WriteString("\nSTYLE\n")
	b.WriteString("- Français, concis, utile pour planifier / comprendre un voyage.\n")
	b.WriteString("- Pas de jargon infra (Bifrost, pods, Infisical).\n")
	b.WriteString("- Si tu n'es pas sûr : dis-le ; ne invente pas d'horaires / prix / résas.\n")
	return b.String()
}

func prepareMessages(ctx PromptContext, req ChatRequest) ([]ChatMessage, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages required")
	}
	msgs := req.Messages
	if len(msgs) > maxChatHistory {
		msgs = msgs[len(msgs)-maxChatHistory:]
	}
	for i := range msgs {
		msgs[i].Role = strings.TrimSpace(msgs[i].Role)
		msgs[i].Content = strings.TrimSpace(msgs[i].Content)
		if msgs[i].Role == "" || msgs[i].Content == "" {
			return nil, fmt.Errorf("each message needs role and content")
		}
		if msgs[i].Role != "user" && msgs[i].Role != "assistant" {
			return nil, fmt.Errorf("role must be user or assistant")
		}
	}
	promptCtx := ctx
	if strings.TrimSpace(promptCtx.TripID) == "" {
		promptCtx.TripID = strings.TrimSpace(req.TripID)
	}
	out := make([]ChatMessage, 0, len(msgs)+1)
	out = append(out, ChatMessage{Role: "system", Content: SystemPrompt(promptCtx)})
	out = append(out, msgs...)
	return out, nil
}
