package models

import (
	"time"

	"gorm.io/gorm"
)

// PublishJob tracks an asynchronous seed publish request.
type PublishJob struct {
	ID            string     `gorm:"primaryKey;size:64" json:"id"`
	SourceID      string     `gorm:"not null;index;size:64" json:"source_id"`
	TripID        string     `gorm:"not null;index;size:255" json:"trip_id"`
	SeedPath      string     `gorm:"not null;size:255" json:"seed_path"`
	Operation     string     `gorm:"not null;size:32" json:"operation"` // create | update
	Status        string     `gorm:"not null;index;size:32" json:"status"`
	Stage         string     `gorm:"size:64" json:"stage"`
	Progress      int        `gorm:"not null;default:0" json:"progress"`
	RequestedBy   string     `gorm:"not null;size:255" json:"requested_by"`
	ExpectedSHA   string     `gorm:"size:64" json:"expected_sha"`
	GitSHA        string     `gorm:"size:64" json:"git_sha"`
	ConfirmCreate bool       `gorm:"not null;default:false" json:"confirm_create"`
	ErrorsJSON    string     `gorm:"type:json" json:"-"`
	WarningsJSON  string     `gorm:"type:json" json:"-"`
	SummaryJSON   string     `gorm:"type:json" json:"-"`
	DataVersion   *int64     `json:"data_version"`
	ErrorCode     string     `gorm:"size:64" json:"error_code"`
	CreatedAt     time.Time  `gorm:"autoCreateTime" json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
}

// BeforeSave ensures Postgres json columns never receive '' (SQLSTATE 22P02).
func (j *PublishJob) BeforeSave(tx *gorm.DB) error {
	if j.ErrorsJSON == "" {
		j.ErrorsJSON = "[]"
	}
	if j.WarningsJSON == "" {
		j.WarningsJSON = "[]"
	}
	if j.SummaryJSON == "" {
		j.SummaryJSON = "null"
	}
	return nil
}
