package polarsteps

import (
	"strings"
	"testing"
)

func TestParseConfigJSON(t *testing.T) {
	c, err := parseConfigJSON([]byte(`{"bifrostBaseUrl":"http://bifrost/v1","captionModel":"opencode-go/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || !c.Ready() {
		t.Fatal("enabled should default true")
	}
	c2, err := parseConfigJSON([]byte(`{"enabled":false,"bifrostBaseUrl":"http://b","captionModel":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c2.Enabled || c2.Ready() {
		t.Fatal("expected disabled")
	}
}

func TestCleanLabelStripsPNR(t *testing.T) {
	got := cleanLabel("Décollage Nice → Genève — LX523 (SWISS) · A220-300 · PNR 8WQZPY")
	if strings.Contains(got, "PNR") || strings.Contains(got, "LX523") || strings.Contains(got, "8WQZPY") {
		t.Fatalf("still dirty: %q", got)
	}
	if !strings.Contains(got, "Nice") {
		t.Fatalf("lost place: %q", got)
	}
}
