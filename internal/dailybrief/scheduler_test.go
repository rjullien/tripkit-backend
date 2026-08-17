package dailybrief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
)

func TestWorkerEnabled_DefaultOn(t *testing.T) {
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "")
	if !WorkerEnabled() {
		t.Fatal("auto WhatsApp worker must be on by default")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "0")
	if WorkerEnabled() {
		t.Fatal("0 must disable the auto worker")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "false")
	if WorkerEnabled() {
		t.Fatal("false must disable the auto worker")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "1")
	if !WorkerEnabled() {
		t.Fatal("1 must keep the auto worker on")
	}
}

func TestAPIStartsDailyBriefWorkerWhenEnabled(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "api", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "dailybrief.WorkerEnabled()") {
		t.Fatal("cmd/api must gate the auto worker on WorkerEnabled()")
	}
	if !strings.Contains(s, `(&dailybrief.Worker{DB: db, Service: briefSvc}).Start()`) {
		t.Fatal("Start() must remain in cmd/api (in-process, not a k8s CronJob)")
	}
}

func TestDueForSend_SameDayCatchUp(t *testing.T) {
	loc := time.UTC
	at := func(h, m int) time.Time {
		return time.Date(2026, 8, 17, h, m, 0, 0, loc)
	}
	if dueForSend(at(8, 44), 8, 45) {
		t.Fatal("08:44 is before target")
	}
	if !dueForSend(at(8, 45), 8, 45) {
		t.Fatal("08:45 should send")
	}
	if !dueForSend(at(9, 1), 8, 45) {
		t.Fatal("09:01 must catch up a missed 15-min window")
	}
	if !dueForSend(at(12, 0), 8, 45) {
		t.Fatal("noon must catch up the same local day")
	}
	next := time.Date(2026, 8, 18, 0, 2, 0, 0, loc)
	if dueForSend(next, 8, 45) {
		t.Fatal("next-day 00:02 must not send yesterday's brief")
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
