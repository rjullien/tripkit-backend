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
// So both sides are normalised (lowercased, accents stripped) and split into
// words on whitespace and punctuation, and EVERY keyword token must match a
// WHOLE word of the name or theme id, up to a light plural rule. Requiring all
// tokens keeps "shopping long" from matching a plain shop.
//
// Matching whole words rather than substrings matters in both directions: it
// keeps "long" (from "shopping long") out of "Longueuil", and one-character
// tokens — the "d" of "musées d'art moderne" once the apostrophe is split on —
// are dropped entirely instead of matching nearly every name. A false positive
// on a dislike costs +10 and demotes a legitimate item.
//
// The accepted cost is the compound-word hit substring matching used to catch:
// "vélo" no longer matches "Vélodrome", "art" no longer matches "Artothèque". A
// word-PREFIX match cannot buy those back without giving "Longueuil" away too —
// "vélo" and "long" are both four letters, so no length threshold separates them.
// The misses are pinned by TestMatchKeyword_CompoundWordsAreKnownMisses.
func matchKeyword(nameLower, themeIDLower, keyword string) bool {
	tokens := keywordTokens(keyword)
	if len(tokens) == 0 {
		return false
	}
	haystack := wordForms(nameLower, themeIDLower)
	for _, tok := range tokens {
		matched := false
		for _, form := range matchForms(tok) {
			if haystack[form] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// keywordTokens normalises a keyword into match tokens, dropping the
// one-character ones (an apostrophe or a hyphen splits words into fragments that
// would match anything).
func keywordTokens(keyword string) []string {
	fields := splitWords(normalizeText(keyword))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len([]rune(f)) < 2 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// wordForms collects every match form of every word of the given texts, so a
// keyword token can be compared against whole words only.
func wordForms(texts ...string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range texts {
		for _, w := range splitWords(normalizeText(t)) {
			for _, f := range matchForms(w) {
				set[f] = true
			}
		}
	}
	return set
}

// splitWords cuts a normalised string on anything that is not a letter or digit.
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
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

// matchForms returns the forms a word may match as, applying light French plural
// rules so a plural keyword matches a singular name: "parcs" -> "parc",
// "musées" -> "musee". Short words are left alone ("bus", "aux" would otherwise
// be mangled).
//
// French has two families of "-aux" plurals and guessing one of them is wrong
// half the time: "nationaux" -> "national" but "châteaux" -> "château",
// "bateaux" -> "bateau". Both candidates are returned and either may match, and
// the word itself is always kept so a singular keyword still matches a plural
// name ("château" against "Route des Châteaux").
func matchForms(word string) []string {
	forms := []string{word}
	if len(word) <= 3 {
		return forms
	}
	if strings.HasSuffix(word, "aux") && len(word) > 4 {
		base := strings.TrimSuffix(word, "aux")
		return append(forms, base+"al", base+"au")
	}
	if strings.HasSuffix(word, "s") || strings.HasSuffix(word, "x") {
		return append(forms, word[:len(word)-1])
	}
	return forms
}
