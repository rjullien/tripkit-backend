// Package formalities implements admin-check and health-check rule engines.
// Country detection from seed data (locations + flights + transit), nationality
// crossing, and embedded rules database with ops overrides.
package formalities

// AdminCheckItem represents a single administrative requirement check result.
type AdminCheckItem struct {
	Country   string   `json:"country"`
	Type      string   `json:"type"`
	Label     string   `json:"label"`
	Status    string   `json:"status"` // "ok", "warning", "action_required"
	AppliesTo []string `json:"appliesTo"`
	Detail    string   `json:"detail"`
	URL       string   `json:"url,omitempty"`
	Cost      string   `json:"cost,omitempty"`
	// Deadline is the administrative lead time ("72h", "3 jours"), required by
	// SPEC §7.1 ("avec échéance et lien officiel").
	Deadline string `json:"deadline,omitempty"`
}

// Traveler is one person on the trip, with the passports they hold.
// Nationalities is per-person on purpose: crossing the *union* of the group's
// passports against a destination hides real requirements (a FR-only traveler
// still needs an ESTA even when a FR+US bi-national travels with them).
type Traveler struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Nationalities []string `json:"nationalities"`
}

// TravelerChecklist is the per-traveler admin checklist mandated by SPEC §7.1
// ("une checklist par voyageur, statut ✅ / ⚠️ / ❌").
type TravelerChecklist struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Nationalities []string         `json:"nationalities"`
	Verdict       string           `json:"verdict"`
	Items         []AdminCheckItem `json:"items"`
}

// HealthCheckItem represents a single health advisory item.
type HealthCheckItem struct {
	Country string `json:"country"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Status  string `json:"status"` // "ok", "warning", "action_required"
	Detail  string `json:"detail"`
}

// AdminCheckResult holds the full admin-check output for a trip.
// Summary is the optional LLM synthesis of Items: omitempty keeps the envelope
// byte-identical to the contract fixtures when no Completer is configured.
type AdminCheckResult struct {
	Verdict   string   `json:"verdict"` // "ok", "warning", "action_required"
	Countries []string `json:"countries"`
	// Travelers is extra (SPEC §7.1). The frontend contract reads items[] and
	// regroups client-side; travelers[] alone is rejected. Both are populated.
	Travelers []TravelerChecklist `json:"travelers,omitempty"`
	Items     []AdminCheckItem    `json:"items"`
	Summary   string              `json:"summary,omitempty"`
}

// HealthCheckResult holds the full health-check output for a trip.
// Summary is the optional LLM synthesis of Items (see AdminCheckResult).
type HealthCheckResult struct {
	Verdict   string            `json:"verdict"` // "none", "ok", "warning", "action_required"
	Countries []string          `json:"countries"`
	Items     []HealthCheckItem `json:"items"`
	// Summary is empty when Verdict is "none" (SPEC §7.2 silence) or when no Completer is configured.
	Summary string `json:"summary,omitempty"`
}

// worstVerdict returns the most severe verdict from a list of statuses.
func worstVerdict(statuses []string) string {
	worst := "ok"
	for _, s := range statuses {
		switch s {
		case "action_required":
			return "action_required"
		case "warning":
			worst = "warning"
		}
	}
	return worst
}
