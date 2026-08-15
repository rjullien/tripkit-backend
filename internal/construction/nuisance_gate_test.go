package construction

import "testing"

func codes(vs []QAViolation) map[string]string {
	out := map[string]string{}
	for _, v := range vs {
		out[v.Code] = v.Severity
	}
	return out
}

// TestNuisanceBlockers covers SPEC §8: an untreated red verdict blocks the
// Ph3 → Ph4 transition, and an indeterminate result is never silent.
func TestNuisanceBlockers(t *testing.T) {
	cases := []struct {
		name         string
		verdicts     []nuisanceVerdict
		targetPhase  int
		wantCode     string
		wantSeverity string
	}{
		{
			name:         "red blocks entering phase 4",
			verdicts:     []nuisanceVerdict{{LocationID: "h1", LocationName: "Hotel Bruyant", Verdict: "ELEVE"}},
			targetPhase:  4,
			wantCode:     "nuisance_unresolved",
			wantSeverity: "red",
		},
		{
			name:         "red only warns before phase 4",
			verdicts:     []nuisanceVerdict{{LocationID: "h1", Verdict: "ELEVE"}},
			targetPhase:  3,
			wantCode:     "nuisance_unresolved",
			wantSeverity: "yellow",
		},
		{
			name:         "indeterminate warns at every phase",
			verdicts:     []nuisanceVerdict{{LocationID: "h1", Verdict: "INDETERMINE"}},
			targetPhase:  4,
			wantCode:     "nuisance_indeterminate",
			wantSeverity: "yellow",
		},
		{
			name:         "green but partial still warns",
			verdicts:     []nuisanceVerdict{{LocationID: "h1", Verdict: "FAIBLE", Partial: true}},
			targetPhase:  4,
			wantCode:     "nuisance_indeterminate",
			wantSeverity: "yellow",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codes(NuisanceBlockers(tc.verdicts, tc.targetPhase))
			sev, ok := got[tc.wantCode]
			if !ok {
				t.Fatalf("want code %q, got %+v", tc.wantCode, got)
			}
			if sev != tc.wantSeverity {
				t.Errorf("severity = %q, want %q", sev, tc.wantSeverity)
			}
		})
	}
}

// TestNuisanceBlockers_CleanIsSilent: a fully green verdict adds nothing, so the
// gate does not invent work.
func TestNuisanceBlockers_CleanIsSilent(t *testing.T) {
	got := NuisanceBlockers([]nuisanceVerdict{
		{LocationID: "h1", Verdict: "FAIBLE"},
		{LocationID: "h2", Verdict: "MODERE"},
	}, 4)
	if len(got) != 0 {
		t.Errorf("want no violations for a clean check, got %+v", got)
	}
}

// TestNuisanceBlockers_BlocksTransition ties the gate to CanTransition.
func TestNuisanceBlockers_BlocksTransition(t *testing.T) {
	violations := NuisanceBlockers([]nuisanceVerdict{
		{LocationID: "h1", LocationName: "Hotel Bruyant", Verdict: "ELEVE"},
	}, 4)

	allowed, blockers := CanTransition(violations, 4, false)
	if allowed {
		t.Fatal("a red nuisance verdict must block the Ph3 -> Ph4 transition")
	}
	if len(blockers) == 0 {
		t.Error("want the blocker list returned, not a bare refusal")
	}
}

// TestNuisanceBlockers_NoDataIsSilent: never analysed is not the same as failed.
// The gate must not block a trip that simply has not run the check.
func TestNuisanceBlockers_NoDataIsSilent(t *testing.T) {
	if got := NuisanceBlockers(nil, 4); len(got) != 0 {
		t.Errorf("want no violations without nuisance data, got %+v", got)
	}
}
