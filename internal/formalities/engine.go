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
	AppliesTo []string `json:"applies_to"`
	Detail    string   `json:"detail"`
	URL       string   `json:"url,omitempty"`
	Cost      string   `json:"cost,omitempty"`
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
type AdminCheckResult struct {
	Verdict   string           `json:"verdict"` // "ok", "warning", "action_required"
	Countries []string         `json:"countries"`
	Items     []AdminCheckItem `json:"items"`
}

// HealthCheckResult holds the full health-check output for a trip.
type HealthCheckResult struct {
	Verdict   string            `json:"verdict"` // "none", "ok", "warning", "action_required"
	Countries []string          `json:"countries"`
	Items     []HealthCheckItem `json:"items"`
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
