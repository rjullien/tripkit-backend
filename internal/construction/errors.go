package construction

import "fmt"

// TransitionBlockedError is returned by Service.TransitionPhase when QA gates
// refuse the transition. It carries the blocking violations as structured data
// so the HTTP layer can serialize them instead of stringifying JSON into an
// error message (the frontend renders them with the same badges as the QA list).
type TransitionBlockedError struct {
	Blockers []QAViolation
}

// Error implements the error interface.
func (e *TransitionBlockedError) Error() string {
	return fmt.Sprintf("transition blocked: %d blocker(s)", len(e.Blockers))
}
