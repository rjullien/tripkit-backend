package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxOverpassBody = 2 << 20 // 2 MiB

// Querier looks up OSM nodes/ways for one theme around a point.
type Querier interface {
	Search(ctx context.Context, lat, lon float64, theme Theme) ([]Item, error)
}

// Client is the first geo HTTP client in the backend. Soft-fail: errors → empty list.
//
// A single call may hit several endpoints: the primary BaseURL then the
// Mirrors, one per attempt. The public Overpass instance allots ~2 slots per
// IP and sheds the rest with 429/504, which used to surface as
// "Donnée indisponible" on the first hiccup — hence the retry + rotation.
type Client struct {
	BaseURL    string
	Mirrors    []string
	HTTPClient *http.Client
	Timeout    time.Duration // QL timeout announced to Overpass

	// Attempts is the total number of tries per query, endpoints included
	// (1 = no retry). Zero means defaultAttempts.
	Attempts int
	// RetryBase is the first backoff step, doubled at each attempt and capped
	// at maxRetryDelay. Zero means defaultRetryBase.
	RetryBase time.Duration
	// Sleep is the injection point for backoff (tests). Nil means a real,
	// context-aware sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

func NewClient(cfg OverpassConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSec * time.Second
	}
	if timeout > maxTimeoutSec*time.Second {
		timeout = maxTimeoutSec * time.Second
	}
	return &Client{
		BaseURL:   strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		Mirrors:   normalizeEndpoints(cfg.Mirrors),
		Timeout:   timeout,
		Attempts:  cfg.MaxAttempts,
		RetryBase: time.Duration(cfg.RetryBackoffMs) * time.Millisecond,
		HTTPClient: &http.Client{
			// The QL timeout is the server's own budget; ours has to be larger
			// or a server-side Overpass timeout gets masked as a client
			// deadline and we lose the "query timed out" remark.
			Timeout: timeout + httpTimeoutMargin,
		},
	}
}

func (c *Client) Search(ctx context.Context, lat, lon float64, theme Theme) ([]Item, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	q := buildOverpassQL(lat, lon, theme, int(c.Timeout.Seconds()))
	if q == "" {
		return nil, nil
	}
	items, err := c.postWithRetry(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		// Unnamed features: dropped for discovery, kept for themes that measure
		// a nuisance (see Theme.KeepUnnamed). The name is left empty on purpose
		// so the maps URL falls back to the coordinates.
		if it.Name == "" && !theme.KeepUnnamed {
			continue
		}
		if excludedName(it.Name, theme.ExcludeNames) {
			continue
		}
		it.ThemeID = theme.ID
		it.Source = "osm"
		it.DistKm = round1(haversineKm(lat, lon, it.Lat, it.Lon))
		if it.URL == "" && it.Lat != 0 && it.Lon != 0 {
			it.URL = mapsURL(it.Lat, it.Lon, it.Name)
		}
		out = append(out, it)
	}
	return out, nil
}

