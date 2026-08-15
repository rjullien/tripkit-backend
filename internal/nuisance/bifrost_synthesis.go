package nuisance

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/bifrost"
)

const synthesisSystem = `Tu es un assistant de voyage expert en analyse de nuisances.
On te fournit les resultats d'une analyse de nuisances pour un ou plusieurs lieux.
Tu dois rediger un rapport synthetique en francais avec :
1. Un resume du verdict global
2. Pour chaque lieu avec un score ELEVE ou MODERE, une recommandation concrete
3. Si possible, des alternatives (lieux plus calmes a proximite)

Reponds en JSON avec la structure :
{"recommendations": [{"locationId": "...", "text": "...", "alternatives": ["..."]}]}`

// LocationResults groups the scoring output for one location.
type LocationResults struct {
	LocationID   string           `json:"locationId"`
	LocationName string           `json:"locationName"`
	Verdict      string           `json:"verdict"`
	Categories   []CategoryResult `json:"categories"`
}

// Synthesize calls Bifrost once with ALL locations' results and returns
// recommendations keyed by location ID.
func Synthesize(completer bifrost.Completer, allResults []LocationResults) (map[string]SynthesisResult, error) {
	if completer == nil {
		// No completer configured: skip synthesis (soft-fail).
		// This is expected when BIFROST_* env vars are not set (e.g. local dev).
		out := make(map[string]SynthesisResult, len(allResults))
		for _, lr := range allResults {
			out[lr.LocationID] = SynthesisResult{}
		}
		return out, nil
	}

	userPrompt := buildSynthesisPrompt(allResults)
	resp, err := completer.Complete(synthesisSystem, userPrompt)
	if err != nil {
		// Soft-fail: return empty recommendations rather than failing the whole check.
		out := make(map[string]SynthesisResult, len(allResults))
		for _, lr := range allResults {
			out[lr.LocationID] = SynthesisResult{}
		}
		return out, nil
	}

	return parseSynthesisResponse(resp, allResults), nil
}

// SynthesisResult is the Bifrost output for one location.
type SynthesisResult struct {
	Recommendation string   `json:"recommendation"`
	Alternatives   []string `json:"alternatives"`
}

func buildSynthesisPrompt(allResults []LocationResults) string {
	var b strings.Builder
	b.WriteString("Voici les resultats d'analyse de nuisances :\n\n")
	for _, lr := range allResults {
		fmt.Fprintf(&b, "## %s (%s)\nVerdict: %s %s\n", lr.LocationName, lr.LocationID, VerdictEmoji(lr.Verdict), lr.Verdict)
		for _, cat := range lr.Categories {
			fmt.Fprintf(&b, "- %s %s: %s - %s\n", cat.Emoji, cat.Category, cat.Level, cat.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}

type synthesisResponse struct {
	Recommendations []struct {
		LocationID   string   `json:"locationId"`
		Text         string   `json:"text"`
		Alternatives []string `json:"alternatives"`
	} `json:"recommendations"`
}

func parseSynthesisResponse(raw string, allResults []LocationResults) map[string]SynthesisResult {
	out := make(map[string]SynthesisResult, len(allResults))
	for _, lr := range allResults {
		out[lr.LocationID] = SynthesisResult{}
	}

	// Try to extract JSON from the response (may be wrapped in markdown code block).
	jsonStr := extractJSON(raw)
	var resp synthesisResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return out
	}

	for _, rec := range resp.Recommendations {
		out[rec.LocationID] = SynthesisResult{
			Recommendation: rec.Text,
			Alternatives:   rec.Alternatives,
		}
	}
	return out
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip markdown code fences if present.
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	s = strings.TrimSpace(s)
	// Find the first { and last }.
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
