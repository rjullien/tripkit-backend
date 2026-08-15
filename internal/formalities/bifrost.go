package formalities

import (
	"fmt"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
)

// FormatAdminResults uses the Completer to generate a human-readable summary
// of admin check results. Falls back to a structured plain-text if the LLM is unavailable.
func FormatAdminResults(completer bifrost.Completer, result *AdminCheckResult) (string, error) {
	if completer == nil || len(result.Items) == 0 {
		return formatAdminPlain(result), nil
	}

	system := `Tu es un assistant de voyage. Resume les formalites administratives detectees 
pour ce voyage de maniere claire et concise. Utilise des emojis pour les statuts.
Reponds en francais.`

	user := formatAdminPlain(result)

	summary, err := completer.Complete(system, user)
	if err != nil {
		// Soft-fail: return plain text on LLM error.
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

	system := `Tu es un assistant de voyage. Resume les conseils sante detectes 
pour ce voyage de maniere claire et concise. Utilise des emojis.
Reponds en francais.`

	user := formatHealthPlain(result)

	summary, err := completer.Complete(system, user)
	if err != nil {
		// Soft-fail: return plain text on LLM error.
		return user, nil
	}
	return summary, nil
}

// formatAdminPlain builds a structured plain-text fallback for admin results.
func formatAdminPlain(result *AdminCheckResult) string {
	if len(result.Items) == 0 {
		return "Aucune formalite administrative detectee."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Formalites administratives (pays: %s)\n\n", strings.Join(result.Countries, ", "))

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
		return "Aucun conseil sante particulier pour cette destination."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Conseils sante (pays: %s)\n\n", strings.Join(result.Countries, ", "))

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
