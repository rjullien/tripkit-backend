package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode"

	"github.com/rjullien/tripkit-backend/internal/leo"
)

const maxEditorialItems = 8

const editorialSystemPrompt = `Tu es Léo. Tu as accès à la recherche web. Tu DOIS chercher sur internet (pas ta mémoire d'entraînement) les événements pour le LIEU et la DATE indiqués.

Réponds UNIQUEMENT avec un JSON array, sans markdown, sans texte autour :
[{"name":"…","when":"2026-08-21","url":"https://…","note":"une ligne"}]

Règles :
- Date de référence = le champ date du message, PAS aujourd'hui.
- Seulement des événements réellement programmés à cette date (ou ce week-end autour, si le thème est saisonnier).
- Si tu ne trouves rien de fiable : []
- N'invente pas de nom, de date, ni d'URL.
- URL officielle ou billetterie si possible.
- Maximum 8 résultats, les plus pertinents pour ce lieu.
- N'écris rien dans git. Pas de seed. JSON seulement.`

// EditorialQuery is one Leo web-search for a Jour place+date.
type EditorialQuery struct {
	Theme    Theme
	Place    string
	TripName string
	DateISO  string
	Lat      float64
	Lon      float64
}

// EditorialSearcher looks up festivals / spectacles via Leo (Hermes web search).
type EditorialSearcher interface {
	Search(ctx context.Context, q EditorialQuery) ([]Item, error)
}

// LeoEditorial calls Hermes with a discovery-only prompt (no seed writes).
type LeoEditorial struct {
	Complete func(ctx context.Context, system, user string) (string, error)
}

// NewLeoEditorialFromEnv returns a searcher if Hermes is configured, else nil.
func NewLeoEditorialFromEnv() EditorialSearcher {
	cfg := leo.LoadConfigFromEnv()
	if !cfg.Ready() {
		log.Printf("discovery: editorial Leo skipped (Hermes not configured)")
		return nil
	}
	return &LeoEditorial{
		Complete: func(ctx context.Context, system, user string) (string, error) {
			return cfg.AgentComplete(ctx, system, user)
		},
	}
}

func (l *LeoEditorial) Search(ctx context.Context, q EditorialQuery) ([]Item, error) {
	if l == nil || l.Complete == nil {
		return nil, fmt.Errorf("leo editorial not configured")
	}
	raw, err := l.Complete(ctx, editorialSystemPrompt, editorialUserPrompt(q))
	if err != nil {
		return nil, err
	}
	return parseEditorialJSON(raw, q.Theme)
}

func editorialUserPrompt(q EditorialQuery) string {
	hints := strings.Join(q.Theme.QueryHints, ", ")
	var b strings.Builder
	fmt.Fprintf(&b, "Thème : %s (%s)\n", q.Theme.Label, q.Theme.ID)
	if hints != "" {
		fmt.Fprintf(&b, "Mots-clés : %s\n", hints)
	}
	fmt.Fprintf(&b, "Lieu : %s\n", strings.TrimSpace(q.Place))
	if strings.TrimSpace(q.TripName) != "" {
		fmt.Fprintf(&b, "Voyage : %s\n", q.TripName)
	}
	fmt.Fprintf(&b, "Date : %s\n", q.DateISO)
	if q.Lat != 0 || q.Lon != 0 {
		fmt.Fprintf(&b, "Coordonnées : %.4f,%.4f\n", q.Lat, q.Lon)
	}
	if q.Theme.Seasonal {
		b.WriteString("Saisonnier : cherche aussi le week-end autour de cette date.\n")
	} else {
		b.WriteString("Spectacles / concerts / théâtre ce jour-là (soirée comprise).\n")
	}
	b.WriteString("Cherche sur internet maintenant, puis JSON only.")
	return b.String()
}

type editorialHit struct {
	Name string `json:"name"`
	When string `json:"when"`
	URL  string `json:"url"`
	Note string `json:"note"`
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

func parseEditorialJSON(raw string, theme Theme) ([]Item, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if m := fenceRe.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array")
	}
	s = s[start : end+1]
	var hits []editorialHit
	if err := json.Unmarshal([]byte(s), &hits); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(hits))
	seen := map[string]bool{}
	for _, h := range hits {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Item{
			ID:      "editorial:" + theme.ID + ":" + slug(name),
			ThemeID: theme.ID,
			Name:    name,
			URL:     strings.TrimSpace(h.URL),
			When:    strings.TrimSpace(h.When),
			Note:    strings.TrimSpace(h.Note),
			Source:  "editorial",
		})
		if len(out) >= maxEditorialItems {
			break
		}
	}
	return out, nil
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "item"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}
