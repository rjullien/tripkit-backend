package discovery

import (
	"strings"
	"testing"
)

func TestRankItems_Table(t *testing.T) {
	tests := []struct {
		name      string
		items     []Item
		cfg       RankConfig
		wantFirst string
		wantLast  string
		wantLen   int
	}{
		{
			name: "distance only when no preferences",
			items: []Item{
				{Name: "Far Place", ThemeID: "rando", DistKm: 20},
				{Name: "Near Place", ThemeID: "outlets", DistKm: 2},
				{Name: "Mid Place", ThemeID: "rando", DistKm: 10},
			},
			cfg:       RankConfig{},
			wantFirst: "Near Place",
			wantLast:  "Far Place",
			wantLen:   3,
		},
		{
			name: "liked item boosted above closer item",
			items: []Item{
				{Name: "Generic Shop", ThemeID: "outlets", DistKm: 3},
				{Name: "Aviation Museum", ThemeID: "musees", DistKm: 6},
				{Name: "Random Cafe", ThemeID: "food", DistKm: 5},
			},
			cfg: RankConfig{
				Interests: map[string]InterestPref{
					"rene": {Likes: []string{"aviation"}, Dislikes: nil},
				},
			},
			wantFirst: "Aviation Museum",
			wantLast:  "Random Cafe",
			wantLen:   3,
		},
		{
			name: "disliked item demoted but still present",
			items: []Item{
				{Name: "Shopping Mall", ThemeID: "shopping", DistKm: 1},
				{Name: "National Park", ThemeID: "rando", DistKm: 8},
				{Name: "Village Market", ThemeID: "marches", DistKm: 5},
			},
			cfg: RankConfig{
				Interests: map[string]InterestPref{
					"rene": {Likes: nil, Dislikes: []string{"shopping"}},
				},
			},
			wantFirst: "Village Market",
			wantLast:  "Shopping Mall",
			wantLen:   3,
		},
		{
			name: "multiple persons combined scoring",
			items: []Item{
				{Name: "Hot Springs Resort", ThemeID: "nature", DistKm: 12},
				{Name: "Art Moderne Gallery", ThemeID: "musees", DistKm: 3},
				{Name: "Village Charm", ThemeID: "villages", DistKm: 10},
			},
			cfg: RankConfig{
				Interests: map[string]InterestPref{
					"laurine": {Likes: []string{"hot springs"}, Dislikes: nil},
					"nicole":  {Likes: []string{"villages"}, Dislikes: nil},
					"rene":    {Likes: nil, Dislikes: []string{"art moderne"}},
				},
			},
			wantFirst: "Village Charm",
			wantLast:  "Art Moderne Gallery",
			wantLen:   3,
		},
		{
			name: "theme ID match for like keyword",
			items: []Item{
				{Name: "Some Place", ThemeID: "aviation", DistKm: 7},
				{Name: "Closer Spot", ThemeID: "food", DistKm: 3},
			},
			cfg: RankConfig{
				Interests: map[string]InterestPref{
					"rene": {Likes: []string{"aviation"}, Dislikes: nil},
				},
			},
			wantFirst: "Some Place",
			wantLast:  "Closer Spot",
			wantLen:   2,
		},
		{
			name: "empty items returns empty",
			items: nil,
			cfg: RankConfig{
				Interests: map[string]InterestPref{
					"rene": {Likes: []string{"x"}, Dislikes: nil},
				},
			},
			wantFirst: "",
			wantLast:  "",
			wantLen:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RankItems(tc.items, tc.cfg)
			if len(got) != tc.wantLen {
				t.Fatalf("len=%d, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen == 0 {
				return
			}
			if got[0].Name != tc.wantFirst {
				t.Errorf("first=%q, want %q; scores: %+v", got[0].Name, tc.wantFirst, got)
			}
			if got[len(got)-1].Name != tc.wantLast {
				t.Errorf("last=%q, want %q; scores: %+v", got[len(got)-1].Name, tc.wantLast, got)
			}
		})
	}
}

