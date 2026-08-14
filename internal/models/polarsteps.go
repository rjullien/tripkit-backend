package models

import "time"

// PolarstepsCaption is the last generated Polarsteps journal draft for a trip day.
type PolarstepsCaption struct {
	TripID    string    `gorm:"primaryKey;size:255" json:"trip_id"`
	DayNumber int       `gorm:"primaryKey" json:"day_number"`
	Kind      string    `gorm:"size:16" json:"kind"`
	Text      string    `gorm:"type:text" json:"text"`
	UserNote  string    `gorm:"type:text" json:"user_note"`
	QAVerdict string    `gorm:"size:16" json:"qa_verdict"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
