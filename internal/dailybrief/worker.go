package dailybrief

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// sendWindowMinutes: fire when local time is in [want, want+window).
// Exact-minute match alone drops sends if a tick is late (pod restart, GC, slow prior tick).
// Idempotence is daily_brief_sends(sent=true); retries inside the window are cheap skips.
const sendWindowMinutes = 15

// Worker is an in-process minute ticker (no k8s CronJob).
// Auto-send is retired (WorkerEnabled default false). Admin POST /brief/send remains.
// For each enabled trip it evaluates "is it send time in THIS day's TZ?"
// (trip.briefSendTime if set, else ops sendLocalHour/Minute)
// so cross-timezone itineraries fire at local morning for that day.
type Worker struct {
	DB      *gorm.DB
	Service *Service
	every   time.Duration
	nowFn   func() time.Time // tests
}

// WorkerEnabled reports whether the morning WhatsApp auto-send should run.
// Default: off. Set TRIPKIT_DAILY_BRIEF_WORKER=1 only for explicit ops/tests.
func WorkerEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("TRIPKIT_DAILY_BRIEF_WORKER"))
	switch strings.ToLower(raw) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
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
		log.Printf("dailybrief: worker started (tick=%s, window=%dm, in-process — not k8s CronJob)", w.every, sendWindowMinutes)
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
	cfg := w.Service.cfg()
	globalHour, globalMin := cfg.SendHourMinute()
	nowUTC := w.nowFn().UTC()

	var trips []models.Trip
	if err := w.DB.Find(&trips).Error; err != nil {
		return
	}
	for _, trip := range trips {
		enabled, group := TripFlags(trip)
		if !enabled || group == "" {
			continue
		}
		wantHour, wantMin := globalHour, globalMin
		if h, m, ok := TripBriefSendTime(trip); ok {
			wantHour, wantMin = h, m
		}
		if trip.StartDate == nil || trip.EndDate == nil {
			continue
		}
		start, err1 := time.Parse("2006-01-02", *trip.StartDate)
		end, err2 := time.Parse("2006-01-02", *trip.EndDate)
		if err1 != nil || err2 != nil {
			continue
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
			if !inSendWindow(localNow, wantHour, wantMin, sendWindowMinutes) {
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
			log.Printf("dailybrief: due %s day=%d tz=%s target=%02d:%02d local=%s now=%s",
				trip.ID, dayNumber, tzName, wantHour, wantMin, dateStr, localNow.Format("15:04"))
			res, err := w.Service.GenerateAndSend(trip.ID, dayNumber, false)
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

// inSendWindow reports whether localNow falls in [wantHour:wantMin, +windowMins).
// Does not wrap past midnight (morning briefs only; evening edge cases clip at 24:00).
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
