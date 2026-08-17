package construction

import (
	"encoding/json"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// nuisanceVerdict is the subset of nuisance.CheckResult this package needs.
// Declared locally rather than importing internal/nuisance, which would create
// an import cycle (nuisance already depends on construction's models).
type nuisanceVerdict struct {
	LocationID   string `json:"locationId"`
	LocationName string `json:"locationName"`
	Verdict      string `json:"verdict"`
	Partial      bool   `json:"partial"`
	Accepted     bool   `json:"accepted"`
}

// NuisanceBlockers turns stored nuisance results into QA violations, per
// SPEC §8: "Gate de phase — verdict 🔴 non traité = blocage du passage Ph3 → Ph4".
//
// Severity is phase-dependent, like the rest of the QA engine:
//   - entering phase 4 (activités) or later, a red verdict is a blocker ;
//   - before that it is a warning, because the hotel may still change.
//
// An indeterminate verdict is reported as a warning at every phase: it is not a
// known problem, but it is not a clean result either, and letting it pass
// silently is what made the check useless in the first place.
func NuisanceBlockers(verdicts []nuisanceVerdict, targetPhase int) []QAViolation {
	var out []QAViolation
	for _, v := range verdicts {
		name := v.LocationName
		if name == "" {
			name = v.LocationID
		}
		// An accepted verdict no longer blocks.
		if v.Accepted {
			continue
		}
		switch v.Verdict {
		case "ELEVE":
			severity := "yellow"
			if targetPhase >= 4 {
				severity = "red"
			}
			out = append(out, QAViolation{
				Code:     "nuisance_unresolved",
				Severity: severity,
				Message:  "Nuisances élevées non traitées : " + name,
				Detail:   "Acceptez le risque dans l'onglet Résa (carte hôtel) ou changez d'hébergement.",
			})
		case "INDETERMINE":
			out = append(out, QAViolation{
				Code:     "nuisance_indeterminate",
				Severity: "yellow",
				Message:  "Analyse de nuisances incomplète : " + name,
				Detail:   "Au moins une catégorie n'a pas pu être évaluée. Rafraîchissez l'analyse (bouton Rafraîchir).",
			})
		default:
			if v.Partial {
				out = append(out, QAViolation{
					Code:     "nuisance_indeterminate",
					Severity: "yellow",
					Message:  "Analyse de nuisances partielle : " + name,
					Detail:   "Au moins une catégorie n'a pas pu être évaluée. Rafraîchissez l'analyse (bouton Rafraîchir).",
				})
			}
		}
	}
	return out
}

// LoadNuisanceVerdicts reads the stored nuisance results for a trip.
// A missing or unparseable row is skipped: the gate degrades to "no nuisance
// data" rather than failing the whole transition.
func LoadNuisanceVerdicts(db *gorm.DB, tripID string) []nuisanceVerdict {
	if db == nil {
		return nil
	}
	var checks []models.ConstructionCheck
	if err := db.Where("trip_id = ? AND kind = ?", tripID, "nuisance").Find(&checks).Error; err != nil {
		return nil
	}
	var out []nuisanceVerdict
	for _, c := range checks {
		var v nuisanceVerdict
		if err := json.Unmarshal([]byte(c.Data), &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}
