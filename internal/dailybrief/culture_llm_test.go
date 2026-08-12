package dailybrief

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateCultureExpress_RetryThenOk(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		_ = body
		text := `{"text":"Tutoiement facile ici : le « tu » est la norme au Québec."}`
		if calls == 1 {
			// Too close to alreadySent.
			text = `{"text":"Au Québec on dit bienvenue pour de rien. Pourboire resto ~15 %."}`
		}
		_ = json.NewEncoder(w).Encode(bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: text}}}})
	}))
	defer srv.Close()

	c := NewBifrostClient(srv.URL, "", "test-model")
	used := []string{"Au Québec on dit « bienvenue » pour « de rien ». Pourboire resto ~15 % si service non inclus."}
	tip, err := c.GenerateCultureExpress(&DayBriefData{PlaceName: "Québec"}, used)
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || tip.Key == "" || strings.Contains(strings.ToLower(tip.Text), "bienvenue") {
		t.Fatalf("want fresh tip, got %#v", tip)
	}
	if calls != 2 {
		t.Fatalf("want 1 retry, calls=%d", calls)
	}
}

func TestGenerateCultureExpress_FailsAfterRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: `{"text":"Au Québec on dit bienvenue pour de rien. Pourboire resto ~15 %."}`}}}})
	}))
	defer srv.Close()

	c := NewBifrostClient(srv.URL, "", "test-model")
	used := []string{"Au Québec on dit « bienvenue » pour « de rien ». Pourboire resto ~15 % si service non inclus."}
	_, err := c.GenerateCultureExpress(&DayBriefData{PlaceName: "Québec"}, used)
	if err == nil {
		t.Fatal("expected redite error after retry")
	}
}
