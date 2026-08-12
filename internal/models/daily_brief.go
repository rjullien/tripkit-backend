package models

import "time"

// DailyBriefSend records one successful (or blocked) daily-brief attempt for idempotency.
type DailyBriefSend struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID     string    `gorm:"not null;uniqueIndex:idx_brief_trip_day_date;size:255" json:"trip_id"`
	DayNumber  int       `gorm:"not null;uniqueIndex:idx_brief_trip_day_date" json:"day_number"`
	LocalDate  string    `gorm:"not null;uniqueIndex:idx_brief_trip_day_date;size:10" json:"local_date"` // YYYY-MM-DD
	QAVerdict  string    `gorm:"not null;size:16" json:"qa_verdict"`
	Sent       bool      `gorm:"not null;default:false" json:"sent"`
	WhatsAppTo string    `gorm:"column:whatsapp_to;size:64" json:"whatsapp_to"`
	MessageID  string    `gorm:"size:128" json:"message_id"`
	MessageLen int       `json:"message_len"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// DailyBriefUsedTip stores anti-redite keys for a trip (survives reseed; cleared on last brief).
type DailyBriefUsedTip struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TripID    string    `gorm:"not null;uniqueIndex:idx_brief_used_trip_key;size:255" json:"trip_id"`
	TipKey    string    `gorm:"not null;uniqueIndex:idx_brief_used_trip_key;size:64" json:"tip_key"`
	Kind      string    `gorm:"not null;size:32;index" json:"kind"` // culture_express|food_generic|pratique_cash|place_fact
	TipText   string    `gorm:"type:text" json:"tip_text"`          // original text for LLM "don't repeat" context
	DayNumber int       `gorm:"not null" json:"day_number"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
