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

func TestConsumeHermesSSE_DeltasAndDone(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"id":"1","object":"chat.completion.chunk","model":"hermes-agent","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"1","object":"chat.completion.chunk","model":"hermes-agent","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
		``,
		`data: {"id":"1","object":"chat.completion.chunk","model":"hermes-agent","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
		``,
		`event: hermes.tool.progress`,
		`data: {"name":"read_file","status":"started"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var events []string
	var texts []string
	var reply string
	var upstream string
	err := consumeHermesSSE(strings.NewReader(raw), func(event string, data StreamEvent) error {
		events = append(events, event)
		if event == "delta" {
			texts = append(texts, data.Text)
		}
		if event == "done" {
			reply = data.Reply
			upstream = data.Upstream
			if data.Model != "" {
				t.Fatalf("consume must not set Model from Hermes echo, got %q", data.Model)
			}
		}
		if event == "tool" {
			b, _ := json.Marshal(data.Tool)
			if !strings.Contains(string(b), "read_file") {
				t.Fatalf("tool payload=%s", b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts, "") != "Hello" {
		t.Fatalf("texts=%v", texts)
	}
	if reply != "Hello" {
		t.Fatalf("reply=%q", reply)
	}
	if upstream != "hermes-agent" {
		t.Fatalf("upstream=%q (Hermes echo must not be used as TripKit model)", upstream)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "delta") || !strings.Contains(joined, "tool") || !strings.Contains(joined, "done") {
		t.Fatalf("events=%v", events)
	}
}

func TestStreamChat_LiveShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var body openAIStreamReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream {
			t.Fatal("expected stream:true")
		}
		if body.Model != "opencode-go/deepseek-v4-pro" {
			t.Fatalf("model=%q", body.Model)
		}
		if body.Provider != hermesProvider {
			t.Fatalf("provider=%q", body.Provider)
		}
		if len(body.Messages) < 2 || body.Messages[0].Role != "system" {
			t.Fatalf("messages=%+v", body.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"A\"}}]}\n\n"))
		fl.Flush()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"B\"}}]}\n\n"))
		fl.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}
	var got strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := cfg.StreamChat(ctx, PromptContext{Username: "rene", AllowedRepos: []string{"rjullien/tripkit-seeds"}, IsAdmin: true},
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Model: "opencode-go/deepseek-v4-pro"},
		func(event string, data StreamEvent) error {
			if event == "delta" {
				got.WriteString(data.Text)
			}
			if event == "done" || event == "meta" || event == "delta" {
				if data.Model != "opencode-go/deepseek-v4-pro" {
					t.Fatalf("event %s model=%q (must be allowlisted id, not hermes echo)", event, data.Model)
				}
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "AB" {
		t.Fatalf("got=%q", got.String())
	}
}

func TestStreamChat_UnknownModelFallsBack(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body openAIStreamReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sent = body.Model
		if body.Provider != hermesProvider {
			t.Fatalf("provider=%q", body.Provider)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"hermes-agent\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var doneModel, doneUpstream string
	err := cfg.StreamChat(ctx, PromptContext{Username: "rene"},
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Model: "not-a-real-model"},
		func(event string, data StreamEvent) error {
			if event == "done" {
				doneModel = data.Model
				doneUpstream = data.Upstream
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if sent != defaultLeoModel {
		t.Fatalf("hermes received model=%q want default %q", sent, defaultLeoModel)
	}
	if doneModel != defaultLeoModel {
		t.Fatalf("done.model=%q", doneModel)
	}
	if doneUpstream != "hermes-agent" {
		t.Fatalf("done.upstream=%q", doneUpstream)
	}
}
