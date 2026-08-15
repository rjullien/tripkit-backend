package construction

import (
	"encoding/json"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// ConstructionDates holds the trip date window for construction planning.
type ConstructionDates struct {
	StartDate string `json:"startDate,omitempty"`
	Window    string `json:"window,omitempty"`
	Days      int    `json:"days,omitempty"`
	Flexible  bool   `json:"flexible,omitempty"`
}

// QASummary captures the last QA verdict for a construction state.
type QASummary struct {
	At       string   `json:"at,omitempty"`
	Verdict  string   `json:"verdict,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
}

// ConstructionState represents the current construction state of a trip.
type ConstructionState struct {
	Phase          int                `json:"phase"`
	IdeaRef        string             `json:"ideaRef,omitempty"`
	TransportModes []string           `json:"transportModes,omitempty"`
	Dates          *ConstructionDates `json:"dates,omitempty"`
	LastQA         *QASummary         `json:"lastQA,omitempty"`
}

// tripDataEnvelope is the top-level trip.Data JSON structure (only the fields
// we care about for construction state read/write).
type tripDataEnvelope struct {
	Construction *ConstructionState `json:"construction,omitempty"`
	// Preserve all other fields as raw JSON.
	Extra map[string]json.RawMessage `json:"-"`
}

// ReadState reads the construction state from a trip's Data JSON field.
// Returns nil (no error) if the trip has no construction data -- retro-compatible.
func ReadState(db *gorm.DB, tripID string) (*ConstructionState, error) {
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, err
	}
	if trip.Data == nil || *trip.Data == "" {
		return nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*trip.Data), &raw); err != nil {
		return nil, nil
	}

	cRaw, ok := raw["construction"]
	if !ok || len(cRaw) == 0 || string(cRaw) == "null" {
		return nil, nil
	}

	var state ConstructionState
	if err := json.Unmarshal(cRaw, &state); err != nil {
		return nil, nil
	}
	return &state, nil
}

// WriteState updates the construction field inside trip.Data JSON and bumps
// the trip's updated_at timestamp (touchTrip equivalent).
func WriteState(db *gorm.DB, tripID string, state *ConstructionState) error {
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return err
	}

	// Parse existing data or start fresh.
	var raw map[string]json.RawMessage
	if trip.Data != nil && *trip.Data != "" {
		if err := json.Unmarshal([]byte(*trip.Data), &raw); err != nil {
			raw = make(map[string]json.RawMessage)
		}
	} else {
		raw = make(map[string]json.RawMessage)
	}

	// Marshal the construction state and insert it.
	cBytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	raw["construction"] = cBytes

	// Re-serialize the full data envelope.
	dataBytes, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	dataStr := string(dataBytes)

	// Update trip.Data and updated_at in one shot.
	return db.Model(&models.Trip{}).Where("id = ?", tripID).Updates(map[string]any{
		"data":       dataStr,
		"updated_at": time.Now(),
	}).Error
}
