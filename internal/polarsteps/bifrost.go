package polarsteps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Completer is the LLM hop (Bifrost in prod, fake in tests).
type Completer interface {
	Complete(system, user string) (string, error)
}

// BifrostCompleter calls OpenAI-compatible chat/completions (not Hermes).
type BifrostCompleter struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// NewBifrostCompleter builds a caption completer from ops config.
func NewBifrostCompleter(cfg Config) *BifrostCompleter {
	return &BifrostCompleter{
		BaseURL: strings.TrimRight(strings.TrimSpace(cfg.BifrostBaseURL), "/"),
		APIKey:  strings.TrimSpace(cfg.BifrostAPIKey),
		Model:   strings.TrimSpace(cfg.CaptionModel),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type bifrostMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type bifrostReq struct {
	Model     string       `json:"model"`
	Messages  []bifrostMsg `json:"messages"`
	MaxTokens int          `json:"max_tokens,omitempty"`
}

type bifrostResp struct {
	Choices []struct {
		Message bifrostMsg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *BifrostCompleter) Complete(system, user string) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("bifrost not configured")
	}
	body, err := json.Marshal(bifrostReq{
		Model: c.Model,
		Messages: []bifrostMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: 800,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tripkit-backend-polarsteps")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bifrost unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	var parsed bifrostResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("bifrost invalid JSON (HTTP %d)", res.StatusCode)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("bifrost: %s", parsed.Error.Message)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("bifrost HTTP %d", res.StatusCode)
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
