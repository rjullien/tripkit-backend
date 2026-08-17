package dailybrief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
)

// The morning WhatsApp brief is an in-process ticker in cmd/api, not a
// Kubernetes CronJob and not an OpenClaw/Hermes cron. This test locks that
// wiring so the retired agent cron cannot be "replaced" by moving the
// scheduler out of the API process.
func TestAPIStartsInProcessDailyBriefWorker(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "api", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	want := `(&dailybrief.Worker{DB: db, Service: briefSvc}).Start()`
	if !strings.Contains(s, want) {
		t.Fatalf("daily brief must start as in-process Worker in cmd/api; do not move it to a k8s/OpenClaw cron")
	}
	if strings.Contains(s, "kind: CronJob") {
		t.Fatal("cmd/api must not embed a CronJob for daily brief")
	}
}

func TestTripFlags_WorkerSkipsUnlessEnabledAndGroup(t *testing.T) {
	raw := `{"dailyBrief":false,"whatsappGroup":"120363000000000001@g.us"}`
	en, g := TripFlags(models.Trip{Data: &raw})
	if en {
		t.Fatal("dailyBrief=false must disable the worker even if a group is set")
	}
	if g == "" {
		t.Fatal("group should still parse")
	}

	raw2 := `{"dailyBrief":true}`
	en, g = TripFlags(models.Trip{Data: &raw2})
	if !en {
		t.Fatal("dailyBrief=true")
	}
	if g != "" {
		t.Fatalf("empty group, got %q", g)
	}

	raw3 := `{"dailyBrief":true,"whatsappGroup":"120363000000000001@g.us"}`
	en, g = TripFlags(models.Trip{Data: &raw3})
	if !en || g == "" {
		t.Fatalf("enabled trip must have group, got enabled=%v group=%q", en, g)
	}
}

func TestInSendWindow_NoCatchUpAfterWindow(t *testing.T) {
	// Québec briefSendTime 08:45, 15 min window. After 09:00 the worker must
	// not fire — no OpenClaw-style late catch-up that would "repartir".
	loc := time.UTC
	at := func(h, m int) time.Time {
		return time.Date(2026, 8, 17, h, m, 0, 0, loc)
	}
	if inSendWindow(at(9, 1), 8, 45, sendWindowMinutes) {
		t.Fatal("09:01 must be outside [08:45, 09:00)")
	}
	if inSendWindow(at(12, 0), 8, 45, sendWindowMinutes) {
		t.Fatal("noon must not catch up a missed morning window")
	}
	if !inSendWindow(at(8, 50), 8, 45, sendWindowMinutes) {
		t.Fatal("08:50 should be inside the send window")
	}
}