// TestMatchKeyword_RealSeedVocabulary feeds the matcher the ACTUAL keywords
// stored in tripkit-seeds/travel-profile.js (multi-word, accented, plural)
// against realistic OSM names. Whole-phrase substring matching failed every one
// of these except "aviation".
func TestMatchKeyword_RealSeedVocabulary(t *testing.T) {
	tests := []struct {
		name    string
		item    string
		themeID string
		keyword string
		want    bool
	}{
		{"parcs nationaux matches a singular national park", "Parc national de la Jacques-Cartier", "rando", "parcs nationaux", true},
		{"musees techniques matches a technical museum", "Musée des techniques de Montréal", "musees", "musées techniques", true},
		{"musees d'art moderne matches a modern art museum", "Musée d'art moderne de Québec", "musees", "musées d'art moderne", true},
		{"musees d'art moderne does not match a technical museum", "Musée des techniques de Montréal", "musees", "musées d'art moderne", false},
		{"marches matches the accented market name", "Marché du Vieux-Port", "marches", "marchés", true},
		{"shopping long does not match an unrelated shop", "Boutique Souvenirs du Port", "shopping", "shopping long", false},
		{"shopping long matches a long shopping session venue", "Long Shopping Outlet Mall", "shopping", "shopping long", true},
		{"villages matches a village name", "Village de Sainte-Rose", "villages", "villages", true},
		{"rando difficile does not match an easy trail", "Sentier facile du lac", "rando", "rando difficile", false},
		{"aviation still matches on the theme id", "Some Place", "aviation", "aviation", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword)
			if got != tc.want {
				t.Errorf("matchKeyword(%q, %q, %q) = %v, want %v", tc.item, tc.themeID, tc.keyword, got, tc.want)
			}
		})
	}
}

// TestMatchKeyword_PluralsAndWordBoundaries covers the two edges of the matcher:
//
//   - "-aux" plurals: mapping them all to "-al" is right for the "national"
//     family only, so "châteaux" became "chateal" and could never match
//     "Château Frontenac" — a false negative on an entirely plausible interest;
//   - substring matching: "long" (from "shopping long") matched inside
//     "Longueuil" and the one-letter "d" of "musées d'art moderne" matched
//     nearly any name — a false positive on a dislike costs +10 and demotes a
//     legitimate item.
func TestMatchKeyword_PluralsAndWordBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		item    string
		themeID string
		keyword string
		want    bool
	}{
		// -aux plurals, both families.
		{"chateaux matches a singular chateau", "Château Frontenac", "patrimoine", "châteaux", true},
		{"bateaux matches a singular bateau", "Musée du bateau de Québec", "musees", "bateaux", true},
		{"parcs nationaux still matches the -al family", "Parc national de la Jacques-Cartier", "rando", "parcs nationaux", true},
		{"a singular keyword matches a plural name", "Route des Châteaux", "patrimoine", "château", true},
		{"chateaux does not match an unrelated name", "Musée des techniques de Montréal", "musees", "châteaux", false},

		// Word boundaries.
		{"shopping long does not match Longueuil", "Centre commercial Longueuil", "shopping", "shopping long", false},
		{"shopping long still matches a long shopping venue", "Long Shopping Outlet Mall", "shopping", "shopping long", true},
		{"a token is not matched inside a longer word", "Parcheminerie du Vieux-Québec", "patrimoine", "parc", false},
		{"a token still matches a hyphenated word", "Vieux-Port de Montréal", "patrimoine", "port", true},

		// One-character tokens.
		{"the apostrophe token does not create a match on its own", "Marché du Vieux-Port", "marches", "d", false},
		{"a keyword made only of one-letter tokens never matches", "Musée d'art moderne", "musees", "d'", false},
		{"dropping the apostrophe token keeps the real tokens required", "Boulangerie Denise", "food", "d'art", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword)
			if got != tc.want {
				t.Errorf("matchKeyword(%q, %q, %q) = %v, want %v", tc.item, tc.themeID, tc.keyword, got, tc.want)
			}
		})
	}
}

