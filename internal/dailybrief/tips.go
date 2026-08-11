package dailybrief

import (
	"fmt"
	"strings"
)

const maxOptionalTips = 5
const maxActualites = 3

// SelectDayTips fills CultureExpress, mandatory PracticalTip, and 0–5 optional tips.
// Call after weather / placeFacts / actualites enrichment.
func SelectDayTips(data *DayBriefData) {
	if data == nil {
		return
	}
	data.CultureExpress = pickCultureExpress(data.PlaceName, data.Label)
	data.PracticalTip = pickPracticalTip(data)

	var candidates []Tip
	add := func(t *Tip) {
		if t == nil || strings.TrimSpace(t.Text) == "" {
			return
		}
		candidates = append(candidates, *t)
	}

	add(tipPlanB(data))
	add(tipTransport(data))
	add(tipFood(data))
	add(tipPhoto(data))
	add(tipTiming(data))
	add(tipBudget(data))
	add(tipFamille(data))
	add(tipSecurite(data))

	if len(candidates) > maxOptionalTips {
		candidates = candidates[:maxOptionalTips]
	}
	data.Tips = candidates
}

func pickPracticalTip(data *DayBriefData) *Tip {
	// Always one line — prioritize by day context.
	if data.TravelDay {
		dist := strings.TrimSpace(data.Dist)
		dur := strings.TrimSpace(data.Duration)
		text := "Jour de route : pauses toutes les 2h, plein d'essence / borne avant les zones peu denses."
		if dist != "" && dist != "-" {
			text = fmt.Sprintf("Trajet %s", dist)
			if dur != "" && dur != "-" {
				text += " (~" + dur + ")"
			}
			text += " — pauses régulières, vérifiez essence / borne avant de partir."
		}
		return &Tip{Kind: "pratique", Title: "Astuce pratique", Text: text}
	}
	if raining(data) {
		return &Tip{
			Kind:  "pratique",
			Title: "Astuce pratique",
			Text:  "Pluie au programme : chaussures imperméables + petite serviette dans le sac ; privilégiez les tronçons indoor / cafés entre deux averses.",
		}
	}
	if data.Hotel != nil {
		if ci, _ := data.Hotel["checkin"].(string); strings.TrimSpace(ci) != "" {
			return &Tip{
				Kind:  "pratique",
				Title: "Astuce pratique",
				Text:  "Check-in " + strings.TrimSpace(ci) + " — gardez le mail/SMS avec le code d'accès sous la main.",
			}
		}
	}
	place := strings.TrimSpace(data.PlaceName)
	if place == "" {
		place = "la zone du jour"
	}
	return &Tip{
		Kind:  "pratique",
		Title: "Astuce pratique",
		Text:  "Gardez un peu de cash pour petits commerces / pourboires ; carte OK partout ailleurs à " + place + ".",
	}
}

func pickCultureExpress(place, label string) *Tip {
	blob := strings.ToLower(place + " " + label)
	type entry struct {
		match []string
		text  string
	}
	bank := []entry{
		{[]string{"québec", "quebec", "montreal", "montréal", "canada", "charlevoix", "saguenay", "gaspé", "gaspe", "tadoussac"},
			`Au Québec on dit « bienvenue » pour « de rien ». Pourboire resto ~15 % si service non inclus.`},
		{[]string{"paris", "france", "lyon", "bordeaux", "marseille", "nice", "langon"},
			`« Bonjour » en entrant dans un commerce — sinon ça froisse. Pourboire resto : arrondi ou ~5–10 % si service déjà inclus.`},
		{[]string{"usa", "vegas", "new york", "california", "miami"},
			`Pourboire resto 15–20 % quasi obligatoire. « Excuse me » pour attirer l'attention poliment.`},
		{[]string{"spain", "espagne", "barcelona", "barcelone", "madrid", "balears", "mallorca", "majorque"},
			`« ¡Hola! » / « gracias ». Dîner souvent tard (21h+). Pourboire léger (arrondi).`},
		{[]string{"italy", "italie", "rome", "roma", "florence", "milan"},
			`« Coperto » (couvert) parfois facturé à part — normal. Espresso se boit souvent debout au zinc.`},
		{[]string{"germany", "allemagne", "berlin", "munich", "münchen"},
			`Cash encore fréquent dans petits commerces. « Bitte » / « Danke » suffisent au quotidien.`},
		{[]string{"japan", "japon", "tokyo", "osaka"},
			`Pas de pourboire. Parler bas dans les transports. Files et ponctualité = respect.`},
		{[]string{"philippines", "manila", "cebu"},
			`« Salamat » = merci. Attitude cool et souriante ; négocier avec le sourire sur marchés.`},
	}
	for _, e := range bank {
		for _, m := range e.match {
			if strings.Contains(blob, m) {
				return &Tip{Kind: "culture_express", Title: "Culture express", Text: e.text}
			}
		}
	}
	return &Tip{
		Kind:  "culture_express",
		Title: "Culture express",
		Text:  "Un sourire + « bonjour / hello / merci » dans la langue locale ouvre presque toutes les portes.",
	}
}

func tipPlanB(data *DayBriefData) *Tip {
	if !raining(data) || data.TravelDay {
		return nil
	}
	place := orPlace(data)
	return &Tip{
		Kind:  "plan_b",
		Title: "Plan B météo",
		Text:  "Si l'averse s'intensifie à " + place + " : musée / galerie / café-librairie à proximité — reprenez le outdoor dès une éclaircie.",
	}
}

