package nuisance

import (
	"context"
	"errors"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/discovery"
)

// failingQuerier always errors, like a rate-limited Overpass instance.
type failingQuerier struct{ calls int }

func (f *failingQuerier) Search(ctx context.Context, lat, lon float64, theme discovery.Theme) ([]discovery.Item, error) {
	f.calls++
	return nil, errors.New("overpass 429")
}

// TestScoreCategoryUnavailable_IsNotGreen is the unit-level guard: a category
// whose data source failed must not be scored FAIBLE.
func TestScoreCategoryUnavailable_IsNotGreen(t *testing.T) {
	cat := NuisanceCategory{ID: "trains", Label: "voie ferree", Emoji: "🚆"}
	got := ScoreCategoryUnavailable(cat, "Overpass")

	if got.Level != LevelIndetermine {
		t.Fatalf("level = %q, want %q", got.Level, LevelIndetermine)
	}
	if got.Level == LevelFaible {
		t.Fatal("a failed query must never score FAIBLE")
	}
	if got.Detail == "" {
		t.Error("want a detail explaining the data source failed")
	}
}

// TestGlobalVerdict_UnknownBeatsGreen covers the rollup: all-green plus one
// unevaluated category is INDETERMINE, not FAIBLE.
func TestGlobalVerdict_UnknownBeatsGreen(t *testing.T) {
	cases := []struct {
		name string
		in   []CategoryResult
		want string
	}{
		{
			name: "all green stays green",
			in: []CategoryResult{
				{Level: LevelFaible}, {Level: LevelFaible},
			},
			want: LevelFaible,
		},
		{
			name: "green plus unknown is indeterminate",
			in: []CategoryResult{
				{Level: LevelFaible}, {Level: LevelIndetermine},
			},
			want: LevelIndetermine,
		},
		{
			name: "yellow outranks unknown",
			in: []CategoryResult{
				{Level: LevelIndetermine}, {Level: LevelModere},
			},
			want: LevelModere,
		},
		{
			name: "red still wins",
			in: []CategoryResult{
				{Level: LevelIndetermine}, {Level: LevelEleve},
			},
			want: LevelEleve,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GlobalVerdict(tc.in); got != tc.want {
				t.Errorf("GlobalVerdict = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerdictEmoji_Indeterminate(t *testing.T) {
	if got := VerdictEmoji(LevelIndetermine); got != "❓" {
		t.Errorf("emoji = %q, want ❓ (never a green light)", got)
	}
}

func TestHasUnknown(t *testing.T) {
	if HasUnknown([]CategoryResult{{Level: LevelFaible}}) {
		t.Error("no unknown category, want false")
	}
	if !HasUnknown([]CategoryResult{{Level: LevelFaible}, {Level: LevelIndetermine}}) {
		t.Error("one unknown category, want true")
	}
}

// TestAnalyzeLocation_OverpassDownIsNotGreen is the integration-level guard:
// with every Overpass call failing, the location must not come back 🟢 FAIBLE.
// This is the behaviour that made a rate-limited request look like a quiet hotel.
func TestAnalyzeLocation_OverpassDownIsNotGreen(t *testing.T) {
	fq := &failingQuerier{}
	svc := &Service{Overpass: fq}

	got := svc.analyzeLocation(context.Background(), location{
		id: "hotel-1", name: "Hotel Test", lat: 45.5, lon: -73.6,
	})

	if fq.calls == 0 {
		t.Fatal("expected Overpass to be queried")
	}
	if got.Verdict == LevelFaible {
		t.Fatalf("verdict = FAIBLE with a dead Overpass: this is the fail-open bug")
	}
	if got.Verdict != LevelIndetermine {
		t.Errorf("verdict = %q, want %q", got.Verdict, LevelIndetermine)
	}
	if !got.Partial {
		t.Error("Partial must be true when a category could not be evaluated")
	}

	// Every tag-based category should be reported as indeterminate rather than
	// silently scored on zero items.
	var unknown int
	for _, c := range got.Categories {
		if c.Level == LevelIndetermine {
			unknown++
		}
	}
	if unknown == 0 {
		t.Error("want at least one INDETERMINE category")
	}
}
