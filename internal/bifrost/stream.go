package bifrost

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

type streamReq struct {
	Model     string    `json:"model"`
	Messages  []chatMsg `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Stream    bool      `json:"stream"`
}

// StreamComplete performs a streaming chat completion. Each content delta is
// passed to emit. Returns the full accumulated text on success.
func (c *Client) StreamComplete(ctx context.Context, model, system, user string, emit func(delta string) error) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("bifrost not configured")
	}
	if model == "" {
		model = c.Model
	}

	body, err := json.Marshal(streamReq{
		Model: model,
		Messages: []chatMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: true,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "tripkit-backend-bifrost-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := &http.Client{Timeout: 0}
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		client.Transport = c.HTTPClient.Transport
	}

	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("bifrost unreachable: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return "", fmt.Errorf("bifrost HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}

	return consumeSSE(res.Body, emit)
}

// consumeSSE reads an OpenAI-compatible SSE stream, calling emit for each
// content delta. Returns the full accumulated text.
func consumeSSE(r io.Reader, emit func(delta string) error) (string, error) {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)

	var (
		dataLines []string
		full      strings.Builder
	)

	flushFrame := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil

		if data == "[DONE]" {
			return nil
		}

		var chunk struct {
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
			// Ignore non-JSON keepalives
			return nil
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("bifrost stream: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		text := chunk.Choices[0].Delta.Content
		if text == "" {
			return nil
		}
		full.WriteString(text)
		if emit != nil {
			return emit(text)
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flushFrame(); err != nil {
				return full.String(), err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / keepalive
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		// Ignore event: lines and other fields for this simple consumer.
	}

	// Flush any remaining data
	if err := flushFrame(); err != nil {
		return full.String(), err
	}

	if err := sc.Err(); err != nil {
		return full.String(), fmt.Errorf("bifrost stream read: %w", err)
	}

	return full.String(), nil
}
