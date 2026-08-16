package construction

// SeedPushResult is attached to a successful phase transition so the client
// can tell whether the seed repo was updated. It is response-only: never set
// it before WriteState, or it would be persisted inside trip.Data.
type SeedPushResult struct {
	OK        bool   `json:"ok"`
	Repo      string `json:"repo,omitempty"`
	Path      string `json:"path,omitempty"`
	Ref       string `json:"ref,omitempty"`
	SHA       string `json:"sha,omitempty"`
	Unchanged bool   `json:"unchanged,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SeedGit pushes a typed patch to the family seed repo. Optional on Service:
// nil means tests / no GitHub, and the HTTP body omits seedPush.
type SeedGit interface {
	PushPhase(tripID string, phase int, user string) (*SeedPushResult, error)
}
