// Package construction assembles server-side context for Leo construction modes.
// It reads trip.Data JSON to extract travelers, style, budget, and interests
// so that mode-specific prompts can include relevant planning context.
package construction

import (
	"encoding/json"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// TravelerBrief is a summary of one traveler for prompt injection.
type TravelerBrief struct {
	Name        string `json:"name"`
	Nationality string `json:"nationality,omitempty"`
	IsChild     bool   `json:"isChild,omitempty"`
	AgeLabel    string `json:"ageLabel,omitempty"`
	HealthNote  string `json:"healthNote,omitempty"`
}

// StyleBrief captures the family travel style preferences.
type StyleBrief struct {
	Pace             string `json:"pace,omitempty"`
	MaxDrivingPerDay string `json:"maxDrivingPerDay,omitempty"`
	MajorSitesPerDay int    `json:"majorSitesPerDay,omitempty"`
}

// BudgetBrief captures budget constraints.
type BudgetBrief struct {
	AccommodationMax int    `json:"accommodationMax,omitempty"`
	RestaurantMax    int    `json:"restaurantMax,omitempty"`
	ActivitiesMax    int    `json:"activitiesMax,omitempty"`
	Currency         string `json:"currency,omitempty"`
}

// InterestBrief captures one person's likes/dislikes.
type InterestBrief struct {
	Likes    []string `json:"likes,omitempty"`
	Dislikes []string `json:"dislikes,omitempty"`
}

// Context carries trip-level construction data for mode-specific prompts.
type Context struct {
	TripName  string                   `json:"tripName,omitempty"`
	Travelers []TravelerBrief          `json:"travelers,omitempty"`
	Style     *StyleBrief              `json:"style,omitempty"`
	Budget    *BudgetBrief             `json:"budget,omitempty"`
	Interests map[string]InterestBrief `json:"interests,omitempty"`
}

// BuildLeoContext reads trip.Data from the database and assembles construction
// context. Returns nil (not an error) if the trip has no construction-relevant
// data -- this is expected for non-construction trips.
func BuildLeoContext(db *gorm.DB, tripID string) (*Context, error) {
	var trip models.Trip
	if err := db.First(&trip, "id = ?", tripID).Error; err != nil {
		// Trip not found is not a construction error; return nil gracefully.
		return nil, nil
	}
	if trip.Data == nil || *trip.Data == "" {
		return nil, nil
	}

	var data tripData
	if err := json.Unmarshal([]byte(*trip.Data), &data); err != nil {
		// Malformed JSON: not a blocker, just no context.
		return nil, nil
	}

	// Check if there is any construction-relevant data at all.
	if data.TravelProfile == nil && len(data.People) == 0 {
		return nil, nil
	}

	ctx := &Context{TripName: trip.Name}

	// Extract travelers from people array.
	for _, p := range data.People {
		tb := TravelerBrief{
			Name:        p.Name,
			Nationality: p.Nationality,
			IsChild:     p.IsChild,
			AgeLabel:    p.AgeLabel,
			HealthNote:  p.HealthNote,
		}
		ctx.Travelers = append(ctx.Travelers, tb)
	}

	// Extract style from travelProfile.travelStyle.
	if data.TravelProfile != nil {
		if ts := data.TravelProfile.TravelStyle; ts != nil {
			ctx.Style = &StyleBrief{
				Pace:             ts.Pace,
				MaxDrivingPerDay: ts.MaxDrivingPerDay,
				MajorSitesPerDay: ts.MajorSitesPerDay,
			}
		}

		// Extract budget from travelProfile.budgetRules.
		if br := data.TravelProfile.BudgetRules; br != nil {
			ctx.Budget = &BudgetBrief{
				AccommodationMax: br.Accommodation.MaxPerNight,
				RestaurantMax:    br.Restaurant.MaxPerPerson,
				ActivitiesMax:    br.Activities.MaxPerPerson,
				Currency:         br.Accommodation.Currency,
			}
		}

		// Extract interests.
		if len(data.TravelProfile.Interests) > 0 {
			ctx.Interests = make(map[string]InterestBrief, len(data.TravelProfile.Interests))
			for name, ip := range data.TravelProfile.Interests {
				ctx.Interests[name] = InterestBrief{
					Likes:    ip.Likes,
					Dislikes: ip.Dislikes,
				}
			}
		}
	}

	return ctx, nil
}

// ── Internal JSON shapes matching trip.Data ──────────────────────────────────

type tripData struct {
	People        []personEntry  `json:"people"`
	TravelProfile *travelProfile `json:"travelProfile"`
}

type personEntry struct {
	Name        string `json:"name"`
	Nationality string `json:"nationality"`
	IsChild     bool   `json:"isChild"`
	AgeLabel    string `json:"ageLabel"`
	HealthNote  string `json:"healthNote"`
}

type travelProfile struct {
	TravelStyle *travelStyle           `json:"travelStyle"`
	BudgetRules *budgetRules           `json:"budgetRules"`
	Interests   map[string]interestDef `json:"interests"`
}

type travelStyle struct {
	Pace             string `json:"pace"`
	MaxDrivingPerDay string `json:"maxDrivingPerDay"`
	MajorSitesPerDay int    `json:"majorSitesPerDay"`
}

type budgetRules struct {
	Accommodation budgetLimit `json:"accommodation"`
	Restaurant    budgetLimit `json:"restaurant"`
	Activities    budgetLimit `json:"activities"`
}

type budgetLimit struct {
	MaxPerNight  int    `json:"maxPerNight"`
	MaxPerPerson int    `json:"maxPerPerson"`
	Currency     string `json:"currency"`
}

type interestDef struct {
	Likes    []string `json:"likes"`
	Dislikes []string `json:"dislikes"`
}
