package discovery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// theme is the smallest valid geo theme; every test here queries the same one.
func retryTheme() Theme {
	return Theme{ID: "outlets", RadiusKm: 10, Overpass: []string{"shop=outlet"}}
}

func okBody(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"elements": []any{
			map[string]any{"type": "node", "id": 1, "lat": 48.15, "lon": -69.72, "tags": map[string]string{"name": "Village"}},
		},
	})
}

// recordingSleep captures backoff durations instead of waiting.
type recordingSleep struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (r *recordingSleep) fn(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.waits = append(r.waits, d)
	r.mu.Unlock()
	return ctx.Err()
}

func (r *recordingSleep) all() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.waits...)
}

// A 429 followed by a 200 must return data. This is the case that used to be
// surfaced as "Donnée indisponible (Overpass injoignable)." on the very first
// hiccup, even though the public API answers fine a second later.
func TestRetry_RateLimitedThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "rate limited")
			return
		}
		okBody(w)
	}))
	t.Cleanup(srv.Close)

	sleeper := &recordingSleep{}
	c := &Client{BaseURL: srv.URL, Timeout: time.Second, Attempts: 3, Sleep: sleeper.fn}
	items, err := c.Search(context.Background(), 48, -69, retryTheme())
	if err != nil {
		t.Fatalf("want success after retry, got %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	if calls != 2 {
		t.Errorf("calls=%d, want 2", calls)
	}
	// Retry-After wins over the computed backoff.
	if waits := sleeper.all(); len(waits) != 1 || waits[0] != 3*time.Second {
		t.Errorf("waits=%v, want [3s] from the Retry-After header", waits)
	}
}

// When the primary keeps failing, the mirror must answer. Without rotation the
// whole category was lost every time overpass-api.de shed load.
func TestRetry_FallsBackToMirror(t *testing.T) {
	var primary, mirror int
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primary++
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(primarySrv.Close)
	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirror++
		okBody(w)
	}))
	t.Cleanup(mirrorSrv.Close)

	sleeper := &recordingSleep{}
	c := &Client{BaseURL: primarySrv.URL, Mirrors: []string{mirrorSrv.URL}, Timeout: time.Second, Attempts: 3, Sleep: sleeper.fn}
	items, err := c.Search(context.Background(), 48, -69, retryTheme())
	if err != nil {
		t.Fatalf("mirror should have answered: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	if primary != 1 || mirror != 1 {
		t.Errorf("primary=%d mirror=%d, want 1/1 (primary first, then rotate)", primary, mirror)
	}
	// Switching endpoint only needs a short pause: the rate limit is per server.
	if waits := sleeper.all(); len(waits) != 1 || waits[0] > endpointSwitchDelay {
		t.Errorf("waits=%v, want a single wait <= %v when switching endpoint", waits, endpointSwitchDelay)
	}
}

// Every endpoint down: the error must name the effort and stay recognisable
// (the caller maps it to a French reason), and no item may be returned.
func TestRetry_AllEndpointsDown(t *testing.T) {
	down := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
	}
	a, b := down(), down()
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)

	sleeper := &recordingSleep{}
	c := &Client{BaseURL: a.URL, Mirrors: []string{b.URL}, Timeout: time.Second, Attempts: 4, Sleep: sleeper.fn}
	items, err := c.Search(context.Background(), 48, -69, retryTheme())
	if err == nil {
		t.Fatal("want an error when every endpoint fails")
	}
	if len(items) != 0 {
		t.Fatalf("items=%d, want 0", len(items))
	}
	if !strings.Contains(err.Error(), "4 attempt(s) over 2 endpoint(s)") {
		t.Errorf("err=%v, want the attempt/endpoint count", err)
	}
	if !IsRateLimited(err) {
		t.Errorf("err=%v, want IsRateLimited (the caller words the detail from it)", err)
	}
	if !IsTransient(err) {
		t.Errorf("err=%v, want IsTransient", err)
	}
	if got := len(sleeper.all()); got != 3 {
		t.Errorf("waits=%d, want 3 (one between each of the 4 attempts)", got)
	}
}

// HTTP 400 means our own query is wrong: retrying it anywhere is pointless.
func TestRetry_BadRequestIsNotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "line 1: parse error")
	}))
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Timeout: time.Second, Attempts: 4, Sleep: func(context.Context, time.Duration) error { return nil }}
	if _, err := c.Search(context.Background(), 48, -69, retryTheme()); err == nil {
		t.Fatal("want an error")
	} else if IsTransient(err) {
		t.Errorf("err=%v, want a deterministic (non-transient) error", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (400 is our fault, not the endpoint's)", calls)
	}
}

// Overpass answers HTTP 200 with a "remark" and an empty element list when it
// gives up. Reading that as "nothing nearby" is the fail-open bug: it must be
// an error, and it must be retried.
func TestRetry_RuntimeErrorRemarkIsAFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"remark":   "runtime error: Query timed out in \"query\" at line 3 after 26 seconds.",
				"elements": []any{},
			})
			return
		}
		okBody(w)
	}))
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Timeout: time.Second, Attempts: 2, Sleep: func(context.Context, time.Duration) error { return nil }}
	items, err := c.Search(context.Background(), 48, -69, retryTheme())
	if err != nil {
		t.Fatalf("want success on the second attempt, got %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	if calls != 2 {
		t.Errorf("calls=%d, want 2 (the remark must not pass for an empty area)", calls)
	}
}

