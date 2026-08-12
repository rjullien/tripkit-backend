package pluschat

import (
	"fmt"
	"strings"
	"time"
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

// PromptContext is server-side identity + trip facts injected into the system prompt.
type PromptContext struct {
	Username string
	TripID   string
	Trip     *TripContext
}

// SystemPrompt builds the fixed assistant prompt (read-only — seed writes stay with Léo).
func SystemPrompt(ctx PromptContext) string {
	user := strings.TrimSpace(ctx.Username)
	if user == "" {
		user = "(inconnu)"
	}
	var b strings.Builder
	b.WriteString("Tu es l'assistant voyage TripKit (lecture seule).\n")
	b.WriteString("Rôle : questions sur AUJOURD'HUI, DEMAIN et le voyage (météo, hôtel, codes, adresses, bookings, itinéraire).\n")
	b.WriteString("Tu n'as PAS d'outils d'écriture : tu ne modifies pas les seeds, pas GitHub, pas WhatsApp.\n")
	b.WriteString("Pour créer/modifier un seed → renvoyer l'utilisateur vers Léo (box Plus).\n\n")

	b.WriteString("IDENTITÉ\n")
	b.WriteString("- Utilisateur Authelia : ")
	b.WriteString(user)
	b.WriteByte('\n')
	if trip := strings.TrimSpace(ctx.TripID); trip != "" {
		b.WriteString("- Voyage actif : ")
		b.WriteString(trip)
		b.WriteByte('\n')
	}

	b.WriteString("\nRÈGLES\n")
	b.WriteString("- Utilise le bloc CONTEXTE_JSON ci-dessous comme source de vérité.\n")
	b.WriteString("- Interprète « aujourd'hui / demain / lundi / J3 » via nowLocal + calendar + today/tomorrow.\n")
	b.WriteString("- Pour codes pin, wifi, confirmation, adresse hôtel : cite bookings.hotel (et dayBooking) sans inventer.\n")
	b.WriteString("- Si une info manque dans le contexte : dis-le clairement.\n")
	b.WriteString("- Français, concis, utile. Pas de jargon infra.\n")

	if ctx.Trip != nil {
		b.WriteString("\nCONTEXTE_JSON\n")
		b.WriteString(FormatContextJSON(ctx.Trip))
		b.WriteByte('\n')
	} else {
		b.WriteString("\n(Pas de contexte voyage chargé — demande le voyage actif.)\n")
	}
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

// NowFn is overridable in tests.
var NowFn = time.Now
