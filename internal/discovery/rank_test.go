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

	// allowPrefix=false: the strict whole-word semantics, which is what the
	// dislikes use. The like direction is covered by
	// TestMatchKeyword_PrefixMatchingIsLikeOnly.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword, false)
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

	// allowPrefix=false, as for the dislikes: this is where "long" must stay out
	// of "Longueuil", and that guarantee is exactly what prefix matching is NOT
	// allowed to weaken.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword, false)
			if got != tc.want {
				t.Errorf("matchKeyword(%q, %q, %q) = %v, want %v", tc.item, tc.themeID, tc.keyword, got, tc.want)
			}
		})
	}
}

// TestMatchKeyword_PrefixMatchingIsLikeOnly pins the compound-word trade-off on
// the axis that actually separates the wanted hits from the unwanted ones.
//
// Substring matching used to catch a keyword inside a compound name: "vélo" hit
// "Vélodrome", "art" hit "Artothèque". Whole-word comparison loses both, and
// LENGTH cannot buy them back — "vélo" and the "long" of "shopping long" are both
// four letters. DIRECTION can: "long" comes from a dislike, and per interestScore
// a false positive on a dislike demotes a legitimate item (+10) while a false
// positive on a like only hands out a ranking bonus. So the likes prefix-match and
// the dislikes stay on whole words, and the same token can legitimately behave
// differently depending on which list it came from.
func TestMatchKeyword_PrefixMatchingIsLikeOnly(t *testing.T) {
	tests := []struct {
		name        string
		item        string
		themeID     string
		keyword     string
		allowPrefix bool // true = the keyword came from Likes
		want        bool
	}{
		// The hits recovered: a like prefix-matches inside a compound word.
		{"velo matches Velodrome as a like", "Vélodrome de Montréal", "sport", "vélo", true, true},
		{"art matches Artotheque as a like", "Artothèque de Québec", "musees", "art", true, true},

		// The guarantee kept: a dislike never does, so "shopping long" cannot
		// demote a mall in Longueuil.
		{"long stays out of Longueuil as a dislike", "Centre commercial Longueuil", "shopping", "long", false, false},
		{"shopping long stays out of Longueuil as a dislike", "Centre commercial Longueuil", "shopping", "shopping long", false, false},
		{"velo does not match Velodrome as a dislike", "Vélodrome de Montréal", "sport", "vélo", false, false},

		// Accepted cost of the like direction: a prefix hit a whole-word match
		// would have refused. It only adds a bonus, never a penalty.
		{"parc matches Parcheminerie as a like (accepted false positive)", "Parcheminerie du Vieux-Québec", "patrimoine", "parc", true, true},

		// Floor on the prefix: a two-letter token would prefix half a city.
		{"a two-letter token does not prefix-match even as a like", "Vélodrome de Montréal", "sport", "vé", true, false},

		// Whole words still match in both directions.
		{"the whole word matches as a like", "Piste cyclable vélo du canal", "sport", "vélo", true, true},
		{"the whole word matches as a dislike", "Piste cyclable vélo du canal", "sport", "vélo", false, true},

		// Every token of a multi-word keyword is still required.
		{"a multi-word like still needs all its tokens", "Musée des techniques", "musees", "musées d'art moderne", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchKeyword(strings.ToLower(tc.item), strings.ToLower(tc.themeID), tc.keyword, tc.allowPrefix)
			if got != tc.want {
				t.Errorf("matchKeyword(%q, %q, %q, allowPrefix=%v) = %v, want %v",
					tc.item, tc.themeID, tc.keyword, tc.allowPrefix, got, tc.want)
			}
		})
	}
}

// TestInterestScore_PrefixOnlyHelpsLikes proves the direction rule through the
// scorer rather than the matcher: the same four-letter token is a bonus when it
// prefixes an item for a like, and no penalty at all when it prefixes one for a
// dislike.
func TestInterestScore_PrefixOnlyHelpsLikes(t *testing.T) {
	velodrome := Item{Name: "Vélodrome de Montréal", ThemeID: "sport"}
	longueuil := Item{Name: "Centre commercial Longueuil", ThemeID: "shopping"}

	liked := map[string]InterestPref{"rene": {Likes: []string{"vélo"}}}
	if got := interestScore(velodrome, liked); got != likeBoost {
		t.Errorf("a liked prefix must boost: got %v, want %v", got, likeBoost)
	}

	disliked := map[string]InterestPref{"rene": {Dislikes: []string{"shopping long"}}}
	if got := interestScore(longueuil, disliked); got != 0 {
		t.Errorf("a disliked keyword must not match a prefix: got %v, want 0", got)
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
