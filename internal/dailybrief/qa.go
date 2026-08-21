package dailybrief

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type QAVerdict string

const (
	QAPassed  QAVerdict = "PASSED"
	QAWarning QAVerdict = "WARNING"
	QAFailed  QAVerdict = "FAILED"
)

type QAResult struct {
	Verdict        QAVerdict `json:"verdict"`
	Summary        string    `json:"summary"`
	Contradictions []string  `json:"contradictions,omitempty"`
	Placeholders   []string  `json:"placeholders,omitempty"`
	Completeness   []string  `json:"completeness,omitempty"`
	Hallucinations []string  `json:"hallucinations,omitempty"`
}

var forbiddenPatterns = []struct {
	re    *regexp.Regexp
	label string
	crit  bool
}{
	{regexp.MustCompile(`(?i)\[HEURE [AÀ] CONFIRMER\]`), "Placeholder heure", true},
	{regexp.MustCompile(`(?i)\[DATE [AÀ] CONFIRMER\]`), "Placeholder date", true},
	{regexp.MustCompile(`(?i)\[LIEU [AÀ] CONFIRMER\]`), "Placeholder lieu", true},
	{regexp.MustCompile(`\?\?\?`), "Triple question mark", true},
	{regexp.MustCompile(`(?i)\[FIXME\]`), "FIXME", true},
	{regexp.MustCompile(`\bTODO\b`), "TODO", true},
	{regexp.MustCompile(`(?i)\[[^\]]*[AÀ] CONFIRMER[^\]]*\]`), "Placeholder générique", true},
	{regexp.MustCompile(`\{\{.*\}\}`), "Template variable", false},
	{regexp.MustCompile(`console\.log`), "console.log", false},
}

var (
	reHotelSection = regexp.MustCompile(`(?i)h[oô]tel|check.?in|logement|h[eé]bergement|loft|🏨|🔑`)
	reTimeline     = regexp.MustCompile(`\d{1,2}[h:]\d{0,2}`)
	reWeather      = regexp.MustCompile(`(?i)°[CF]|meteo|météo|☀️|🌧|🌧️|🌤|temp|pluie|soleil`)
	rePhone        = regexp.MustCompile(`(?:\+?\d{1,3}[-.\s]?)?\(?\d{2,4}\)?[-.\s]?\d{3,4}[-.\s]?\d{3,4}`)
	reURL          = regexp.MustCompile(`https?://[^\s)]+`)
	rePhoneNoise   = regexp.MustCompile(`[-.\s()]`)
	reASavoir      = regexp.MustCompile(`(?i)à savoir|a savoir|⭐|🌟`)
	reActualite    = regexp.MustCompile(`(?i)actualit[eé]|📰`)
	rePratique     = regexp.MustCompile(`(?i)astuce pratique|💡.*pratique|pratique`)
	reListesCloud  = regexp.MustCompile(`(?i)listes?\s*cloud|📋|checklist|valise|avant de partir`)
	reDernierCheck = regexp.MustCompile(`(?i)dernier check|✅|\bcheck\b`)
	reTelecharg    = regexp.MustCompile(`(?i)télécharg|telecharg|hors-ligne|hors ligne|📥|préparer|preparer`)
	reVisibility   = regexp.MustCompile(`(?i)ne (vois|voit) pas|je ne sais pas|j['’]espère|perso|locales?`)
	reOrdreEtapes  = regexp.MustCompile(`(?i)ordre des [eé]tapes|🗺️`)
)

