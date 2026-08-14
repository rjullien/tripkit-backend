package polarsteps

import (
	"regexp"
	"strings"
)

var (
	reHTML     = regexp.MustCompile(`(?i)<[^>]+>`)
	rePNR      = regexp.MustCompile(`(?i)\bPNR\s+[A-Z0-9]{5,8}\b`)
	reFlight   = regexp.MustCompile(`\b(?:LX|LH|AC|AF|BA|UA|DL|AA|SWISS)\s*-?\s*\d{2,5}\b`)
	reA220     = regexp.MustCompile(`(?i)\bA\d{3}(?:-\d+)?\b`)
	reHashList = regexp.MustCompile(`#plus/listes/[^\s<]+`)
)

func cleanLabel(s string) string {
	s = reHTML.ReplaceAllString(s, " ")
	s = reHashList.ReplaceAllString(s, " ")
	s = rePNR.ReplaceAllString(s, " ")
	s = reFlight.ReplaceAllString(s, " ")
	s = reA220.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "·", " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func clipUserNote(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > maxUserNote {
		return string(r[:maxUserNote])
	}
	return s
}
