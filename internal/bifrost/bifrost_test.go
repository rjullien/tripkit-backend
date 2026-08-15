package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    bool
		wantText   string
		errContain string
	}{
		{
			name: "successful completion returns parsed text",
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := chatResp{
					Choices: []struct {
						Message chatMsg `json:"message"`
					}{
						{Message: chatMsg{Role: "assistant", Content: "Hello world"}},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantText: "Hello world",
		},
		{
			name: "HTTP error returns error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := chatResp{
					Error: &struct {
						Message string `json:"message"`
					}{Message: "rate limit exceeded"},
				}
				json.NewEncoder(w).Encode(resp)
			},
			wantErr:    true,
			errContain: "rate limit exceeded",
		},
		{
			name: "empty choices returns error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := chatResp{Choices: nil}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantErr:    true,
			errContain: "no choices",
		},
		{
			name: "request contains correct headers and body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-key" {
					http.Error(w, "bad auth", 401)
					return
				}
				if r.Header.Get("Content-Type") != "application/json" {
					http.Error(w, "bad content-type", 400)
					return
				}
				var req chatReq
				json.NewDecoder(r.Body).Decode(&req)
				if req.Model != "gpt-4" {
					http.Error(w, "bad model: "+req.Model, 400)
					return
				}
				if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
					http.Error(w, "bad messages", 400)
					return
				}
				if req.MaxTokens != 800 {
					http.Error(w, fmt.Sprintf("bad max_tokens: %d", req.MaxTokens), 400)
					return
				}
				resp := chatResp{
					Choices: []struct {
						Message chatMsg `json:"message"`
					}{
						{Message: chatMsg{Role: "assistant", Content: "ok"}},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantText: "ok",
		},
		{
			name: "WithMaxTokens overrides default",
			handler: func(w http.ResponseWriter, r *http.Request) {
				var req chatReq
				json.NewDecoder(r.Body).Decode(&req)
				if req.MaxTokens != 2000 {
					http.Error(w, fmt.Sprintf("expected 2000 got %d", req.MaxTokens), 400)
					return
				}
				resp := chatResp{
					Choices: []struct {
						Message chatMsg `json:"message"`
					}{
						{Message: chatMsg{Role: "assistant", Content: "big"}},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			},
			wantText: "big",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			c := NewClient(srv.URL, "test-key", "gpt-4")

			var (
				got string
				err error
			)
			if tc.name == "WithMaxTokens overrides default" {
				got, err = c.Complete(context.Background(), "gpt-4", "sys", "usr", WithMaxTokens(2000))
			} else {
				got, err = c.Complete(context.Background(), "gpt-4", "sys", "usr")
			}

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContain != "" && !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantText {
				t.Fatalf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestWithRetry(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		failN    int // number of times fn fails before success
		wantErr  bool
		wantText string
	}{
		{
			name:     "succeeds on first attempt",
			attempts: 3,
			failN:    0,
			wantText: "ok",
		},
		{
			name:     "retry succeeds on second attempt",
			attempts: 3,
			failN:    1,
			wantText: "ok",
		},
		{
			name:     "all attempts fail",
			attempts: 2,
			failN:    5,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fn := func() (string, error) {
				calls++
				if calls <= tc.failN {
					return "", fmt.Errorf("fail #%d", calls)
				}
				return "ok", nil
			}

			got, err := WithRetry(tc.attempts, fn)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantText {
				t.Fatalf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestStreamComplete(t *testing.T) {
	tests := []struct {
		name       string
		sseBody    string
		wantFull   string
		wantDeltas []string
		wantErr    bool
		errContain string
	}{
		{
			name: "stream delivers concatenated text",
			sseBody: "data: " + mustJSON(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": "Hello"}}},
			}) + "\n\n" +
				"data: " + mustJSON(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": " world"}}},
			}) + "\n\n" +
				"data: [DONE]\n\n",
			wantFull:   "Hello world",
			wantDeltas: []string{"Hello", " world"},
		},
		{
			name: "stream with error chunk",
			sseBody: "data: " + mustJSON(map[string]any{
				"error": map[string]string{"message": "overloaded"},
			}) + "\n\n",
			wantErr:    true,
			errContain: "overloaded",
		},
		{
			name:     "empty stream returns empty string",
			sseBody:  "data: [DONE]\n\n",
			wantFull: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.Write([]byte(tc.sseBody))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "key", "model")
			var deltas []string
			got, err := c.StreamComplete(context.Background(), "model", "sys", "usr", func(delta string) error {
				deltas = append(deltas, delta)
				return nil
			})

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContain != "" && !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantFull {
				t.Fatalf("full text got %q, want %q", got, tc.wantFull)
			}
			if tc.wantDeltas != nil {
				if len(deltas) != len(tc.wantDeltas) {
					t.Fatalf("deltas count got %d, want %d: %v", len(deltas), len(tc.wantDeltas), deltas)
				}
				for i, d := range deltas {
					if d != tc.wantDeltas[i] {
						t.Fatalf("delta[%d] got %q, want %q", i, d, tc.wantDeltas[i])
					}
				}
			}
		})
	}
}

func TestCompleterInterface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResp{
			Choices: []struct {
				Message chatMsg `json:"message"`
			}{
				{Message: chatMsg{Role: "assistant", Content: "interface works"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "model")

	// Client.AsCompleter satisfies Completer interface
	var comp Completer = c.AsCompleter()
	got, err := comp.Complete("sys", "usr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "interface works" {
		t.Fatalf("got %q, want %q", got, "interface works")
	}
}

func TestCompleteFn(t *testing.T) {
	var comp Completer = CompleteFn(func(system, user string) (string, error) {
		return "fake: " + user, nil
	})
	got, err := comp.Complete("sys", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fake: hello" {
		t.Fatalf("got %q, want %q", got, "fake: hello")
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
