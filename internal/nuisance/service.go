package nuisance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/geocode"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Nuisance analysis is deliberately serial. The public Overpass API allots
// roughly two slots per IP and sheds everything else with 429/504, and the
// backend has one egress IP: fanning out locations in parallel is what turned
// a busy minute into a page full of "Donnée indisponible". One query at a time,
// spaced, is slower but it actually answers — and since the job is read
// asynchronously by the frontend (Léo pattern: POST returns a jobId, the SSE
// stream reports progress, the results are fetched from the DB), being slower
// costs nothing but a few more progress frames.
const (
	// concurrency is 1 on purpose: no location is analysed in parallel.
	concurrency = 1

	// queryPace is the minimum spacing between two Overpass queries, applied
	// service-wide so two simultaneous jobs cannot double the rate either.
	queryPace = 800 * time.Millisecond

	// categoryBudget caps one category, retries and mirror rotation included,
	// so a single hanging endpoint cannot eat the whole job.
	categoryBudget = 150 * time.Second

	// heartbeatEvery keeps the job contract of .cursor/skills/tripkit-llm-jobs:
	// a progress frame every ≤10s. One frame per category is not enough now that
	// a single category may legitimately take a minute, and the frontend would
	// show a frozen "Analyse en cours…" in the meantime.
	heartbeatEvery = 8 * time.Second

	// analysisBudget caps the whole run. It sits below leo's jobRunTimeout
	// (10 min) so the job ends on our terms: what has been analysed is stored
	// and the rest is reported as indeterminate, instead of the Hub killing the
	// job and leaving the user with a bare "timeout".
	analysisBudget = 8 * time.Minute
)

// Service runs nuisance analysis for trip locations.
type Service struct {
	DB       *gorm.DB
	Overpass discovery.Querier
	Bifrost  bifrost.Completer
	Hub      *leo.Hub
	// Geocoder resolves a booked hotel's address. Nil means the analysis stays
	// on the trip stop and says so, rather than pretending.
	Geocoder geocode.Geocoder
	// Now is an optional clock override used by the cache TTL (tests only).
	Now func() time.Time
	// Sleep overrides the pacing wait (tests). Nil means a real, cancellable sleep.
	Sleep func(ctx context.Context, d time.Duration) error
	// Pace overrides queryPace (ops tuning). Zero means queryPace.
	Pace time.Duration

	// paceMu serialises the *start* of every Overpass query going through this
	// service, whichever job it belongs to.
	paceMu    sync.Mutex
	lastQuery time.Time
}

// ProgressFunc reports progress for SSE streaming.
type ProgressFunc func(locationID string, done int, total int)

// CheckRequest is the input to start a nuisance check job.
type CheckRequest struct {
	TripID      string   `json:"tripId"`
	LocationIDs []string `json:"locationIds"`
	All         bool     `json:"all"`
}

