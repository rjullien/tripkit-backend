package leo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)


type openAIStreamReq struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

// StreamEvent is a normalized TripKit SSE payload (not raw OpenAI).
// event names: delta | tool | done | error | meta
type StreamEvent struct {
	Text   string `json:"text,omitempty"`
	Reply  string `json:"reply,omitempty"`
	Model  string `json:"model,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
	Tool   any    `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// EmitFunc writes one SSE event to the client.
type EmitFunc func(event string, data StreamEvent) error

func prepareMessages(ctx PromptContext, req ChatRequest) ([]ChatMessage, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages required")
	}
	msgs := req.Messages
	if len(msgs) > maxChatHistory {
		msgs = msgs[len(msgs)-maxChatHistory:]
	}
	for i := range msgs {
		msgs[i].Role = strings.TrimSpace(msgs[i].Role)
		msgs[i].Content = strings.TrimSpace(msgs[i].Content)
		if msgs[i].Role == "" || msgs[i].Content == "" {
			return nil, fmt.Errorf("each message needs role and content")
		}
		if msgs[i].Role != "user" && msgs[i].Role != "assistant" {
			return nil, fmt.Errorf("role must be user or assistant")
		}
	}
	promptCtx := ctx
	if strings.TrimSpace(promptCtx.TripID) == "" {
		promptCtx.TripID = strings.TrimSpace(req.TripID)
	}
	out := make([]ChatMessage, 0, len(msgs)+1)
	out = append(out, ChatMessage{Role: "system", Content: SystemPrompt(promptCtx)})
	out = append(out, msgs...)
	return out, nil
}

// StreamChat calls Hermes with stream:true and emits TripKit SSE events.
// Keep-alive comments are the caller's responsibility (handler heartbeat).
func (c Config) StreamChat(ctx context.Context, pctx PromptContext, req ChatRequest, emit EmitFunc) error {
	if !c.Ready() {
		return fmt.Errorf("TRIPKIT_HERMES_API_KEY not configured")
	}
	msgs, err := prepareMessages(pctx, req)
	if err != nil {
		return err
	}

	// No max_tokens on stream — seed edits need room; sync Chat still caps replies.
	body, err := json.Marshal(openAIStreamReq{
		Model:    "default",
		Messages: msgs,
		Stream:   true,
	})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "tripkit-backend-leo-stream")

	// No overall Timeout — stream can run for several minutes; ctx cancels on disconnect.
	client := &http.Client{Timeout: 0}
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		client.Transport = c.HTTPClient.Transport
	}

	res, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("hermes unreachable: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if msg := extractHermesError(raw); msg != "" {
			return fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, msg)
		}
		return fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}

	ct := res.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/event-stream") {
		// Some gateways return a JSON error with 200 — handle conservatively.
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if msg := extractHermesError(raw); msg != "" {
			return fmt.Errorf("hermes: %s", msg)
		}
	}

	return consumeHermesSSE(res.Body, emit)
}

func consumeHermesSSE(r io.Reader, emit EmitFunc) error {
	sc := bufio.NewScanner(r)
	// Tool progress / big chunks — default 64K may be tight.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)

	var (
		eventName   string
		dataLines   []string
		full        strings.Builder
		model       string
		doneEmitted bool
	)

	flushFrame := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		ev := eventName
		eventName = ""

		if data == "[DONE]" {
			doneEmitted = true
			return emit("done", StreamEvent{Reply: full.String(), Model: model})
		}

		if ev == "hermes.tool.progress" {
			var tool any
			if err := json.Unmarshal([]byte(data), &tool); err != nil {
				tool = map[string]string{"raw": truncate(data, 200)}
			}
			return emit("tool", StreamEvent{Tool: tool})
		}

		// Default OpenAI chat.completion.chunk
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Ignore non-JSON keepalives / comments already stripped
			return nil
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return emit("error", StreamEvent{
				Error: "Échec Léo. Réessaie.",
				Code:  "leo_chat_failed",
				Detail: chunk.Error.Message,
			})
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		text := chunk.Choices[0].Delta.Content
		if text == "" {
			return nil
		}
		full.WriteString(text)
		return emit("delta", StreamEvent{Text: text, Model: model})
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flushFrame(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / Hermes keepalive
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
	}
	if err := flushFrame(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("hermes stream read: %w", err)
	}
	if doneEmitted {
		return nil
	}
	// Stream ended without [DONE] — still close with accumulated text.
	return emit("done", StreamEvent{Reply: full.String(), Model: model})
}

// StreamHTTPClient returns a client suitable for long SSE (no hard Timeout).
func StreamHTTPClient() *http.Client {
	return &http.Client{Timeout: 0}
}
