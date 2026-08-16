package construction

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// CandidateActivity is one discovery item written into trip.activities.
type CandidateActivity struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Theme         string  `json:"theme,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	DistKm        float64 `json:"distKm,omitempty"`
	URL           string  `json:"url,omitempty"`
	Source        string  `json:"source,omitempty"`
	BookingStatus string  `json:"bookingStatus"`
}

// RetainResult is the HTTP envelope for POST /discovery/retain.
type RetainResult struct {
	Activity map[string]any  `json:"activity"`
	SeedPush *SeedPushResult `json:"seedPush,omitempty"`
}

// PinResult is the HTTP envelope for POST /nuisance-check/pin.
type PinResult struct {
	LastQa   map[string]any            `json:"lastQa"`
	Hotels   map[string]map[string]any `json:"hotels,omitempty"`
	SeedPush *SeedPushResult           `json:"seedPush,omitempty"`
}

// BuildCandidateActivity sanitizes a discovery item into a seed activity.
func BuildCandidateActivity(id, name, themeID string, lat, lon, distKm float64, url, source string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("item.name is required")
	}
	actID := sanitizeActivityID(id, name)
	out := map[string]any{
		"id":            actID,
		"name":          name,
		"bookingStatus": "candidate",
	}
	if theme := strings.TrimSpace(themeID); theme != "" {
		out["theme"] = theme
	}
	if lat != 0 {
		out["lat"] = lat
	}
	if lon != 0 {
		out["lon"] = lon
	}
	if distKm != 0 {
		out["distKm"] = distKm
	}
	if u := strings.TrimSpace(url); u != "" {
		out["url"] = u
	}
	if s := strings.TrimSpace(source); s != "" {
		out["source"] = s
	}
	return out, nil
}

func sanitizeActivityID(id, name string) string {
	raw := strings.TrimSpace(id)
	if raw == "" {
		raw = strings.TrimSpace(name)
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == ':' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "activity"
	}
	return s
}

// RetainActivity writes one candidate into trip.Data.activities (DB SoT), then
// best-effort pushes the same entry to the family seed repo.
func (s *Service) RetainActivity(tripID, user string, activity map[string]any) (*RetainResult, int, error) {
	if s == nil || s.DB == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("construction service not configured")
	}
	id, _ := activity["id"].(string)
	if strings.TrimSpace(id) == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("activity id required")
	}

	data, err := s.loadTripDataErr(tripID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}

	acts, _ := data["activities"].(map[string]any)
	if acts == nil {
		acts = map[string]any{}
		data["activities"] = acts
	}
	acts[id] = activity
	if err := s.persistTripData(tripID, data); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	out := &RetainResult{Activity: activity}
	if s.SeedGit != nil {
		push, err := s.SeedGit.PushActivity(tripID, activity, user)
		if err != nil && push == nil {
			push = &SeedPushResult{OK: false, Error: err.Error()}
		}
		if push != nil {
			out.SeedPush = push
		}
	}
	return out, http.StatusOK, nil
}

// PinNuisance copies stored nuisance (and last QA) into trip.Data, then
// best-effort pushes hotels[].nuisance + trip.construction.lastQa to the seed.
func (s *Service) PinNuisance(tripID, user string) (*PinResult, int, error) {
	if s == nil || s.DB == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("construction service not configured")
	}

	data, err := s.loadTripDataErr(tripID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}

	nuisanceRows, qaRow, err := s.loadPinSources(tripID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(nuisanceRows) == 0 && qaRow == nil {
		return nil, http.StatusBadRequest, fmt.Errorf("nothing to pin: run a nuisance check or QA first")
	}

	hotels := hotelDict(data)
	pinnedHotels := map[string]map[string]any{}
	for _, row := range nuisanceRows {
		hotelID := strings.TrimSpace(row.HotelID)
		if hotelID == "" {
			hotelID = strings.TrimSpace(row.LocationID)
		}
		if hotelID == "" {
			continue
		}
		hotel, ok := hotels[hotelID]
		if !ok {
			continue
		}
		compact := compactNuisance(row)
		hotel["nuisance"] = compact
		pinnedHotels[hotelID] = compact
	}

	lastQa := buildLastQa(qaRow, nuisanceRows)
	cons, _ := data["construction"].(map[string]any)
	if cons == nil {
		cons = map[string]any{}
		data["construction"] = cons
	}
	// DB / GET /construction reads lastQA (ConstructionState). The seed file
	// uses lastQa — PushPin writes that key separately.
	cons["lastQA"] = lastQa

	if err := s.persistTripData(tripID, data); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	out := &PinResult{LastQa: lastQa}
	if len(pinnedHotels) > 0 {
		out.Hotels = pinnedHotels
	}
	if s.SeedGit != nil {
		push, err := s.SeedGit.PushPin(tripID, lastQa, pinnedHotels, user)
		if err != nil && push == nil {
			push = &SeedPushResult{OK: false, Error: err.Error()}
		}
		if push != nil {
			out.SeedPush = push
		}
	}
	return out, http.StatusOK, nil
}

type pinNuisanceRow struct {
	LocationID     string
	HotelID        string
	Verdict        string
	Recommendation string
	AnalyzedAt     time.Time
	Categories     []pinCategory
}

type pinCategory struct {
	Category string
	Level    string
	Detail   string
}

func (s *Service) loadPinSources(tripID string) ([]pinNuisanceRow, *models.ConstructionCheck, error) {
	var checks []models.ConstructionCheck
	if err := s.DB.Where("trip_id = ? AND kind IN ?", tripID, []string{"nuisance", "qa"}).
		Order("created_at DESC").Find(&checks).Error; err != nil {
		return nil, nil, err
	}
	var nuisanceRows []pinNuisanceRow
	var qaRow *models.ConstructionCheck
	for i := range checks {
		c := checks[i]
		switch c.Kind {
		case "nuisance":
			var raw map[string]any
			if err := json.Unmarshal([]byte(c.Data), &raw); err != nil {
				continue
			}
			row := pinNuisanceRow{
				LocationID:     stringOf(raw["locationId"]),
				HotelID:        stringOf(raw["hotelId"]),
				Verdict:        stringOf(raw["verdict"]),
				Recommendation: stringOf(raw["recommendation"]),
			}
			if t, ok := raw["analyzedAt"].(string); ok {
				if parsed, err := time.Parse(time.RFC3339, t); err == nil {
					row.AnalyzedAt = parsed
				}
			}
			if row.AnalyzedAt.IsZero() {
				row.AnalyzedAt = c.UpdatedAt
			}
			if cats, ok := raw["categories"].([]any); ok {
				for _, c := range cats {
					m, ok := c.(map[string]any)
					if !ok {
						continue
					}
					row.Categories = append(row.Categories, pinCategory{
						Category: stringOf(m["category"]),
						Level:    stringOf(m["level"]),
						Detail:   stringOf(m["detail"]),
					})
				}
			}
			nuisanceRows = append(nuisanceRows, row)
		case "qa":
			if qaRow == nil {
				cp := c
				qaRow = &cp
			}
		}
	}
	return nuisanceRows, qaRow, nil
}

func compactNuisance(row pinNuisanceRow) map[string]any {
	at := row.AnalyzedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	mainIssue, detail := mainNuisanceIssue(row)
	out := map[string]any{
		"verdict": row.Verdict,
		"at":      at.UTC().Format(time.RFC3339),
	}
	if mainIssue != "" {
		out["mainIssue"] = mainIssue
	}
	if detail != "" {
		out["detail"] = detail
	}
	return out
}

func mainNuisanceIssue(row pinNuisanceRow) (string, string) {
	bestIdx := -1
	bestRank := 0
	for i, c := range row.Categories {
		r := nuisanceRank(c.Level)
		if r > bestRank {
			bestRank = r
			bestIdx = i
		}
	}
	if bestIdx >= 0 && bestRank > nuisanceRank("FAIBLE") {
		c := row.Categories[bestIdx]
		detail := c.Detail
		if detail == "" {
			detail = row.Recommendation
		}
		return c.Category, detail
	}
	return "", row.Recommendation
}

func nuisanceRank(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "ELEVE":
		return 4
	case "INDETERMINE":
		return 3
	case "MODERE":
		return 2
	case "FAIBLE":
		return 1
	default:
		return 0
	}
}

func buildLastQa(qaRow *models.ConstructionCheck, nuisanceRows []pinNuisanceRow) map[string]any {
	if qaRow != nil {
		var violations []QAViolation
		_ = json.Unmarshal([]byte(qaRow.Data), &violations)
		var blockers []string
		verdict := "PASS"
		hasYellow := false
		for _, v := range violations {
			if v.Severity == "red" {
				verdict = "FAIL"
				blockers = append(blockers, qaBlockerID(v))
			} else if v.Severity == "yellow" {
				hasYellow = true
			}
		}
		if verdict == "PASS" && hasYellow {
			verdict = "WARNING"
			for _, v := range violations {
				if v.Severity == "yellow" {
					blockers = append(blockers, qaBlockerID(v))
				}
			}
		}
		if blockers == nil {
			blockers = []string{}
		}
		return map[string]any{
			"at":       qaRow.CreatedAt.UTC().Format(time.RFC3339),
			"verdict":  verdict,
			"blockers": blockers,
		}
	}
	worst := "FAIBLE"
	at := time.Now().UTC()
	var blockers []string
	for _, row := range nuisanceRows {
		if nuisanceRank(row.Verdict) > nuisanceRank(worst) {
			worst = row.Verdict
		}
		if !row.AnalyzedAt.IsZero() && row.AnalyzedAt.After(at) {
			at = row.AnalyzedAt
		}
		if strings.EqualFold(row.Verdict, "ELEVE") {
			name := row.HotelID
			if name == "" {
				name = row.LocationID
			}
			if name != "" {
				blockers = append(blockers, "nuisance:"+name)
			}
		}
	}
	if blockers == nil {
		blockers = []string{}
	}
	seedVerdict := "PASS"
	switch strings.ToUpper(worst) {
	case "ELEVE":
		seedVerdict = "FAIL"
	case "MODERE", "INDETERMINE":
		seedVerdict = "WARNING"
	}
	return map[string]any{
		"at":       at.Format(time.RFC3339),
		"verdict":  seedVerdict,
		"blockers": blockers,
	}
}

func qaBlockerID(v QAViolation) string {
	if v.DayNum > 0 {
		return fmt.Sprintf("%s:%d", v.Code, v.DayNum)
	}
	return v.Code
}

func hotelDict(data map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	switch h := data["hotels"].(type) {
	case map[string]any:
		for id, v := range h {
			if m, ok := v.(map[string]any); ok {
				out[id] = m
			}
		}
	case []any:
		for _, v := range h {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id == "" {
				continue
			}
			out[id] = m
		}
	}
	return out
}

func (s *Service) loadTripDataErr(tripID string) (map[string]any, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, err
	}
	data := map[string]any{}
	if trip.Data != nil && *trip.Data != "" {
		if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
			data = map[string]any{}
		}
	}
	return data, nil
}

func (s *Service) persistTripData(tripID string, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	str := string(b)
	return s.DB.Model(&models.Trip{}).Where("id = ?", tripID).Updates(map[string]any{
		"data":       str,
		"updated_at": time.Now(),
	}).Error
}

func stringOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
