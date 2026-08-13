package leo

import (
	"strings"
	"testing"
)

func TestParseOpsJSON(t *testing.T) {
	c, err := parseOpsJSON([]byte(`{
		"defaultModel": "opencode-go/deepseek-v4-pro",
		"models": [
			{"id": "opencode-go/deepseek-v4-flash", "label": "Flash"},
			{"id": "opencode-go/deepseek-v4-pro", "label": "Pro"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultModel != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("default=%q", c.DefaultModel)
	}
	if len(c.Models) != 2 || c.Models[1].Label != "Pro" {
		t.Fatalf("models=%+v", c.Models)
	}
}

func TestParseOpsJSON_DefaultNotInList(t *testing.T) {
	c, err := parseOpsJSON([]byte(`{
		"defaultModel": "opencode-go/kimi-k2.6",
		"models": [{"id": "opencode-go/deepseek-v4-flash", "label": "Flash"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Models[0].ID != "opencode-go/kimi-k2.6" {
		t.Fatalf("expected default prepended, got %+v", c.Models)
	}
}

func TestParseOpsJSON_RejectsEmpty(t *testing.T) {
	if _, err := parseOpsJSON([]byte(`{"models":[{"id":"x"}]}`)); err == nil {
		t.Fatal("expected missing defaultModel")
	}
	if _, err := parseOpsJSON([]byte(`{"defaultModel":"x","models":[]}`)); err == nil {
		t.Fatal("expected missing models")
	}
}

func TestOpsResolve_AllowlistAndFallback(t *testing.T) {
	ops := DefaultOpsConfig()
	if got := ops.Resolve("opencode-go/deepseek-v4-pro"); got != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("pro=%q", got)
	}
	if got := ops.Resolve(""); got != defaultLeoModel {
		t.Fatalf("empty=%q", got)
	}
	if got := ops.Resolve("gpt-evil"); got != defaultLeoModel {
		t.Fatalf("unknown=%q", got)
	}
	if got := ops.Resolve("  opencode-go/glm-5.2  "); got != "opencode-go/glm-5.2" {
		t.Fatalf("trim=%q", got)
	}
}

func TestStatusWithOps(t *testing.T) {
	st := Config{APIKey: "k", BaseURL: "http://h"}.StatusPayload().WithOps(DefaultOpsConfig())
	if !st.Ready {
		t.Fatal("expected ready")
	}
	if st.DefaultModel != defaultLeoModel {
		t.Fatalf("default=%q", st.DefaultModel)
	}
	if len(st.Models) < 2 {
		t.Fatalf("models=%+v", st.Models)
	}
	joined := ""
	for _, m := range st.Models {
		joined += m.ID + ","
	}
	if !strings.Contains(joined, "opencode-go/deepseek-v4-pro") {
		t.Fatalf("missing pro in %s", joined)
	}
}
