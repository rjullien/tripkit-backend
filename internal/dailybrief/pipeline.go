package dailybrief

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service wires config + clients for generate / send.
type Service struct {
	DB     *gorm.DB
	Loader *Loader
}

type GenerateResult struct {
	Text        string         `json:"text"`
	DayNumber   int            `json:"dayNumber"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Weather     map[string]any `json:"weather,omitempty"`
	QA          QAResult       `json:"qa"`
	Sent        bool           `json:"sent"`
	Timezone    string         `json:"timezone,omitempty"`
	// QALoopUsed is true when a single Bifrost correction pass ran after QA FAILED.
	QALoopUsed bool `json:"qaLoopUsed,omitempty"`
	Source     *DayBriefData `json:"-"`
}

// SendOptions controls force / destination override (admin test).
type SendOptions struct {
	Force bool
	// To overrides seed whatsappGroup (admin DM for tests). Empty = group from seed.
	To string
	// SkipConfigGate allows generate/send when trip.dailyBrief is not yet in DB (admin test).
	SkipConfigGate bool
}

type SendResult struct {
	Sent          bool      `json:"sent"`
	Group         string    `json:"group"`
	MessageLength int       `json:"messageLength"`
	MessageID     string    `json:"messageId,omitempty"`
	QAVerdict     QAVerdict `json:"qaVerdict"`
	QALoopUsed    bool      `json:"qaLoopUsed,omitempty"`
	SentAt        time.Time `json:"sentAt"`
	Timezone      string    `json:"timezone,omitempty"`
	Error         string    `json:"error,omitempty"`
}

func (s *Service) cfg() Config {
	if s.Loader != nil {
		return s.Loader.Get()
	}
	return DefaultConfig()
}

// Generate builds the WhatsApp brief (no send).
func (s *Service) Generate(tripID string, dayNumber int) (*GenerateResult, error) {
	return s.GenerateOpts(tripID, dayNumber, ExtractOpts{RequireConfigured: true})
}

// GenerateOpts builds the brief with extract options.
func (s *Service) GenerateOpts(tripID string, dayNumber int, opts ExtractOpts) (*GenerateResult, error) {
	src, err := ExtractDayOpts(s.DB, tripID, dayNumber, opts)
	if err != nil {
		return nil, err
	}

	var trip models.Trip
	_ = s.DB.First(&trip, "id = ?", tripID)
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}
	var day models.Day
	dayData := map[string]any{}
	if err := s.DB.Where("trip_id = ? AND day_num = ?", tripID, dayNumber).First(&day).Error; err == nil {
		_ = json.Unmarshal([]byte(day.Data), &dayData)
	}
	if lat, lon, ok := CoordsFromTripData(tripData, dayData); ok {
		_ = EnrichDay(src, lat, lon)
	}
	// Prep briefs (day -1 = J0-1, day 0 = J0): skip place news/wiki noise.
	if !IsPrepDay(dayNumber) {
		EnrichPlaceContext(src)
	}

	cfg := s.cfg()
	bf := NewBifrostClient(cfg.BifrostBaseURL, cfg.BifrostAPIKey, cfg.BriefModel)

	// Prep days + day-1 départ: attach shared cloud list progress.
	if IsPrepDay(dayNumber) || dayNumber == 1 {
		src.Prep = BuildPrepContext(s.DB, src)
	}

	var text string
	qaLoopUsed := false

	if IsPrepDay(dayNumber) {
		// Dedicated prep briefs — no Actualité / Culture express loop.
		SelectDayTips(src, nil)
		src.CultureExpress = nil
		src.Actualites = nil
		src.Tips = filterTipsForPrep(src.Tips)
		if src.PracticalTip == nil || strings.TrimSpace(src.PracticalTip.Text) == "" {
			src.PracticalTip = &Tip{
				Kind:  "pratique",
				Title: "Astuce pratique",
				Text:  "Cochez les listes en mode partagé dans TripKit pour que tout le monde voie l'avancement.",
			}
		}
		var prepErr error
		text, prepErr = bf.FormatPrep(src)
		if prepErr != nil {
			return nil, prepErr
		}
		qa := RunQA(text, src)
		if qa.Verdict == QAFailed {
			corrected, corrErr := bf.FormatPrepCorrect(src, text, qa)
			if corrErr != nil {
				log.Printf("dailybrief: prep QA correction failed: %v", corrErr)
			} else {
				qaLoopUsed = true
				text = corrected
				qa = RunQA(text, src)
			}
		}
		return &GenerateResult{
			Text:        text,
			DayNumber:   dayNumber,
			GeneratedAt: time.Now().UTC(),
			Weather:     src.Weather,
			QA:          qa,
			Sent:        false,
			Timezone:    src.Timezone,
			QALoopUsed:  qaLoopUsed,
			Source:      src,
		}, nil
	}

	// Extra LLM call: curate + dig Actualité (actionable detail; drop listicles / junk).
	if len(src.Actualites) > 0 {
		candidates := src.Actualites
		if curated, err := bf.CurateActualites(src, candidates); err != nil {
			log.Printf("dailybrief: actu curation failed, fallback filter: %v", err)
			src.Actualites = fallbackActualites(candidates)
		} else if len(curated) > 0 {
			src.Actualites = curated
		} else {
			src.Actualites = fallbackActualites(candidates)
		}
	}

	usedTips := LoadUsedTips(s.DB, tripID, "")
	// Drop place facts already sent on this trip (À savoir anti-redite) before tip pick / Format.
	src.PlaceFacts = filterPlaceFacts(src.PlaceFacts, usedTips)
	if len(src.PlaceFactsBySegment) > 0 {
		src.PlaceFactsBySegment = filterPlaceFactsBySegment(src.PlaceFactsBySegment, usedTips)
	}

	SelectDayTips(src, usedTips)
	// Culture express: LLM creative + used texts in prompt + 1-shot redite check; bank = fallback.
	cultureUsed := usedTexts(usedOfKind(usedTips, kindCulture))
	if tip, err := bf.GenerateCultureExpress(src, cultureUsed); err != nil {
		log.Printf("dailybrief: culture LLM failed, bank fallback: %v", err)
	} else if tip != nil {
		src.CultureExpress = tip
	}
	// Actualité is mandatory in the brief — soft placeholder if feeds are empty.
	if len(src.Actualites) == 0 {
		place := strings.TrimSpace(src.PlaceName)
		if place == "" {
			place = "la destination"
		}
		src.Actualites = []ActualiteItem{{
			Title:  "Pas de une locale actionnable ce matin — focus sur le programme à " + place,
			Detail: "Pas de une locale actionnable ce matin — focus sur le programme à " + place,
			Source: "TripKit",
		}}
	}

	text, err = bf.Format(src)
	if err != nil {
		return nil, err
	}
	qa := RunQA(text, src)
	if qa.Verdict == QAFailed {
		corrected, corrErr := bf.FormatCorrect(src, text, qa)
		if corrErr != nil {
			log.Printf("dailybrief: QA correction failed: %v", corrErr)
		} else {
			qaLoopUsed = true
			text = corrected
			qa = RunQA(text, src)
		}
	}
	return &GenerateResult{
		Text:        text,
		DayNumber:   dayNumber,
		GeneratedAt: time.Now().UTC(),
		Weather:     src.Weather,
		QA:          qa,
		Sent:        false,
		Timezone:    src.Timezone,
		QALoopUsed:  qaLoopUsed,
		Source:      src,
	}, nil
}

func filterTipsForPrep(tips []Tip) []Tip {
	// Veille: keep at most rain Plan B; drop sightseeing tips.
	var out []Tip
	for _, t := range tips {
		if t.Kind == "plan_b" {
			out = append(out, t)
		}
	}
	return out
}

func fallbackActualites(candidates []ActualiteItem) []ActualiteItem {
	var out []ActualiteItem
	for _, c := range candidates {
		if !travelerRelevant(c.Title) {
			continue
		}
		c.Detail = fallbackDetail(c)
		out = append(out, c)
		if len(out) >= maxActualites {
			break
		}
	}
	return out
}

// GenerateAndSend runs pipeline + GoWA send when QA allows.
func (s *Service) GenerateAndSend(tripID string, dayNumber int, force bool) (*SendResult, error) {
	return s.GenerateAndSendOpts(tripID, dayNumber, SendOptions{Force: force})
}

// HasSentBrief reports whether a successful send already exists for this trip/day/local date.
func HasSentBrief(db *gorm.DB, tripID string, dayNumber int, localDate string) bool {
	if db == nil || tripID == "" || localDate == "" {
		return false
	}
	var n int64
	_ = db.Model(&models.DailyBriefSend{}).
		Where("trip_id = ? AND day_number = ? AND local_date = ? AND sent = ?", tripID, dayNumber, localDate, true).
		Count(&n).Error
	return n > 0
}

// GenerateAndSendOpts supports force + To override (admin test DM).
func (s *Service) GenerateAndSendOpts(tripID string, dayNumber int, opt SendOptions) (*SendResult, error) {
	// Resolve local date + cheap idempotence *before* LLM generate (send window retries every minute).
	tzName := "UTC"
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err == nil {
		tzName = DayTimezone(s.DB, trip, dayNumber)
	}
	localDate := time.Now().UTC().Format("2006-01-02")
	if loc, err := time.LoadLocation(tzName); err == nil {
		localDate = time.Now().In(loc).Format("2006-01-02")
	}

	if !opt.Force {
		var existing models.DailyBriefSend
		err := s.DB.Where("trip_id = ? AND day_number = ? AND local_date = ? AND sent = ?", tripID, dayNumber, localDate, true).
			First(&existing).Error
		if err == nil {
			return &SendResult{
				Sent:          true,
				Group:         existing.WhatsAppTo,
				MessageLength: existing.MessageLen,
				MessageID:     existing.MessageID,
				QAVerdict:     QAVerdict(existing.QAVerdict),
				SentAt:        existing.CreatedAt,
				Timezone:      tzName,
				Error:         "already_sent",
			}, nil
		}
	}

	extractOpts := ExtractOpts{RequireConfigured: !opt.SkipConfigGate}
	gen, err := s.GenerateOpts(tripID, dayNumber, extractOpts)
	if err != nil {
		return nil, err
	}

	to := strings.TrimSpace(opt.To)
	if to == "" && gen.Source != nil {
		to = gen.Source.WhatsAppGroup
	}
	if to == "" {
		return nil, fmt.Errorf("no WhatsApp destination (seed whatsappGroup empty and no to= override)")
	}

	// Prefer TZ from extract when available (same as historical behaviour).
	if gen.Source != nil && gen.Source.Timezone != "" {
		tzName = gen.Source.Timezone
		if loc, err := time.LoadLocation(tzName); err == nil {
			localDate = time.Now().In(loc).Format("2006-01-02")
		}
	}

	// Re-check after generate (concurrent tick / TZ refine).
	if !opt.Force {
		var existing models.DailyBriefSend
		err := s.DB.Where("trip_id = ? AND day_number = ? AND local_date = ? AND sent = ?", tripID, dayNumber, localDate, true).
			First(&existing).Error
		if err == nil {
			return &SendResult{
				Sent:          true,
				Group:         existing.WhatsAppTo,
				MessageLength: existing.MessageLen,
				MessageID:     existing.MessageID,
				QAVerdict:     QAVerdict(existing.QAVerdict),
				SentAt:        existing.CreatedAt,
				Timezone:      gen.Timezone,
				Error:         "already_sent",
			}, nil
		}
	}

	if gen.QA.Verdict == QAFailed {
		_ = s.recordSend(tripID, dayNumber, localDate, gen, to, "", false, gen.QA.Summary)
		_ = s.notifyAdminQAFailed(gen)
		return &SendResult{
			Sent:       false,
			Group:      to,
			QAVerdict:  QAFailed,
			QALoopUsed: gen.QALoopUsed,
			SentAt:     time.Now().UTC(),
			Timezone:   gen.Timezone,
			Error:      gen.QA.Summary,
		}, fmt.Errorf("QA FAILED: %s", gen.QA.Summary)
	}

	cfg := s.cfg()
	gowa := NewGowaClient(cfg.GowaBaseURL)
	msgID, err := gowa.Send(to, gen.Text)
	if err != nil {
		_ = s.recordSend(tripID, dayNumber, localDate, gen, to, "", false, err.Error())
		return nil, err
	}
	_ = s.recordSend(tripID, dayNumber, localDate, gen, to, msgID, true, "")
	recordAntiRediteAfterSend(s.DB, gen, tripID, dayNumber)
	if gen.QALoopUsed {
		_ = s.notifyAdminQALoopUsed(gen)
	}
	return &SendResult{
		Sent:          true,
		Group:         to,
		MessageLength: len(gen.Text),
		MessageID:     msgID,
		QAVerdict:     gen.QA.Verdict,
		QALoopUsed:    gen.QALoopUsed,
		SentAt:        time.Now().UTC(),
		Timezone:      gen.Timezone,
	}, nil
}

func (s *Service) recordSend(tripID string, dayNumber int, localDate string, gen *GenerateResult, to, msgID string, sent bool, errMsg string) error {
	row := models.DailyBriefSend{
		TripID:     tripID,
		DayNumber:  dayNumber,
		LocalDate:  localDate,
		QAVerdict:  string(gen.QA.Verdict),
		Sent:       sent,
		WhatsAppTo: to,
		MessageID:  msgID,
		MessageLen: len(gen.Text),
		Error:      errMsg,
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trip_id"}, {Name: "day_number"}, {Name: "local_date"}},
		DoUpdates: clause.AssignmentColumns([]string{"qa_verdict", "sent", "whatsapp_to", "message_id", "message_len", "error"}),
	}).Create(&row).Error
}

func (s *Service) notifyAdminQAFailed(gen *GenerateResult) error {
	cfg := s.cfg()
	if cfg.AdminPhone == "" || gen == nil || gen.Source == nil {
		return nil
	}
	detail := formatQAIssues(gen.QA)
	loop := "non"
	if gen.QALoopUsed {
		loop = "oui (1 correction)"
	}
	msg := fmt.Sprintf("⚠️ Daily Brief QA FAILED après correction\ntrip=%s day=%d\nqaLoop=%s\n%s\n%s",
		gen.Source.TripID, gen.DayNumber, loop, gen.QA.Summary, detail)
	gowa := NewGowaClient(cfg.GowaBaseURL)
	_, err := gowa.Send(cfg.AdminPhone, msg)
	if err != nil {
		log.Printf("dailybrief: admin notify failed: %v", err)
	}
	return err
}

func (s *Service) notifyAdminQALoopUsed(gen *GenerateResult) error {
	cfg := s.cfg()
	if cfg.AdminPhone == "" || gen == nil || gen.Source == nil {
		return nil
	}
	msg := fmt.Sprintf("ℹ️ Daily Brief : boucle QA utilisée (1 correction)\ntrip=%s day=%d\nverdict final=%s\n%s",
		gen.Source.TripID, gen.DayNumber, gen.QA.Verdict, gen.QA.Summary)
	gowa := NewGowaClient(cfg.GowaBaseURL)
	_, err := gowa.Send(cfg.AdminPhone, msg)
	if err != nil {
		log.Printf("dailybrief: admin qa-loop notify failed: %v", err)
	}
	return err
}

func formatQAIssues(qa QAResult) string {
	var b strings.Builder
	appendList := func(label string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(label)
		b.WriteString(":\n")
		for _, it := range items {
			b.WriteString("• ")
			b.WriteString(it)
			b.WriteByte('\n')
		}
	}
	appendList("contradictions", qa.Contradictions)
	appendList("placeholders", qa.Placeholders)
	appendList("completeness", qa.Completeness)
	appendList("hallucinations", qa.Hallucinations)
	return strings.TrimSpace(b.String())
}
