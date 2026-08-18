package dailybrief

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/publish"
	"gorm.io/gorm"
)

// sendWindowMinutes: fire when local time is in [want, want+window).
// Exact-minute match alone drops sends if a tick is late (pod restart, GC, slow prior tick).
// Idempotence is daily_brief_sends(sent=true); retries inside the window are cheap skips.
const sendWindowMinutes = 15

// Worker is an in-process minute ticker (no k8s CronJob).
// Default on. For each enabled trip it evaluates "is it at/after send time
// in THIS day's TZ?" and catch-up the same local day if the 15-min window was missed.
type Worker struct {
	DB      *gorm.DB
	Service *Service
	every   time.Duration
	nowFn   func() time.Time // tests
	// sendFn, when set, replaces Service.GenerateAndSend (unit tests: no Bifrost/LLM).
	sendFn func(tripID string, dayNumber int) (*SendResult, error)
}

// WorkerEnabled reports whether the morning WhatsApp auto-send should run.
// Default: on. Set TRIPKIT_DAILY_BRIEF_WORKER=0 to disable.
func WorkerEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("TRIPKIT_DAILY_BRIEF_WORKER"))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// Start launches the background loop (non-blocking).
func (w *Worker) Start() {
	if w == nil || w.DB == nil || w.Service == nil {
		return
	}
	if !WorkerEnabled() {
		log.Printf("dailybrief: auto worker disabled")
		return
	}
	if w.every == 0 {
		w.every = time.Minute
	}
	if w.nowFn == nil {
		w.nowFn = time.Now
	}
	go func() {
		n := BackfillFlagColumns(w.DB)
		if n > 0 {
			log.Printf("dailybrief: backfilled auto-send columns on %d trip(s)", n)
		}
		log.Printf("dailybrief: worker started (tick=%s, same-day catch-up, in-process — not k8s CronJob)", w.every)
		w.tick() // catch up immediately after restart; ticker waits `every` before first fire
		t := time.NewTicker(w.every)
		defer t.Stop()
		for range t.C {
			w.tick()
		}
	}()
}

func (w *Worker) tick() {
	if !WorkerEnabled() {
		return
	}
	globalHour, globalMin := 8, 0
	if w.Service != nil {
		globalHour, globalMin = w.Service.cfg().SendHourMinute()
	}
	nowFn := w.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	nowUTC := nowFn().UTC()

	var trips []models.Trip
	if err := w.DB.Find(&trips).Error; err != nil {
		return
	}
	for _, trip := range trips {
		if trip.StartDate == nil || trip.EndDate == nil {
			continue
		}
		start, err1 := time.Parse("2006-01-02", *trip.StartDate)
		end, err2 := time.Parse("2006-01-02", *trip.EndDate)
		if err1 != nil || err2 != nil {
			continue
		}
		enabled, group := TripFlags(trip)
		if !enabled || group == "" {
			if !nowUTC.Before(start.UTC().AddDate(0, 0, -2)) && !nowUTC.After(end.UTC().AddDate(0, 0, 2)) {
				logFlagSkip(trip.ID)
			}
			continue
		}
		wantHour, wantMin := globalHour, globalMin
		if h, m, ok := TripBriefSendTime(trip); ok {
			wantHour, wantMin = h, m
		}

		// Scan day numbers that could be "today" in some TZ (±1 day cushion).
		dayNums := candidateDayNumbers(start, end, nowUTC)
		for _, dayNumber := range dayNums {
			tzName := DayTimezone(w.DB, trip, dayNumber)
			loc, err := time.LoadLocation(tzName)
			if err != nil {
				loc = time.UTC
			}
			localNow := nowUTC.In(loc)
			if !dueForSend(localNow, wantHour, wantMin) {
				continue
			}
			expectedDate := start.AddDate(0, 0, dayNumber-1)
			localDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
			expDate := time.Date(expectedDate.Year(), expectedDate.Month(), expectedDate.Day(), 0, 0, 0, 0, loc)
			if !localDate.Equal(expDate) {
				continue
			}
			dateStr := localDate.Format("2006-01-02")
			if HasSentBrief(w.DB, trip.ID, dayNumber, dateStr) {
				continue
			}
			if HasFailedQABrief(w.DB, trip.ID, dayNumber, dateStr) {
				log.Printf("dailybrief: skip %s day=%d qa_failed", trip.ID, dayNumber)
				continue
			}
			log.Printf("dailybrief: due %s day=%d tz=%s target=%02d:%02d local=%s now=%s",
				trip.ID, dayNumber, tzName, wantHour, wantMin, dateStr, localNow.Format("15:04"))
			res, err := w.send(trip.ID, dayNumber)
			if err != nil {
				log.Printf("dailybrief: fail %s day=%d tz=%s: %v", trip.ID, dayNumber, tzName, err)
				continue
			}
			if res != nil && res.Error == "already_sent" {
				log.Printf("dailybrief: skip %s day=%d already_sent", trip.ID, dayNumber)
				continue
			}
			if res != nil && res.Sent {
				log.Printf("dailybrief: sent %s day=%d tz=%s local=%s msg=%s",
					trip.ID, dayNumber, tzName, dateStr, res.MessageID)
			} else {
				log.Printf("dailybrief: skip %s day=%d (not sent)", trip.ID, dayNumber)
			}
		}
	}
}

