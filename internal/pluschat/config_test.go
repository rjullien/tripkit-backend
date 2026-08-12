package pluschat

import (
	"strings"
	"testing"
)

func TestParseConfigJSON(t *testing.T) {
	raw := []byte(`{"bifrostBaseUrl":"http://bifrost/v1","chatModel":"opencode-go/x"}`)
	c, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled {
		t.Fatal("enabled should default true when omitted")
	}
	if c.ChatModel != "opencode-go/x" {
		t.Fatalf("model=%q", c.ChatModel)
	}

	raw2 := []byte(`{"enabled":false,"bifrostBaseUrl":"http://bifrost/v1","chatModel":"m"}`)
	c2, err := parseConfigJSON(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Enabled || c2.Ready() {
		t.Fatal("expected disabled")
	}
}

func TestPrepareMessages(t *testing.T) {
	_, err := prepareMessages(PromptContext{Username: "rene"}, ChatRequest{})
	if err == nil {
		t.Fatal("expected empty messages error")
	}
	msgs, err := prepareMessages(PromptContext{Username: "rene", TripID: "quebec-2026"}, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "salut"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "system" {
		t.Fatalf("got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "quebec-2026") {
		t.Fatal("system prompt should mention trip")
	}
}