// endpoints returns the primary URL followed by the mirrors, deduplicated and
// order-preserving. The primary always comes first: mirrors are a fallback, not
// a load balancer.
func (c *Client) endpoints() []string {
	out := make([]string, 0, 1+len(c.Mirrors))
	seen := map[string]bool{}
	for _, raw := range append([]string{c.BaseURL}, c.Mirrors...) {
		u := strings.TrimRight(strings.TrimSpace(raw), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func (c *Client) attempts() int {
	n := c.Attempts
	if n <= 0 {
		n = defaultAttempts
	}
	if n > maxAttempts {
		n = maxAttempts
	}
	return n
}

func (c *Client) retryBase() time.Duration {
	if c.RetryBase > 0 {
		return c.RetryBase
	}
	return defaultRetryBase
}

// postWithRetry runs one query, retrying transient failures and rotating over
// the configured endpoints (attempt i uses endpoints[i % len(endpoints)]). It
// stops early on a deterministic failure, on context cancellation, and when the
// remaining context budget is too small for another attempt.
func (c *Client) postWithRetry(ctx context.Context, ql string) ([]Item, error) {
	eps := c.endpoints()
	attempts := c.attempts()
	var lastErr error

	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return nil, c.wrapExhausted(i, eps, lastErr, err)
		}
		endpoint := eps[i%len(eps)]

		items, err := c.post(ctx, endpoint, ql)
		if err == nil {
			if i > 0 {
				log.Printf("overpass: attempt %d/%d succeeded on %s", i+1, attempts, shortEndpoint(endpoint))
			}
			return items, nil
		}
		lastErr = err

		// Deterministic failure (bad QL): another endpoint would answer the same.
		if !IsTransient(err) {
			return nil, err
		}
		if i == attempts-1 {
			break
		}

		next := eps[(i+1)%len(eps)]
		delay := c.retryDelay(i, err, endpoint != next)
		log.Printf("overpass: attempt %d/%d failed (%v) — retrying on %s in %s",
			i+1, attempts, err, shortEndpoint(next), delay)
		if err := c.sleep(ctx, delay); err != nil {
			return nil, c.wrapExhausted(i+1, eps, lastErr, err)
		}
	}
	return nil, c.wrapExhausted(attempts, eps, lastErr, nil)
}

// retryDelay is the wait before the next attempt. A Retry-After header wins;
// otherwise the backoff doubles at each attempt. Switching to another endpoint
// only needs a short pause, because the rate limit that just bit us is counted
// per server.
func (c *Client) retryDelay(attempt int, err error, switchingEndpoint bool) time.Duration {
	var oe *OverpassError
	if errors.As(err, &oe) && oe.RetryAfter > 0 {
		if oe.RetryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return oe.RetryAfter
	}
	delay := c.retryBase() << attempt // base, 2×base, 4×base…
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	if switchingEndpoint {
		short := c.retryBase() / 4
		if short < endpointSwitchDelay {
			short = endpointSwitchDelay
		}
		if short < delay {
			delay = short
		}
	}
	return delay
}

// sleep waits for d, or returns early if ctx ends first.
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
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

// wrapExhausted keeps the server's own wording (429, remark…) reachable via
// errors.As while stating how hard we tried, so the log line and the French
// "Donnée indisponible (…)" detail stay explainable.
func (c *Client) wrapExhausted(tried int, eps []string, lastErr, ctxErr error) error {
	if lastErr == nil {
		if ctxErr == nil {
			ctxErr = errQueryDeadline
		}
		return &OverpassError{Transient: true, Err: ctxErr}
	}
	return fmt.Errorf("%d attempt(s) over %d endpoint(s): %w", tried, len(eps), lastErr)
}

func (c *Client) post(ctx context.Context, endpoint, ql string) ([]Item, error) {
	form := url.Values{}
	form.Set("data", ql)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &OverpassError{Endpoint: endpoint, Err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "tripkit-backend-discovery")
	req.Header.Set("Accept", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		// Transport failures (DNS, refused, our own deadline) are transient:
		// another endpoint, or the same one later, may well answer.
		return nil, &OverpassError{Endpoint: endpoint, Transient: true, Err: err}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxOverpassBody))
	if err != nil {
		return nil, &OverpassError{Endpoint: endpoint, Status: res.StatusCode, Transient: true, Err: err}
	}
	if res.StatusCode != http.StatusOK {
		return nil, &OverpassError{
			Endpoint:   endpoint,
			Status:     res.StatusCode,
			RetryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
			Transient:  statusIsTransient(res.StatusCode),
			Remark:     bodySnippet(body),
			Err:        fmt.Errorf("overpass HTTP %d", res.StatusCode),
		}
	}

	items, remark, err := parseOverpassBody(body)
	if err != nil {
		// A 200 that is not the JSON we asked for is an Overpass error page or
		// a proxy in between: transient, and worth trying on a mirror.
		return nil, &OverpassError{
			Endpoint:  endpoint,
			Status:    res.StatusCode,
			Transient: true,
			Remark:    remark,
			Err:       err,
		}
	}
	return items, nil
}

type overpassResp struct {
	// Remark is how Overpass reports a failure while still answering 200 with
	// an empty element list ("runtime error: Query timed out…").
	Remark   string       `json:"remark"`
	Elements []overpassEl `json:"elements"`
}

