package polarsteps

import (
	"regexp"
	"strings"
	"unicode"
)

// QAVerdict mirrors dailybrief-style PASSED / WARNING / FAILED.
type QAVerdict string

const (
	QAPassed  QAVerdict = "PASSED"
	QAWarning QAVerdict = "WARNING"
	QAFailed  QAVerdict = "FAILED"
)

// QAResult is returned to the FE (FAILED → do not copy as Polarsteps).
type QAResult struct {
	Verdict QAVerdict `json:"verdict"`
	Summary string    `json:"summary"`
	Issues  []string  `json:"issues,omitempty"`
}

var (
	reQAPnr    = regexp.MustCompile(`(?i)\bPNR\b`)
	reQAFlight = regexp.MustCompile(`\b(?:LX|LH|AC|AF|BA|UA|DL|AA)\s*\d{2,5}\b`)
	reQAPrice  = regexp.MustCompile(`(?i)€|\bCAD\b|\bEUR\b`)
	reQAPack   = regexp.MustCompile(`(?i)\bvalise\b|\bchecklist\b`)
)

// RunQA validates caption text against the generation input.
func RunQA(text string, in *Input) QAResult {
	res := QAResult{Verdict: QAPassed}
	text = strings.TrimSpace(text)
	if in == nil {
		res.Verdict = QAFailed
		res.Summary = "QA FAILED - input nil"
		return res
	}
	n := utf8len(text)
	minLen := 220
	followUp := len(in.AlreadyPosted) > 0
	if followUp {
		minLen = 120
	}
	if n < minLen || n > 1200 {
		res.fail("longueur")
	}
	if reQAPnr.MatchString(text) || reQAFlight.MatchString(text) {
		res.fail("PNR/vol")
	}
	if reQAPrice.MatchString(text) {
		res.fail("prix")
	}
	if reQAPack.MatchString(text) {
		res.fail("packing")
	}
	if isRedite(text, priorTexts(in.AlreadyPosted)) {
		res.fail("redite")
	}
	folded := fold(text)
	from := fold(in.From)
	to := fold(in.To)
	if followUp {
		// Prior steps already placed the day; don't force Nice/Montréal again.
	} else if from == "" && to == "" {
		// no toponyme to require
	} else if from != "" && strings.Contains(folded, from) {
		// ok
	} else if to != "" && strings.Contains(folded, to) {
		// ok
	} else {
		res.fail("toponyme")
	}
	if !followUp && in.Kind == "opening" && len(in.Phases) > 0 {
		found := 0
		for _, p := range in.Phases {
			if phaseMentioned(folded, p) {
				found++
			}
		}
		if found == 0 {
			res.fail("phases")
		}
	}
	if in.UserNote != "" && !noteReflected(in.UserNote, text) {
		res.warn("note user peu reflétée")
	}
	if res.Verdict == QAPassed {
		res.Summary = "QA PASSED"
	} else if res.Verdict == QAWarning {
		res.Summary = "QA WARNING - " + strings.Join(res.Issues, "; ")
	} else {
		res.Summary = "QA FAILED - " + strings.Join(res.Issues, "; ")
	}
	return res
}

func (r *QAResult) fail(issue string) {
	r.Issues = append(r.Issues, issue)
	r.Verdict = QAFailed
}

func (r *QAResult) warn(issue string) {
	r.Issues = append(r.Issues, issue)
	if r.Verdict == QAPassed {
		r.Verdict = QAWarning
	}
}

func utf8len(s string) int {
	return len([]rune(s))
}

func fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'é', 'è', 'ê', 'ë':
			b.WriteRune('e')
		case 'à', 'â':
			b.WriteRune('a')
		case 'ù', 'û':
			b.WriteRune('u')
		case 'ô':
			b.WriteRune('o')
		case 'î', 'ï':
			b.WriteRune('i')
		case 'ç':
			b.WriteRune('c')
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func noteReflected(note, text string) bool {
	note = fold(note)
	text = fold(text)
	had := false
	for _, w := range strings.Fields(note) {
		if len([]rune(w)) < 6 {
			continue
		}
		had = true
		if strings.Contains(text, w) {
			return true
		}
	}
	return !had
}

func phaseMentioned(foldedText, phase string) bool {
	f := fold(phase)
	if f == "" {
		return false
	}
	if strings.Contains(foldedText, f) {
		return true
	}
	words, hit := 0, 0
	for _, w := range strings.Fields(f) {
		if len([]rune(w)) < 4 {
			continue
		}
		words++
		if strings.Contains(foldedText, w) {
			hit++
		}
	}
	return words > 0 && hit == words
}
