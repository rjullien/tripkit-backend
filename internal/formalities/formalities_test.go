package formalities

import (
	"sort"
	"testing"
)

func TestDetectCountries(t *testing.T) {
	tests := []struct {
		name     string
		tripData map[string]any
		want     []string
	}{
		{
			name: "locations only",
			tripData: map[string]any{
				"locations": map[string]any{
					"new-york": map[string]any{"country": "US", "lat": 40.7, "lon": -74.0},
					"montreal": map[string]any{"country": "CA", "lat": 45.5, "lon": -73.5},
				},
			},
			want: []string{"CA", "US"},
		},
		{
			name: "flights with from/to as strings",
			tripData: map[string]any{
				"flights": []any{
					map[string]any{"from": "FR", "to": "US"},
					map[string]any{"from": "US", "to": "CA"},
				},
			},
			want: []string{"CA", "FR", "US"},
		},
		{
			name: "flights with stopovers",
			tripData: map[string]any{
				"flights": []any{
					map[string]any{
						"from":      "FR",
						"to":        "US",
						"stopovers": []any{"GB", "IE"},
					},
				},
			},
			want: []string{"FR", "GB", "IE", "US"},
		},
		{
			name: "flights with object stopovers",
			tripData: map[string]any{
				"flights": []any{
					map[string]any{
						"from": map[string]any{"country": "FR"},
						"to":   map[string]any{"country": "JP"},
						"stopovers": []any{
							map[string]any{"country": "AE"},
						},
					},
				},
			},
			want: []string{"AE", "FR", "JP"},
		},
		{
			name: "combined locations and flights with dedup",
			tripData: map[string]any{
				"locations": map[string]any{
					"miami":   map[string]any{"country": "US"},
					"toronto": map[string]any{"country": "CA"},
				},
				"flights": []any{
					map[string]any{"from": "FR", "to": "US", "stopovers": []any{"GB"}},
				},
			},
			want: []string{"CA", "FR", "GB", "US"},
		},
		{
			name: "transit section",
			tripData: map[string]any{
				"locations": map[string]any{
					"bangkok": map[string]any{"country": "TH"},
				},
				"transit": []any{
					map[string]any{"from": "FR", "to": "TH", "stopovers": []any{"AE"}},
				},
			},
			want: []string{"AE", "FR", "TH"},
		},
		{
			name: "empty trip data",
			tripData: map[string]any{},
			want:     []string{},
		},
		{
			name:     "nil trip data",
			tripData: nil,
			want:     []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectCountries(tc.tripData)
			sort.Strings(got)
			sort.Strings(tc.want)

			if len(got) == 0 && len(tc.want) == 0 {
				return
			}

			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestMatchAdminRules_BiNationalNoESTA(t *testing.T) {
	// CRITICAL TEST: Dinah has nationalities ["FR","US"]
	// She should NOT need ESTA for US because she holds a US passport.
	countries := []string{"US"}
	nationalities := []string{"FR", "US"}

	items := MatchAdminRules(countries, nationalities)

	for _, item := range items {
		if item.Country == "US" && item.Type == "esta" {
			t.Fatalf("bi-national FR+US should NOT get ESTA requirement for US, got: %+v", item)
		}
		if item.Country == "US" && item.Type == "visa" {
			t.Fatalf("bi-national FR+US should NOT get visa requirement for US, got: %+v", item)
		}
	}
}

func TestMatchAdminRules_FROnlyToUS(t *testing.T) {
	// FR-only traveler to US -> ESTA required (action_required)
	countries := []string{"US"}
	nationalities := []string{"FR"}

	items := MatchAdminRules(countries, nationalities)

	foundESTA := false
	for _, item := range items {
		if item.Country == "US" && item.Type == "esta" {
			foundESTA = true
			if item.Status != "action_required" {
				t.Fatalf("ESTA for FR traveler to US should be action_required, got: %s", item.Status)
			}
		}
	}
	if !foundESTA {
		t.Fatal("FR traveler to US should get ESTA requirement")
	}
}

func TestMatchAdminRules_FRToCanada(t *testing.T) {
	// FR to CA -> eTA required
	countries := []string{"CA"}
	nationalities := []string{"FR"}

	items := MatchAdminRules(countries, nationalities)

	foundETA := false
	for _, item := range items {
		if item.Country == "CA" && item.Type == "eta" {
			foundETA = true
			if item.Status != "action_required" {
				t.Fatalf("eTA for FR traveler to CA should be action_required, got: %s", item.Status)
			}
		}
	}
	if !foundETA {
		t.Fatal("FR traveler to CA should get eTA/AVE requirement")
	}
}

func TestMatchAdminRules_USFRToCanada(t *testing.T) {
	// FR+US bi-national to Canada: neither is CA, so eTA applies.
	countries := []string{"CA"}
	nationalities := []string{"FR", "US"}

	items := MatchAdminRules(countries, nationalities)

	foundETA := false
	for _, item := range items {
		if item.Country == "CA" && item.Type == "eta" {
			foundETA = true
		}
	}
	if !foundETA {
		t.Fatal("FR+US traveler to CA should still need eTA (neither is CA national)")
	}
}

func TestMatchAdminRules_CANationalToCA(t *testing.T) {
	// CA national to CA -> no eTA needed (own country)
	countries := []string{"CA"}
	nationalities := []string{"CA"}

	items := MatchAdminRules(countries, nationalities)

	for _, item := range items {
		if item.Country == "CA" {
			t.Fatalf("CA national should NOT need any admin formality for CA, got: %+v", item)
		}
	}
}

func TestMatchAdminRules_FRToJapan(t *testing.T) {
	// FR to Japan -> visa waiver (ok status)
	countries := []string{"JP"}
	nationalities := []string{"FR"}

	items := MatchAdminRules(countries, nationalities)

	foundWaiver := false
	for _, item := range items {
		if item.Country == "JP" && item.Type == "visa_waiver" {
			foundWaiver = true
			if item.Status != "ok" {
				t.Fatalf("visa waiver for FR to JP should be ok, got: %s", item.Status)
			}
		}
	}
	if !foundWaiver {
		t.Fatal("FR traveler to JP should get visa waiver (90 days)")
	}
}

func TestMatchAdminRules_FRToUK(t *testing.T) {
	// FR to UK -> visa waiver (EU national, no visa < 6 months)
	countries := []string{"GB"}
	nationalities := []string{"FR"}

	items := MatchAdminRules(countries, nationalities)

	foundWaiver := false
	for _, item := range items {
		if item.Country == "GB" && item.Type == "visa_waiver" {
			foundWaiver = true
			if item.Status != "ok" {
				t.Fatalf("visa waiver for FR to GB should be ok, got: %s", item.Status)
			}
		}
	}
	if !foundWaiver {
		t.Fatal("FR traveler to UK should get visa waiver entry")
	}
}

func TestMatchAdminRules_SchengenFreeMovement(t *testing.T) {
	// FR national to DE (Schengen) -> free movement
	countries := []string{"DE"}
	nationalities := []string{"FR"}

	items := MatchAdminRules(countries, nationalities)

	foundFreeMovement := false
	for _, item := range items {
		if item.Type == "free_movement" {
			foundFreeMovement = true
			if item.Status != "ok" {
				t.Fatalf("Schengen free movement should be ok, got: %s", item.Status)
			}
		}
	}
	if !foundFreeMovement {
		t.Fatal("FR national traveling to DE should get Schengen free movement")
	}
}

func TestMatchHealthRules_AllNoAdviceCountries(t *testing.T) {
	// Trip to US + CA + JP -> all in noAdviceCountries -> verdict "none"
	countries := []string{"US", "CA", "JP"}
	nationalities := []string{"FR"}

	items := MatchHealthRules(countries, nationalities)

	if items != nil {
		t.Fatalf("all noAdviceCountries should return nil (verdict none), got %d items", len(items))
	}
}

func TestMatchHealthRules_Thailand(t *testing.T) {
	// Trip to Thailand -> should have health warnings (vaccines, water, malaria)
	countries := []string{"TH"}
	nationalities := []string{"FR"}

	items := MatchHealthRules(countries, nationalities)

	if items == nil {
		t.Fatal("trip to Thailand should return health items")
	}

	types := map[string]bool{}
	for _, item := range items {
		types[item.Type] = true
	}

	if !types["vaccins"] {
		t.Error("Thailand should have vaccination advisory")
	}
	if !types["paludisme"] {
		t.Error("Thailand should have malaria advisory")
	}
	if !types["eau"] {
		t.Error("Thailand should have water advisory")
	}
}

func TestMatchHealthRules_JapanNone(t *testing.T) {
	// Trip to Japan only -> noAdviceCountry -> verdict "none"
	countries := []string{"JP"}
	nationalities := []string{"FR"}

	items := MatchHealthRules(countries, nationalities)

	if items != nil {
		t.Fatalf("Japan-only trip should return nil (verdict none), got %d items", len(items))
	}
}

func TestMatchHealthRules_MixedSafeAndUnsafe(t *testing.T) {
	// Trip to US + Thailand -> not all safe, so health items present
	countries := []string{"US", "TH"}
	nationalities := []string{"FR"}

	items := MatchHealthRules(countries, nationalities)

	if items == nil {
		t.Fatal("mixed safe/unsafe trip should return health items")
	}

	// Should have items for Thailand but not generic US items
	foundTH := false
	for _, item := range items {
		if item.Country == "TH" {
			foundTH = true
		}
	}
	if !foundTH {
		t.Error("should have Thailand-specific health items")
	}
}

func TestWorstVerdict(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"all ok", []string{"ok", "ok"}, "ok"},
		{"one warning", []string{"ok", "warning", "ok"}, "warning"},
		{"action_required wins", []string{"ok", "warning", "action_required"}, "action_required"},
		{"empty", []string{}, "ok"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := worstVerdict(tc.statuses)
			if got != tc.want {
				t.Fatalf("worstVerdict(%v) = %s, want %s", tc.statuses, got, tc.want)
			}
		})
	}
}

