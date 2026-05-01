// Package models defines GORM models matching the existing SQLite schema.
package models

import (
	"time"
)

// Trip represents a travel trip.
type Trip struct {
	ID        string    `gorm:"primaryKey;size:255" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	Emoji     *string   `json:"emoji"`
	StartDate *string   `json:"start_date"`
	EndDate   *string   `json:"end_date"`
	Data      *string   `gorm:"type:json" json:"data"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Days   []Day   `gorm:"foreignKey:TripID;constraint:OnDelete:CASCADE" json:"-"`
	Hotels []Hotel `gorm:"foreignKey:TripID;constraint:OnDelete:CASCADE" json:"-"`
	Lists  []List  `gorm:"foreignKey:TripID;constraint:OnDelete:CASCADE" json:"-"`
}

// Day represents a single day in a trip.
type Day struct {
	ID     uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID string `gorm:"not null;uniqueIndex:idx_trip_day" json:"trip_id"`
	DayNum int    `gorm:"not null;uniqueIndex:idx_trip_day" json:"day_num"`
	Data   string `gorm:"type:json;not null" json:"data"`
}

// Hotel represents accommodation for a day.
type Hotel struct {
	ID     uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID string `gorm:"not null;index:idx_hotel_trip_day" json:"trip_id"`
	DayNum int    `gorm:"not null;index:idx_hotel_trip_day" json:"day_num"`
	Data   string `gorm:"type:json;not null" json:"data"`
}

// List represents a checklist (packing, shopping, etc.).
type List struct {
	ID        string    `gorm:"primaryKey;size:255" json:"id"`
	TripID    string    `gorm:"not null;index" json:"trip_id"`
	Type      string    `gorm:"not null" json:"type"`
	Title     string    `gorm:"not null" json:"title"`
	Data      string    `gorm:"type:json;not null" json:"data"`
	OwnerUser string    `gorm:"size:255;default:''" json:"owner_user"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Checks      []ListCheck      `gorm:"foreignKey:ListID;constraint:OnDelete:CASCADE" json:"-"`
	CustomItems []ListCustomItem `gorm:"foreignKey:ListID;constraint:OnDelete:CASCADE" json:"-"`
	Hidden      []ListHidden     `gorm:"foreignKey:ListID;constraint:OnDelete:CASCADE" json:"-"`
}

// ListCheck stores per-item check state for sync.
type ListCheck struct {
	ListID    string `gorm:"primaryKey;size:255" json:"list_id"`
	ItemID    string `gorm:"primaryKey;size:255" json:"item_id"`
	Checked   bool   `gorm:"not null;default:false" json:"checked"`
	UpdatedAt int64  `gorm:"not null" json:"updated_at"`
}

// ListCustomItem stores user-added items.
type ListCustomItem struct {
	ID           string `gorm:"primaryKey;size:255" json:"id"`
	ListID       string `gorm:"not null;index" json:"list_id"`
	Text         string `gorm:"not null" json:"text"`
	SectionIndex int    `gorm:"not null;default:0" json:"section_index"`
	CreatedAt    int64  `gorm:"not null" json:"created_at"`
}

// ListHidden stores per-device hidden items.
type ListHidden struct {
	ListID   string `gorm:"primaryKey;size:255" json:"list_id"`
	DeviceID string `gorm:"primaryKey;size:255" json:"device_id"`
	ItemID   string `gorm:"primaryKey;size:255" json:"item_id"`
}

// MagicToken is a one-time invite link token.
type MagicToken struct {
	Token     string    `gorm:"primaryKey;size:64" json:"token"`
	Name      string    `gorm:"not null;size:255" json:"name"`
	Role      string    `gorm:"not null;size:50;default:'viewer'" json:"role"` // admin, viewer
	TripID    string    `gorm:"not null;size:255" json:"trip_id"`
	UsedBy    *string   `gorm:"size:255" json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// IsValid checks if the token can still be used.
func (t MagicToken) IsValid() bool {
	return t.UsedBy == nil && time.Now().Before(t.ExpiresAt)
}
