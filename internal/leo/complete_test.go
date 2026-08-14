package leo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentComplete_StreamCustomSystemNoSeedPrompt(t *testing.T) {
	var got openAIStreamReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"[{\\\"name\\\":\\\"Festifoule\\\"}]\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sys := "Tu DOIS chercher sur internet. JSON only. N'écris rien dans git."
	reply, err := cfg.AgentComplete(ctx, sys, "Date : 2026-08-21\nLieu : Tadoussac")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stream {
		t.Fatal("stream must be true so Hermes can use web-search tools")
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("messages=%+v", got.Messages)
	}
	if got.Messages[0].Content != sys {
		t.Fatalf("system=%q", got.Messages[0].Content)
	}
	for _, banned := range []string{"Écriture git", "Repo seed autorisé", "écris dans le seed"} {
		if strings.Contains(got.Messages[0].Content, banned) {
			t.Fatalf("must not inject seed-write SystemPrompt, got %q", got.Messages[0].Content)
		}
	}
	if !strings.Contains(reply, "Festifoule") {
		t.Fatalf("reply=%q", reply)
	}
}

func TestAgentComplete_RequiresSystemAndUser(t *testing.T) {
	cfg := Config{BaseURL: "http://example.invalid", APIKey: "k"}
	if _, err := cfg.AgentComplete(context.Background(), "", "hi"); err == nil {
		t.Fatal("empty system")
	}
	if _, err := cfg.AgentComplete(context.Background(), "sys", "  "); err == nil {
		t.Fatal("empty user")
	}
}
