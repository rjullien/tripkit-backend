package dailybrief

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BifrostClient calls OpenAI-compatible chat/completions (format only — not Hermes).
type BifrostClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func NewBifrostClient(baseURL, apiKey, model string) *BifrostClient {
	if model == "" {
		model = defaultModel
	}
	return &BifrostClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Model:   model,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type bifrostMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type bifrostReq struct {
	Model     string       `json:"model"`
	Messages  []bifrostMsg `json:"messages"`
	MaxTokens int          `json:"max_tokens,omitempty"`
}

type bifrostResp struct {
	Choices []struct {
		Message bifrostMsg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

const formatSystemPrompt = `Tu es l'assistant du trip. Tu reçois les données du jour et tu dois produire un message WhatsApp formaté.

Règles :
- Utilise les emojis et le formatage WhatsApp (*gras*, _italique_)
- Sois concis mais chaleureux, scannable en ~20 secondes
- Mets les infos critiques en premier (alertes, heures de départ)
- Adapte le ton au contexte (jour relax vs jour chargé, pluie vs soleil, route vs journée sur place)
- N'invente AUCUNE information. Utilise UNIQUEMENT les données fournies (highlights, placeFacts, actualites, tips, cultureExpress, practicalTip, timeline, hotel, weather…).
- N'ajoute pas de liens ou numéros de téléphone que tu n'as pas reçus en input.
- Réponds UNIQUEMENT avec le message formaté, sans explication ni commentaire.
- Inclus toujours le jour de la semaine ET la date en français (ex. dimanche 16 août).
- Si un hôtel est fourni, mentionne-le clairement (nom + check-in si dispo).

SECTIONS OBLIGATOIRES (toujours, dans cet esprit) :
1) ⭐ *À savoir* — 2 à 4 pépites sympas sur le lieu / la zone (historique, géologique, anecdote). Base-toi sur highlights + placeFacts. Ne dilue PAS ces pépites dans le programme : elles vont DANS cette section.
2) 📰 *Actualité* — jusqu'à 3 items issus de actualites[] : utilise *detail* s'il est fourni (sinon title), puis le *url* sur la ligne suivante s'il est présent. INTERDIT : politique, procès, polarisant, faits divers, listicles vagues sans détail. N'invente pas d'URL.
3) 💡 *Astuce pratique* — UNE seule ligne, depuis practicalTip (obligatoire).

SECTIONS OPTIONNELLES (si présentes dans les données) :
- 🗣️ *Culture express* — depuis cultureExpress
- Autres tips[] (photo, plan B, timing, food, transport, budget, famille, sécurité) : 0 à 5, seulement ceux fournis, une ligne chacun
- Ne crée PAS de tip « famille / parc / enfants » si hasKids=false
- Ne force PAS de tip route si travelDay=false`

const correctSystemPrompt = formatSystemPrompt + `

Tu corriges un message WhatsApp déjà généré qui a échoué au QA.
On te donne : (1) les données source, (2) ton message précédent, (3) le rapport QA.
Corrige UNIQUEMENT les problèmes listés. N'invente rien. Réponds UNIQUEMENT avec le message corrigé.`

// Format asks Bifrost to turn enriched day JSON into WhatsApp text.
func (c *BifrostClient) Format(enriched any) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("bifrost not configured")
	}
	userPayload, err := json.Marshal(enriched)
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt <= 2; attempt++ {
		text, err := c.chatOnce(formatSystemPrompt, string(userPayload))
		if err == nil {
			return text, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	return "", lastErr
}

// FormatCorrect asks Bifrost to fix a previous brief using QA findings (one shot).
func (c *BifrostClient) FormatCorrect(enriched any, previousText string, qa QAResult) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", fmt.Errorf("bifrost not configured")
	}
	srcJSON, err := json.Marshal(enriched)
	if err != nil {
		return "", err
	}
	qaJSON, err := json.Marshal(qa)
	if err != nil {
		return "", err
	}
	user := "Données source (JSON):\n" + string(srcJSON) +
		"\n\nMessage précédent:\n" + previousText +
		"\n\nRapport QA (JSON):\n" + string(qaJSON) +
		"\n\nCorrige le message pour passer le QA."

	var lastErr error
	for attempt := 0; attempt <= 2; attempt++ {
		text, err := c.chatOnce(correctSystemPrompt, user)
		if err == nil {
			return text, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	return "", lastErr
}

const curateActuSystemPrompt = `Tu filtres et CREUSES des actualités pour un brief voyageur WhatsApp.
Objectif : infos ACTIONNABLES pendant le séjour DANS CETTE VILLE (placeStayFrom → placeStayTo), surtout utiles aujourd'hui (date).

GARDE seulement si le voyageur peut agir pendant cette fenêtre : lieu/événement nommé, fermeture utile, spectacle daté, resto, expo en cours.
REJETTE : politique, procès, faits divers, polarisant.
REJETTE listicles vagues ("6 sorties", "20 shows en août", "à faire en…") sauf si le snippet donne un événement nommé + timing utilisable pendant placeStayFrom…placeStayTo.
Si l'événement est clairement hors de la fenêtre sur place → rejette.

Pour chaque item retenu, écris "detail" = UNE phrase concrète (quoi + où + quand si connu dans title/snippet).
N'invente RIEN (pas de date/lieu/heure absents du title/snippet).
Recopie title/source/url EXACTEMENT depuis le candidat.

Réponds UNIQUEMENT avec un JSON array (0 à 3 objets) :
[{"title":"...","source":"...","url":"...","detail":"..."}]`

// CurateActualites asks Bifrost to pick ≤3 actionable traveler headlines and dig a detail line.
func (c *BifrostClient) CurateActualites(data *DayBriefData, candidates []ActualiteItem) ([]ActualiteItem, error) {
	if c == nil || c.BaseURL == "" {
		return nil, fmt.Errorf("bifrost not configured")
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ctx := map[string]any{
		"placeName":      data.PlaceName,
		"locationId":     data.LocationID,
		"weekday":        data.Weekday,
		"date":           data.Date,
		"placeStayFrom":  data.PlaceStayFrom,
		"placeStayTo":    data.PlaceStayTo,
		"travelDay":      data.TravelDay,
		"hasKids":        data.HasKids,
		"weather":        data.Weather,
		"highlights":     data.Highlights,
		"candidates":     candidates,
	}
	payload, err := json.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	user := "Contexte jour + séjour sur place + candidats JSON:\n" + string(payload) +
		"\n\nRetiens jusqu'à 3 actualités actionnables pour CE séjour dans la ville (JSON array uniquement)."

	raw, err := c.chatOnce(curateActuSystemPrompt, user)
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	// Strip optional markdown fence
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
	}
	var picked []ActualiteItem
	if err := json.Unmarshal([]byte(raw), &picked); err != nil {
		return nil, fmt.Errorf("curate json: %w (%s)", err, truncate(raw, 120))
	}
	// Keep only items that match a candidate title (anti-hallucination)
	byTitle := map[string]ActualiteItem{}
	for _, cnd := range candidates {
		byTitle[strings.ToLower(strings.TrimSpace(cnd.Title))] = cnd
	}
	var out []ActualiteItem
	for _, p := range picked {
		key := strings.ToLower(strings.TrimSpace(p.Title))
		if key == "" || newsHardDeny(p.Title) || newsVagueDeny(p.Title) {
			continue
		}
		orig, ok := byTitle[key]
		if !ok {
			continue
		}
		detail := strings.TrimSpace(p.Detail)
		if detail == "" {
			detail = fallbackDetail(orig)
		}
		item := orig
		item.Detail = detail
		// Never invent URLs — keep candidate URL only.
		out = append(out, item)
		if len(out) >= maxActualites {
			break
		}
	}
	return out, nil
}

func fallbackDetail(it ActualiteItem) string {
	if s := strings.TrimSpace(it.Snippet); s != "" {
		if len(s) > 160 {
			return s[:157] + "…"
		}
		return s
	}
	return strings.TrimSpace(it.Title)
}

func (c *BifrostClient) chatOnce(system, userContent string) (string, error) {
	body, err := json.Marshal(bifrostReq{
		Model: c.Model,
		Messages: []bifrostMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: userContent},
		},
		MaxTokens: 2000,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tripkit-backend-dailybrief")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bifrost unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))

	var parsed bifrostResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("bifrost invalid JSON (HTTP %d): %s", res.StatusCode, truncate(string(raw), 200))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("bifrost: %s", parsed.Error.Message)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("bifrost HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("bifrost returned no choices")
	}
	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("bifrost returned empty text")
	}
	return text, nil
}
