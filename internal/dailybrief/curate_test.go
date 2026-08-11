package dailybrief

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurateActualites_PicksOnlyMatchingCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: `[
			{"title":"Festival d'été de Québec","source":"Le Soleil","url":"https://example.com/fest","detail":"Festival d'été — spectacles ce soir sur les Plaines."},
			{"title":"Invented junk","source":"Fake","detail":"nope"}
		]`}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewBifrostClient(srv.URL, "", "test-model")
	data := &DayBriefData{
		PlaceName: "Québec", Date: "2026-08-16",
		LocationID: "quebec", PlaceStayFrom: "2026-08-15", PlaceStayTo: "2026-08-17",
	}
	candidates := []ActualiteItem{
		{Title: "Évènement antiavortement annulé", Source: "La Presse"},
		{Title: "Festival d'été de Québec", Source: "Le Soleil", URL: "https://example.com/fest", Snippet: "Spectacles ce soir."},
		{Title: "Exposition au musée", Source: "Le Devoir"},
	}
	out, err := c.CurateActualites(data, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Festival d'été de Québec" {
		t.Fatalf("want only Festival candidate, got %#v", out)
	}
	if out[0].Detail == "" || out[0].URL != "https://example.com/fest" {
		t.Fatalf("want detail+url from candidate, got %#v", out[0])
	}
}

func TestCurateActualites_RejectsDeniedEvenIfLLMReturns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "chat") {
			http.NotFound(w, r)
			return
		}
		resp := bifrostResp{Choices: []struct {
			Message bifrostMsg `json:"message"`
		}{{Message: bifrostMsg{Role: "assistant", Content: `[{"title":"Évènement antiavortement annulé","source":"La Presse"}]`}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewBifrostClient(srv.URL, "", "test-model")
	out, err := c.CurateActualites(&DayBriefData{PlaceName: "Québec"}, []ActualiteItem{
		{Title: "Évènement antiavortement annulé", Source: "La Presse"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("denied title must be dropped, got %#v", out)
	}
}