func TestParseOverpassBody_RemarkIsNotAnEmptyArea(t *testing.T) {
	raw := []byte(`{"remark":"runtime error: Query timed out","elements":[]}`)
	items, remark, err := parseOverpassBody(raw)
	if err == nil {
		t.Fatal("a runtime-error remark must be an error, never an empty result")
	}
	if len(items) != 0 {
		t.Errorf("items=%d, want 0", len(items))
	}
	if remark == "" {
		t.Error("want the remark surfaced for logs and the French detail")
	}

	// A harmless remark must not break a valid answer.
	if _, _, err := parseOverpassBody([]byte(`{"remark":"considered 3 elements","elements":[]}`)); err != nil {
		t.Errorf("informational remark treated as failure: %v", err)
	}
}

// A cancelled context must stop the loop instead of burning every attempt.
func TestRetry_ContextCancellationStops(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{BaseURL: srv.URL, Timeout: time.Second, Attempts: 5, Sleep: func(ctx context.Context, d time.Duration) error {
		cancel() // the caller gave up while we were backing off
		return ctx.Err()
	}}
	if _, err := c.Search(ctx, 48, -69, retryTheme()); err == nil {
		t.Fatal("want an error")
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (stop as soon as the context is done)", calls)
	}
}

func TestClient_EndpointsDedupePrimaryAndMirrors(t *testing.T) {
	c := &Client{
		BaseURL: "https://overpass-api.de/api/interpreter",
		Mirrors: []string{
			"https://overpass-api.de/api/interpreter/", // same as primary, trailing slash
			" https://overpass.kumi.systems/api/interpreter ",
			"",
		},
	}
	got := c.endpoints()
	want := []string{
		"https://overpass-api.de/api/interpreter",
		"https://overpass.kumi.systems/api/interpreter",
	}
	if len(got) != len(want) {
		t.Fatalf("endpoints=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("endpoints=%v, want %v (primary first)", got, want)
		}
	}
}

func TestConfig_MirrorsAndAttemptsDefaults(t *testing.T) {
	// A config written before mirrors existed must still get the fallbacks.
	c, err := parseConfigJSON([]byte(`{"version":1,"themes":[{"id":"outlets","label":"O","engine":"geo","radiusKm":10,"overpass":["shop=outlet"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Overpass.Mirrors) != len(defaultMirrors) {
		t.Errorf("mirrors=%v, want the defaults %v", c.Overpass.Mirrors, defaultMirrors)
	}
	if c.Overpass.MaxAttempts != defaultAttempts {
		t.Errorf("maxAttempts=%d, want %d", c.Overpass.MaxAttempts, defaultAttempts)
	}

	// Ops must be able to opt out, and an absurd timeout is clamped.
	c, err = parseConfigJSON([]byte(`{"version":1,"overpass":{"mirrors":["none"],"timeoutSec":9999,"maxAttempts":99},"themes":[{"id":"outlets","label":"O","engine":"geo","radiusKm":10,"overpass":["shop=outlet"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Overpass.Mirrors) != 0 {
		t.Errorf("mirrors=%v, want none", c.Overpass.Mirrors)
	}
	if c.Overpass.TimeoutSec != maxTimeoutSec {
		t.Errorf("timeoutSec=%d, want the %ds ceiling", c.Overpass.TimeoutSec, maxTimeoutSec)
	}
	if c.Overpass.MaxAttempts != maxAttempts {
		t.Errorf("maxAttempts=%d, want the %d ceiling", c.Overpass.MaxAttempts, maxAttempts)
	}
}

// The public instance allots ~2 slots per IP and the backend has one egress IP:
// the default fan-out must not exceed it.
func TestConfig_ConcurrencyStaysWithinOverpassSlots(t *testing.T) {
	if defaultConcurrency > 2 {
		t.Errorf("defaultConcurrency=%d, want <= 2 (public Overpass allots ~2 slots/IP)", defaultConcurrency)
	}
}

// Unnamed OSM features must survive for themes that measure a nuisance, and
// only for those. Measured live: both landuse=industrial polygons near a hotel
// carry no name, so dropping them scored "industriel" on zero items — a green
// verdict next to an industrial zone.
func TestSearch_UnnamedFeaturesKeptOnlyWhenAsked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"elements": []any{
				map[string]any{"type": "way", "id": 1, "center": map[string]any{"lat": 48.15, "lon": -69.72}}, // no tags at all
				map[string]any{"type": "way", "id": 2, "center": map[string]any{"lat": 48.16, "lon": -69.70}, "tags": map[string]string{"landuse": "industrial"}},
				map[string]any{"type": "node", "id": 3, "lat": 48.17, "lon": -69.71, "tags": map[string]string{"name": "Zone Est"}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(OverpassConfig{BaseURL: srv.URL, TimeoutSec: 2, RetryBackoffMs: 1})

	discoveryTheme := Theme{ID: "magasinage", RadiusKm: 1, Overpass: []string{"shop=mall"}}
	items, err := c.Search(context.Background(), 48.14, -69.71, discoveryTheme)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Zone Est" {
		t.Errorf("discovery kept %d items (%v), want only the named one", len(items), items)
	}

	nuisanceTheme := Theme{ID: "nuisance-industrial", RadiusKm: 1, Overpass: []string{"landuse=industrial"}, KeepUnnamed: true}
	items, err = c.Search(context.Background(), 48.14, -69.71, nuisanceTheme)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("nuisance kept %d items, want 3 (unnamed included)", len(items))
	}
	for _, it := range items {
		if it.DistKm <= 0 {
			t.Errorf("item %s has no distance: scoring needs it", it.ID)
		}
		if it.Name == "" && it.URL == "" {
			t.Errorf("item %s: want a coordinate-based maps URL", it.ID)
		}
	}
}