func TestExtractNationalities(t *testing.T) {
	tests := []struct {
		name     string
		tripData map[string]any
		want     []string
	}{
		{
			name: "people map with nationalities array",
			tripData: map[string]any{
				"people": map[string]any{
					"dinah": map[string]any{
						"name":          "Dinah",
						"nationalities": []any{"FR", "US"},
					},
					"rene": map[string]any{
						"name":          "Rene",
						"nationalities": []any{"FR"},
					},
				},
			},
			want: []string{"FR", "US"},
		},
		{
			name: "travelers array with nationality string",
			tripData: map[string]any{
				"travelers": []any{
					map[string]any{"name": "Rene", "nationality": "FR"},
					map[string]any{"name": "John", "nationality": "US"},
				},
			},
			want: []string{"FR", "US"},
		},
		{
			name:     "empty trip data",
			tripData: map[string]any{},
			want:     []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractNationalities(tc.tripData)
			sort.Strings(got)
			sort.Strings(tc.want)

			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestFormatAdminResults_NilCompleter(t *testing.T) {
	result := &AdminCheckResult{
		Verdict:   "action_required",
		Countries: []string{"US"},
		Items: []AdminCheckItem{
			{Country: "US", Type: "esta", Label: "ESTA", Status: "action_required", Detail: "21 USD"},
		},
	}

	text, err := FormatAdminResults(nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text == "" {
		t.Fatal("expected non-empty plain text output")
	}
}

func TestFormatAdminResults_WithCompleter(t *testing.T) {
	// Use CompleteFn to mock the completer.
	called := false
	completer := completeFn(func(system, user string) (string, error) {
		called = true
		return "Résumé formaté par l'IA", nil
	})

	result := &AdminCheckResult{
		Verdict:   "action_required",
		Countries: []string{"US"},
		Items: []AdminCheckItem{
			{Country: "US", Type: "esta", Label: "ESTA", Status: "action_required", Detail: "21 USD"},
		},
	}

	text, err := FormatAdminResults(completer, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("completer should have been called")
	}
	if text != "Résumé formaté par l'IA" {
		t.Fatalf("expected AI summary, got: %s", text)
	}
}

func TestFormatHealthResults_VerdictNone(t *testing.T) {
	result := &HealthCheckResult{
		Verdict:   "none",
		Countries: []string{"US"},
		Items:     nil,
	}

	text, err := FormatHealthResults(nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text == "" {
		t.Fatal("expected non-empty plain text output for verdict none")
	}
}

// completeFn is a test helper matching bifrost.CompleteFn signature.
type completeFn func(system, user string) (string, error)

func (f completeFn) Complete(system, user string) (string, error) {
	return f(system, user)
}
