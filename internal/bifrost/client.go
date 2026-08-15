package bifrost

import (
	"net/http"
	"strings"
	"time"
)

// Client talks to an OpenAI-compatible chat/completions endpoint (Bifrost).
type Client struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// NewClient builds a ready-to-use Bifrost client.
func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Model:   strings.TrimSpace(model),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}
