package dailybrief

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	kindCulture      = "culture_express"
	kindFoodGeneric  = "food_generic"
	kindPratiqueCash = "pratique_cash"
	kindPlaceFact    = "place_fact"
	// Timing is intentionally NOT anti-redite: same operational reminder stays useful each stay day
	// (like weather) — not a "content anecdote" that feels like a remake.
)

// UsedTip is one anti-redite row (key + text for LLM context).
type UsedTip struct {
	Key  string
	Kind string
	Text string
}

// LoadUsedTips returns tips already sent for this trip (optionally filtered by kind).
func LoadUsedTips(db *gorm.DB, tripID, kind string) []UsedTip {
	if db == nil || strings.TrimSpace(tripID) == "" {
		return nil
	}
	var rows []models.DailyBriefUsedTip
	q := db.Where("trip_id = ?", tripID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]UsedTip, 0, len(rows))
	for _, r := range rows {
		out = append(out, UsedTip{Key: r.TipKey, Kind: r.Kind, Text: r.TipText})
	}
	return out
}

// LoadUsedTipKeys returns tip keys already sent for this trip (anti-redite).
func LoadUsedTipKeys(db *gorm.DB, tripID, kind string) []string {
	tips := LoadUsedTips(db, tripID, kind)
	out := make([]string, 0, len(tips))
	for _, t := range tips {
		out = append(out, t.Key)
	}
	return out
}

func usedTexts(tips []UsedTip) []string {
	out := make([]string, 0, len(tips))
	for _, t := range tips {
		if s := strings.TrimSpace(t.Text); s != "" {
			out = append(out, s)
		} else if t.Key != "" {
			out = append(out, t.Key)
		}
	}
	return out
}

func usedOfKind(tips []UsedTip, kind string) []UsedTip {
	var out []UsedTip
	for _, t := range tips {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

func usedKeySet(tips []UsedTip) map[string]bool {
	m := map[string]bool{}
	for _, t := range tips {
		m[t.Key] = true
	}
	return m
}

// RecordUsedTip stores a tip key/text after a successful WhatsApp send (survives reseed).
func RecordUsedTip(db *gorm.DB, tripID, kind, key, text string, dayNumber int) error {
	if db == nil || tripID == "" || key == "" {
		return nil
	}
	if kind == "" {
		kind = kindCulture
	}
	row := models.DailyBriefUsedTip{
		TripID:    tripID,
		TipKey:    key,
		Kind:      kind,
		TipText:   strings.TrimSpace(text),
		DayNumber: dayNumber,
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "tip_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"tip_text", "kind", "day_number"}),
	}).Create(&row).Error
}

// ClearUsedTips removes anti-redite history for a trip (call after last-day send).
func ClearUsedTips(db *gorm.DB, tripID string) error {
	if db == nil || tripID == "" {
		return nil
	}
	return db.Where("trip_id = ?", tripID).Delete(&models.DailyBriefUsedTip{}).Error
}

// IsLastTripDay is true when dayNumber is the last calendar day of the trip.
func IsLastTripDay(db *gorm.DB, tripID string, dayNumber int) bool {
	if db == nil || dayNumber < 1 {
		return false
	}
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		return false
	}
	if trip.StartDate == nil || trip.EndDate == nil {
		return false
	}
	start, err1 := time.Parse("2006-01-02", *trip.StartDate)
	end, err2 := time.Parse("2006-01-02", *trip.EndDate)
	if err1 != nil || err2 != nil {
		return false
	}
	maxDay := int(end.Sub(start).Hours()/24) + 1
	return dayNumber == maxDay
}

func tipKeyFromText(kind, text string) string {
	norm := normalizeRedite(text)
	sum := sha1.Sum([]byte(kind + "|" + norm))
	prefix := kind
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return prefix + "." + hex.EncodeToString(sum[:8])
}

func placeFactKey(text string) string {
	return tipKeyFromText(kindPlaceFact, text)
}

