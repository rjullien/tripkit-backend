package polarsteps

import (
	"log"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

const (
	maxPriors        = 12
	maxPriorRunes    = 700
	historyGraceDays = 5
)

// PriorStep is one already-generated Polarsteps paragraph (LLM anti-redite).
type PriorStep struct {
	Day  int    `json:"day"`
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

func loadPriors(db *gorm.DB, tripID string) []PriorStep {
	if db == nil || strings.TrimSpace(tripID) == "" {
		return nil
	}
	migrateLegacyCaptions(db, tripID)
	var rows []models.PolarstepsStep
	if err := db.Where("trip_id = ?", tripID).Order("seq asc").Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]PriorStep, 0, len(rows))
	for _, r := range rows {
		t := strings.TrimSpace(r.Text)
		if t == "" {
			continue
		}
		out = append(out, PriorStep{Day: r.DayNumber, Seq: r.Seq, Text: clipRunes(t, maxPriorRunes)})
	}
	if len(out) > maxPriors {
		out = out[len(out)-maxPriors:]
	}
	return out
}

func migrateLegacyCaptions(db *gorm.DB, tripID string) {
	var n int64
	if err := db.Model(&models.PolarstepsStep{}).Where("trip_id = ?", tripID).Count(&n).Error; err != nil || n > 0 {
		return
	}
	var old []models.PolarstepsCaption
	if err := db.Where("trip_id = ?", tripID).Order("day_number asc").Find(&old).Error; err != nil {
		return
	}
	seq := 0
	for _, r := range old {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		seq++
		_ = db.Create(&models.PolarstepsStep{
			TripID:    r.TripID,
			DayNumber: r.DayNumber,
			Seq:       seq,
			Kind:      r.Kind,
			Text:      r.Text,
			UserNote:  r.UserNote,
			QAVerdict: r.QAVerdict,
			CreatedAt: r.UpdatedAt,
		}).Error
	}
}

func nextSeq(db *gorm.DB, tripID string) int {
	var max int
	_ = db.Model(&models.PolarstepsStep{}).Where("trip_id = ?", tripID).
		Select("COALESCE(MAX(seq), 0)").Scan(&max)
	return max + 1
}

func saveStep(db *gorm.DB, tripID string, in *Input, text, qa string) error {
	row := models.PolarstepsStep{
		TripID:    tripID,
		DayNumber: in.Day,
		Seq:       nextSeq(db, tripID),
		Kind:      in.Kind,
		Text:      text,
		UserNote:  in.UserNote,
		QAVerdict: qa,
	}
	return db.Create(&row).Error
}

func purgeTripHistory(db *gorm.DB, tripID string) {
	if db == nil || tripID == "" {
		return
	}
	if err := db.Where("trip_id = ?", tripID).Delete(&models.PolarstepsStep{}).Error; err != nil {
		log.Printf("polarsteps: purge steps failed trip=%s: %v", tripID, err)
	}
	if err := db.Where("trip_id = ?", tripID).Delete(&models.PolarstepsCaption{}).Error; err != nil {
		log.Printf("polarsteps: purge captions failed trip=%s: %v", tripID, err)
	}
}

func priorTexts(priors []PriorStep) []string {
	out := make([]string, 0, len(priors))
	for _, p := range priors {
		if s := strings.TrimSpace(p.Text); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n])
}

func filterHappened(happened []Happened, priors []PriorStep) []Happened {
	if len(happened) == 0 || len(priors) == 0 {
		return happened
	}
	blob := fold(strings.Join(priorTexts(priors), " "))
	var out []Happened
	for _, h := range happened {
		d := fold(h.D)
		if d == "" {
			continue
		}
		if blob != "" && strings.Contains(blob, d) {
			continue
		}
		// Also drop if most content words already appeared.
		if happenedAlreadyTold(d, blob) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func happenedAlreadyTold(foldedDesc, priorBlob string) bool {
	if priorBlob == "" || foldedDesc == "" {
		return false
	}
	words, hit := 0, 0
	for _, w := range strings.Fields(foldedDesc) {
		if len([]rune(w)) < 5 {
			continue
		}
		words++
		if strings.Contains(priorBlob, w) {
			hit++
		}
	}
	return words > 0 && hit*2 >= words
}

func isRedite(text string, used []string) bool {
	n := fold(text)
	if n == "" {
		return false
	}
	for _, u := range used {
		un := fold(u)
		if un == "" {
			continue
		}
		if n == un {
			return true
		}
		if len(n) >= 24 && len(un) >= 24 {
			if strings.Contains(n, un) || strings.Contains(un, n) {
				return true
			}
		}
		if tokenOverlap(n, un) >= 0.72 {
			return true
		}
	}
	return false
}

func tokenOverlap(a, b string) float64 {
	ta := strings.Fields(a)
	tb := strings.Fields(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		if len([]rune(t)) >= 4 {
			set[t] = true
		}
	}
	if len(set) == 0 {
		return 0
	}
	hit := 0
	for _, t := range tb {
		if len([]rune(t)) >= 4 && set[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(set))
}
