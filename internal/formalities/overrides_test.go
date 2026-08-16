package formalities

import (
	"encoding/json"
	"testing"
)

func TestApplyAdminOverrides_PatchesCost(t *testing.T) {
	raw := json.RawMessage(`{"cost":"10 CAD"}`)
	got := ApplyAdminOverrides(baseAdminRules, map[string]json.RawMessage{"CA.eta": raw})
	var eta *AdminRule
	for i := range got {
		if got[i].Country == "CA" && got[i].Type == "eta" {
			eta = &got[i]
			break
		}
	}
	if eta == nil {
		t.Fatal("CA.eta missing after overlay")
	}
	if eta.Cost != "10 CAD" {
		t.Errorf("cost = %q, want 10 CAD", eta.Cost)
	}
	if eta.URL == "" {
		t.Error("URL should be kept from the base rule")
	}
}

func TestApplyAdminOverrides_EmptyIsNoop(t *testing.T) {
	got := ApplyAdminOverrides(baseAdminRules, map[string]json.RawMessage{})
	if len(got) != len(baseAdminRules) {
		t.Fatalf("len=%d want %d", len(got), len(baseAdminRules))
	}
}

func TestPendingAdminActions_FRToCanada(t *testing.T) {
	items := PendingAdminActions(map[string]any{
		"locations": map[string]any{"mtl": map[string]any{"country": "CA"}},
		"people":    map[string]any{"dinah": map[string]any{"nationalities": []any{"FR"}}},
	})
	if len(items) == 0 {
		t.Fatal("expected at least the Canadian eTA")
	}
	found := false
	for _, it := range items {
		if it.Type == "eta" && it.Country == "CA" && it.Status == "action_required" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing CA eTA action_required, got %+v", items)
	}
}
