package construction

// The phase model of construction/SPEC.md §5. The spec defines four working
// phases plus a Live phase; PhaseNotStarted is not a spec phase but the
// retro-compatible default for a trip with no construction state, which the
// frontend labels "Construction pas encore démarrée".
const (
	PhaseNotStarted = 0 // no construction state yet (default)
	PhaseIdeation   = 1 // §5 Phase 1 — Idéation
	PhaseRoute      = 2 // §5 Phase 2 — Tracé
	PhaseHotels     = 3 // §5 Phase 3 — Hôtels
	PhaseActivities = 4 // §5 Phase 4 — Activités
	PhaseLive       = 5 // §5 Phase 5 — Live
)

// ValidPhase reports whether target is a phase the model defines. The order of
// the phases is not enforced: the spec allows going back (Ph3 → Ph2 → Ph3 is an
// assumed loop) and Ph3/Ph4 are explicitly parallelizable, so only the range is
// checked here. The QA gates below decide whether a given move is allowed.
func ValidPhase(target int) bool {
	return target >= PhaseNotStarted && target <= PhaseLive
}

// RedViolations returns the blocking (red) subset of violations.
func RedViolations(violations []QAViolation) []QAViolation {
	var reds []QAViolation
	for _, v := range violations {
		if v.Severity == "red" {
			reds = append(reds, v)
		}
	}
	return reds
}

// CanTransition checks if a phase transition is allowed given QA violations.
// Returns (allowed, blockers). If force is true, always returns (true, nil):
// callers that need to know what was skipped must call RedViolations themselves
// (Service.TransitionPhase does, to fill the audit record).
func CanTransition(violations []QAViolation, targetPhase int, force bool) (bool, []QAViolation) {
	if force {
		return true, nil
	}

	blockers := RedViolations(violations)
	if len(blockers) > 0 {
		return false, blockers
	}
	return true, nil
}
