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
// Runs QA gates: if blockers (red violations) exist for the target phase,
// the transition is refused unless force=true.
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

	// Run QA to check for blockers.
	tripData := s.loadTripData(tripID)
	var profile map[string]any
	if tp, ok := tripData["travelProfile"].(map[string]any); ok {
		profile = tp
	}
	violations := RunQA(tripData, profile, target)

	// SPEC §8: an untreated red nuisance verdict blocks Ph3 → Ph4.
	violations = append(violations, NuisanceBlockers(LoadNuisanceVerdicts(s.DB, tripID), target)...)

	allowed, blockers := CanTransition(violations, target, force)
	if !allowed {
		blockersJSON, _ := json.Marshal(blockers)
		return nil, http.StatusConflict, fmt.Errorf("transition blocked: %s", string(blockersJSON))
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
	if err := s.DB.Create(&logEntry).Error; err != nil {
		return nil, http.StatusInternalServerError, err
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
