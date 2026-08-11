package dailybrief

import (
	"testing"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPickCultureExpress_SkipsUsedKeys(t *testing.T) {
	first := pickCultureExpress("Québec", "", nil)
	if first == nil || first.Key != "qc.bienvenue" {
		t.Fatalf("first want qc.bienvenue got %#v", first)
	}
	second := pickCultureExpress("Québec", "", []string{"qc.bienvenue"})
	if second == nil || second.Key != "qc.tu" {
		t.Fatalf("second want qc.tu got %#v", second)
	}
	if second.Text == first.Text {
		t.Fatal("anti-redite must change culture text")
	}
}

func TestAntiRedite_RecordAndClear(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:antiredite?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Trip{}, &models.DailyBriefUsedTip{})
	start, end := "2026-08-14", "2026-08-16" // 3 days
	_ = db.Create(&models.Trip{ID: "t1", Name: "T", StartDate: &start, EndDate: &end}).Error

	if err := RecordUsedTip(db, "t1", "culture_express", "qc.bienvenue", 1); err != nil {
		t.Fatal(err)
	}
	keys := LoadUsedTipKeys(db, "t1", "culture_express")
	if len(keys) != 1 || keys[0] != "qc.bienvenue" {
		t.Fatalf("keys %#v", keys)
	}
	if IsLastTripDay(db, "t1", 2) {
		t.Fatal("day 2 is not last")
	}
	if !IsLastTripDay(db, "t1", 3) {
		t.Fatal("day 3 should be last")
	}

	gen := &GenerateResult{Source: &DayBriefData{
		CultureExpress: &Tip{Kind: "culture_express", Key: "qc.tu", Text: "x"},
	}}
	recordAntiRediteAfterSend(db, gen, "t1", 3) // last day → record then clear
	keys = LoadUsedTipKeys(db, "t1", "culture_express")
	if len(keys) != 0 {
		t.Fatalf("expected clear on last day, got %#v", keys)
	}
}