// CheckResult is the stored output for one analysed point.
// Incomplete/FailedCategories make a persisted result self-describing: a reader
// can tell "we looked and found nothing" apart from "we could not look".
// AddressSource/AddressUsed answer the other question a reader must be able to
// ask: WHERE was this measured — at the hotel door or at the centre of its city?
type CheckResult struct {
	LocationID       string           `json:"locationId"`
	LocationName     string           `json:"locationName"`
	HotelID          string           `json:"hotelId,omitempty"`
	AddressSource    string           `json:"addressSource,omitempty"` // hotel | step
	AddressUsed      string           `json:"addressUsed,omitempty"`
	AddressNote      string           `json:"addressNote,omitempty"`
	Verdict          string           `json:"verdict"`
	VerdictEmoji     string           `json:"verdictEmoji"`
	Categories       []CategoryResult `json:"categories"`
	Recommendation   string           `json:"recommendation"`
	Alternatives     []string         `json:"alternatives"`
	Incomplete       bool             `json:"incomplete,omitempty"`
	FailedCategories []string         `json:"failedCategories,omitempty"`
	Partial          bool             `json:"partial,omitempty"`
	AnalyzedAt       time.Time        `json:"analyzedAt"`
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

	// Resolve WHAT to analyse: a booked hotel is analysed at its own address,
	// anything else at the trip stop (see resolveTargets).
	locations, err := s.resolveTargets(ctx, req.TripID, req.LocationIDs, req.All)
	if err != nil {
		return err
	}
	if len(locations) == 0 {
		_ = emit("done", leo.StreamEvent{Text: "Aucun lieu à analyser."})
		return nil
	}

	total := len(locations)
	allResults := make([]LocationResults, 0, total)

	// Our own budget, below the Hub's job timeout: when it runs out we still
	// own the ending (store what we have, report the rest as indeterminate).
	budgetCtx, cancelBudget := context.WithTimeout(ctx, analysisBudget)
	defer cancelBudget()

	// One Overpass query can now take up to categoryBudget, so the stream keeps
	// ticking on its own while it is in flight.
	stage := &progressStage{}
	stopHeartbeat := startHeartbeat(budgetCtx, emit, stage, heartbeatEvery)
	defer stopHeartbeat()

	// Strictly sequential: one location, then the next. No goroutine, no
	// semaphore — which also makes the progress frames arrive in order (the
	// parallel version could announce "3/5" before "1/5").
	for i, loc := range locations {
		// Only an explicit cancellation (user, shutdown) aborts the run; our own
		// budget expiring is handled inside analyzeLocation, which marks the
		// remaining categories as indeterminate rather than dropping them.
		if err := ctx.Err(); err != nil {
			return err
		}

		stage.set(fmt.Sprintf("Analyse %d/%d : %s…", i+1, total, loc.name), loc.id)
		_ = emit("progress", stage.event())

		results := s.analyzeLocation(budgetCtx, req.TripID, loc, func(cat NuisanceCategory, done int) {
			stage.set(fmt.Sprintf("Analyse %d/%d : %s — %s %s (%d/%d)",
				i+1, total, loc.name, cat.Emoji, cat.Label, done, len(Categories)), loc.id)
			_ = emit("progress", stage.event())
		})
		allResults = append(allResults, results)

		// Persist as soon as a location is known, before the LLM synthesis.
		// The frontend reads results asynchronously, so partial data must be
		// readable while the job runs — and a job that dies later (timeout,
		// pod restart) no longer throws away everything it had learnt.
		s.persist(req.TripID, results, SynthesisResult{})

		_ = emit("delta", leo.StreamEvent{
			Text:   fmt.Sprintf("Analyse %d/%d : %s - %s", i+1, total, loc.name, results.Verdict),
			Detail: results.LocationID,
		})
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Single grouped Bifrost call for synthesis.
	// Soft-fail: if Bifrost is not configured, Synthesize returns empty recommendations
	// and the check still completes with scoring data (no crash).
	if s.Bifrost == nil {
		log.Printf("nuisance: Bifrost completer not configured, skipping synthesis (scores only)")
	}
	synthesis, _ := Synthesize(s.Bifrost, allResults)

	// Second pass: rewrite each stored result with its recommendation.
	for i := range allResults {
		s.persist(req.TripID, allResults[i], synthesis[allResults[i].LocationID])
	}

	// Touch trip to update version.
	s.touchTrip(req.TripID)

	_ = emit("done", leo.StreamEvent{
		Text: s.doneMessage(budgetCtx, total, allResults),
	})
	return nil
}

// progressStage is the sentence describing what the run is doing right now,
// shared between the loop and the heartbeat goroutine.
type progressStage struct {
	mu     sync.Mutex
	text   string
	detail string
}

func (p *progressStage) set(text, detail string) {
	p.mu.Lock()
	p.text, p.detail = text, detail
	p.mu.Unlock()
}

func (p *progressStage) event() leo.StreamEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return leo.StreamEvent{Text: p.text, Detail: p.detail}
}

