package dailybrief

import (
	"log"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LoadUsedTipKeys returns tip keys already sent for this trip (anti-redite).
func LoadUsedTipKeys(db *gorm.DB, tripID, kind string) []string {
	if db == nil || strings.TrimSpace(tripID) == "" {
		return nil
	}
	var rows []models.DailyBriefUsedTip
	q := db.Where("trip_id = ?", tripID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.TipKey)
	}
	return out
}

// RecordUsedTip stores a tip key after a successful WhatsApp send (survives reseed).
func RecordUsedTip(db *gorm.DB, tripID, kind, key string, dayNumber int) error {
	if db == nil || tripID == "" || key == "" {
		return nil
	}
	if kind == "" {
		kind = "culture_express"
	}
	row := models.DailyBriefUsedTip{
		TripID:    tripID,
		TipKey:    key,
		Kind:      kind,
		DayNumber: dayNumber,
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "tip_key"}},
		DoNothing: true,
	}).Create(&row).Error
}

// ClearUsedTips removes anti-redite history for a trip (call after last-day send).
func ClearUsedTips(db *gorm.DB, tripID string) error {
	if db == nil || tripID == "" {
		return nil
	}
	return db.Where("trip_id = ?", tripID).Delete(&models.DailyBriefUsedTip{}).Error
}

// IsLastTripDay is true when dayNumber is the last calendar day of the trip.
func IsLastTripDay(db *gorm.DB, tripID string, dayNumber int) bool {
	if db == nil || dayNumber < 1 {
		return false
	}
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return false
	}
	if trip.StartDate == nil || trip.EndDate == nil {
		return false
	}
	start, err1 := time.Parse("2006-01-02", *trip.StartDate)
	end, err2 := time.Parse("2006-01-02", *trip.EndDate)
	if err1 != nil || err2 != nil {
		return false
	}
	maxDay := int(end.Sub(start).Hours()/24) + 1
	return dayNumber == maxDay
}

func recordAntiRediteAfterSend(db *gorm.DB, gen *GenerateResult, tripID string, dayNumber int) {
	if db == nil || gen == nil || gen.Source == nil {
		return
	}
	if tip := gen.Source.CultureExpress; tip != nil && tip.Key != "" {
		if err := RecordUsedTip(db, tripID, tip.Kind, tip.Key, dayNumber); err != nil {
			log.Printf("dailybrief: anti-redite record failed: %v", err)
		}
	}
	if IsLastTripDay(db, tripID, dayNumber) {
		if err := ClearUsedTips(db, tripID); err != nil {
			log.Printf("dailybrief: anti-redite clear failed: %v", err)
		} else {
			log.Printf("dailybrief: anti-redite cleared after last day trip=%s day=%d", tripID, dayNumber)
		}
	}
}
