package nuisance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// serialQuerier records how many Search calls overlap.
type serialQuerier struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	calls       int
	order       []string
}

func (q *serialQuerier) Search(_ context.Context, _, _ float64, theme discovery.Theme) ([]discovery.Item, error) {
	q.mu.Lock()
	q.inFlight++
	q.calls++
	q.order = append(q.order, theme.ID)
	if q.inFlight > q.maxInFlight {
		q.maxInFlight = q.inFlight
	}
	q.mu.Unlock()

	// Long enough that a parallel implementation would overlap.
	time.Sleep(2 * time.Millisecond)

	q.mu.Lock()
	q.inFlight--
	q.mu.Unlock()
	return nil, nil
}

func (q *serialQuerier) stats() (calls, maxInFlight int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls, q.maxInFlight
}

// newMultiLocationDB builds a trip with three located stops.
func newMultiLocationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.ConstructionCheck{}, &models.DiscoveryCache{}); err != nil {
		t.Fatal(err)
	}
	tripData := map[string]any{
		"locations": map[string]any{
			"kingman":   map[string]any{"name": "Kingman", "lat": 35.1894, "lon": -114.0530},
			"flagstaff": map[string]any{"name": "Flagstaff", "lat": 35.1983, "lon": -111.6513},
			"sedona":    map[string]any{"name": "Sedona", "lat": 34.8697, "lon": -111.7610},
		},
	}
	raw, _ := json.Marshal(tripData)
	s := string(raw)
	if err := db.Create(&models.Trip{ID: "test-trip", Name: "Test Trip", Data: &s}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// Overpass must be queried one call at a time. Parallel locations are what
// exhausted the ~2 slots per IP and turned the analysis into a page of
// "Donnée indisponible"; the frontend reads the job asynchronously, so serial
// is only slower, never worse.
func TestRunCheck_QueriesOverpassSerially(t *testing.T) {
	db := newMultiLocationDB(t)
	q := &serialQuerier{}
	svc := &Service{DB: db, Overpass: q, Hub: leo.NewHub(), Sleep: noPace}

	job := svc.StartCheck("test-user", CheckRequest{TripID: "test-trip", All: true})
	<-job.Done()
	if job.Status() != leo.JobDone {
		t.Fatalf("job status=%s", job.Status())
	}

	calls, maxInFlight := q.stats()
	if maxInFlight != 1 {
		t.Errorf("max concurrent Overpass calls=%d, want 1", maxInFlight)
	}
	if want := 3 * taggedCategories(); calls != want {
		t.Errorf("calls=%d, want %d (3 locations x %d queried categories)", calls, want, taggedCategories())
	}
}

// Every query but the first waits for its turn: the pause is what keeps the
// public instance from answering 429.
func TestRunCheck_PacesEveryQueryButTheFirst(t *testing.T) {
	db := newTestDB(t)
	q := &mockQuerier{}
	frozen := time.Now()

	var mu sync.Mutex
	var waits []time.Duration
	svc := &Service{
		DB: db, Overpass: q, Hub: leo.NewHub(),
		Now: func() time.Time { return frozen },
		Sleep: func(_ context.Context, d time.Duration) error {
			mu.Lock()
			waits = append(waits, d)
			mu.Unlock()
			return nil
		},
	}

	runCheckSync(t, svc)

	mu.Lock()
	got := len(waits)
	first := time.Duration(0)
	if got > 0 {
		first = waits[0]
	}
	mu.Unlock()

	if want := taggedCategories() - 1; got != want {
		t.Errorf("pauses=%d, want %d (one before each query except the first)", got, want)
	}
	if first != queryPace {
		t.Errorf("pause=%v, want %v", first, queryPace)
	}
}

// The pacing is held on the service, so two jobs running at once share one
// query rate instead of doubling it.
func TestThrottle_IsSharedAcrossJobs(t *testing.T) {
	svc := &Service{}
	var mu sync.Mutex
	var waits int
	svc.Sleep = func(_ context.Context, _ time.Duration) error {
		mu.Lock()
		waits++
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.throttle(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if waits != 5 {
		t.Errorf("pauses=%d, want 5 (6 queries, the first goes straight through)", waits)
	}
}

// Running out of budget must not drop categories: a missing category reads as
// "nothing here". They are reported as indeterminate instead.
func TestAnalyzeLocation_BudgetExhausted_NoCategoryIsDropped(t *testing.T) {
	q := &mockQuerier{}
	svc := &Service{Overpass: q, Sleep: noPace}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // budget already gone

	got := svc.analyzeLocation(ctx, "trip-1", location{id: "hotel-1", name: "Hotel", lat: 45.5, lon: -73.6}, nil)

	if len(got.Categories) != len(Categories) {
		t.Fatalf("categories=%d, want %d (none may be skipped)", len(got.Categories), len(Categories))
	}
	if q.callCount() != 0 {
		t.Errorf("issued %d queries with no budget left, want 0", q.callCount())
	}
	if got.Verdict != LevelIndetermine {
		t.Errorf("verdict=%s, want %s", got.Verdict, LevelIndetermine)
	}
	if len(got.FailedCategories) != taggedCategories() {
		t.Errorf("failedCategories=%v, want %d", got.FailedCategories, taggedCategories())
	}
	for _, c := range got.Categories {
		if c.Category == "security" {
			continue // placeholder, never queried
		}
		if c.Level != LevelIndetermine || !c.Unavailable {
			t.Errorf("category %s: level=%s unavailable=%v", c.Category, c.Level, c.Unavailable)
		}
		if c.Detail == "" {
			t.Errorf("category %s: want a detail saying why", c.Category)
		}
	}
}

// Results are stored per location, before the LLM synthesis: the frontend polls
// them asynchronously, and a job that dies later must not lose everything.
func TestRunCheck_PersistsBeforeSynthesis(t *testing.T) {
	db := newMultiLocationDB(t)
	q := &mockQuerier{}

	var storedWhenSynthesisRan int64
	completer := bifrost.CompleteFn(func(system, user string) (string, error) {
		_ = db.Model(&models.ConstructionCheck{}).
			Where("trip_id = ? AND kind = ?", "test-trip", "nuisance").
			Count(&storedWhenSynthesisRan)
		return `{"recommendations":[]}`, nil
	})

	svc := &Service{DB: db, Overpass: q, Bifrost: completer, Hub: leo.NewHub(), Sleep: noPace}
	job := svc.StartCheck("test-user", CheckRequest{TripID: "test-trip", All: true})
	<-job.Done()

	if storedWhenSynthesisRan != 3 {
		t.Errorf("%d results stored when synthesis started, want 3 (persist as you go)", storedWhenSynthesisRan)
	}
	results, err := svc.GetResults("test-trip")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("final results=%d, want 3", len(results))
	}
}

// The French detail must say *why* the data is missing. "Overpass injoignable"
// on a rate limit is what sent the user asking why they had that message.
func TestUnavailableReason_SaysWhy(t *testing.T) {
	rateLimited := &discovery.OverpassError{Status: http.StatusTooManyRequests, Transient: true}
	serverTimeout := &discovery.OverpassError{Status: http.StatusGatewayTimeout, Transient: true}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"rate limit", rateLimited, "Overpass saturé, réessaie dans quelques minutes"},
		{"server timeout", serverTimeout, "délai Overpass dépassé"},
		{"our deadline", context.DeadlineExceeded, "délai Overpass dépassé"},
		{"cancelled", context.Canceled, "analyse interrompue"},
		{"unknown", errors.New("boom"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unavailableReason(tc.err); got != tc.want {
				t.Errorf("unavailableReason=%q, want %q", got, tc.want)
			}
		})
	}

	// An empty reason keeps the historical wording, which the contract fixture
	// still carries.
	cat := *CategoryByID("trains")
	if got := ScoreCategoryUnavailable(cat, "").Detail; got != unavailableDetail {
		t.Errorf("detail=%q, want the default %q", got, unavailableDetail)
	}
	// A reason is wrapped in the same sentence, never replacing it.
	if got := ScoreCategoryUnavailable(cat, unavailableReason(rateLimited)).Detail; got != "Donnée indisponible (Overpass saturé, réessaie dans quelques minutes)." {
		t.Errorf("detail=%q", got)
	}
}

