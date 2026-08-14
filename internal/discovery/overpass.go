package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

func NewClient(cfg OverpassConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSec * time.Second
	}
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		Timeout: timeout,
		HTTPClient: &http.Client{
			Timeout: timeout,
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
	items, err := c.post(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
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

func (c *Client) post(ctx context.Context, ql string) ([]Item, error) {
	form := url.Values{}
	form.Set("data", ql)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
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
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxOverpassBody))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		return nil, fmt.Errorf("overpass HTTP %d", res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overpass HTTP %d", res.StatusCode)
	}
	return parseOverpassJSON(body)
}

type overpassResp struct {
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

func parseOverpassJSON(raw []byte) ([]Item, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, fmt.Errorf("malformed overpass json")
	}
	var resp overpassResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
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
		name := osmName(el.Tags)
		if name == "" {
			continue
		}
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
	return out, nil
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
