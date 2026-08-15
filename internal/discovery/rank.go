package discovery

import (
	"sort"
	"strings"
	"unicode"
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

// matchKeyword reports whether a keyword matches the item name or themeID.
//
// The real vocabulary in tripkit-seeds/travel-profile.js is multi-word French
// ("parcs nationaux", "musées techniques", "shopping long"), which never appears
// as a contiguous substring of an OSM name ("Parc national de la Jacques-Cartier").
// So the keyword is normalised (lowercased, accents stripped), split on
// whitespace and punctuation, lightly singularised, and EVERY token must be
// found in the normalised name or theme id. Requiring all tokens keeps
// "shopping long" from matching a plain shop.
func matchKeyword(nameLower, themeIDLower, keyword string) bool {
	tokens := keywordTokens(keyword)
	if len(tokens) == 0 {
		return false
	}
	name := normalizeText(nameLower)
	theme := normalizeText(themeIDLower)
	for _, tok := range tokens {
		if !strings.Contains(name, tok) && !strings.Contains(theme, tok) {
			return false
		}
	}
	return true
}

// keywordTokens normalises a keyword into singularised match tokens.
func keywordTokens(keyword string) []string {
	fields := strings.FieldsFunc(normalizeText(keyword), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = singularize(f)
		if f == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// normalizeText lowercases and strips French accents so "musées" matches "musees".
func normalizeText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if repl, ok := accentFolds[r]; ok {
			b.WriteRune(repl)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// accentFolds maps the accented letters used in French seed data to their
// unaccented form. Kept explicit to avoid a golang.org/x/text dependency.
var accentFolds = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'á': 'a', 'ã': 'a', 'å': 'a',
	'ç': 'c',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'î': 'i', 'ï': 'i', 'í': 'i', 'ì': 'i',
	'ô': 'o', 'ö': 'o', 'ó': 'o', 'õ': 'o', 'ò': 'o',
	'ù': 'u', 'û': 'u', 'ü': 'u', 'ú': 'u',
	'ÿ': 'y',
	'ñ': 'n',
}

// singularize applies light French plural rules so a plural keyword matches a
// singular name: "parcs" -> "parc", "nationaux" -> "national", "musées" -> "musee".
// Short words are left alone ("bus", "aux" would otherwise be mangled).
func singularize(tok string) string {
	if len(tok) <= 3 {
		return tok
	}
	if strings.HasSuffix(tok, "aux") && len(tok) > 4 {
		return strings.TrimSuffix(tok, "aux") + "al"
	}
	if strings.HasSuffix(tok, "s") || strings.HasSuffix(tok, "x") {
		return tok[:len(tok)-1]
	}
	return tok
}
