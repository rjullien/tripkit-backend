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

func TestIsRedite_Overlap(t *testing.T) {
	used := []string{"Au Québec on dit bienvenue pour de rien. Pourboire resto ~15 %."}
	if !isRedite("Au Québec on dit « bienvenue » pour « de rien ». Pourboire resto ~15 % si service non inclus.", used) {
		t.Fatal("expected redite on paraphrase")
	}
	if isRedite("Le dépanneur québécois = épicerie de coin ouverte tard.", used) {
		t.Fatal("different tip must not be redite")
	}
}

func TestFilterPlaceFacts(t *testing.T) {
	used := []UsedTip{{
		Kind: kindPlaceFact,
		Key:  "k1",
		Text: "Québec fut fondée en 1608 par Champlain sur le cap Diamant.",
	}}
	facts := []string{
		"Québec fut fondée en 1608 par Champlain sur le cap Diamant.",
		"Le Château Frontenac domine la terrasse Dufferin.",
	}
	out := filterPlaceFacts(facts, used)
	if len(out) != 1 || out[0] != facts[1] {
		t.Fatalf("got %#v", out)
	}
}

func TestSelectDayTips_FoodGenericAndCashAntiRedite(t *testing.T) {
	data := &DayBriefData{
		PlaceName:  "Québec City",
		Label:      "Québec — journée",
		TravelDay:  false,
		HasKids:    false,
		Highlights: []string{"Vieux-Québec"},
		Timeline:   []map[string]any{{"time": "10:00", "label": "Balade"}},
	}
	SelectDayTips(data, nil)
	if data.PracticalTip == nil || data.PracticalTip.Key != kindPratiqueCash {
		t.Fatalf("expected cash practical tip, got %#v", data.PracticalTip)
	}
	var foodKey string
	for _, tip := range data.Tips {
		if tip.Key == "food_generic.qc.poutine" {
			foodKey = tip.Key
		}
	}
	if foodKey == "" {
		t.Fatal("expected generic quebec food tip")
	}

	used := []UsedTip{
		{Key: kindPratiqueCash, Kind: kindPratiqueCash, Text: "cash"},
		{Key: foodKey, Kind: kindFoodGeneric, Text: "poutine"},
	}
	SelectDayTips(data, used)
	if data.PracticalTip == nil || data.PracticalTip.Key == kindPratiqueCash {
		t.Fatalf("cash tip should rotate after use, got %#v", data.PracticalTip)
	}
	for _, tip := range data.Tips {
		if tip.Key == foodKey {
			t.Fatal("generic food tip must not repeat")
		}
	}
	// Timing still present (not anti-redite).
	hasTiming := false
	for _, tip := range data.Tips {
		if tip.Kind == "timing" {
			hasTiming = true
		}
	}
	if !hasTiming {
		t.Fatal("timing tip should still appear (not anti-redite)")
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

	if err := RecordUsedTip(db, "t1", "culture_express", "qc.bienvenue", "bienvenue tip", 1); err != nil {
		t.Fatal(err)
	}
	keys := LoadUsedTipKeys(db, "t1", "culture_express")
	if len(keys) != 1 || keys[0] != "qc.bienvenue" {
		t.Fatalf("keys %#v", keys)
	}
	tips := LoadUsedTips(db, "t1", "culture_express")
	if len(tips) != 1 || tips[0].Text != "bienvenue tip" {
		t.Fatalf("tips %#v", tips)
	}
	if IsLastTripDay(db, "t1", 2) {
		t.Fatal("day 2 is not last")
	}
	if !IsLastTripDay(db, "t1", 3) {
		t.Fatal("day 3 should be last")
	}

	gen := &GenerateResult{Source: &DayBriefData{
		CultureExpress: &Tip{Kind: "culture_express", Key: "qc.tu", Text: "x"},
		PracticalTip:   &Tip{Kind: "pratique", Key: kindPratiqueCash, Text: "cash line"},
		Tips:           []Tip{{Kind: kindFoodGeneric, Key: "food_generic.qc.poutine", Text: "poutine"}},
		PlaceFacts:     []string{"Fait wiki unique sur Québec."},
	}}
	recordAntiRediteAfterSend(db, gen, "t1", 3) // last day → record then clear
	keys = LoadUsedTipKeys(db, "t1", "")
	if len(keys) != 0 {
		t.Fatalf("expected clear on last day, got %#v", keys)
	}
}
