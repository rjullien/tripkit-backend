package polarsteps

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Result is a generated (or last saved) caption.
type Result struct {
	Day      int      `json:"day"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text,omitempty"`
	UserNote string   `json:"userNote,omitempty"`
	QA       QAResult `json:"qa"`
}

// Service generates and stores Polarsteps captions.
type Service struct {
	DB        *gorm.DB
	Loader    *Loader
	Completer Completer
	Now       func() time.Time
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) completer() Completer {
	if s != nil && s.Completer != nil {
		return s.Completer
	}
	if s == nil || s.Loader == nil {
		return nil
	}
	cfg := s.Loader.Get()
	return NewBifrostCompleter(cfg)
}

// Status reports whether the Plus box should show for this trip.
func (s *Service) Status(tripID string, now time.Time) (map[string]any, error) {
	ops := DefaultConfig()
	if s != nil && s.Loader != nil {
		ops = s.Loader.Get()
	}
	out := map[string]any{
		"opsEnabled": ops.Enabled,
		"ready":      false,
		"enabled":    false,
		"origin":     ops.Origin,
		"model":      ops.CaptionModel,
	}
	if s == nil || s.DB == nil {
		return out, nil
	}
	var trip models.Trip
	if err := s.DB.First(&trip, "id = ?", tripID).Error; err != nil {
		return nil, fmt.Errorf("trip not found")
	}
	tripData := map[string]any{}
	if trip.Data != nil {
		_ = json.Unmarshal([]byte(*trip.Data), &tripData)
	}
	_, hasGate := polarstepsMap(tripData)
	gate := TripPolarsteps(tripData)
	seedOn := gate.Enabled
	if !hasGate {
		// Live Québec was published before trip.polarsteps.enabled. Show the
		// box while the trip is happening; explicit enabled:false still hides.
		seedOn = true
	}
	start, end := "", ""
	if trip.StartDate != nil {
		start = *trip.StartDate
	}
	if trip.EndDate != nil {
		end = *trip.EndDate
	}
	startT, err := time.Parse("2006-01-02", start)
	localDate := ""
	if err == nil {
		localNow, _, _ := pickLocalDay(s.DB, tripID, startT, tripData, now)
		localDate = localNow.Format("2006-01-02")
	} else {
		homeTZ := str(tripData["homeTz"])
		if homeTZ == "" {
			homeTZ = "Europe/Paris"
		}
		loc, locErr := time.LoadLocation(homeTZ)
		if locErr != nil {
			loc = time.UTC
		}
		localDate = now.In(loc).Format("2006-01-02")
	}
	active := TripActive(start, end, localDate)
	out["tripUrl"] = gate.TripURL
	out["seedEnabled"] = seedOn
	out["active"] = active
	out["enabled"] = ops.Enabled && seedOn && active
	out["ready"] = ops.Ready() && seedOn && active
	return out, nil
}

// Generate runs extract → LLM → QA → persist on PASSED/WARNING.
func (s *Service) Generate(tripID, userNote, clientNowISO string) (*Result, int, error) {
	if s == nil || s.DB == nil {
		return nil, 500, fmt.Errorf("service not configured")
	}
	now := s.now()
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(clientNowISO)); err == nil {
		now = t
	}
	st, err := s.Status(tripID, now)
	if err != nil {
		return nil, 404, err
	}
	if enabled, _ := st["enabled"].(bool); !enabled {
		return nil, 404, fmt.Errorf("polarsteps disabled")
	}
	if ready, _ := st["ready"].(bool); !ready {
		return nil, 503, fmt.Errorf("polarsteps not ready")
	}

	in, _, _, err := BuildInput(s.DB, tripID, now, userNote)
	if err != nil {
		return nil, 400, err
	}
	if in.Day < 1 {
		return nil, 400, fmt.Errorf("pas de texte Polarsteps avant J1")
	}

	c := s.completer()
	if c == nil {
		return nil, 503, fmt.Errorf("llm not configured")
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, 500, err
	}
	text, err := c.Complete(systemPrompt, string(payload))
	if err != nil {
		return nil, 502, err
	}
	qa := RunQA(text, in)
	res := &Result{Day: in.Day, Kind: in.Kind, UserNote: in.UserNote, QA: qa}
	if qa.Verdict == QAFailed {
		return res, 422, nil
	}
	res.Text = text
	row := models.PolarstepsCaption{
		TripID:    tripID,
		DayNumber: in.Day,
		Kind:      in.Kind,
		Text:      text,
		UserNote:  in.UserNote,
		QAVerdict: string(qa.Verdict),
	}
	if err := s.DB.Save(&row).Error; err != nil {
		return nil, 500, err
	}
	return res, 200, nil
}

// Last returns the stored caption for the computed day (if any).
func (s *Service) Last(tripID, clientNowISO string) (*Result, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("service not configured")
	}
	now := s.now()
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(clientNowISO)); err == nil {
		now = t
	}
	in, _, _, err := BuildInput(s.DB, tripID, now, "")
	if err != nil {
		return nil, err
	}
	if in.Day < 1 {
		return &Result{Day: in.Day, Kind: in.Kind, QA: QAResult{Verdict: QAPassed, Summary: "empty"}}, nil
	}
	var row models.PolarstepsCaption
	if err := s.DB.Where("trip_id = ? AND day_number = ?", tripID, in.Day).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return &Result{Day: in.Day, Kind: in.Kind, QA: QAResult{Verdict: QAPassed, Summary: "empty"}}, nil
		}
		return nil, err
	}
	return &Result{
		Day:      row.DayNumber,
		Kind:     row.Kind,
		Text:     row.Text,
		UserNote: row.UserNote,
		QA:       QAResult{Verdict: QAVerdict(row.QAVerdict), Summary: row.QAVerdict},
	}, nil
}
