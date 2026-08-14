package leo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AgentComplete runs one Hermes turn with a custom system prompt.
// Stream:true so Hermes can use web-search tools. Does NOT inject the
// seed-write SystemPrompt (discovery / checks must not commit).
func (c Config) AgentComplete(ctx context.Context, system, user string) (string, error) {
	if !c.Ready() {
		return "", fmt.Errorf("TRIPKIT_HERMES_API_KEY not configured")
	}
	system = strings.TrimSpace(system)
	user = strings.TrimSpace(user)
	if system == "" || user == "" {
		return "", fmt.Errorf("system and user required")
	}
	resolved := c.opsOrDefault().Resolve("")
	body, err := json.Marshal(openAIStreamReq{
		Model:    resolved,
		Provider: hermesProvider,
		Messages: []ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: true,
	})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "tripkit-backend-leo-agent")

	client := &http.Client{Timeout: 0}
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		client.Transport = c.HTTPClient.Transport
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("hermes unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if msg := extractHermesError(raw); msg != "" {
			return "", fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, msg)
		}
		return "", fmt.Errorf("hermes HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	var reply string
	err = consumeHermesSSE(res.Body, func(event string, data StreamEvent) error {
		if event == "done" && strings.TrimSpace(data.Reply) != "" {
			reply = data.Reply
		}
		if event == "error" && data.Detail != "" {
			return fmt.Errorf("hermes: %s", data.Detail)
		}
		return ctx.Err()
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(reply) == "" {
		return "", fmt.Errorf("hermes returned empty text")
	}
	return reply, nil
}
