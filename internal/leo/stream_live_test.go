//go:build live

package leo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run with:
//   TRIPKIT_HERMES_API_KEY=… TRIPKIT_HERMES_BASE_URL=http://127.0.0.1:18642 \
//     go test -tags=live ./internal/leo/ -run TestStreamChat_AgainstHermes -v -count=1
func TestStreamChat_AgainstHermes(t *testing.T) {
	if os.Getenv("TRIPKIT_HERMES_API_KEY") == "" {
		t.Skip("TRIPKIT_HERMES_API_KEY not set")
	}
	cfg := LoadConfigFromEnv()
	if !cfg.Ready() {
		t.Fatal("config not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var full strings.Builder
	err := cfg.StreamChat(ctx, PromptContext{
		Username:     "rene",
		IsAdmin:      true,
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
	}, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Réponds uniquement: STREAM_LIVE_OK"}},
	}, func(event string, data StreamEvent) error {
		t.Logf("event=%s text=%q reply=%q tool=%v err=%q", event, data.Text, data.Reply, data.Tool, data.Error)
		if event == "delta" {
			full.WriteString(data.Text)
		}
		if event == "error" {
			t.Fatalf("stream error: %+v", data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := full.String()
	if got == "" {
		t.Fatal("empty stream reply")
	}
	t.Logf("assembled=%q", got)
}
