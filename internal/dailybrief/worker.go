package dailybrief

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Worker is an in-process minute ticker (no k8s CronJob).
// For each enabled trip it evaluates "is it send time in THIS day's TZ?"
// (trip.briefSendTime if set, else ops sendLocalHour/Minute)
// so cross-timezone itineraries fire at local morning for that day.
type Worker struct {
	DB      *gorm.DB
	Service *Service
	every   time.Duration
	nowFn   func() time.Time // tests
}

// Start launches the background loop (non-blocking).
func (w *Worker) Start() {
	if w == nil || w.DB == nil || w.Service == nil {
		return
	}
	if w.every == 0 {
		w.every = time.Minute
	}
	if w.nowFn == nil {
		w.nowFn = time.Now
	}
	go func() {
		log.Printf("dailybrief: worker started (tick=%s, in-process — not k8s CronJob)", w.every)
		t := time.NewTicker(w.every)
		defer t.Stop()
		for range t.C {
			w.tick()
		}
	}()
}

func (w *Worker) tick() {
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
			if localNow.Hour() != wantHour || localNow.Minute() != wantMin {
				continue
			}
			expectedDate := start.AddDate(0, 0, dayNumber-1)
			localDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
			expDate := time.Date(expectedDate.Year(), expectedDate.Month(), expectedDate.Day(), 0, 0, 0, 0, loc)
			if !localDate.Equal(expDate) {
				continue
			}
			if _, err := w.Service.GenerateAndSend(trip.ID, dayNumber, false); err != nil {
				log.Printf("dailybrief: send %s day=%d tz=%s: %v", trip.ID, dayNumber, tzName, err)
			} else {
				log.Printf("dailybrief: sent %s day=%d tz=%s local=%s", trip.ID, dayNumber, tzName, localDate.Format("2006-01-02"))
			}
		}
	}
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
		if d < 1 || d > maxDay {
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
