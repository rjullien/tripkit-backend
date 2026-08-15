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

// ConstructionProfileRequest tracks a user request to edit the travel profile via Leo.
type ConstructionProfileRequest struct {
	ID        string    `gorm:"primaryKey;size:255" json:"id"`
	TripID    string    `gorm:"not null;index;size:255" json:"trip_id"`
	Target    string    `gorm:"not null;size:100" json:"target"`
	Text      string    `gorm:"not null" json:"text"`
	JobID     string    `gorm:"size:255" json:"job_id"`
	Status    string    `gorm:"not null;size:50" json:"status"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// ConstructionCheck stores QA or other check results for a trip.
type ConstructionCheck struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID    string    `gorm:"not null;index;size:255" json:"trip_id"`
	Kind      string    `gorm:"not null;size:50" json:"kind"`
	TargetID  string    `gorm:"size:255" json:"target_id"`
	Data      string    `gorm:"type:json" json:"data"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
