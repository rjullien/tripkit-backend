package dailybrief

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

const maxUncheckedShown = 10

// ListBriefSummary is shared-cloud list progress for the prep brief.
type ListBriefSummary struct {
	ListID       string   `json:"listId"`
	Type         string   `json:"type"` // packing|todo|shopping|…
	Title        string   `json:"title"`
	TotalItems   int      `json:"totalItems"`
	CheckedItems int      `json:"checkedItems"`
	Unchecked    []string `json:"unchecked,omitempty"` // capped sample for WhatsApp
	PriorityOpen []string `json:"priorityOpen,omitempty"` // docs/tech first when packing
}

// PrepContext feeds the two préparation briefs + optional day-1 inject.
type PrepContext struct {
	// Mode: "j0m1" (day -1 / J0-1), "j0" (day 0 / J0), or "depart" (day 1 inject).
	Mode string `json:"mode"`
	// Label for WhatsApp header (Brief J0-1 / Brief J0).
	Title string `json:"title,omitempty"`
	// VisibilityNote explains what the bot can / cannot see.
	VisibilityNote string `json:"visibilityNote"`
	Lists          []ListBriefSummary `json:"lists,omitempty"`
	// Downloads / prepare lines derived from day timeline + packing tech items.
	Downloads []string `json:"downloads,omitempty"`
	// LastCheck deterministic bullets (docs, charge, wake…).
	LastCheck []string `json:"lastCheck,omitempty"`
	// Comment is a short Go-built status line (LLM may paraphrase).
	Comment string `json:"comment,omitempty"`
}

// IsPrepDay is true for the two dedicated préparation briefs (day -1 and day 0).
func IsPrepDay(dayNumber int) bool {
	return dayNumber == -1 || dayNumber == 0
}

type seedListData struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Sections []struct {
		Title string `json:"title"`
		Items []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			Note string `json:"note,omitempty"`
		} `json:"items"`
	} `json:"sections"`
}

// LoadSharedListSummaries reads seed/shared lists (owner_user="") + ListCheck rows.
// Personal packing ("Ma valise") and local-only checks are invisible here.
func LoadSharedListSummaries(db *gorm.DB, tripID string) []ListBriefSummary {
	if db == nil || tripID == "" {
		return nil
	}
	var lists []models.List
	if err := db.Where("trip_id = ? AND owner_user = ?", tripID, "").
		Order("type asc, title asc").Find(&lists).Error; err != nil {
		return nil
	}
	var out []ListBriefSummary
	for _, l := range lists {
		sum := summarizeList(db, l)
		if sum.TotalItems == 0 && l.Type != "packing" && l.Type != "todo" {
			// Skip empty shopping/generic unless useful later.
			continue
		}
		if l.Type == "packing" || l.Type == "todo" || l.Type == "shopping" {
			out = append(out, sum)
		}
	}
	return out
}