// The job contract (.cursor/skills/tripkit-llm-jobs) asks for a progress frame
// every 10s at most. One frame per category is no longer enough now that a
// single category may take a minute: the stream ticks on its own.
func TestHeartbeat_KeepsTheStreamTicking(t *testing.T) {
	var mu sync.Mutex
	var frames []string
	emit := func(event string, data leo.StreamEvent) error {
		mu.Lock()
		if event == "progress" {
			frames = append(frames, data.Text)
		}
		mu.Unlock()
		return nil
	}

	stage := &progressStage{}
	stop := startHeartbeat(context.Background(), emit, stage, 2*time.Millisecond)

	// Nothing started yet: no empty frame may be sent.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	early := len(frames)
	mu.Unlock()
	if early != 0 {
		t.Errorf("%d frames before any stage was set, want 0", early)
	}

	stage.set("Analyse 1/1 : Hôtel — 🚂 Trains (1/6)", "hotel-1")
	time.Sleep(40 * time.Millisecond)
	stop()

	mu.Lock()
	got := append([]string(nil), frames...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no heartbeat frame while a query was in flight")
	}
	if got[0] != "Analyse 1/1 : Hôtel — 🚂 Trains (1/6)" {
		t.Errorf("frame=%q, want the current stage", got[0])
	}

	// Stopping must actually stop it. A tick already selected when stop() ran
	// may still land, so the baseline is taken after a short settle.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	baseline := len(frames)
	mu.Unlock()
	time.Sleep(40 * time.Millisecond) // ~20 ticks if it were still running
	mu.Lock()
	after := len(frames)
	mu.Unlock()
	if after != baseline {
		t.Errorf("%d frames after stop, want none", after-baseline)
	}
}

// The heartbeat must die with the run, even if nobody calls stop.
func TestHeartbeat_StopsWithContext(t *testing.T) {
	var mu sync.Mutex
	var frames int
	emit := func(event string, _ leo.StreamEvent) error {
		mu.Lock()
		frames++
		mu.Unlock()
		return nil
	}
	stage := &progressStage{}
	stage.set("en cours", "x")

	ctx, cancel := context.WithCancel(context.Background())
	_ = startHeartbeat(ctx, emit, stage, 2*time.Millisecond)
	cancel()
	time.Sleep(15 * time.Millisecond)

	mu.Lock()
	got := frames
	mu.Unlock()
	if got > 1 {
		t.Errorf("%d frames after the context was cancelled, want at most 1", got)
	}
}
