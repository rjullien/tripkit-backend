package formalities

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
)

// summaryTimeout bounds the LLM summary call. The deterministic items are the
// product; a slow model must never hold the HTTP response.
const summaryTimeout = 10 * time.Second

// FormatAdminResults uses the Completer to generate a human-readable summary
// of admin check results. Falls back to a structured plain-text if the LLM is unavailable.
func FormatAdminResults(completer bifrost.Completer, result *AdminCheckResult) (string, error) {
	if completer == nil || len(result.Items) == 0 {
		return formatAdminPlain(result), nil
	}

	system := `Tu es un assistant de voyage. Résume les formalités administratives détectées 
pour ce voyage de manière claire et concise. Utilise des emojis pour les statuts.
Réponds en français.`

	user := formatAdminPlain(result)

	summary, err := completeBounded(completer, system, user)
	if err != nil {
		// Soft-fail: return plain text on LLM error or timeout.
		return user, nil
	}
	return summary, nil
}

// FormatHealthResults uses the Completer to generate a human-readable summary
// of health check results.
func FormatHealthResults(completer bifrost.Completer, result *HealthCheckResult) (string, error) {
	if completer == nil || result.Verdict == "none" || len(result.Items) == 0 {
		return formatHealthPlain(result), nil
	}

	system := `Tu es un assistant de voyage. Résume les conseils santé détectés 
pour ce voyage de manière claire et concise. Utilise des emojis.
Réponds en français.`

	user := formatHealthPlain(result)

	summary, err := completeBounded(completer, system, user)
	if err != nil {
		// Soft-fail: return plain text on LLM error or timeout.
		return user, nil
	}
	return summary, nil
}

// completeBounded calls the completer and gives up after summaryTimeout.
// bifrost.Completer takes no context, so a late answer is discarded rather than
// cancelled; the client's own HTTP timeout ends the goroutine.
func completeBounded(completer bifrost.Completer, system, user string) (string, error) {
	type out struct {
		text string
		err  error
	}
	ch := make(chan out, 1)
	go func() {
		text, err := completer.Complete(system, user)
		ch <- out{text: text, err: err}
	}()
	select {
	case res := <-ch:
		return res.text, res.err
	case <-time.After(summaryTimeout):
		log.Printf("formalities: summary timed out after %s, falling back to plain text", summaryTimeout)
		return "", fmt.Errorf("formalities: summary timeout after %s", summaryTimeout)
	}
}

// formatAdminPlain builds a structured plain-text fallback for admin results.
func formatAdminPlain(result *AdminCheckResult) string {
	if len(result.Items) == 0 {
		return "Aucune formalité administrative détectée."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Formalités administratives (pays: %s)\n\n", strings.Join(result.Countries, ", "))

	for _, item := range result.Items {
		emoji := statusEmoji(item.Status)
		fmt.Fprintf(&b, "%s %s (%s) - %s\n", emoji, item.Label, item.Country, item.Detail)
		if item.URL != "" {
			fmt.Fprintf(&b, "   -> %s\n", item.URL)
		}
	}

	return b.String()
}

// formatHealthPlain builds a structured plain-text fallback for health results.
func formatHealthPlain(result *HealthCheckResult) string {
	if result.Verdict == "none" || len(result.Items) == 0 {
		return "Aucun conseil santé particulier pour cette destination."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Conseils santé (pays: %s)\n\n", strings.Join(result.Countries, ", "))

	for _, item := range result.Items {
		emoji := statusEmoji(item.Status)
		fmt.Fprintf(&b, "%s %s - %s\n", emoji, item.Label, item.Detail)
	}

	return b.String()
}

// statusEmoji returns an emoji for the given status.
func statusEmoji(status string) string {
	switch status {
	case "ok":
		return "✅"
	case "warning":
		return "⚠️"
	case "action_required":
		return "❌"
	default:
		return "ℹ️"
	}
}
