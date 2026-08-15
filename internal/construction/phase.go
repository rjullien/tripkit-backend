package construction

// CanTransition checks if a phase transition is allowed given QA violations.
// Returns (allowed, blockers). If force is true, always returns (true, nil).
func CanTransition(violations []QAViolation, targetPhase int, force bool) (bool, []QAViolation) {
	if force {
		return true, nil
	}

	var blockers []QAViolation
	for _, v := range violations {
		if v.Severity == "red" {
			blockers = append(blockers, v)
		}
	}

	if len(blockers) > 0 {
		return false, blockers
	}
	return true, nil
}