func summarizeList(db *gorm.DB, l models.List) ListBriefSummary {
	sum := ListBriefSummary{
		ListID: l.ID,
		Type:   l.Type,
		Title:  strings.TrimSpace(l.Title),
	}
	var data seedListData
	_ = json.Unmarshal([]byte(l.Data), &data)
	if sum.Title == "" {
		sum.Title = strings.TrimSpace(data.Title)
	}
	if sum.Type == "" {
		sum.Type = data.Type
	}

	checked := map[string]bool{}
	var rows []models.ListCheck
	_ = db.Where("list_id = ?", l.ID).Find(&rows)
	for _, r := range rows {
		if r.Checked {
			checked[r.ItemID] = true
		}
	}

	type itemRef struct {
		id, text, section string
		priority          int
	}
	var items []itemRef
	for _, sec := range data.Sections {
		secTitle := strings.TrimSpace(sec.Title)
		prio := 2
		low := strings.ToLower(secTitle)
		if strings.Contains(low, "document") || strings.Contains(low, "tech") {
			prio = 0
		} else if strings.Contains(low, "avion") || strings.Contains(low, "cabine") || strings.Contains(low, "organisation") {
			prio = 1
		}
		for _, it := range sec.Items {
			text := strings.TrimSpace(it.Text)
			if text == "" || it.ID == "" {
				continue
			}
			items = append(items, itemRef{id: it.ID, text: text, section: secTitle, priority: prio})
		}
	}
	// Shared custom items count too.
	var customs []models.ListCustomItem
	_ = db.Where("list_id = ?", l.ID).Find(&customs)
	for _, c := range customs {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		items = append(items, itemRef{id: "custom-" + c.ID, text: text, section: "perso", priority: 3})
	}

	sum.TotalItems = len(items)
	var unchecked []itemRef
	for _, it := range items {
		if checked[it.id] {
			sum.CheckedItems++
		} else {
			unchecked = append(unchecked, it)
		}
	}
	sort.SliceStable(unchecked, func(i, j int) bool {
		if unchecked[i].priority != unchecked[j].priority {
			return unchecked[i].priority < unchecked[j].priority
		}
		return unchecked[i].text < unchecked[j].text
	})
	for i, it := range unchecked {
		if i >= maxUncheckedShown {
			break
		}
		sum.Unchecked = append(sum.Unchecked, it.text)
		if it.priority == 0 && len(sum.PriorityOpen) < 6 {
			sum.PriorityOpen = append(sum.PriorityOpen, it.text)
		}
	}
	return sum
}

// BuildPrepContext assembles prep payload from lists + day extract.
// Two prep briefs: day -1 (J0-1) and day 0 (J0). Day 1 gets a light depart inject.
func BuildPrepContext(db *gorm.DB, src *DayBriefData) *PrepContext {
	if src == nil {
		return nil
	}
	mode, title := "", ""
	switch src.DayNumber {
	case -1:
		mode, title = "j0m1", "Brief J0-1 (J−2 avant départ)"
	case 0:
		mode, title = "j0", "Brief J0 (veille)"
	case 1:
		mode, title = "depart", "Dernier check listes"
	default:
		return nil
	}
	lists := LoadSharedListSummaries(db, src.TripID)
	ctx := &PrepContext{
		Mode:  mode,
		Title: title,
		Lists: lists,
		VisibilityNote: "Je vois seulement les listes partagées (cloud TripKit). " +
			"Valises perso, coches locales, et tout hors listes : je ne sais pas — j'espère que c'est fait.",
		Downloads: extractDownloadReminders(src, lists),
		LastCheck: extractLastCheck(src, lists, mode),
		Comment:   listProgressComment(lists, mode),
	}
	return ctx
}

func listProgressComment(lists []ListBriefSummary, mode string) string {
	if len(lists) == 0 {
		return "Aucune liste partagée visible en base — ouvrez TripKit → Listes pour cocher (mode partagé)."
	}
	var parts []string
	openCritical := 0
	for _, l := range lists {
		if l.TotalItems == 0 {
			continue
		}
		parts = append(parts, l.Title+": "+strconv.Itoa(l.CheckedItems)+"/"+strconv.Itoa(l.TotalItems))
		openCritical += len(l.PriorityOpen)
		if l.Type == "todo" {
			left := l.TotalItems - l.CheckedItems
			if left > 0 {
				openCritical += left
			}
		}
	}
	base := "Listes cloud — " + strings.Join(parts, " · ")
	switch mode {
	case "j0m1":
		if openCritical > 0 {
			return base + ". J0-1 : démarrez valise + avant-de-partir — encore beaucoup d'ouverts."
		}
		return base + ". Belle avance dès J0-1 — gardez le rythme jusqu'à demain (J0)."
	case "j0":
		if openCritical > 0 {
			return base + ". J0 veille : priorité docs/tech + finir avant-de-partir avant demain."
		}
		return base + ". Listes partagées quasi closes — dernier tour ce soir."
	case "depart":
		if openCritical > 0 {
			return base + ". Encore des points critiques ouverts — dernier check avant de partir."
		}
		return base + ". Côté listes partagées, ça a l'air nickel — bon vol."
	default:
		return base
	}
}

