package nuisance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

const concurrency = 4

// Service runs nuisance analysis for trip locations.
type Service struct {
	DB       *gorm.DB
	Overpass discovery.Querier
	Bifrost  bifrost.Completer
	Hub      *leo.Hub
}

// ProgressFunc reports progress for SSE streaming.
type ProgressFunc func(locationID string, done int, total int)

// CheckRequest is the input to start a nuisance check job.
type CheckRequest struct {
	TripID      string   `json:"tripId"`
	LocationIDs []string `json:"locationIds"`
	All         bool     `json:"all"`
}

// CheckResult is the stored output for one location.
type CheckResult struct {
	LocationID     string           `json:"locationId"`
	LocationName   string           `json:"locationName"`
	Verdict        string           `json:"verdict"`
	VerdictEmoji   string           `json:"verdictEmoji"`
	Categories     []CategoryResult `json:"categories"`
	Recommendation string           `json:"recommendation"`
	Alternatives   []string         `json:"alternatives"`
	AnalyzedAt     time.Time        `json:"analyzedAt"`
	// Partial is true when at least one category could not be evaluated, so the
	// frontend can say "incomplet" instead of implying a full clean bill.
	Partial bool `json:"partial,omitempty"`
}

// StartCheck launches a nuisance analysis as a leo.Hub job.
// Returns the job immediately; the analysis runs in the background.
func (s *Service) StartCheck(user string, req CheckRequest) *leo.Job {
	return s.Hub.Start(user, func(ctx context.Context, emit leo.EmitFunc) error {
		return s.runCheck(ctx, req, emit)
	})
}

func (s *Service) runCheck(ctx context.Context, req CheckRequest, emit leo.EmitFunc) error {
	// Emit meta event to signal job type.
	_ = emit("meta", leo.StreamEvent{
		Text: "nuisance-check",
		Tool: map[string]any{"tripId": req.TripID},
	})

	// Resolve locations from trip data.
	locations, err := s.resolveLocations(req.TripID, req.LocationIDs, req.All)
	if err != nil {
		return err
	}
	if len(locations) == 0 {
		_ = emit("done", leo.StreamEvent{Text: "Aucun lieu a analyser."})
		return nil
	}

	total := len(locations)
	allResults := make([]LocationResults, 0, total)
	var mu sync.Mutex

	// Semaphore for concurrency control.
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, loc := range locations {
		if ctx.Err() != nil {
			break
		}
		loc := loc
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			results := s.analyzeLocation(ctx, loc)
			mu.Lock()
			allResults = append(allResults, results)
			mu.Unlock()

			// Emit progress.
			_ = emit("delta", leo.StreamEvent{
				Text:   fmt.Sprintf("Analyse %d/%d : %s - %s", idx+1, total, loc.name, results.Verdict),
				Detail: results.LocationID,
			})
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Single grouped Bifrost call for synthesis.
	// Soft-fail: if Bifrost is not configured, Synthesize returns empty recommendations
	// and the check still completes with scoring data (no crash).
	if s.Bifrost == nil {
		log.Printf("nuisance: Bifrost completer not configured, skipping synthesis (scores only)")
	}
	synthesis, _ := Synthesize(s.Bifrost, allResults)

	// Store results and build final output.
	for i := range allResults {
		lr := &allResults[i]
		syn := synthesis[lr.LocationID]

		checkResult := CheckResult{
			LocationID:     lr.LocationID,
			LocationName:   lr.LocationName,
			Verdict:        lr.Verdict,
			VerdictEmoji:   VerdictEmoji(lr.Verdict),
			Categories:     lr.Categories,
			Recommendation: syn.Recommendation,
			Alternatives:   syn.Alternatives,
			AnalyzedAt:     time.Now(),
			Partial:        lr.Partial,
		}

		data, _ := json.Marshal(checkResult)
		s.storeResult(req.TripID, lr.LocationID, string(data))
	}

	// Touch trip to update version.
	s.touchTrip(req.TripID)

	_ = emit("done", leo.StreamEvent{
		Text: fmt.Sprintf("Analyse terminee : %d lieux analyses.", total),
	})
	return nil
}

type location struct {
	id   string
	name string
	lat  float64
	lon  float64
}

func (s *Service) resolveLocations(tripID string, locationIDs []string, all bool) ([]location, error) {
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, fmt.Errorf("trip not found: %s", tripID)
	}

	if trip.Data == nil || *trip.Data == "" {
		return nil, nil
	}

	var tripData map[string]any
	if err := json.Unmarshal([]byte(*trip.Data), &tripData); err != nil {
		return nil, nil
	}

	locs, _ := tripData["locations"].(map[string]any)
	if locs == nil {
		return nil, nil
	}

	var result []location
	for id, v := range locs {
		if !all && !contains(locationIDs, id) {
			continue
		}
		locMap, _ := v.(map[string]any)
		if locMap == nil {
			continue
		}
		lat, latOk := asFloat(locMap["lat"])
		lon, lonOk := asFloat(locMap["lon"])
		if !latOk || !lonOk || (lat == 0 && lon == 0) {
			continue
		}
		name, _ := locMap["name"].(string)
		if name == "" {
			name = id
		}
		result = append(result, location{id: id, name: name, lat: lat, lon: lon})
	}
	return result, nil
}

