package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model     string    `json:"model"`
	Messages  []chatMsg `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete performs a non-streaming chat completion.
func (c *Client) Complete(ctx context.Context, model, system, user string, opts ...Option) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("bifrost not configured")
	}

	o := defaults()
	for _, fn := range opts {
		fn(&o)
	}
	if o.Model != "" {
		model = o.Model
	}
	if model == "" {
		model = c.Model
	}

	body, err := json.Marshal(chatReq{
		Model: model,
		Messages: []chatMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: o.MaxTokens,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tripkit-backend-bifrost")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bifrost unreachable: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))

	var parsed chatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("bifrost invalid JSON (HTTP %d): %s", res.StatusCode, truncate(string(raw), 200))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("bifrost: %s", parsed.Error.Message)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("bifrost HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("bifrost returned no choices")
	}
	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("bifrost returned empty text")
	}
	return text, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
