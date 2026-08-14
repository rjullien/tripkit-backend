package models

import "time"

// DiscoveryCache stores Overpass (and later editorial) results per trip/scope/theme.
// Survives a reseed — the seed holds decisions, not search hits.
type DiscoveryCache struct {
	TripID    string    `gorm:"primaryKey;size:255" json:"trip_id"`
	ScopeKey  string    `gorm:"primaryKey;size:128" json:"scope_key"`
	ThemeID   string    `gorm:"primaryKey;size:64" json:"theme_id"`
	Payload   string    `gorm:"type:text" json:"payload"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (DiscoveryCache) TableName() string { return "construction_discovery" }
