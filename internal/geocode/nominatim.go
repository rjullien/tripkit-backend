// Package geocode turns a free-text address into coordinates.
//
// It exists for one reason: a hotel in the seed carries an address
// (hotels[].addr), never a lat/lon — the data model puts geo exclusively in
// locations{} (tripkit-frontend/DATA-MODEL.md, "No geo in days"). Analysing the
// nuisances *around a hotel* (candidate, to_book or booked) therefore needs
// the address resolved.
package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the public Nominatim endpoint.
	DefaultBaseURL = "https://nominatim.openstreetmap.org/search"

	// minInterval is the hard rate limit of the public instance: no more than
	// one request per second, per its usage policy. It is enforced here rather
	// than trusted to the caller.
	minInterval = 1100 * time.Millisecond

	defaultTimeout = 20 * time.Second
	maxBody        = 512 << 10 // 512 KiB

	// userAgent must identify the application: the public instance blocks
	// requests with a generic or absent User-Agent.
	userAgent = "tripkit-backend-nuisance/1.0 (+https://github.com/rjullien/tripkit-backend)"
)

// ErrNotFound means the geocoder answered correctly but knows no such address.
// It is not a transient failure: retrying or switching endpoint changes nothing.
var ErrNotFound = errors.New("address not found")

// Point is a resolved address.
type Point struct {
	Lat         float64
	Lon         float64
	DisplayName string
}

// Geocoder resolves a free-text address.
type Geocoder interface {
	Geocode(ctx context.Context, address string) (Point, error)
}

// Client is a Nominatim client that respects the 1 req/s policy of the public
// instance across all callers.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	// Email is the optional contact address the Nominatim policy asks for on
	// bulk use (TRIPKIT_NOMINATIM_EMAIL).
	Email string
	// Lang is the Accept-Language sent with the query ("fr" by default).
	Lang string
	// Sleep is the injection point for the rate limiter (tests).
	Sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last time.Time
	// Now is an optional clock override (tests).
	Now func() time.Time
}

// NewClient builds a client from a base URL ("" = public instance).
func NewClient(baseURL, email string) *Client {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		u = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(u, "/"),
		Email:      strings.TrimSpace(email),
		Lang:       "fr",
		HTTPClient: &http.Client{Timeout: defaultTimeout},
	}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// throttle enforces minInterval between two requests, whichever job asks.
func (c *Client) throttle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.last.IsZero() {
		if wait := minInterval - c.now().Sub(c.last); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	c.last = c.now()
	return nil
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
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

type nominatimHit struct {
	Lat         string `json:"lat"` // Nominatim sends coordinates as strings
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

// Geocode resolves one address. A blank address is a caller error, not a lookup.
func (c *Client) Geocode(ctx context.Context, address string) (Point, error) {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return Point{}, fmt.Errorf("geocode: empty address")
	}
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return Point{}, fmt.Errorf("geocode: no endpoint configured")
	}
	if err := c.throttle(ctx); err != nil {
		return Point{}, err
	}

	q := url.Values{}
	q.Set("q", addr)
	q.Set("format", "jsonv2")
	q.Set("limit", "1")
	q.Set("addressdetails", "0")
	if c.Email != "" {
		q.Set("email", c.Email)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return Point{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if lang := strings.TrimSpace(c.Lang); lang != "" {
		req.Header.Set("Accept-Language", lang)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return Point{}, fmt.Errorf("geocode: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return Point{}, fmt.Errorf("geocode: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return Point{}, fmt.Errorf("geocode: HTTP %d", res.StatusCode)
	}

	var hits []nominatimHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return Point{}, fmt.Errorf("geocode: malformed answer: %w", err)
	}
	if len(hits) == 0 {
		return Point{}, ErrNotFound
	}
	lat, errLat := strconv.ParseFloat(strings.TrimSpace(hits[0].Lat), 64)
	lon, errLon := strconv.ParseFloat(strings.TrimSpace(hits[0].Lon), 64)
	if errLat != nil || errLon != nil {
		return Point{}, fmt.Errorf("geocode: unparsable coordinates %q,%q", hits[0].Lat, hits[0].Lon)
	}
	// 0,0 is Null Island: a hit there is a bug, not a hotel.
	if lat == 0 && lon == 0 {
		return Point{}, ErrNotFound
	}
	return Point{Lat: lat, Lon: lon, DisplayName: hits[0].DisplayName}, nil
}
