package construction

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Service provides construction state operations.
type Service struct {
	DB      *gorm.DB
	SeedGit SeedGit
	// Ops, when set, is re-read on each QA / phase transition (TTL 2 min).
	Ops *Loader
}

// GetConstruction reads the current construction state for a trip.
// Returns (state, httpStatusCode, error). A nil state with 200 means no
// construction data exists yet (retro-compatible default: phase 0).
func (s *Service) GetConstruction(tripID string) (*ConstructionState, int, error) {
	state, err := ReadState(s.DB, tripID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}
	// Retro-compatible: return default state if none exists.
	if state == nil {
		state = &ConstructionState{Phase: 0}
	}
	return state, http.StatusOK, nil
}

// TransitionPhase moves the construction state to a target phase, optionally
// forced by an admin user. It logs the transition in ConstructionPhaseLog.
// Runs QA gates: if blockers (red violations) exist for the target phase,
// the transition is refused unless force=true.
func (s *Service) TransitionPhase(tripID string, target int, force bool, user string) (*ConstructionState, int, error) {
	// The phase model has a range (construction/SPEC.md §5). Without this check
	// any integer was accepted and persisted, including a phase no gate and no
	// UI knows about.
	if !ValidPhase(target) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid phase %d: must be between %d and %d", target, PhaseNotStarted, PhaseLive)
	}

	// Read current state (or create default).
	state, err := ReadState(s.DB, tripID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}
	if state == nil {
		state = &ConstructionState{Phase: 0}
	}

	// Run QA to check for blockers.
	tripData := s.loadTripData(tripID)
	var profile map[string]any
	if tp, ok := tripData["travelProfile"].(map[string]any); ok {
		profile = tp
	}
	violations := RunQAWith(tripData, profile, s.QAOpts(target))

	// SPEC §8: an untreated red nuisance verdict blocks Ph3 → Ph4.
	violations = append(violations, NuisanceBlockers(LoadNuisanceVerdicts(s.DB, tripID), target)...)

	allowed, blockers := CanTransition(violations, target, force)
	if !allowed {
		return nil, http.StatusConflict, &TransitionBlockedError{Blockers: blockers}
	}

	// Update phase.
	state.Phase = target

	// Log the transition.
	forcedBy := ""
	if force {
		forcedBy = user
		// CanTransition returns no blockers on the forced path, so the audit row
		// used to record WHO overrode the gate but never WHAT was skipped. The
		// red violations computed for the target phase are exactly that.
		blockers = RedViolations(violations)
	}
	blockersStr := "[]"
	if len(blockers) > 0 {
		b, _ := json.Marshal(blockers)
		blockersStr = string(b)
	}
	logEntry := models.ConstructionPhaseLog{
		TripID:   tripID,
		Phase:    target,
		ForcedBy: forcedBy,
		Blockers: blockersStr,
		At:       time.Now(),
	}

	// The new phase and its audit record must land together: a failing log
	// insert has to roll the phase back, otherwise the phase moves while the
	// caller is told the transition failed (and a forced override goes
	// unrecorded).
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		// WriteState also touches trip.updated_at.
		if err := WriteState(tx, tripID, state); err != nil {
			return err
		}
		return tx.Create(&logEntry).Error
	}); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	// Seed repo update is best-effort: the DB is the source of truth. A git
	// failure must not roll back the phase or turn a 200 into an error.
	if s.SeedGit != nil {
		push, err := s.SeedGit.PushPhase(tripID, target, user)
		if err != nil && push == nil {
			push = &SeedPushResult{OK: false, Error: err.Error()}
		}
		if push != nil {
			state.SeedPush = push
		}
	}

	return state, http.StatusOK, nil
}

// loadTripData reads the trip's Data JSON as a map.
func (s *Service) loadTripData(tripID string) map[string]any {
	var trip struct {
		Data *string
	}
	if err := s.DB.Table("trips").Select("data").Where("id = ?", tripID).Scan(&trip).Error; err != nil {
		return make(map[string]any)
	}
	if trip.Data == nil || *trip.Data == "" {
		return make(map[string]any)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		return make(map[string]any)
	}
	return data
}

func (s *Service) QAOpts(phase int) QAOpts {
	opts := QAOpts{Phase: phase, Now: time.Now(), DriveHardLimitMinutes: defaultDriveHardLimitMinutes}
	if s != nil && s.Ops != nil {
		cfg := s.Ops.Get()
		if cfg.QA.DriveHardLimitMinutes > 0 {
			opts.DriveHardLimitMinutes = cfg.QA.DriveHardLimitMinutes
		}
	}
	return opts
}