func normalizeRedite(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'é', 'è', 'ê', 'ë':
			b.WriteRune('e')
		case 'à', 'â':
			b.WriteRune('a')
		case 'ù', 'û':
			b.WriteRune('u')
		case 'ô':
			b.WriteRune('o')
		case 'î', 'ï':
			b.WriteRune('i')
		case 'ç':
			b.WriteRune('c')
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// isRedite reports whether text is too close to any already-used tip.
func isRedite(text string, used []string) bool {
	n := normalizeRedite(text)
	if n == "" {
		return false
	}
	for _, u := range used {
		un := normalizeRedite(u)
		if un == "" {
			continue
		}
		if n == un {
			return true
		}
		// Substring overlap (paraphrase / same anecdote).
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
		if len(t) >= 4 {
			set[t] = true
		}
	}
	if len(set) == 0 {
		return 0
	}
	hit := 0
	for _, t := range tb {
		if len(t) >= 4 && set[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(set))
}

func filterPlaceFacts(facts []string, used []UsedTip) []string {
	if len(facts) == 0 {
		return facts
	}
	placeUsed := usedOfKind(used, kindPlaceFact)
	if len(placeUsed) == 0 {
		// Homogeneous slice from tests / callers that already filtered by kind.
		for _, u := range used {
			if u.Kind != "" && u.Kind != kindPlaceFact {
				return facts // mixed non-place kinds → nothing to filter against yet
			}
		}
		placeUsed = used
	}
	texts := usedTexts(placeUsed)
	if len(texts) == 0 {
		return facts
	}
	var out []string
	for _, f := range facts {
		if isRedite(f, texts) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func filterPlaceFactsBySegment(seg map[string][]string, used []UsedTip) map[string][]string {
	if len(seg) == 0 {
		return seg
	}
	out := make(map[string][]string, len(seg))
	for k, facts := range seg {
		filtered := filterPlaceFacts(facts, used)
		if len(filtered) > 0 {
			out[k] = filtered
		}
	}
	return out
}

func recordAntiRediteAfterSend(db *gorm.DB, gen *GenerateResult, tripID string, dayNumber int) {
	if db == nil || gen == nil || gen.Source == nil {
		return
	}
	src := gen.Source
	if tip := src.CultureExpress; tip != nil {
		key := tip.Key
		if key == "" {
			key = tipKeyFromText(kindCulture, tip.Text)
		}
		if err := RecordUsedTip(db, tripID, kindCulture, key, tip.Text, dayNumber); err != nil {
			log.Printf("dailybrief: anti-redite record culture failed: %v", err)
		}
	}
	if tip := src.PracticalTip; tip != nil && tip.Key == kindPratiqueCash {
		if err := RecordUsedTip(db, tripID, kindPratiqueCash, kindPratiqueCash, tip.Text, dayNumber); err != nil {
			log.Printf("dailybrief: anti-redite record cash failed: %v", err)
		}
	}
	for _, tip := range src.Tips {
		if tip.Key == "" {
			continue
		}
		if tip.Kind == kindFoodGeneric || strings.HasPrefix(tip.Key, "food_generic") {
			if err := RecordUsedTip(db, tripID, kindFoodGeneric, tip.Key, tip.Text, dayNumber); err != nil {
				log.Printf("dailybrief: anti-redite record food failed: %v", err)
			}
		}
	}
	for _, f := range src.PlaceFacts {
		if err := RecordUsedTip(db, tripID, kindPlaceFact, placeFactKey(f), f, dayNumber); err != nil {
			log.Printf("dailybrief: anti-redite record place_fact failed: %v", err)
		}
	}
	if IsLastTripDay(db, tripID, dayNumber) {
		if err := ClearUsedTips(db, tripID); err != nil {
			log.Printf("dailybrief: anti-redite clear failed: %v", err)
		} else {
			log.Printf("dailybrief: anti-redite cleared after last day trip=%s day=%d", tripID, dayNumber)
		}
	}
}