// RunQA validates Bifrost text against source data (deterministic).
func RunQA(text string, src *DayBriefData) QAResult {
	res := QAResult{Verdict: QAPassed}
	if src == nil {
		res.Verdict = QAFailed
		res.Summary = "QA FAILED - source data nil"
		return res
	}

	for _, p := range forbiddenPatterns {
		if p.re.FindString(text) != "" {
			res.Placeholders = append(res.Placeholders, p.label)
			if p.crit {
				res.Verdict = QAFailed
			} else if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}
	}

	if src.Weekday != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(src.Weekday)) {
		res.Contradictions = append(res.Contradictions, "weekday missing/mismatch")
		res.Verdict = QAFailed
	}
	if src.Date != "" {
		human := humanDateFR(src.Date)
		hasISO := strings.Contains(text, src.Date)
		hasHuman := human != "" && strings.Contains(strings.ToLower(text), strings.ToLower(human))
		if !hasISO && !hasHuman {
			// Weekday alone → warning; neither → failed.
			if src.Weekday != "" && strings.Contains(strings.ToLower(text), strings.ToLower(src.Weekday)) {
				res.Contradictions = append(res.Contradictions, "date missing (weekday only)")
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			} else {
				res.Contradictions = append(res.Contradictions, "date missing")
				res.Verdict = QAFailed
			}
		}
	}
	prepMode := src.Prep != nil && (src.Prep.Mode == "j0" || src.Prep.Mode == "j0m1" || src.Prep.Mode == "veille")
	if prepMode {
		runPrepQA(text, src, &res)
	} else {
		if src.Hotel != nil {
			name, _ := src.Hotel["name"].(string)
			hasName := name != "" && textContainsLoose(text, name)
			if name != "" && !hasName {
				res.Completeness = append(res.Completeness, "hotel.name absent")
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			}
			if !reHotelSection.MatchString(text) && !hasName {
				res.Completeness = append(res.Completeness, "hotel section missing")
				res.Verdict = QAFailed
			}
		}
		if src.Restaurant != nil {
			if name, _ := src.Restaurant["name"].(string); name != "" && !strings.Contains(text, name) {
				res.Completeness = append(res.Completeness, "restaurant.name absent")
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			}
		}
		if len(src.Timeline) > 0 && !reTimeline.MatchString(text) {
			res.Completeness = append(res.Completeness, "timeline hours missing")
			res.Verdict = QAFailed
		}
		for _, item := range src.Timeline {
			if t, _ := item["time"].(string); t != "" && !timelineTimePresent(text, t) {
				res.Completeness = append(res.Completeness, fmt.Sprintf("timeline time %s absent", t))
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			}
		}
		if src.MapURL != "" && !strings.Contains(text, src.MapURL) {
			res.Completeness = append(res.Completeness, "mapUrl absent")
			if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}
		if src.Weather != nil && !reWeather.MatchString(text) {
			res.Completeness = append(res.Completeness, "weather absent")
			if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}

		// Mandatory travel-day brief sections
		if !reASavoir.MatchString(text) {
			res.Completeness = append(res.Completeness, "section À savoir missing")
			res.Verdict = QAFailed
		}
		if !reActualite.MatchString(text) {
			res.Completeness = append(res.Completeness, "section Actualité missing")
			res.Verdict = QAFailed
		}
		if src.PracticalTip != nil && strings.TrimSpace(src.PracticalTip.Text) != "" {
			if !rePratique.MatchString(text) {
				res.Completeness = append(res.Completeness, "section Astuce pratique missing")
				res.Verdict = QAFailed
			}
		}
		if src.RouteOrder != nil && strings.TrimSpace(src.RouteOrder.Paragraph) != "" {
			if !reOrdreEtapes.MatchString(text) {
				res.Completeness = append(res.Completeness, "section Ordre des étapes missing")
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			} else if src.RouteOrder.Optimal {
				low := strings.ToLower(text)
				if !strings.Contains(low, "optimal") {
					res.Completeness = append(res.Completeness, "ordre des étapes: optimal not stated")
					if res.Verdict == QAPassed {
						res.Verdict = QAWarning
					}
				}
			} else if !strings.Contains(strings.ToLower(text), "pas optimal") && !strings.Contains(strings.ToLower(text), "détour") {
				res.Completeness = append(res.Completeness, "ordre des étapes: detour not stated")
				if res.Verdict == QAPassed {
					res.Verdict = QAWarning
				}
			}
		}
		if src.HasKids == false && (strings.Contains(strings.ToLower(text), "aire de jeu") || strings.Contains(strings.ToLower(text), "avec enfants")) {
			res.Completeness = append(res.Completeness, "kids tip while hasKids=false")
			if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}
		// Prep/listes section must NOT appear when prep is nil (trip already started).
		if src.Prep == nil {
			low := strings.ToLower(text)
			hasPrepSection := strings.Contains(low, "dernier check listes") ||
				strings.Contains(low, "✅ dernier check") ||
				strings.Contains(low, "listes cloud") ||
				strings.Contains(low, "checklist") ||
				(strings.Contains(low, "valises") && strings.Contains(low, "progression"))
			if hasPrepSection {
				res.Hallucinations = append(res.Hallucinations, "prep/listes section while prep=nil (trip en cours)")
				res.Verdict = QAFailed
			}
		}
	}

	sourceBlob, _ := json.Marshal(src)
	srcStr := string(sourceBlob)
	for _, phone := range rePhone.FindAllString(text, -1) {
		norm := rePhoneNoise.ReplaceAllString(phone, "")
		if !strings.Contains(srcStr, norm) && !strings.Contains(srcStr, phone) {
			res.Hallucinations = append(res.Hallucinations, "phone:"+phone)
			res.Verdict = QAFailed
		}
	}
	for _, u := range reURL.FindAllString(text, -1) {
		if !strings.Contains(srcStr, u) {
			res.Hallucinations = append(res.Hallucinations, "url:"+u)
			res.Verdict = QAFailed
		}
	}

	n := len(res.Contradictions) + len(res.Placeholders) + len(res.Completeness) + len(res.Hallucinations)
	if res.Verdict == QAPassed {
		res.Summary = "QA PASSED - Aucun problème détecté"
	} else {
		res.Summary = fmt.Sprintf("QA %s - %d problème(s) détecté(s)", res.Verdict, n)
	}
	return res
}

