package geocode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeocode_ParsesStringCoordinates(t *testing.T) {
	var gotQuery, gotUA, gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotUA = r.Header.Get("User-Agent")
		gotLang = r.Header.Get("Accept-Language")
		_, _ = io.WriteString(w, `[{"lat":"43.6131151","lon":"1.4529625","display_name":"64 Boulevard Pierre Semard, Toulouse"}]`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "ops@example.com")
	c.Sleep = func(context.Context, time.Duration) error { return nil }

	p, err := c.Geocode(context.Background(), "  64 Boulevard Pierre Sémard, 31000 Toulouse  ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 43.6131151 || p.Lon != 1.4529625 {
		t.Errorf("point=%+v", p)
	}
	if p.DisplayName == "" {
		t.Error("want the resolved display name, to show which address was used")
	}
	if gotQuery != "64 Boulevard Pierre Sémard, 31000 Toulouse" {
		t.Errorf("q=%q, want the trimmed address", gotQuery)
	}
	// The public instance blocks generic User-Agents and asks for a contact.
	if !strings.Contains(gotUA, "tripkit-backend") {
		t.Errorf("User-Agent=%q, want the app identified", gotUA)
	}
	if gotLang != "fr" {
		t.Errorf("Accept-Language=%q, want fr", gotLang)
	}
}

// An address the geocoder does not know is NOT a transient failure: the caller
// must be able to tell it apart so it does not retry forever.
func TestGeocode_EmptyResultIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.Geocode(context.Background(), "nowhere at all"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// Null Island is a bug, not a hotel: (0,0) must not be scored as a real point.
func TestGeocode_NullIslandIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"lat":"0","lon":"0","display_name":"Null Island"}]`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.Geocode(context.Background(), "0,0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestGeocode_HTTPErrorAndMalformedBody(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		c := NewClient(srv.URL, "")
		c.Sleep = func(context.Context, time.Duration) error { return nil }
		if _, err := c.Geocode(context.Background(), "x"); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("html", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "<html>rate limited</html>")
		}))
		t.Cleanup(srv.Close)
		c := NewClient(srv.URL, "")
		c.Sleep = func(context.Context, time.Duration) error { return nil }
		if _, err := c.Geocode(context.Background(), "x"); err == nil {
			t.Fatal("want a parse error, not a silent zero point")
		}
	})
}

func TestGeocode_EmptyAddressIsRejectedWithoutACall(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "")
	if _, err := c.Geocode(context.Background(), "   "); err == nil {
		t.Fatal("want an error on a blank address")
	}
	if calls != 0 {
		t.Errorf("calls=%d, want 0", calls)
	}
}

// The public instance allows one request per second: the limit is enforced here,
// not trusted to the caller.
func TestGeocode_RespectsOneRequestPerSecond(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"lat":"1","lon":"2","display_name":"x"}]`)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	frozen := time.Now()
	c := NewClient(srv.URL, "")
	c.Now = func() time.Time { return frozen }
	c.Sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	for i := 0; i < 3; i++ {
		if _, err := c.Geocode(context.Background(), "somewhere"); err != nil {
			t.Fatal(err)
		}
	}
	if len(waits) != 2 {
		t.Fatalf("waits=%v, want 2 (the first request goes straight through)", waits)
	}
	if waits[0] < time.Second {
		t.Errorf("wait=%v, want >= 1s per the Nominatim usage policy", waits[0])
	}
}