type overpassEl struct {
	Type   string            `json:"type"`
	ID     int64             `json:"id"`
	Lat    float64           `json:"lat"`
	Lon    float64           `json:"lon"`
	Center *overpassCenter   `json:"center"`
	Tags   map[string]string `json:"tags"`
}

type overpassCenter struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// parseOverpassJSON keeps the simple signature used by callers that only care
// about the items.
func parseOverpassJSON(raw []byte) ([]Item, error) {
	items, _, err := parseOverpassBody(raw)
	return items, err
}

// parseOverpassBody decodes an Overpass answer. It returns an error not only on
// malformed JSON but also on a well-formed body whose "remark" reports a
// failure: Overpass answers 200 + {"remark":"runtime error: Query timed
// out","elements":[]} when it gives up, and reading that as "nothing nearby"
// is exactly the fail-open bug that made a dead Overpass look like a quiet
// hotel. The remark is returned alongside so the caller can surface it.
func parseOverpassBody(raw []byte) ([]Item, string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, bodySnippet(raw), fmt.Errorf("malformed overpass json")
	}
	var resp overpassResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, bodySnippet(raw), err
	}
	if remarkIsFailure(resp.Remark) {
		return nil, resp.Remark, fmt.Errorf("overpass remark: %s", truncate(resp.Remark, 160))
	}
	seen := map[string]bool{}
	var out []Item
	for _, el := range resp.Elements {
		lat, lon := el.Lat, el.Lon
		if el.Center != nil {
			if lat == 0 {
				lat = el.Center.Lat
			}
			if lon == 0 {
				lon = el.Center.Lon
			}
		}
		if lat == 0 && lon == 0 {
			continue
		}
		// Unnamed features are decoded, not dropped: only the caller knows
		// whether a name is required (Theme.KeepUnnamed).
		name := osmName(el.Tags)
		id := "osm:" + el.Type + ":" + strconv.FormatInt(el.ID, 10)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Item{
			ID:   id,
			Name: name,
			Lat:  lat,
			Lon:  lon,
		})
	}
	return out, resp.Remark, nil
}

// bodySnippet is a short, single-line excerpt of a response body, for logs and
// error messages (Overpass error pages are HTML and verbose).
func bodySnippet(raw []byte) string {
	s := string(raw)
	if len(s) > 400 {
		s = s[:400]
	}
	return truncate(s, 160)
}

// normalizeEndpoints trims and deduplicates a mirror list.
func normalizeEndpoints(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		u := strings.TrimRight(strings.TrimSpace(raw), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func excludedName(name string, needles []string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, raw := range needles {
		needle := strings.ToLower(strings.TrimSpace(raw))
		if needle != "" && strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

func osmName(tags map[string]string) string {
	if tags == nil {
		return ""
	}
	for _, k := range []string{"name:fr", "name", "brand", "operator"} {
		if v := strings.TrimSpace(tags[k]); v != "" {
			return v
		}
	}
	return ""
}

func buildOverpassQL(lat, lon float64, theme Theme, timeoutSec int) string {
	if len(theme.Overpass) == 0 || theme.RadiusKm <= 0 {
		return ""
	}
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeoutSec
	}
	around := int(theme.RadiusKm * 1000)
	var b strings.Builder
	fmt.Fprintf(&b, "[out:json][timeout:%d];\n(\n", timeoutSec)
	for _, tag := range theme.Overpass {
		k, v, ok := strings.Cut(strings.TrimSpace(tag), "=")
		if !ok || k == "" || v == "" {
			continue
		}
		fmt.Fprintf(&b, "  node[%q=%q](around:%d,%.6f,%.6f);\n", k, v, around, lat, lon)
		fmt.Fprintf(&b, "  way[%q=%q](around:%d,%.6f,%.6f);\n", k, v, around, lat, lon)
	}
	b.WriteString(");\nout center 60;\n")
	return b.String()
}

func mapsURL(lat, lon float64, name string) string {
	q := fmt.Sprintf("%.5f,%.5f", lat, lon)
	if strings.TrimSpace(name) != "" {
		q = name
	}
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(q)
}
