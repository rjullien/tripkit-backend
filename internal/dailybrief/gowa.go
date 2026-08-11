package dailybrief

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GowaClient sends WhatsApp messages via GoWA (never HA).
type GowaClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewGowaClient(baseURL string) *GowaClient {
	return &GowaClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type gowaSendReq struct {
	Phone   string `json:"phone"`
	Message string `json:"message"`
}

type gowaSendResp struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Results *struct {
		MessageID string `json:"message_id"`
		Status    string `json:"status"`
	} `json:"results"`
}

// Send posts to /send/message. phone = group JID or bare DM number.
func (c *GowaClient) Send(phone, message string) (messageID string, err error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("gowa not configured")
	}
	phone = strings.TrimSpace(phone)
	message = strings.TrimSpace(message)
	if phone == "" || message == "" {
		return "", fmt.Errorf("gowa: phone and message required")
	}

	var lastErr error
	backoffs := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		id, err := c.sendOnce(phone, message)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if attempt < len(backoffs) {
			time.Sleep(backoffs[attempt])
		}
	}
	return "", lastErr
}

func (c *GowaClient) sendOnce(phone, message string) (string, error) {
	body, _ := json.Marshal(gowaSendReq{Phone: phone, Message: message})
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/send/message", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tripkit-backend-dailybrief")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gowa unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))

	var parsed gowaSendResp
	_ = json.Unmarshal(raw, &parsed)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := parsed.Message
		if msg == "" {
			msg = truncate(string(raw), 200)
		}
		return "", fmt.Errorf("gowa HTTP %d: %s", res.StatusCode, msg)
	}
	id := ""
	if parsed.Results != nil {
		id = parsed.Results.MessageID
	}
	return id, nil
}

// Health hits /app/status (preferred) or /health.
func (c *GowaClient) Health() error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("gowa not configured")
	}
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/app/status", nil)
	if err != nil {
		return err
	}
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("gowa health HTTP %d", res.StatusCode)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