func (s *Service) analyzeLocation(ctx context.Context, loc location) LocationResults {
	lr := LocationResults{
		LocationID:   loc.id,
		LocationName: loc.name,
	}

	for _, cat := range Categories {
		if ctx.Err() != nil {
			break
		}
		if len(cat.Tags) > 0 && s.Overpass != nil {
			theme := ThemeForCategory(cat)
			got, err := s.Overpass.Search(ctx, loc.lat, loc.lon, theme)
			if err != nil {
				// Soft-fail on the request, but NOT on the verdict: the category
				// is reported as indeterminate so the location cannot come out
				// green on the strength of a failed query.
				log.Printf("nuisance: overpass category=%s location=%s: %v (indeterminate)", cat.ID, loc.id, err)
				lr.Categories = append(lr.Categories, ScoreCategoryUnavailable(cat, "Overpass"))
				continue
			}
			lr.Categories = append(lr.Categories, ScoreCategory(cat, got, loc.lat, loc.lon))
			continue
		}
		lr.Categories = append(lr.Categories, ScoreCategory(cat, nil, loc.lat, loc.lon))
	}

	lr.Verdict = GlobalVerdict(lr.Categories)
	lr.Partial = HasUnknown(lr.Categories)
	return lr
}

func (s *Service) storeResult(tripID, locationID, data string) {
	// Upsert: delete old then create new.
	s.DB.Where("trip_id = ? AND kind = ? AND target_id = ?", tripID, "nuisance", locationID).
		Delete(&models.ConstructionCheck{})
	check := models.ConstructionCheck{
		TripID:   tripID,
		Kind:     "nuisance",
		TargetID: locationID,
		Data:     data,
	}
	s.DB.Create(&check)
}

func (s *Service) touchTrip(tripID string) {
	s.DB.Model(&models.Trip{}).Where("id = ?", tripID).Update("updated_at", time.Now())
}

// GetResults returns stored nuisance check results for a trip.
func (s *Service) GetResults(tripID string) ([]CheckResult, error) {
	var checks []models.ConstructionCheck
	if err := s.DB.Where("trip_id = ? AND kind = ?", tripID, "nuisance").
		Order("created_at DESC").Find(&checks).Error; err != nil {
		return nil, err
	}

	var results []CheckResult
	for _, c := range checks {
		var cr CheckResult
		if err := json.Unmarshal([]byte(c.Data), &cr); err != nil {
			continue
		}
		results = append(results, cr)
	}
	return results, nil
}

// GetResult returns the stored nuisance result for a specific location.
func (s *Service) GetResult(tripID, locationID string) (*CheckResult, error) {
	var check models.ConstructionCheck
	if err := s.DB.Where("trip_id = ? AND kind = ? AND target_id = ?", tripID, "nuisance", locationID).
		Order("created_at DESC").First(&check).Error; err != nil {
		return nil, err
	}
	var cr CheckResult
	if err := json.Unmarshal([]byte(check.Data), &cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