func runPrepQA(text string, src *DayBriefData, res *QAResult) {
	if !reListesCloud.MatchString(text) {
		res.Completeness = append(res.Completeness, "section Listes cloud missing")
		res.Verdict = QAFailed
	}
	if !reDernierCheck.MatchString(text) {
		res.Completeness = append(res.Completeness, "section Dernier check missing")
		res.Verdict = QAFailed
	}
	if !reTelecharg.MatchString(text) {
		res.Completeness = append(res.Completeness, "section télécharger/préparer missing")
		res.Verdict = QAFailed
	}
	if !reVisibility.MatchString(text) {
		res.Completeness = append(res.Completeness, "visibility / je ne sais pas missing")
		res.Verdict = QAFailed
	}
	if src.PracticalTip != nil && strings.TrimSpace(src.PracticalTip.Text) != "" {
		if !rePratique.MatchString(text) {
			res.Completeness = append(res.Completeness, "section Astuce pratique missing")
			res.Verdict = QAFailed
		}
	}
	// Soft: mention at least one unchecked / progress digit if lists present.
	if src.Prep != nil && len(src.Prep.Lists) > 0 {
		hasProgress := strings.Contains(text, "/") || strings.Contains(text, "coches") || strings.Contains(text, "ouvert")
		if !hasProgress {
			res.Completeness = append(res.Completeness, "list progress not mentioned")
			if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}
	}
}

func humanDateFR(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return ""
	}
	months := []string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
	var m int
	fmt.Sscanf(parts[1], "%d", &m)
	day := strings.TrimLeft(parts[2], "0")
	if m < 1 || m > 12 {
		return ""
	}
	return day + " " + months[m]
}

// timelineTimePresent accepts "09:00", "9:00", "09h00", "9h00".
func timelineTimePresent(text, t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return true
	}
	if strings.Contains(text, t) {
		return true
	}
	norm := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(t, "H", ":"), "h", ":"))
	parts := strings.SplitN(norm, ":", 2)
	if len(parts) != 2 {
		return false
	}
	hNum := strings.TrimLeft(parts[0], "0")
	if hNum == "" {
		hNum = "0"
	}
	min := parts[1]
	if len(min) == 1 {
		min = "0" + min
	}
	hPad := hNum
	if len(hPad) == 1 {
		hPad = "0" + hPad
	}
	variants := []string{
		hNum + ":" + min,
		hPad + ":" + min,
		hNum + "h" + min,
		hPad + "h" + min,
	}
	// French short form "9h" / "09h" when minutes are :00
	if min == "00" {
		variants = append(variants, hNum+"h", hPad+"h", hNum+"H", hPad+"H")
	}
	low := strings.ToLower(text)
	for _, v := range variants {
		if strings.Contains(low, strings.ToLower(v)) {
			return true
		}
	}
	return false
}

// textContainsLoose compares after normalizing unicode dashes/spaces.
func textContainsLoose(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(haystack, needle) {
		return true
	}
	return strings.Contains(normalizeLoose(haystack), normalizeLoose(needle))
}

func normalizeLoose(s string) string {
	r := strings.NewReplacer(
		"\u2011", "-", // non-breaking hyphen
		"\u2010", "-",
		"\u2012", "-",
		"\u2013", "-", // en dash
		"\u2014", "-", // em dash
		"\u2212", "-",
		"\u00a0", " ",
	)
	return strings.ToLower(r.Replace(s))
}