// startHeartbeat re-emits the current stage at a fixed interval so the SSE
// stream never goes quiet for more than `every`, whatever a single Overpass call
// is doing. Returns the stop function.
func startHeartbeat(ctx context.Context, emit leo.EmitFunc, stage *progressStage, every time.Duration) func() {
	if emit == nil || every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				ev := stage.event()
				if ev.Text == "" {
					continue // nothing started yet: no empty frame
				}
				_ = emit("progress", ev)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// persist stores one location result, with or without its LLM synthesis.
func (s *Service) persist(tripID string, lr LocationResults, syn SynthesisResult) {
	checkResult := CheckResult{
		LocationID:       lr.LocationID,
		LocationName:     lr.LocationName,
		HotelID:          lr.HotelID,
		AddressSource:    lr.AddressSource,
		AddressUsed:      lr.AddressUsed,
		AddressNote:      lr.AddressNote,
		Verdict:          lr.Verdict,
		VerdictEmoji:     VerdictEmoji(lr.Verdict),
		Categories:       lr.Categories,
		Recommendation:   syn.Recommendation,
		Alternatives:     syn.Alternatives,
		Incomplete:       len(lr.FailedCategories) > 0,
		FailedCategories: lr.FailedCategories,
		Partial:          lr.Partial,
		AnalyzedAt:       s.now(),
	}
	data, _ := json.Marshal(checkResult)
	// Keyed by target, not by location: two booked hotels in the same city are
	// two distinct verdicts and must not overwrite each other.
	s.storeResult(tripID, firstNonEmpty(lr.targetID, lr.LocationID), string(data))
}

// doneMessage says plainly whether the analysis is complete. A run that ran out
// of budget or hit Overpass failures must not end on a reassuring sentence.
func (s *Service) doneMessage(budgetCtx context.Context, total int, results []LocationResults) string {
	failed := 0
	for _, lr := range results {
		failed += len(lr.FailedCategories)
	}
	msg := fmt.Sprintf("Analyse terminée : %d lieux analysés.", total)
	if budgetCtx.Err() != nil {
		msg = fmt.Sprintf("Analyse interrompue (temps imparti) : %d lieux traités sur %d.", len(results), total)
	}
	if failed > 0 {
		msg += fmt.Sprintf(" %d catégorie(s) non vérifiée(s) : résultat incomplet.", failed)
	}
	return msg
}

// categoryProgress is called after each category so the SSE stream can report
// progress while the run is still going. Nil is allowed.
type categoryProgress func(cat NuisanceCategory, done int)

func (s *Service) analyzeLocation(ctx context.Context, tripID string, loc target, onCategory categoryProgress) LocationResults {
	lr := LocationResults{
		LocationID:    firstNonEmpty(loc.locationID, loc.id),
		LocationName:  loc.name,
		HotelID:       loc.hotelID,
		AddressSource: loc.source,
		AddressUsed:   loc.addr,
		AddressNote:   loc.note,
		targetID:      loc.id,
	}

	for i, cat := range Categories {
		done := i + 1
		// Categories are never skipped, even out of budget: a missing category
		// would be read as "nothing here". It is reported as indeterminate.
		if len(cat.Tags) == 0 || s.Overpass == nil {
			// security is a placeholder with no OSM tags: scored, not queried.
			lr.Categories = append(lr.Categories, ScoreCategory(cat, nil, loc.lat, loc.lon))
			s.report(onCategory, cat, done)
			continue
		}

		theme := ThemeForCategory(cat)
		if cached, ok := s.loadCache(tripID, loc.lat, loc.lon, theme.ID); ok {
			// Cache hit: no network, no pacing, no budget spent.
			lr.Categories = append(lr.Categories, ScoreCategory(cat, cached, loc.lat, loc.lon))
			s.report(onCategory, cat, done)
			continue
		}

		items, err := s.queryCategory(ctx, cat, theme, loc)
		if err != nil {
			log.Printf("nuisance: overpass category=%s location=%s: %v (unavailable)", cat.ID, loc.id, err)
			lr.Categories = append(lr.Categories, ScoreCategoryUnavailable(cat, unavailableReason(err)))
			lr.FailedCategories = append(lr.FailedCategories, cat.ID)
			s.report(onCategory, cat, done)
			continue
		}
		s.saveCache(tripID, loc.lat, loc.lon, theme.ID, items)
		lr.Categories = append(lr.Categories, ScoreCategory(cat, items, loc.lat, loc.lon))
		s.report(onCategory, cat, done)
	}

	lr.Verdict = GlobalVerdict(lr.Categories)
	lr.Partial = HasUnknown(lr.Categories)
	return lr
}

func (s *Service) report(fn categoryProgress, cat NuisanceCategory, done int) {
	if fn != nil {
		fn(cat, done)
	}
}

// queryCategory runs one Overpass query: waits for its turn (global pacing),
// then gives the client its own slice of the budget so one slow endpoint cannot
// starve the categories that follow.
func (s *Service) queryCategory(ctx context.Context, cat NuisanceCategory, theme discovery.Theme, loc target) ([]discovery.Item, error) {
	if err := s.throttle(ctx); err != nil {
		return nil, err
	}
	qctx, cancel := context.WithTimeout(ctx, categoryBudget)
	defer cancel()
	return s.Overpass.Search(qctx, loc.lat, loc.lon, theme)
}

// throttle blocks until the next Overpass query is allowed to start. It is held
// on the Service, not on the job, so two concurrent nuisance jobs share one
// query rate instead of doubling it.
func (s *Service) throttle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.paceMu.Lock()
	defer s.paceMu.Unlock()

	pace := s.pace()
	if !s.lastQuery.IsZero() && pace > 0 {
		if wait := pace - s.now().Sub(s.lastQuery); wait > 0 {
			if err := s.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	s.lastQuery = s.now()
	return nil
}

func (s *Service) pace() time.Duration {
	if s.Pace != 0 {
		if s.Pace < 0 {
			return 0 // explicit opt-out
		}
		return s.Pace
	}
	return queryPace
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// unavailableReason turns a failure into the short French reason shown in the
// category detail, so "Donnée indisponible (…)" finally says why. An empty
// string keeps the historical generic wording ("Overpass injoignable").
func unavailableReason(err error) string {
	switch {
	case err == nil:
		return ""
	case discovery.IsRateLimited(err):
		return "Overpass saturé, réessaie dans quelques minutes"
	case errors.Is(err, context.DeadlineExceeded), discovery.IsTimeout(err):
		return "délai Overpass dépassé"
	case errors.Is(err, context.Canceled):
		return "analyse interrompue"
	default:
		return ""
	}
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

// GetResult returns the stored nuisance result for one id, which may be a hotel
// id or a stop id. Results are keyed by target (hotel id when a hotel drove the
// point), so a lookup by stop id falls back to scanning: the Résa button has
// always sent the stop id and must keep working.
func (s *Service) GetResult(tripID, id string) (*CheckResult, error) {
	var check models.ConstructionCheck
	err := s.DB.Where("trip_id = ? AND kind = ? AND target_id = ?", tripID, "nuisance", id).
		Order("created_at DESC").First(&check).Error
	if err == nil {
		var cr CheckResult
		if err := json.Unmarshal([]byte(check.Data), &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	}

	results, listErr := s.GetResults(tripID)
	if listErr != nil {
		return nil, err
	}
	for i := range results {
		if results[i].LocationID == id || results[i].HotelID == id {
			return &results[i], nil
		}
	}
	return nil, err
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
