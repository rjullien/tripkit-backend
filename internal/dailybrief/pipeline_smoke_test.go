package dailybrief

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPipeline_GenerateAndSend_Smoke(t *testing.T) {
	var gotPhone, gotMsg string
	gowa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send/message" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req gowaSendReq
		_ = json.Unmarshal(body, &req)
		gotPhone, gotMsg = req.Phone, req.Message
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"SUCCESS","results":{"message_id":"msg-smoke-1","status":"PENDING"}}`))
	}))
	defer gowa.Close()

	bf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		text := "📅 *mercredi 15 avril*\n🏨 SpringHill\n• 08:00 - Depart\n⭐ *À savoir*\n• Fait local\n📰 *Actualité*\n• Expo locale\n💡 *Astuce pratique*\nCash pour le parking"
		resp := bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: text}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer bf.Close()

	db, err := gorm.Open(sqlite.Open("file:dailybrief_smoke?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.DailyBriefSend{}); err != nil {
		t.Fatal(err)
	}

	start := "2026-04-13"
	tripData, _ := json.Marshal(map[string]any{
		"dailyBrief":    true,
		"whatsappGroup": "120363000000000001@g.us",
		"locations": map[string]any{
			"mtl": map[string]any{"tz": "America/Toronto"},
		},
	})
	td := string(tripData)
	if err := db.Create(&models.Trip{
		ID: "smoke-2026", Name: "Smoke", StartDate: &start, Data: &td,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dayData, _ := json.Marshal(map[string]any{
		"title":      "Jour test",
		"locationId": "mtl",
		"timeline": []map[string]any{
			{"time": "08:00", "label": "Depart"},
		},
		"hotel": map[string]any{"name": "SpringHill"},
	})
	if err := db.Create(&models.Day{TripID: "smoke-2026", DayNum: 3, Data: string(dayData)}).Error; err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		GowaBaseURL:    gowa.URL,
		BifrostBaseURL: bf.URL + "/v1",
		BriefModel:     "test/model",
		AdminPhone:     "",
		Origin:         "test",
	}
	loader := &Loader{cfg: cfg, lastFetch: time.Now()}
	svc := &Service{DB: db, Loader: loader}

	res, err := svc.GenerateAndSendOpts("smoke-2026", 3, SendOptions{
		Force: true,
		To:    "15555550100",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !res.Sent || res.MessageID != "msg-smoke-1" {
		t.Fatalf("result %#v", res)
	}
	if gotPhone != "15555550100" {
		t.Fatalf("phone=%q", gotPhone)
	}
	if !strings.Contains(gotMsg, "SpringHill") && !strings.Contains(gotMsg, "mercredi") {
		t.Fatalf("message unexpected: %q", gotMsg)
	}
	if res.QAVerdict == QAFailed {
		t.Fatalf("QA failed: %s", res.QAVerdict)
	}
}

func TestParseOpsDailyBriefJSON_Shape(t *testing.T) {
	// Shape check against private ops file when present (cloud agent multi-repo).
	raw := []byte(`{
	  "gowaBaseUrl": "http://gowa.gowa.svc.cluster.local:3000",
	  "bifrostBaseUrl": "http://bifrost.openclaw.svc.cluster.local:8080/v1",
	  "briefModel": "opencode-go/deepseek-v4-pro",
	  "adminPhone": "15555550100",
	  "sendLocalHour": 8,
	  "sendLocalMinute": 0
	}`)
	c, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	h, m := c.SendHourMinute()
	if h != 8 || m != 0 {
		t.Fatalf("hour=%d minute=%d", h, m)
	}
}

// Real Bifrost + GoWA (set E2E_DAILY_BRIEF=1 + HTTPS_PROXY socks to Tailscale).
// Phones come from env — never hardcode PII in this public repo.
func TestPipeline_RealBifrostGowa(t *testing.T) {
	if os.Getenv("E2E_DAILY_BRIEF") != "1" {
		t.Skip("set E2E_DAILY_BRIEF=1 for live Bifrost/GoWA smoke")
	}
	gowaURL := strings.TrimSpace(os.Getenv("GOWA_URL"))
	bfURL := strings.TrimSpace(os.Getenv("BIFROST_URL"))
	to := strings.TrimSpace(os.Getenv("E2E_TO"))
	if gowaURL == "" || bfURL == "" || to == "" {
		t.Fatal("need GOWA_URL, BIFROST_URL, E2E_TO")
	}

	db, err := gorm.Open(sqlite.Open("file:dailybrief_live?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.DailyBriefSend{}); err != nil {
		t.Fatal(err)
	}
	start := "2026-04-13"
	tripData, _ := json.Marshal(map[string]any{
		"dailyBrief":    true,
		"whatsappGroup": "120363000000000001@g.us",
		"locations":     map[string]any{"mtl": map[string]any{"tz": "America/Toronto"}},
	})
	td := string(tripData)
	if err := db.Create(&models.Trip{ID: "live-smoke", Name: "Live smoke", StartDate: &start, Data: &td}).Error; err != nil {
		t.Fatal(err)
	}
	dayData, _ := json.Marshal(map[string]any{
		"title": "Live smoke day", "locationId": "mtl",
		"timeline": []map[string]any{{"time": "08:00", "label": "Depart"}},
		"hotel":    map[string]any{"name": "SpringHill"},
	})
	if err := db.Create(&models.Day{TripID: "live-smoke", DayNum: 3, Data: string(dayData)}).Error; err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{
		"gowaBaseUrl": gowaURL, "bifrostBaseUrl": bfURL,
		"briefModel": "opencode-go/deepseek-v4-pro", "adminPhone": to,
		"sendLocalHour": 8, "sendLocalMinute": 0,
	})
	t.Setenv("TRIPKIT_DAILY_BRIEF_JSON", string(raw))
	loader := NewLoaderFromEnv()
	loader.Bootstrap()
	svc := &Service{DB: db, Loader: loader}

	res, err := svc.GenerateAndSendOpts("live-smoke", 3, SendOptions{Force: true, To: to})
	if err != nil {
		t.Fatalf("live send: %v", err)
	}
	if !res.Sent || res.MessageID == "" {
		t.Fatalf("not sent: %#v", res)
	}
	t.Logf("sent ok qa=%s len=%d id=%s", res.QAVerdict, res.MessageLength, res.MessageID)
}
