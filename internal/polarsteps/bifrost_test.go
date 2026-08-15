package polarsteps

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBifrostCompleter_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") != "tripkit-backend-polarsteps" {
			t.Fatalf("ua=%s", r.Header.Get("User-Agent"))
		}
		raw, _ := io.ReadAll(r.Body)
		var req bifrostReq
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "opencode-go/deepseek-v4-flash" {
			t.Fatalf("model=%s", req.Model)
		}
		_ = json.NewEncoder(w).Encode(bifrostResp{
			Choices: []struct {
				Message bifrostMsg `json:"message"`
			}{{Message: bifrostMsg{Role: "assistant", Content: "  hello polarsteps  "}}},
		})
	}))
	defer srv.Close()

	c := &BifrostCompleter{
		BaseURL:    srv.URL,
		Model:      "opencode-go/deepseek-v4-flash",
		HTTPClient: srv.Client(),
	}
	got, err := c.Complete("sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello polarsteps" {
		t.Fatalf("got=%q", got)
	}
}

func TestNewBifrostCompleter_TimeoutAboveNginxDefault(t *testing.T) {
	c := NewBifrostCompleter(DefaultConfig())
	if c.HTTPClient == nil || c.HTTPClient.Timeout != 90*time.Second {
		t.Fatalf("timeout=%v want 90s (must exceed nginx's 60s default)", c.HTTPClient.Timeout)
	}
}

func TestBifrostCompleter_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(bifrostResp{Error: &struct {
			Message string `json:"message"`
		}{Message: "upstream"}})
	}))
	defer srv.Close()
	c := &BifrostCompleter{BaseURL: srv.URL, Model: "m", HTTPClient: srv.Client()}
	_, err := c.Complete("s", "u")
	if err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("err=%v", err)
	}
}
