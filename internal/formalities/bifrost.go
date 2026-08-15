package formalities

import (
	"fmt"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
)

// FormatAdminResults uses the Completer to generate a human-readable summary
// of admin check results. Falls back to a structured plain-text if the LLM is unavailable.
func FormatAdminResults(completer bifrost.Completer, result *AdminCheckResult) (string, error) {
	if completer == nil || (len(result.Items) == 0 && len(result.Travelers) == 0) {
		return formatAdminPlain(result), nil
	}

	system := `Tu es un assistant de voyage. Tu mets en forme une liste de formalites
administratives DEJA calculee. Regles strictes :
- N'ajoute aucune formalite, aucun pays, aucun cout et aucun delai qui ne soit pas dans les donnees.
- N'enleve aucun voyageur : chaque personne doit apparaitre, meme si elle n'a rien a faire.
- Garde le statut fourni pour chaque ligne (emoji), ne le reinterprete pas.
- Organise par voyageur, en une ou deux phrases par personne.
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

	system := `Tu es un assistant de voyage. Tu mets en forme des conseils sante DEJA calcules.
Regles strictes :
- N'ajoute aucun vaccin, aucun traitement et aucune destination absents des donnees.
- Ne donne pas d'avis medical personnalise : renvoie vers un professionnel de sante.
- Garde le statut fourni pour chaque ligne (emoji).
Reponds en francais, en quelques phrases.`

	user := formatHealthPlain(result)

	summary, err := completer.Complete(system, user)
	if err != nil {
		// Soft-fail: return plain text on LLM error.
		return user, nil
	}
	return summary, nil
}

// formatAdminPlain builds a structured plain-text fallback for admin results.
// Grouped by traveler, because that is the unit the user acts on: "who has to
// file an ESTA" is the question, not "does this trip involve an ESTA".
func formatAdminPlain(result *AdminCheckResult) string {
	if len(result.Travelers) == 0 && len(result.Items) == 0 {
		return "Aucune formalite administrative detectee."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Formalites administratives (pays: %s)\n\n", strings.Join(result.Countries, ", "))

	if len(result.Travelers) == 0 {
		for _, item := range result.Items {
			writeAdminItem(&b, item)
		}
		return b.String()
	}

	for _, t := range result.Travelers {
		nats := strings.Join(t.Nationalities, "+")
		if nats == "" {
			nats = "nationalite inconnue"
		}
		fmt.Fprintf(&b, "%s %s (%s)\n", statusEmoji(t.Verdict), t.Name, nats)
		if len(t.Items) == 0 {
			b.WriteString("   Aucune formalite requise.\n")
		}
		for _, item := range t.Items {
			b.WriteString("   ")
			writeAdminItem(&b, item)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func writeAdminItem(b *strings.Builder, item AdminCheckItem) {
	emoji := statusEmoji(item.Status)
	if item.Country != "" {
		fmt.Fprintf(b, "%s %s (%s) - %s\n", emoji, item.Label, item.Country, item.Detail)
	} else {
		fmt.Fprintf(b, "%s %s - %s\n", emoji, item.Label, item.Detail)
	}
	if item.URL != "" {
		fmt.Fprintf(b, "      -> %s\n", item.URL)
	}
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