// TestMatchKeyword_CompoundWordsAreKnownMisses records the price of whole-word
// comparison, so the trade-off is a decision under test rather than an accident
// nobody notices moving.
//
// Substring matching used to catch a keyword inside a compound name: "vélo" hit
// "Vélodrome", "art" hit "Artothèque". Whole-word comparison loses both. The
// obvious repair — accepting a word-PREFIX match above a length threshold — does
// not work here: "vélo" (4 letters) and the "long" of "shopping long" (4 letters)
// are the same length, so no threshold keeps the wanted prefix hit while still
// keeping "long" out of "Longueuil". Since a false positive lands on dislikes too
// (+10, demoting a legitimate item) while a false negative only forgoes a ranking
// bonus, the false negatives below are the deliberate choice.
//
// If prefix matching is ever revisited, these cases flip to `true` and this test
// is where that shows up.
func TestMatchKeyword_CompoundWordsAreKnownMisses(t *testing.T) {
	tests := []struct {
		name    string
		item    string
		themeID string
		keyword string
		want    bool
	}{
		{"velo does not match Velodrome (accepted miss)", "Vélodrome de Montréal", "sport", "vélo", false},
		{"art does not match Artotheque (accepted miss)", "Artothèque de Québec", "musees", "art", false},
		{"the same rule keeps long out of Longueuil (the reason)", "Centre commercial Longueuil", "shopping", "long", false},
		{"the whole word still matches", "Piste cyclable vélo du canal", "sport", "vélo", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword)
			if got != tc.want {
				t.Errorf("matchKeyword(%q, %q, %q) = %v, want %v", tc.item, tc.themeID, tc.keyword, got, tc.want)
			}
		})
	}
}

// TestRankItems_RealSeedInterests proves the ranking actually moves with the
// production vocabulary: rene's likes promote the national park, his dislikes
// demote the modern-art museum without dropping it.
func TestRankItems_RealSeedInterests(t *testing.T) {
	items := []Item{
		{Name: "Musée d'art moderne de Québec", ThemeID: "musees", DistKm: 1},
		{Name: "Parc national de la Jacques-Cartier", ThemeID: "rando", DistKm: 9},
		{Name: "Marché du Vieux-Port", ThemeID: "marches", DistKm: 4},
	}
	cfg := RankConfig{
		Interests: map[string]InterestPref{
			"rene": {
				Likes:    []string{"parcs nationaux", "musées techniques", "aviation"},
				Dislikes: []string{"shopping long", "musées d'art moderne"},
			},
		},
	}
	got := RankItems(items, cfg)
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (dislikes must demote, never drop)", len(got))
	}
	if got[0].Name != "Parc national de la Jacques-Cartier" {
		t.Errorf("first=%q, want the liked national park", got[0].Name)
	}
	if got[len(got)-1].Name != "Musée d'art moderne de Québec" {
		t.Errorf("last=%q, want the disliked modern art museum", got[len(got)-1].Name)
	}
}

// A dislike matching by accident costs +10 and pushes a legitimate item to the
// bottom of the list. "shopping long" must therefore leave a mall in Longueuil
// where distance put it.
func TestRankItems_NoFalsePositiveOnSubstring(t *testing.T) {
	items := []Item{
		{Name: "Centre commercial Longueuil", ThemeID: "shopping", DistKm: 2},
		{Name: "Sentier du lac", ThemeID: "rando", DistKm: 6},
	}
	cfg := RankConfig{
		Interests: map[string]InterestPref{
			"rene": {Dislikes: []string{"shopping long"}},
		},
	}
	got := RankItems(items, cfg)
	if got[0].Name != "Centre commercial Longueuil" {
		t.Errorf("first=%q, want the nearer mall: \"long\" must not match inside \"Longueuil\"", got[0].Name)
	}
}

func TestRankItems_DislikedNeverRemoved(t *testing.T) {
	items := []Item{
		{Name: "Disliked Place", ThemeID: "shopping", DistKm: 1},
		{Name: "Neutral Place", ThemeID: "rando", DistKm: 5},
	}
	cfg := RankConfig{
		Interests: map[string]InterestPref{
			"rene":   {Dislikes: []string{"shopping"}},
			"nicole": {Dislikes: []string{"shopping"}},
		},
	}
	got := RankItems(items, cfg)
	if len(got) != 2 {
		t.Fatalf("disliked item was removed! got %d items", len(got))
	}
	found := false
	for _, it := range got {
		if it.Name == "Disliked Place" {
			found = true
		}
	}
	if !found {
		t.Fatal("disliked item missing from results")
	}
}
