package leo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusPayload_MissingKey(t *testing.T) {
	cfg := Config{
		BaseURL:      defaultBaseURL,
		DashboardURL: defaultDashboardURL,
		TelegramURL:  "https://t.me/example",
	}
	st := cfg.StatusPayload()
	if st.Ready {
		t.Fatal("expected ready=false without API key")
	}
	if st.Reason != "missing_hermes_key" {
		t.Fatalf("reason=%q", st.Reason)
	}
	if st.DashboardURL != defaultDashboardURL {
		t.Fatalf("dashboard=%q", st.DashboardURL)
	}
	if st.TelegramURL != "https://t.me/example" {
		t.Fatalf("telegram=%q", st.TelegramURL)
	}
}

func TestExtractHermesError(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"error":"invalid_api_key"}`, "invalid_api_key"},
		{`{"error":{"message":"model overloaded","code":"overloaded"}}`, "model overloaded"},
		{`{"message":"nope"}`, "nope"},
		{`plain failure`, "plain failure"},
		{``, ""},
	}
	for _, tc := range cases {
		got := extractHermesError([]byte(tc.raw))
		if got != tc.want {
			t.Fatalf("raw=%q got=%q want=%q", tc.raw, got, tc.want)
		}
	}
}

func TestSystemPrompt_ScopedUser(t *testing.T) {
	p := SystemPrompt(PromptContext{
		Username:     "nadia",
		AllowedRepos: []string{"rjullien/tripkit-seeds-nadia"},
		TripID:       "sicile-2026",
	})
	for _, needle := range []string{
		"Utilisateur Authelia : nadia",
		"Repo seed autorisé UNIQUEMENT : rjullien/tripkit-seeds-nadia",
		"Je suis Léo, ton agent de planification de voyage",
		"je ne peux pas répondre à d'autres questions",
		"Voyage actif",
		"sicile-2026",
		"économie de tokens",
	} {
		if !strings.Contains(p, needle) {
			t.Fatalf("prompt missing %q\n%s", needle, p)
		}
	}
	if strings.Contains(p, "rjullien/tripkit-seeds-laurine") {
		t.Fatal("scoped prompt must not mention laurine repo")
	}
	if strings.Contains(p, "Repo seed autorisé UNIQUEMENT : rjullien/tripkit-seeds\n") {
		t.Fatal("scoped prompt must not authorize jullien repo for Nadia")
	}
}

func TestSystemPrompt_NoRepos(t *testing.T) {
	p := SystemPrompt(PromptContext{Username: "guest"})
	if !strings.Contains(p, "AUCUN") {
		t.Fatalf("expected AUCUN repos:\n%s", p)
	}
}

func TestChat_RequiresMessages(t *testing.T) {
	cfg := Config{BaseURL: "http://example", APIKey: "k"}
	_, err := cfg.Chat(PromptContext{Username: "rene"}, ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "messages required") {
		t.Fatalf("err=%v", err)
	}
}

func TestChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("auth=%q", got)
		}
		var body openAIReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) < 2 || body.Messages[0].Role != "system" {
			t.Fatalf("messages=%+v", body.Messages)
		}
		sys := body.Messages[0].Content
		if !strings.Contains(sys, "Utilisateur Authelia : rene") {
			t.Fatalf("system prompt missing user: %s", sys)
		}
		if !strings.Contains(sys, "rjullien/tripkit-seeds") {
			t.Fatalf("system prompt missing allowed repo: %s", sys)
		}
		_ = json.NewEncoder(w).Encode(openAIResp{
			Model: "test-model",
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{{Message: ChatMessage{Role: "assistant", Content: "OK je m'en occupe"}}},
		})
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:    srv.URL,
		APIKey:     "secret",
		HTTPClient: srv.Client(),
	}
	resp, err := cfg.Chat(PromptContext{
		Username:     "rene",
		AllowedRepos: []string{"rjullien/tripkit-seeds"},
		IsAdmin:      true,
	}, ChatRequest{
		TripID:   "quebec-2026",
		Messages: []ChatMessage{{Role: "user", Content: "Ajoute un jour à Québec"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reply != "OK je m'en occupe" {
		t.Fatalf("reply=%q", resp.Reply)
	}
}