func tipTransport(data *DayBriefData) *Tip {
	if !data.TravelDay {
		return nil
	}
	from := strings.TrimSpace(data.From)
	text := "Jour de transfert : démarrez tôt, snacks + eau dans la voiture, playlist prête."
	if from != "" {
		text = "Départ côté " + from + " — anticiper le trafic urbain en sortant de ville."
	}
	return &Tip{Kind: "transport", Title: "Transport", Text: text}
}

func tipFood(data *DayBriefData) *Tip {
	if data.Restaurant != nil {
		if name, _ := data.Restaurant["name"].(string); strings.TrimSpace(name) != "" {
			return &Tip{
				Kind:  "food",
				Title: "Food tip",
				Text:  "Resto du jour : " + strings.TrimSpace(name) + " — vérifiez l'horaire / résa avant d'y aller.",
			}
		}
	}
	if data.TravelDay || raining(data) {
		return nil
	}
	blob := strings.ToLower(data.PlaceName + " " + data.Label)
	if strings.Contains(blob, "québec") || strings.Contains(blob, "quebec") {
		return &Tip{
			Kind:  "food",
			Title: "Food tip",
			Text:  "Envie locale : poutine ou smoked meat pour un vrai break québécois entre deux flâneries.",
		}
	}
	return nil
}

func tipPhoto(data *DayBriefData) *Tip {
	if data.TravelDay {
		return nil
	}
	if len(data.Highlights) > 0 {
		h := stripEmojiPrefix(data.Highlights[0])
		return &Tip{
			Kind:  "photo",
			Title: "Photo spot",
			Text:  "Cadrez " + h + " — même sous la bruine, l'ambiance vaut le détour.",
		}
	}
	if place := strings.TrimSpace(data.PlaceName); place != "" {
		return &Tip{
			Kind:  "photo",
			Title: "Photo spot",
			Text:  "Cherchez un point de vue dégagé sur " + place + " (terrasse, belvédère, bord d'eau).",
		}
	}
	return nil
}

func tipTiming(data *DayBriefData) *Tip {
	if data.TravelDay || len(data.Timeline) == 0 {
		return nil
	}
	return &Tip{
		Kind:  "timing",
		Title: "Timing",
		Text:  "Les spots phares sont plus calmes avant 10h et après 17h — midi = files et photos bondées.",
	}
}

func tipBudget(data *DayBriefData) *Tip {
	if data.TravelDay {
		return nil
	}
	blob := strings.ToLower(data.PlaceName + " " + strings.Join(data.Highlights, " "))
	if strings.Contains(blob, "chute") || strings.Contains(blob, "montmorency") {
		return &Tip{
			Kind:  "budget",
			Title: "Budget flash",
			Text:  "Chutes / sites payants : prévoyez cash ou carte pour parking + billet ; gardez le ticket pour la journée.",
		}
	}
	return nil
}

func tipFamille(data *DayBriefData) *Tip {
	if !data.HasKids || data.TravelDay {
		return nil
	}
	return &Tip{
		Kind:  "famille",
		Title: "Rythme famille",
		Text:  "Avec enfants : insérez une pause goûter / aire de jeu toutes les 2–3h, même si le programme est dense.",
	}
}

func tipSecurite(data *DayBriefData) *Tip {
	blob := strings.ToLower(data.PlaceName + " " + data.Label + " " + strings.Join(data.Highlights, " "))
	if raining(data) && (strings.Contains(blob, "chute") || strings.Contains(blob, "sentier") || strings.Contains(blob, "fjord") || strings.Contains(blob, "falaise")) {
		return &Tip{
			Kind:  "securite",
			Title: "Sécurité soft",
			Text:  "Sols glissants près des chutes / sentiers humides — chaussures fermées, garde-corps, pas de selfie risqué.",
		}
	}
	if strings.Contains(blob, "ours") || strings.Contains(blob, "parc national") || strings.Contains(blob, "fjord") {
		return &Tip{
			Kind:  "securite",
			Title: "Sécurité soft",
			Text:  "Nature : restez sur les sentiers balisés, nourriture sécurisée, distance avec la faune.",
		}
	}
	return nil
}

func raining(data *DayBriefData) bool {
	if data == nil || data.Weather == nil {
		return false
	}
	cond, _ := data.Weather["conditions"].(string)
	cond = strings.ToLower(cond)
	if strings.Contains(cond, "pluie") || strings.Contains(cond, "orage") {
		return true
	}
	if code, ok := asFloat(data.Weather["weatherCode"]); ok {
		c := int(code)
		return (c >= 51 && c <= 67) || (c >= 80 && c <= 82) || (c >= 95 && c <= 99)
	}
	return false
}

func orPlace(data *DayBriefData) string {
	if data != nil && strings.TrimSpace(data.PlaceName) != "" {
		return strings.TrimSpace(data.PlaceName)
	}
	return "la ville"
}

func stripEmojiPrefix(s string) string {
	s = strings.TrimSpace(s)
	// Drop leading emoji / symbols until first letter
	for i, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 'À' && r <= 'ÿ') || (r >= '0' && r <= '9') {
			return strings.TrimSpace(s[i:])
		}
	}
	return s
}
