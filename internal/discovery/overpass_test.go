package discovery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient_HTTPTimeoutExceedsQL(t *testing.T) {
	c := NewClient(OverpassConfig{TimeoutSec: 25})
	if c.Timeout != 25*time.Second {
		t.Fatalf("QL timeout=%v", c.Timeout)
	}
	if c.HTTPClient == nil || c.HTTPClient.Timeout != 30*time.Second {
		t.Fatalf("HTTP timeout=%v", c.HTTPClient.Timeout)
	}
}

func TestBuildOverpassQL_UsesCatalogueTags(t *testing.T) {
	theme := Theme{ID: "outlets", RadiusKm: 25, Overpass: []string{"shop=outlet", "shop=mall"}}
	q := buildOverpassQL(48.14, -69.71, theme, 25)
	if !strings.Contains(q, `["shop"="outlet"]`) {
		t.Fatalf("missing outlet tag: %s", q)
	}
	if !strings.Contains(q, "around:25000,48.140000,-69.710000") {
		t.Fatalf("around: %s", q)
	}
	if strings.Contains(q, "shop=outlet") && strings.Contains(q, "node[shop=outlet]") {
		t.Fatal("tags must not be hardcoded without quotes")
	}
}

func TestClient_SoftFailTimeout429Malformed(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(80 * time.Millisecond)
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"elements":[]}`)
		}))
		t.Cleanup(srv.Close)
		c := &Client{BaseURL: srv.URL, Timeout: 20 * time.Millisecond, HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}}
		items, err := c.Search(context.Background(), 48, -69, Theme{ID: "outlets", RadiusKm: 10, Overpass: []string{"shop=outlet"}})
		if err == nil {
			t.Fatal("expected timeout error from client (service must swallow it)")
		}
		if len(items) != 0 {
			t.Fatalf("items=%d", len(items))
		}
	})
	t.Run("429", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "rate")
		}))
		t.Cleanup(srv.Close)
		c := NewClient(OverpassConfig{BaseURL: srv.URL, TimeoutSec: 2})
		_, err := c.Search(context.Background(), 48, -69, Theme{ID: "outlets", RadiusKm: 10, Overpass: []string{"shop=outlet"}})
		if err == nil || !strings.Contains(err.Error(), "429") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>nope</html>")
		}))
		t.Cleanup(srv.Close)
		c := NewClient(OverpassConfig{BaseURL: srv.URL, TimeoutSec: 2})
		_, err := c.Search(context.Background(), 48, -69, Theme{ID: "outlets", RadiusKm: 10, Overpass: []string{"shop=outlet"}})
		if err == nil {
			t.Fatal("expected parse error")
		}
	})
	t.Run("ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"elements": []any{
					map[string]any{"type": "node", "id": 1, "lat": 48.15, "lon": -69.72, "tags": map[string]string{"name": "Village"}},
					map[string]any{"type": "way", "id": 2, "center": map[string]any{"lat": 48.16, "lon": -69.70}, "tags": map[string]string{"name": "Mall"}},
				},
			})
		}))
		t.Cleanup(srv.Close)
		c := NewClient(OverpassConfig{BaseURL: srv.URL, TimeoutSec: 2})
		items, err := c.Search(context.Background(), 48.1454, -69.7173, Theme{ID: "outlets", RadiusKm: 10, Overpass: []string{"shop=outlet"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("len=%d", len(items))
		}
		if items[0].ThemeID != "outlets" || items[0].Source != "osm" || items[0].DistKm <= 0 {
			t.Fatalf("%+v", items[0])
		}
	})
}

func TestExcludedName_HardwareChains(t *testing.T) {
	needles := []string{"canadian tire", "home depot", "walmart", "ikea"}
	if !excludedName("Canadian Tire Tadoussac", needles) {
		t.Fatal("Canadian Tire must be excluded")
	}
	if !excludedName("Walmart Supercentre", needles) {
		t.Fatal("Walmart must be excluded")
	}
	if excludedName("Boutique des artisans", needles) {
		t.Fatal("tourist shop must stay")
	}
	if excludedName("", needles) {
		t.Fatal("empty name is not excluded")
	}
}

func TestClient_DropsExcludedNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"elements": []any{
				map[string]any{"type": "node", "id": 1, "lat": 48.15, "lon": -69.72, "tags": map[string]string{"name": "Canadian Tire"}},
				map[string]any{"type": "node", "id": 2, "lat": 48.16, "lon": -69.70, "tags": map[string]string{"name": "Boutique des artisans"}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(OverpassConfig{BaseURL: srv.URL, TimeoutSec: 2})
	items, err := c.Search(context.Background(), 48.1454, -69.7173, Theme{
		ID: "magasinage", RadiusKm: 10,
		Overpass:     []string{"shop=mall"},
		ExcludeNames: []string{"canadian tire"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Boutique des artisans" {
		t.Fatalf("%+v", items)
	}
}

func TestParseOverpassJSON_EmptyElements(t *testing.T) {
	items, err := parseOverpassJSON([]byte(`{"elements":[]}`))
	if err != nil || len(items) != 0 {
		t.Fatalf("err=%v n=%d", err, len(items))
	}
}
