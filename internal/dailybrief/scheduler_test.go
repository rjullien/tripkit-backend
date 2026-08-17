package dailybrief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
)

func TestWorkerEnabled_DefaultOff(t *testing.T) {
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "")
	if WorkerEnabled() {
		t.Fatal("auto WhatsApp worker must stay off by default")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "0")
	if WorkerEnabled() {
		t.Fatal("0 must keep the auto worker off")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "false")
	if WorkerEnabled() {
		t.Fatal("false must keep the auto worker off")
	}
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "1")
	if !WorkerEnabled() {
		t.Fatal("1 is the explicit ops escape hatch")
	}
}

func TestAPIDoesNotStartDailyBriefWorkerUnlessEnabled(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "api", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "dailybrief.WorkerEnabled()") {
		t.Fatal("cmd/api must gate the auto worker on WorkerEnabled()")
	}
	if !strings.Contains(s, `(&dailybrief.Worker{DB: db, Service: briefSvc}).Start()`) {
		t.Fatal("Start() must remain behind the WorkerEnabled gate, not a k8s CronJob")
	}
	idxGate := strings.Index(s, "dailybrief.WorkerEnabled()")
	idxStart := strings.Index(s, `(&dailybrief.Worker{DB: db, Service: briefSvc}).Start()`)
	if idxGate < 0 || idxStart < 0 || idxStart < idxGate {
		t.Fatal("Worker.Start must be inside the WorkerEnabled gate")
	}
}

func TestStart_DisabledDoesNotPanicOnNilService(t *testing.T) {
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "")
	w := &Worker{DB: nil, Service: nil}
	w.Start() // must no-op; auto-send must not restart
}

func TestTick_DisabledDoesNotTouchService(t *testing.T) {
	t.Setenv("TRIPKIT_DAILY_BRIEF_WORKER", "0")
	w := &Worker{Service: nil, nowFn: time.Now}
	w.tick() // would panic on w.Service.cfg() if the auto-send restarted
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
