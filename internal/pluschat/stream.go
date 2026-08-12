package pluschat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

type openAIStreamReq struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

// StreamEvent is a normalized TripKit SSE payload (same events as Leo, sans tool).
type StreamEvent struct {
	Text   string `json:"text,omitempty"`
	Reply  string `json:"reply,omitempty"`
	Model  string `json:"model,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// EmitFunc writes one SSE event to the client.
type EmitFunc func(event string, data StreamEvent) error

// StreamChat calls Bifrost with stream:true and emits TripKit SSE events.
// Optional db loads today+tomorrow trip context into the system prompt.
func (c Config) StreamChat(ctx context.Context, pctx PromptContext, req ChatRequest, emit EmitFunc) error {
	return c.StreamChatDB(ctx, nil, pctx, req, emit)
}

// StreamChatDB is StreamChat with a DB for TripContext injection.
func (c Config) StreamChatDB(ctx context.Context, db *gorm.DB, pctx PromptContext, req ChatRequest, emit EmitFunc) error {
	if !c.Ready() {
		return fmt.Errorf("plus chat not ready")
	}
	if db != nil {
		tripID := strings.TrimSpace(pctx.TripID)
		if tripID == "" {
			tripID = strings.TrimSpace(req.TripID)
		}
		if tripID != "" {
			if tc, err := BuildTripContext(db, tripID, NowFn()); err == nil {
				pctx.Trip = tc
				pctx.TripID = tripID
			}
		}
	}
	msgs, err := prepareMessages(pctx, req)
	if err != nil {
		return err
	}

	body, err := json.Marshal(openAIStreamReq{
		Model:     c.ChatModel,
		Messages:  msgs,
		MaxTokens: 2500,
		Stream:    true,
	})
	if err != nil {
		return err
	}

	url := strings.TrimRight(c.BifrostBaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "tripkit-backend-pluschat")
	if key := strings.TrimSpace(c.BifrostAPIKey); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 0}
	res, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("bifrost unreachable: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return fmt.Errorf("bifrost HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}

	ct := res.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/event-stream") {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return fmt.Errorf("bifrost: %s", truncate(string(raw), 200))
	}

	return consumeOpenAISSE(res.Body, emit)
}

func consumeOpenAISSE(r io.Reader, emit EmitFunc) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)

	var (
		dataLines   []string
		full        strings.Builder
		model       string
		doneEmitted bool
	)

	flushFrame := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil

		if data == "[DONE]" {
			doneEmitted = true
			return emit("done", StreamEvent{Reply: full.String(), Model: model})
		}

		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return emit("error", StreamEvent{
				Error:  "Échec assistant. Réessaie.",
				Code:   "plus_chat_failed",
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
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flushFrame(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !doneEmitted {
		return emit("done", StreamEvent{Reply: full.String(), Model: model})
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
