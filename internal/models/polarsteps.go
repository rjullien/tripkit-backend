package models

import "time"

// PolarstepsCaption is the V1 one-draft-per-day row (kept for AutoMigrate / read fallback).
type PolarstepsCaption struct {
	TripID    string    `gorm:"primaryKey;size:255" json:"trip_id"`
	DayNumber int       `gorm:"primaryKey" json:"day_number"`
	Kind      string    `gorm:"size:16" json:"kind"`
	Text      string    `gorm:"type:text" json:"text"`
	UserNote  string    `gorm:"type:text" json:"user_note"`
	QAVerdict string    `gorm:"size:16" json:"qa_verdict"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// PolarstepsStep is one published/generated Polarsteps journal paragraph.
// Several rows per day are allowed (afternoon + evening). Purged after trip end.
type PolarstepsStep struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TripID    string    `gorm:"index:idx_ps_steps_trip;size:255;not null" json:"trip_id"`
	DayNumber int       `gorm:"index:idx_ps_steps_trip;not null" json:"day_number"`
	Seq       int       `gorm:"not null" json:"seq"`
	Kind      string    `gorm:"size:16" json:"kind"`
	Text      string    `gorm:"type:text" json:"text"`
	UserNote  string    `gorm:"type:text" json:"user_note"`
	QAVerdict string    `gorm:"size:16" json:"qa_verdict"`
	CreatedAt time.Time `json:"created_at"`
}
