package discovery

import "testing"

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
