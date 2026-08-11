package dailybrief

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var reHTMLTag = regexp.MustCompile(`<[^>]+>`)

// EnrichPlaceContext adds Wikipedia place facts + traveler news (soft-fail).
func EnrichPlaceContext(data *DayBriefData) {
	if data == nil {
		return
	}
	place := strings.TrimSpace(data.PlaceName)
	if place == "" {
		place = strings.TrimSpace(data.Label)
	}
	if place == "" {
		return
	}
	if facts := fetchWikiFacts(place); len(facts) > 0 {
		data.PlaceFacts = facts
	}
	// Keep up to ~12 candidates; pipeline LLM curation (or fallback) picks ≤3.
	if news := fetchTravelerNews(place); len(news) > 0 {
		data.Actualites = news
	}
}

func fetchWikiFacts(place string) []string {
	client := &http.Client{Timeout: 8 * time.Second}
	title := wikiResolveTitle(client, place)
	if title == "" {
		title = place
	}
	u := "https://fr.wikipedia.org/api/rest_v1/page/summary/" + url.PathEscape(title)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "tripkit-backend-dailybrief/1.0")
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil
	}
	var parsed struct {
		Extract     string `json:"extract"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	var out []string
	if d := strings.TrimSpace(parsed.Description); d != "" {
		out = append(out, d)
	}
	extract := strings.TrimSpace(parsed.Extract)
	if extract != "" {
		// Split into 1–2 short sentences for À savoir.
		parts := splitSentences(extract)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if len(p) > 220 {
				p = p[:217] + "…"
			}
			out = append(out, p)
			if len(out) >= 3 {
				break
			}
		}
	}
	return out
}

func wikiResolveTitle(client *http.Client, place string) string {
	u := "https://fr.wikipedia.org/w/api.php?action=opensearch&limit=1&namespace=0&format=json&search=" + url.QueryEscape(place)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "tripkit-backend-dailybrief/1.0")
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var parsed []any
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed) < 2 {
		return ""
	}
	titles, _ := parsed[1].([]any)
	if len(titles) == 0 {
		return ""
	}
	t, _ := titles[0].(string)
	return strings.TrimSpace(t)
}

func splitSentences(s string) []string {
	s = strings.ReplaceAll(s, "\n", " ")
	var out []string
	start := 0
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			chunk := strings.TrimSpace(s[start : i+1])
			if chunk != "" {
				out = append(out, chunk)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		if chunk := strings.TrimSpace(s[start:]); chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}

type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Source      string `xml:"source"`
}

func fetchTravelerNews(place string) []ActualiteItem {
	q := fmt.Sprintf("%s (spectacle OR festival OR concert OR exposition OR musée OR événement OR restaurant OR tourisme OR fermeture) when:7d", place)
	u := "https://news.google.com/rss/search?q=" + url.QueryEscape(q) + "&hl=fr-CA&gl=CA&ceid=CA:fr"
	// Broader French news if place looks European
	low := strings.ToLower(place)
	if strings.Contains(low, "paris") || strings.Contains(low, "france") || strings.Contains(low, "lyon") {
		u = "https://news.google.com/rss/search?q=" + url.QueryEscape(q) + "&hl=fr&gl=FR&ceid=FR:fr"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "tripkit-backend-dailybrief/1.0")
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode >= 300 {
		return nil
	}
	var feed rssFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		return nil
	}
	var out []ActualiteItem
	for _, it := range feed.Channel.Items {
		title := cleanNewsTitle(it.Title)
		if title == "" || newsHardDeny(title) || newsVagueDeny(title) {
			continue
		}
		src := strings.TrimSpace(it.Source)
		if src == "" {
			src = newsSourceFromTitle(it.Title)
		}
		snippet := cleanNewsSnippet(it.Description)
		link := strings.TrimSpace(it.Link)
		out = append(out, ActualiteItem{
			Title:   title,
			Source:  src,
			URL:     link,
			Snippet: snippet,
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func cleanNewsTitle(t string) string {
	t = html.UnescapeString(strings.TrimSpace(t))
	t = reHTMLTag.ReplaceAllString(t, "")
	// Google often appends " - Source"
	if i := strings.LastIndex(t, " - "); i > 0 && len(t)-i < 40 {
		t = strings.TrimSpace(t[:i])
	}
	return strings.TrimSpace(t)
}

func cleanNewsSnippet(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = reHTMLTag.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 360 {
		s = s[:357] + "…"
	}
	return strings.TrimSpace(s)
}

func newsSourceFromTitle(t string) string {
	t = html.UnescapeString(t)
	if i := strings.LastIndex(t, " - "); i > 0 {
		return strings.TrimSpace(t[i+3:])
	}
	return ""
}

func newsHardDeny(title string) bool {
	low := strings.ToLower(title)
	deny := []string{
		"homicide", "meurtre", "fusillade", "agression sexuelle", "crash boursier",
		"condamné", "condamnee", "condamnée", "condamne", "procès", "proces", "tribunal",
		"antiavortement", "avortement", "avort", "élection", "election", "parti québécois",
		"ministre", "député", "depute", "politique", "gouvernement", "assemblée nationale",
		"poursuite", "amende", "inculp",
	}
	for _, d := range deny {
		if strings.Contains(low, d) {
			return true
		}
	}
	return false
}

// newsVagueDeny drops listicles that are not actionable without digging.
func newsVagueDeny(title string) bool {
	low := strings.ToLower(title)
	vague := []string{
		"sorties gratuites", "sorties à faire", "sorties a faire",
		"à voir en", "a voir en", "à faire en", "a faire en",
		"shows à voir", "shows a voir", "spectacles à voir",
		"meilleures sorties", "meilleures activités", "idées de sorties", "idees de sorties",
		"guide des sorties", "que faire en", "quoi faire en",
		"top 5", "top 10", "top 20",
	}
	for _, v := range vague {
		if strings.Contains(low, v) {
			return true
		}
	}
	// "6 sorties…", "20 shows…"
	if reListicleCount.MatchString(low) {
		return true
	}
	return false
}

var reListicleCount = regexp.MustCompile(`(?i)\b\d+\s+(sorties|shows|spectacles|activités|activites|choses|événements|evenements)\b`)

// travelerRelevant is a deterministic fallback when LLM curation is unavailable.
func travelerRelevant(title string) bool {
	if newsHardDeny(title) || newsVagueDeny(title) {
		return false
	}
	low := strings.ToLower(title)
	allow := []string{
		"festival", "concert", "spectacle", "exposition", "musée", "musee",
		"tourisme", "restaurant", "hôtel", "hotel", "événement", "evenement",
		"culture", "théâtre", "theatre", "cinéma", "cinema", "marché", "marche",
		"fermeture", "grève", "greve", "chantier", "météo", "meteo", "pluie",
		"voyage", "visiteur", "quartier", "patrimoine", "sortie", "gratuit",
		"été", "ete", "plein air",
	}
	for _, a := range allow {
		if strings.Contains(low, a) {
			return true
		}
	}
	return false
}
