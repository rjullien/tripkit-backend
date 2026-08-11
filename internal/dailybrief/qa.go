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
	reHotelSection = regexp.MustCompile(`(?i)hotel|check.?in|🏨`)
	reTimeline     = regexp.MustCompile(`\d{1,2}[h:]\d{0,2}`)
	reWeather      = regexp.MustCompile(`(?i)°[CF]|meteo|météo|☀️|🌧|🌤|temp`)
	rePhone        = regexp.MustCompile(`(?:\+?\d{1,3}[-.\s]?)?\(?\d{2,4}\)?[-.\s]?\d{3,4}[-.\s]?\d{3,4}`)
	reURL          = regexp.MustCompile(`https?://[^\s)]+`)
	rePhoneNoise   = regexp.MustCompile(`[-.\s()]`)
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
		if !strings.Contains(text, src.Date) && (human == "" || !strings.Contains(text, human)) {
			res.Contradictions = append(res.Contradictions, "date missing")
			res.Verdict = QAFailed
		}
	}
	if src.Hotel != nil {
		if name, _ := src.Hotel["name"].(string); name != "" && !strings.Contains(text, name) {
			res.Completeness = append(res.Completeness, "hotel.name absent")
			if res.Verdict == QAPassed {
				res.Verdict = QAWarning
			}
		}
		if !reHotelSection.MatchString(text) {
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
		if t, _ := item["time"].(string); t != "" && !strings.Contains(text, t) {
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
