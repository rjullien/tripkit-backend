package dailybrief

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadSharedListSummaries_ProgressAndPriority(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:listsbrief?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.List{}, &models.ListCheck{}, &models.ListCustomItem{})

	data, _ := json.Marshal(map[string]any{
		"id": "checklist-qc", "type": "packing", "title": "Valise",
		"sections": []map[string]any{
			{"title": "Documents & Tech", "items": []map[string]any{
				{"id": "doc-1", "text": "Passeports"},
				{"id": "doc-10", "text": "App Maps hors-ligne"},
			}},
			{"title": "Vêtements", "items": []map[string]any{
				{"id": "vet-1", "text": "T-shirts"},
			}},
		},
	})
	_ = db.Create(&models.List{
		ID: "checklist-qc", TripID: "qc", Type: "packing", Title: "Valise", Data: string(data),
	}).Error
	todo, _ := json.Marshal(map[string]any{
		"id": "avant-qc", "type": "todo", "title": "Avant de partir",
		"sections": []map[string]any{
			{"title": "Maison", "items": []map[string]any{
				{"id": "h1", "text": "Couper l'eau"},
				{"id": "h2", "text": "Piscine"},
			}},
		},
	})
	_ = db.Create(&models.List{
		ID: "avant-qc", TripID: "qc", Type: "todo", Title: "Avant de partir", Data: string(todo),
	}).Error
	// Personal list ignored
	_ = db.Create(&models.List{
		ID: "mine", TripID: "qc", Type: "packing", Title: "Ma valise", OwnerUser: "rene",
		Data: `{"sections":[{"items":[{"id":"x","text":"secret"}]}]}`,
	}).Error
	_ = db.Create(&models.ListCheck{ListID: "checklist-qc", ItemID: "doc-1", Checked: true, UpdatedAt: time.Now().Unix()}).Error

	sums := LoadSharedListSummaries(db, "qc")
	if len(sums) != 2 {
		t.Fatalf("want 2 shared lists, got %#v", sums)
	}
	var packing *ListBriefSummary
	for i := range sums {
		if sums[i].Type == "packing" {
			packing = &sums[i]
		}
	}
	if packing == nil || packing.CheckedItems != 1 || packing.TotalItems != 3 {
		t.Fatalf("packing progress %#v", packing)
	}
	if len(packing.PriorityOpen) == 0 || packing.PriorityOpen[0] != "App Maps hors-ligne" {
		t.Fatalf("priority open %#v", packing.PriorityOpen)
	}
}

func TestBuildPrepContext_Veille(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:prepctx?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.List{}, &models.ListCheck{}, &models.ListCustomItem{})
	data, _ := json.Marshal(map[string]any{
		"sections": []map[string]any{{
			"title": "Documents & Tech",
			"items": []map[string]any{{"id": "d1", "text": "Passeports"}},
		}},
	})
	_ = db.Create(&models.List{
		ID: "c1", TripID: "t", Type: "packing", Title: "Valise", Data: string(data),
	}).Error

	src := &DayBriefData{
		TripID:    "t",
		DayNumber: 0,
		Label:     "Préparation Québec",
		Timeline: []map[string]any{
			{"time": "18:00", "label": "Charge appareils + télécharger apps utiles"},
			{"time": "21:00", "label": "Coucher tôt — vol demain 09:10"},
		},
	}
	ctx := BuildPrepContext(db, src)
	if ctx == nil || ctx.Mode != "j0" {
		t.Fatalf("ctx %#v", ctx)
	}
	if len(ctx.Downloads) == 0 || len(ctx.LastCheck) == 0 {
		t.Fatalf("downloads/lastCheck empty: %#v", ctx)
	}
	if ctx.VisibilityNote == "" || ctx.Comment == "" {
		t.Fatal("missing note/comment")
	}

	src.DayNumber = -1
	ctx = BuildPrepContext(db, src)
	if ctx == nil || ctx.Mode != "j0m1" {
		t.Fatalf("j0m1 ctx %#v", ctx)
	}
}

func TestCandidateDayNumbers_IncludesPrepDays(t *testing.T) {
	start, _ := time.Parse("2006-01-02", "2026-08-14")
	end, _ := time.Parse("2006-01-02", "2026-09-01")
	now, _ := time.Parse(time.RFC3339, "2026-08-12T06:00:00Z") // J0-1
	days := candidateDayNumbers(start, end, now)
	foundM1, found0 := false, false
	for _, d := range days {
		if d == -1 {
			foundM1 = true
		}
		if d == 0 {
			found0 = true
		}
	}
	if !foundM1 || !found0 {
		t.Fatalf("expected day -1 and 0 in %v", days)
	}
}

func TestRunQA_PrepMode(t *testing.T) {
	src := &DayBriefData{
		Weekday: "mercredi",
		Date:    "2026-08-13",
		Prep: &PrepContext{
			Mode: "j0",
			Lists: []ListBriefSummary{{
				Title: "Valise", TotalItems: 10, CheckedItems: 3,
			}},
		},
		PracticalTip: &Tip{Text: "Cochez en partagé"},
	}
	bad := "📅 *mercredi 13 août*\nRien"
	qa := RunQA(bad, src)
	if qa.Verdict != QAFailed {
		t.Fatalf("want FAILED, got %s", qa.Verdict)
	}
	good := "📅 *mercredi 13 août*\n📋 *Listes cloud*\nValise 3/10\n🙈 Je ne vois pas les valises perso — j'espère que c'est fait\n📥 *À télécharger*\nMaps hors-ligne\n✅ *Dernier check*\nPasseports\n💡 *Astuce pratique*\nCochez en partagé"
	qa = RunQA(good, src)
	if qa.Verdict == QAFailed {
		t.Fatalf("want pass/warn, got %#v", qa)
	}
}
