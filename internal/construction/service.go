package construction

import (
	"net/http"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Service provides construction state operations.
type Service struct {
	DB *gorm.DB
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
// Phase gate validation (blockers) will come in a later feature.
func (s *Service) TransitionPhase(tripID string, target int, force bool, user string) (*ConstructionState, int, error) {
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

	// Update phase.
	state.Phase = target

	// Write state (also touches trip.updated_at).
	if err := WriteState(s.DB, tripID, state); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	// Log the transition.
	forcedBy := ""
	if force {
		forcedBy = user
	}
	logEntry := models.ConstructionPhaseLog{
		TripID:   tripID,
		Phase:    target,
		ForcedBy: forcedBy,
		Blockers: "[]",
		At:       time.Now(),
	}
	if err := s.DB.Create(&logEntry).Error; err != nil {
		return nil, http.StatusInternalServerError, err
	}

	return state, http.StatusOK, nil
}
