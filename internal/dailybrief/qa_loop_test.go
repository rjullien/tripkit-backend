package dailybrief

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGenerateOpts_QACorrectionOnce(t *testing.T) {
	calls := 0
	formatCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		var req bifrostReq
		_ = json.Unmarshal(body, &req)
		sys := ""
		if len(req.Messages) > 0 {
			sys = req.Messages[0].Content
		}
		text := "TODO bad mercredi 15 avril"
		switch {
		case strings.Contains(sys, "filtres des titres"):
			text = `[{"title":"Expo du jour","source":"Le Soleil"}]`
		case strings.Contains(sys, "Corrige"):
			text = "📅 *mercredi 15 avril*\n🏨 SpringHill check-in\n• 08:00 - Depart\n⭐ *À savoir*\n• Fait local\n📰 *Actualité*\n• Expo du jour\n💡 *Astuce pratique*\nPrendre le parapluie"
		default:
			// First Format fails QA; later Format (if any) stays bad unless Corrige above.
			formatCalls++
			if formatCalls == 1 {
				text = "TODO bad mercredi 15 avril"
			}
		}
		_ = json.NewEncoder(w).Encode(bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: text}}}})
	}))
	defer srv.Close()

	db, err := gorm.Open(sqlite.Open("file:qaloop?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.DailyBriefSend{}, &models.DailyBriefUsedTip{})
	start := "2026-04-13"
	td, _ := json.Marshal(map[string]any{
		"dailyBrief": true, "whatsappGroup": "120363000000000001@g.us",
		"locations": map[string]any{"mtl": map[string]any{"tz": "America/Toronto"}},
	})
	tds := string(td)
	_ = db.Create(&models.Trip{ID: "loop-2026", Name: "Loop", StartDate: &start, Data: &tds}).Error
	dd, _ := json.Marshal(map[string]any{
		"title": "J3", "locationId": "mtl",
		"timeline": []map[string]any{{"time": "08:00", "label": "Depart"}},
		"hotel":    map[string]any{"name": "SpringHill"},
	})
	_ = db.Create(&models.Day{TripID: "loop-2026", DayNum: 3, Data: string(dd)}).Error
	// Also hotel row for extract
	hd, _ := json.Marshal(map[string]any{"name": "SpringHill"})
	_ = db.AutoMigrate(&models.Hotel{})
	_ = db.Create(&models.Hotel{TripID: "loop-2026", DayNum: 3, Data: string(hd)}).Error

	raw, _ := json.Marshal(map[string]any{
		"gowaBaseUrl": "http://127.0.0.1:9", "bifrostBaseUrl": srv.URL + "/v1",
		"briefModel": "test/model", "adminPhone": "15555550100",
		"sendLocalHour": 8, "sendLocalMinute": 0,
	})
	t.Setenv("TRIPKIT_DAILY_BRIEF_JSON", string(raw))
	loader := NewLoaderFromEnv()
	loader.Bootstrap()
	svc := &Service{DB: db, Loader: loader}

	gen, err := svc.GenerateOpts("loop-2026", 3, ExtractOpts{RequireConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if !gen.QALoopUsed {
		t.Fatalf("expected qa loop used, calls=%d qa=%#v text=%q", calls, gen.QA, gen.Text)
	}
	if gen.QA.Verdict == QAFailed {
		t.Fatalf("expected corrected pass/warn, got %s %#v", gen.QA.Verdict, gen.QA)
	}
	if calls < 2 {
		t.Fatalf("expected >=2 bifrost calls, got %d", calls)
	}
	_ = time.Now()
}
