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
	err := consumeHermesSSE(strings.NewReader(raw), func(event string, data StreamEvent) error {
		events = append(events, event)
		if event == "delta" {
			texts = append(texts, data.Text)
		}
		if event == "done" {
			reply = data.Reply
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
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}},
		func(event string, data StreamEvent) error {
			if event == "delta" {
				got.WriteString(data.Text)
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
