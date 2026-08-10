package publish_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestParseSourcesJSON(t *testing.T) {
	list, err := publish.ParseSourcesJSON([]byte(`[
		{"id":"laurine","repo":"rjullien/tripkit-seeds-laurine","enabled":true,"publisherLogins":["laurine"],"ownerLogins":["laurine"],"expectedFamily":"laurine"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "laurine" || !list[0].Enabled {
		t.Fatalf("unexpected: %+v", list)
	}
	if _, err := publish.ParseSourcesJSON([]byte(`[]`)); err == nil {
		t.Fatal("empty array should fail")
	}
}

func TestSourcesLoader_GitHubThenCacheFallback(t *testing.T) {
	payload := []byte(`[
		{"id":"from-gh","repo":"rjullien/tripkit-seeds","ref":"main","expectedFamily":"jullien",
		 "publisherLogins":["rene"],"ownerLogins":["rene"],"enabled":true}
	]`)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "publish-sources.json")
	client := &publish.GitHubClient{
		Token: "test-token",
		Client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = srv.Listener.Addr().String()
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}
	loader := &publish.SourcesLoader{
		GitHub:    client,
		Repo:      "rjullien/tripkit",
		Ref:       "main",
		Path:      "ops/publish-sources.json",
		CachePath: cache,
		TTL:       time.Millisecond,
	}

	reg := publish.NewRegistry(nil)
	if origin := loader.Bootstrap(reg); origin != "github" {
		t.Fatalf("origin=%q", origin)
	}
	if _, ok := reg.Get("from-gh"); !ok {
		t.Fatal("missing from-gh")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// Cold start with GH down → disk cache
	loader2 := &publish.SourcesLoader{
		GitHub: &publish.GitHubClient{
			Token: "test-token",
			Client: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					req.URL.Scheme = "http"
					req.URL.Host = srv.Listener.Addr().String()
					return http.DefaultTransport.RoundTrip(req)
				}),
			},
		},
		Repo:      "rjullien/tripkit",
		Ref:       "main",
		Path:      "ops/publish-sources.json",
		CachePath: cache,
		TTL:       time.Millisecond,
	}
	reg2 := publish.NewRegistry(nil)
	if origin := loader2.Bootstrap(reg2); origin != "cache" {
		t.Fatalf("origin=%q want cache (hits=%d)", origin, hits)
	}
	if _, ok := reg2.Get("from-gh"); !ok {
		t.Fatal("cache miss")
	}
}

func TestSourcesLoader_TryRefreshKeepsPreviousOnFailure(t *testing.T) {
	good := []byte(`[{"id":"keep","repo":"rjullien/tripkit-seeds","enabled":true,"publisherLogins":["rene"],"ownerLogins":["rene"],"expectedFamily":"jullien"}]`)
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(good)
			return
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	loader := &publish.SourcesLoader{
		GitHub: &publish.GitHubClient{
			Token: "t",
			Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = srv.Listener.Addr().String()
				return http.DefaultTransport.RoundTrip(req)
			})},
		},
		Repo:      "rjullien/tripkit",
		Ref:       "main",
		Path:      "ops/publish-sources.json",
		CachePath: filepath.Join(t.TempDir(), "c.json"),
		TTL:       time.Millisecond,
	}
	reg := publish.NewRegistry(nil)
	if loader.Bootstrap(reg) != "github" {
		t.Fatal("bootstrap")
	}
	time.Sleep(2 * time.Millisecond)
	loader.TryRefresh(reg)
	if _, ok := reg.Get("keep"); !ok {
		t.Fatal("refresh failure wiped previous registry")
	}
	if loader.Origin() != "github" {
		t.Fatalf("origin=%q", loader.Origin())
	}
}

func TestSourcesLoader_NoTokenNoCache_Dogfood(t *testing.T) {
	loader := &publish.SourcesLoader{
		GitHub:    &publish.GitHubClient{},
		CachePath: filepath.Join(t.TempDir(), "missing.json"),
	}
	reg := publish.NewRegistry(nil)
	if origin := loader.Bootstrap(reg); origin != "dogfood" {
		t.Fatalf("origin=%q", origin)
	}
	if _, ok := reg.Get("jullien"); !ok {
		t.Fatal("dogfood missing jullien")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
