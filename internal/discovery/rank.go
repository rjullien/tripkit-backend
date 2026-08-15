package discovery

import (
	"sort"
	"strings"
)

// InterestPref holds a person's like/dislike keywords for ranking.
type InterestPref struct {
	Likes    []string `json:"likes"`
	Dislikes []string `json:"dislikes"`
}

// RankConfig controls multi-factor ranking for discovery items.
type RankConfig struct {
	// Interests maps person names to their like/dislike preferences.
	Interests map[string]InterestPref
	// BudgetMax is the per-person activity budget (EUR). Zero means no constraint.
	BudgetMax float64
}

// Scoring constants.
const (
	likeBoost    = -5.0 // subtract from score per matching like (lower = better)
	dislikePenalty = 10.0 // add to score per matching dislike
)

// RankItems scores and sorts items using distance + interest preferences.
// Items matching a dislike are demoted but NEVER removed.
// When no preferences match, distance is the primary sort factor.
func RankItems(items []Item, cfg RankConfig) []Item {
	if len(items) == 0 {
		return items
	}

	type scored struct {
		item  Item
		score float64
	}

	entries := make([]scored, len(items))
	for i, it := range items {
		score := it.DistKm // base score = distance (lower = better)
		score += interestScore(it, cfg.Interests)
		entries[i] = scored{item: it, score: score}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].score < entries[j].score
	})

	out := make([]Item, len(entries))
	for i, e := range entries {
		out[i] = e.item
	}
	return out
}

// interestScore computes the cumulative boost/penalty from all persons' preferences.
func interestScore(it Item, interests map[string]InterestPref) float64 {
	if len(interests) == 0 {
		return 0
	}
	var adj float64
	name := strings.ToLower(it.Name)
	themeID := strings.ToLower(it.ThemeID)

	for _, pref := range interests {
		for _, kw := range pref.Likes {
			if matchKeyword(name, themeID, kw) {
				adj += likeBoost
				break // one boost per person max
			}
		}
		for _, kw := range pref.Dislikes {
			if matchKeyword(name, themeID, kw) {
				adj += dislikePenalty
				break // one penalty per person max
			}
		}
	}
	return adj
}

// matchKeyword checks if a keyword (case-insensitive substring) matches the item name or themeID.
func matchKeyword(nameLower, themeIDLower, keyword string) bool {
	kw := strings.ToLower(keyword)
	return strings.Contains(nameLower, kw) || strings.Contains(themeIDLower, kw)
}