func extractDownloadReminders(src *DayBriefData, lists []ListBriefSummary) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(stripHTML(s))
		if s == "" || seen[strings.ToLower(s)] {
			return
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	blob := strings.ToLower(strings.Join(src.Highlights, " ") + " " + src.Label)
	for _, e := range src.Timeline {
		d, _ := e["label"].(string)
		if d == "" {
			d, _ = e["d"].(string)
		}
		low := strings.ToLower(d)
		if strings.Contains(low, "télécharg") || strings.Contains(low, "telecharg") ||
			strings.Contains(low, "hors-ligne") || strings.Contains(low, "hors ligne") ||
			strings.Contains(low, "apps") || strings.Contains(low, "enregistrement") ||
			(strings.Contains(low, "carte") && strings.Contains(low, "embarqu")) {
			add(d)
		}
	}
	for _, l := range lists {
		if l.Type != "packing" {
			continue
		}
		for _, u := range l.PriorityOpen {
			low := strings.ToLower(u)
			if strings.Contains(low, "maps") || strings.Contains(low, "hors-ligne") ||
				strings.Contains(low, "app ") || strings.Contains(low, "embarqu") ||
				strings.Contains(low, "enregistrement") || strings.Contains(low, "airtag") ||
				strings.Contains(low, "batterie") || strings.Contains(low, "adaptateur") {
				add(u)
			}
		}
	}
	if len(out) == 0 {
		if strings.Contains(blob, "québec") || strings.Contains(blob, "quebec") || strings.Contains(blob, "canada") {
			add("Maps hors-ligne (zones du voyage) + apps utiles")
			add("Cartes d'embarquement / enregistrement en ligne")
		} else {
			add("Apps utiles + cartes hors-ligne de la destination")
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func extractLastCheck(src *DayBriefData, lists []ListBriefSummary, mode string) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(stripHTML(s))
		if s != "" {
			out = append(out, s)
		}
	}
	for _, e := range src.Timeline {
		d, _ := e["label"].(string)
		if d == "" {
			d, _ = e["d"].(string)
		}
		low := strings.ToLower(d)
		match := strings.Contains(low, "document") || strings.Contains(low, "passeport") ||
			strings.Contains(low, "pnr") || strings.Contains(low, "eta") ||
			strings.Contains(low, "charge") || strings.Contains(low, "vérification")
		if mode == "j0" || mode == "depart" {
			match = match || strings.Contains(low, "coucher") || strings.Contains(low, "vol demain")
		}
		if mode == "j0m1" {
			// Early prep: focus house + packing start, not "coucher tôt / vol demain".
			match = strings.Contains(low, "avant de partir") || strings.Contains(low, "valise") ||
				strings.Contains(low, "document") || strings.Contains(low, "télécharg") ||
				strings.Contains(low, "telecharg") || strings.Contains(low, "enregistrement")
		}
		if match {
			t, _ := e["time"].(string)
			if t == "" {
				t, _ = e["t"].(string)
			}
			if t != "" {
				add(t + " — " + d)
			} else {
				add(d)
			}
		}
	}
	for _, l := range lists {
		for _, p := range l.PriorityOpen {
			add("☐ " + p)
			if len(out) >= 8 {
				break
			}
		}
	}
	if len(out) == 0 {
		if mode == "j0m1" {
			add("Ouvrir les listes partagées TripKit (valise + avant de partir)")
			add("Commencer docs/tech (passeports, eTA, Maps hors-ligne)")
			add("Planifier courses / maison sur 48 h")
		} else {
			add("Passeports + cartes d'embarquement / PNR")
			add("Téléphones chargés + batterie externe")
			add("Dernier tour maison (eau, gaz, volets) si applicable")
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func stripHTML(s string) string {
	// Minimal: drop <a …> labels keep text.
	for {
		start := strings.Index(s, "<")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], ">")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+1:]
	}
	return strings.Join(strings.Fields(s), " ")
}
