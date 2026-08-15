package models

import "time"

// ConstructionPhaseLog records each phase transition for audit and debugging.
type ConstructionPhaseLog struct {
	ID       uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID   string    `gorm:"not null;index;size:255" json:"trip_id"`
	Phase    int       `gorm:"not null" json:"phase"`
	ForcedBy string    `gorm:"size:255" json:"forced_by"`
	Blockers string    `gorm:"type:json" json:"blockers"`
	At       time.Time `gorm:"not null" json:"at"`
}
