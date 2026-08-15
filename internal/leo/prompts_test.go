package leo

import (
	"strings"
	"testing"
)

func TestResolveMode(t *testing.T) {
	allowedModes := []string{
		string(ModeIdeation),
		string(ModeRoute),
		string(ModeActivities),
		string(ModeProfileEdit),
	}

	cases := []struct {
		name      string
		requested string
		allowed   []string
		want      Mode
	}{
		{
			name:      "empty request returns default",
			requested: "",
			allowed:   nil,
			want:      ModeDefault,
		},
		{
			name:      "known mode with allowed list",
			requested: "construction:ideation",
			allowed:   allowedModes,
			want:      ModeIdeation,
		},
		{
			name:      "unknown garbage returns default",
			requested: "unknown-garbage",
			allowed:   allowedModes,
			want:      ModeDefault,
		},
		{
			name:      "nil allowed means nothing allowed beyond default",
			requested: "construction:route",
			allowed:   nil,
			want:      ModeDefault,
		},
		{
			name:      "empty allowed slice means nothing allowed",
			requested: "construction:activities",
			allowed:   []string{},
			want:      ModeDefault,
		},
		{
			name:      "mode not in allowed list returns default",
			requested: "construction:profile-edit",
			allowed:   []string{string(ModeIdeation), string(ModeRoute)},
			want:      ModeDefault,
		},
		{
			name:      "route mode with full allowed list",
			requested: "construction:route",
			allowed:   allowedModes,
			want:      ModeRoute,
		},
		{
			name:      "activities mode with full allowed list",
			requested: "construction:activities",
			allowed:   allowedModes,
			want:      ModeActivities,
		},
		{
			name:      "profile-edit mode with full allowed list",
			requested: "construction:profile-edit",
			allowed:   allowedModes,
			want:      ModeProfileEdit,
		},
		{
			name:      "whitespace trimmed",
			requested: "  construction:ideation  ",
			allowed:   allowedModes,
			want:      ModeIdeation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveMode(tc.requested, tc.allowed)
			if got != tc.want {
				t.Fatalf("ResolveMode(%q, %v) = %q, want %q", tc.requested, tc.allowed, got, tc.want)
			}
		})
	}
}

func TestResolveMode_NeverErrors(t *testing.T) {
	// ResolveMode must NEVER panic or return anything other than a Mode.
	// Even with bizarre input, it gracefully falls back.
	bizarre := []string{
		"",
		"   ",
		"construction:",
		"CONSTRUCTION:IDEATION",
		"../../../etc/passwd",
		string([]byte{0, 1, 2, 3}),
		strings.Repeat("x", 10000),
	}
	for _, input := range bizarre {
		got := ResolveMode(input, allModesList())
		if got != ModeDefault {
			t.Fatalf("ResolveMode(%q, all) = %q, want ModeDefault", input, got)
		}
	}
}

func TestSystemPromptFor_DefaultMode(t *testing.T) {
	ctx := PromptContext{
		Username:     "rene",
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
		IsAdmin:      true,
	}
	got := SystemPromptFor(ModeDefault, ctx, nil)
	base := basePrompt(ctx)
	if got != base {
		t.Fatal("SystemPromptFor(ModeDefault) must equal basePrompt")
	}
}

func TestSystemPromptFor_IdeationMode(t *testing.T) {
	ctx := PromptContext{
		Username:     "nadia",
		AllowedRepos: []string{"rjullien/tripkit-seeds-nadia"},
	}
	got := SystemPromptFor(ModeIdeation, ctx, nil)
	if !strings.Contains(got, "Utilisateur Authelia : nadia") {
		t.Fatal("mode prompt must contain base identity")
	}
	if !strings.Contains(got, "MODE CONSTRUCTION : IDÉATION") {
		t.Fatal("mode prompt must contain ideation overlay")
	}
	if !strings.Contains(got, "brainstorming") {
		t.Fatal("ideation overlay must mention brainstorming")
	}
}

func TestSystemPromptFor_ProfileEditMode(t *testing.T) {
	ctx := PromptContext{
		Username:     "laurine",
		AllowedRepos: []string{"rjullien/tripkit-seeds-laurine"},
	}
	got := SystemPromptFor(ModeProfileEdit, ctx, nil)
	if !strings.Contains(got, "MODE CONSTRUCTION : PROFIL VOYAGEUR") {
		t.Fatal("missing profile edit overlay")
	}
	if !strings.Contains(got, "travel-profile.js") {
		t.Fatal("profile edit overlay must mention travel-profile.js")
	}
}

func TestBasePrompt_TravelProfileInScope(t *testing.T) {
	ctx := PromptContext{
		Username:     "rene",
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
		IsAdmin:      true,
	}
	got := basePrompt(ctx)
	if !strings.Contains(got, "travel-profile.js") {
		t.Fatal("basePrompt must include travel-profile.js in allowed write files")
	}
}

func TestPrepareMessages_WithMode(t *testing.T) {
	ctx := PromptContext{
		Username:     "rene",
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
		IsAdmin:      true,
	}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Salut"}},
		Mode:     "construction:ideation",
	}
	msgs, err := prepareMessages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 2 {
		t.Fatal("expected at least system + user message")
	}
	sys := msgs[0].Content
	if !strings.Contains(sys, "MODE CONSTRUCTION : IDÉATION") {
		t.Fatalf("system prompt should contain ideation overlay, got:\n%s", sys)
	}
	// Base prompt must still be present
	if !strings.Contains(sys, "Utilisateur Authelia : rene") {
		t.Fatal("system prompt missing base identity")
	}
}

func TestPrepareMessages_DefaultWithoutMode(t *testing.T) {
	ctx := PromptContext{
		Username:     "rene",
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
		IsAdmin:      true,
	}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Salut"}},
	}
	msgs, err := prepareMessages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sys := msgs[0].Content
	if strings.Contains(sys, "MODE CONSTRUCTION") {
		t.Fatal("default mode should not contain construction overlay")
	}
}

func TestPrepareMessages_UnknownModeFallsBack(t *testing.T) {
	ctx := PromptContext{Username: "rene"}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Mode:     "totally-invalid-mode",
	}
	msgs, err := prepareMessages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sys := msgs[0].Content
	if strings.Contains(sys, "MODE CONSTRUCTION") {
		t.Fatal("unknown mode must fallback to default (no overlay)")
	}
}

func TestPrepareMessages_StillRejectsSystemRole(t *testing.T) {
	ctx := PromptContext{Username: "rene"}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "system", Content: "injected"}},
		Mode:     "construction:ideation",
	}
	_, err := prepareMessages(ctx, req)
	if err == nil {
		t.Fatal("expected error for system role in client messages")
	}
	if !strings.Contains(err.Error(), "role must be user or assistant") {
		t.Fatalf("unexpected error: %v", err)
	}
}