func (w *Worker) send(tripID string, dayNumber int) (*SendResult, error) {
	if w.sendFn != nil {
		return w.sendFn(tripID, dayNumber)
	}
	if w.Service == nil {
		return nil, fmt.Errorf("dailybrief: service not configured")
	}
	return w.Service.GenerateAndSend(tripID, dayNumber, false)
}

// dueForSend reports whether localNow is at or after wantHour:wantMin on this civil day.
// Catch-up: a missed 15-min window still sends later the same local day.
// Does not wrap past midnight (next-day 00:02 is before 08:45).
func dueForSend(localNow time.Time, wantHour, wantMin int) bool {
	nowM := localNow.Hour()*60 + localNow.Minute()
	wantM := wantHour*60 + wantMin
	return nowM >= wantM
}

// inSendWindow reports whether localNow falls in [wantHour:wantMin, +windowMins).
// Kept for tests; the worker uses dueForSend (same-day catch-up).
func inSendWindow(localNow time.Time, wantHour, wantMin, windowMins int) bool {
	if windowMins < 1 {
		windowMins = 1
	}
	nowM := localNow.Hour()*60 + localNow.Minute()
	wantM := wantHour*60 + wantMin
	endM := wantM + windowMins
	if endM > 24*60 {
		endM = 24 * 60
	}
	return nowM >= wantM && nowM < endM
}

// candidateDayNumbers returns day indices that might match "today" around nowUTC.
func candidateDayNumbers(start, end time.Time, nowUTC time.Time) []int {
	maxDay := int(end.Sub(start).Hours()/24) + 1
	if maxDay < 1 {
		return nil
	}
	// Rough UTC day index ±2 (covers extreme TZ offsets).
	approx := int(nowUTC.UTC().Sub(time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)).Hours()/24) + 1
	var out []int
	seen := map[int]bool{}
	for d := approx - 2; d <= approx+2; d++ {
		// day -1 = J0-1 (2 days before start); day 0 = J0 (veille); 1..maxDay = travel
		if d < -1 || d > maxDay {
			continue
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func parseCronHM(expr string) (min, hour int) {
	min, hour = 30, 6
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) < 2 {
		return
	}
	var m, h int
	if _, err := fmt.Sscanf(fields[0]+" "+fields[1], "%d %d", &m, &h); err == nil {
		return m, h
	}
	return
}

var flagSkipLog sync.Map // tripID -> time.Time

func logFlagSkip(tripID string) {
	now := time.Now()
	if v, ok := flagSkipLog.Load(tripID); ok {
		if now.Sub(v.(time.Time)) < 15*time.Minute {
			return
		}
	}
	flagSkipLog.Store(tripID, now)
	log.Printf("dailybrief: skip %s — dailyBrief/whatsappGroup missing (columns and trip.data)", tripID)
}

// BackfillFlagColumns copies dailyBrief flags from trip.data JSON into columns
// when the columns are still NULL (post-migrate). Safe to run on every Start.
func BackfillFlagColumns(db *gorm.DB) int {
	if db == nil {
		return 0
	}
	var trips []models.Trip
	if err := db.Find(&trips).Error; err != nil {
		return 0
	}
	n := 0
	for _, trip := range trips {
		if trip.DailyBrief != nil && trip.WhatsappGroup != nil && trip.BriefSendTime != nil && trip.HomeTz != nil {
			continue
		}
		data := map[string]any{}
		if trip.Data != nil && *trip.Data != "" {
			_ = json.Unmarshal([]byte(*trip.Data), &data)
		}
		upd := publish.FlagColumnUpdates(data)
		if upd == nil {
			continue
		}
		if trip.DailyBrief != nil {
			delete(upd, "daily_brief")
		}
		if trip.WhatsappGroup != nil {
			delete(upd, "whatsapp_group")
		}
		if trip.BriefSendTime != nil {
			delete(upd, "brief_send_time")
		}
		if trip.HomeTz != nil {
			delete(upd, "home_tz")
		}
		if len(upd) == 0 {
			continue
		}
		if err := db.Model(&trip).Updates(upd).Error; err != nil {
			continue
		}
		n++
	}
	return n
}
