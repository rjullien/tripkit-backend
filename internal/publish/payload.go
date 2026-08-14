package publish

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SeedFile is the top-level object extracted from a voyage seed JS file.
type SeedFile struct {
	Trip        map[string]any            `json:"trip"`
	Days        []map[string]any          `json:"days"`
	Hotels      json.RawMessage           `json:"hotels"`
	Lists       map[string]map[string]any `json:"lists"`
	Restaurants map[string]any            `json:"restaurants"`
	Culture     any                       `json:"culture"`
	Locations   map[string]any            `json:"locations"`
	Flights     any                       `json:"flights"`
	CarRental   any                       `json:"carRental"`
	Ferry       any                       `json:"ferry"`
	Ferries     any                       `json:"ferries"`
	Events      any                       `json:"events"`
}

// Person is a subset of people.js used for ACL + trip.data.people.
type Person struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Login string `json:"login"`
	Note  string `json:"note"`
	// Extra fields (documents, etc.) preserved via Raw for trip.data — stripped of login on persist.
	Raw map[string]any `json:"-"`
}

// ChecklistConfig is the minimal checklist-config.js surface we validate.
type ChecklistConfig struct {
	Family string `json:"family"`
}

// CanonicalPayload is the import-ready representation after parse + people resolve.
type CanonicalPayload struct {
	TripID     string
	Name       string
	Emoji      *string
	StartDate  *string
	EndDate    *string
	TripData   map[string]any
	Days       []map[string]any
	Hotels     []HotelUpsert
	Lists      map[string]ListUpsert
	Assets     []AssetFile
	ACLMembers []string
	GitSHA     string
	SourceID   string
	Family     string
}

// HotelUpsert is one hotel row keyed by first day number (seed-import compatible).
type HotelUpsert struct {
	DayNum int
	Data   map[string]any
}

// ListUpsert is one seed list.
type ListUpsert struct {
	ID    string
	Type  string
	Title string
	Data  map[string]any
}

// AssetFile is a blob to store after apply (or in the same transaction when small).
type AssetFile struct {
	Filename    string
	ContentType string
	Data        []byte
}

// BuildCanonical merges seed + people into an import payload.
func BuildCanonical(seed SeedFile, people map[string]Person, family, sourceID, gitSHA string, assetFiles []AssetFile) (CanonicalPayload, error) {
	if seed.Trip == nil {
		return CanonicalPayload{}, fmt.Errorf("seed.trip missing")
	}
	tripID, _ := seed.Trip["id"].(string)
	name, _ := seed.Trip["name"].(string)
	if tripID == "" || name == "" {
		return CanonicalPayload{}, fmt.Errorf("seed.trip.id and seed.trip.name required")
	}
	if len(seed.Days) == 0 {
		return CanonicalPayload{}, fmt.Errorf("seed.days empty")
	}

	travelersRaw, _ := seed.Trip["travelers"].([]any)
	resolvedTravelers := make([]map[string]any, 0, len(travelersRaw))
	tripPeople := map[string]any{}
	acl := []string{}
	aclSeen := map[string]bool{}

	for _, t := range travelersRaw {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		pid, _ := tm["personId"].(string)
		out := map[string]any{}
		for k, v := range tm {
			out[k] = v
		}
		if pid != "" {
			if p, ok := people[pid]; ok {
				if out["name"] == nil || out["name"] == "" {
					out["name"] = p.Name
				}
				if out["emoji"] == nil || out["emoji"] == "" {
					out["emoji"] = p.Emoji
				}
				// Persist person fiche without login.
				fiche := map[string]any{}
				if p.Raw != nil {
					for k, v := range p.Raw {
						if k == "login" {
							continue
						}
						fiche[k] = v
					}
				} else {
					fiche["id"] = p.ID
					fiche["name"] = p.Name
					if p.Emoji != "" {
						fiche["emoji"] = p.Emoji
					}
					if p.Note != "" {
						fiche["note"] = p.Note
					}
				}
				tripPeople[pid] = fiche
				if login := strings.TrimSpace(p.Login); login != "" {
					login = strings.ToLower(login)
					if !aclSeen[login] {
						aclSeen[login] = true
						acl = append(acl, login)
					}
				}
			}
		}
		resolvedTravelers = append(resolvedTravelers, out)
	}

	var emoji *string
	if e, ok := seed.Trip["emoji"].(string); ok {
		emoji = &e
	}
	var start, end *string
	if s, ok := seed.Trip["startDate"].(string); ok {
		start = &s
	}
	if e, ok := seed.Trip["endDate"].(string); ok {
		end = &e
	}

	tripData := map[string]any{
		"travelers":     resolvedTravelers,
		"people":        tripPeople,
		"phases":        seed.Trip["phases"],
		"restaurants":   orEmptyMap(seed.Restaurants),
		"culture":       seed.Culture,
		"locations":     orEmptyMap(seed.Locations),
		"flights":       seed.Flights,
		"carRental":     seed.CarRental,
		"ferry":         seed.Ferry,
		"ferries":       seed.Ferries,
		"events":        seed.Events,
		"mapImage":      seed.Trip["mapImage"],
		"mapHtml":       seed.Trip["mapHtml"],
		"meteoHtml":     seed.Trip["meteoHtml"],
		"routeUrl":      seed.Trip["routeUrl"],
		"users":         orEmptyMapAny(seed.Trip["users"]),
		"homeTz":        stringOr(seed.Trip["homeTz"], "Europe/Paris"),
		"dailyBrief":    seed.Trip["dailyBrief"],
		"whatsappGroup": seed.Trip["whatsappGroup"],
		// Optional per-trip wall-clock "HH:MM" (day location TZ). Overrides ops sendLocalHour/Minute.
		"briefSendTime": seed.Trip["briefSendTime"],
		"polarsteps":    seed.Trip["polarsteps"],
	}

	// Hotels block also stored in trip.data (FE expects it).
	hotelsObj, hotelUpserts, err := normalizeHotels(seed.Hotels, seed.Days)
	if err != nil {
		return CanonicalPayload{}, err
	}
	tripData["hotels"] = hotelsObj

	lists := map[string]ListUpsert{}
	for id, list := range seed.Lists {
		typ, _ := list["type"].(string)
		if typ == "" {
			typ = "generic"
		}
		title, _ := list["title"].(string)
		if title == "" {
			title = id
		}
		lists[id] = ListUpsert{ID: id, Type: typ, Title: title, Data: list}
	}

	return CanonicalPayload{
		TripID:     tripID,
		Name:       name,
		Emoji:      emoji,
		StartDate:  start,
		EndDate:    end,
		TripData:   tripData,
		Days:       seed.Days,
		Hotels:     hotelUpserts,
		Lists:      lists,
		Assets:     assetFiles,
		ACLMembers: acl,
		GitSHA:     gitSHA,
		SourceID:   sourceID,
		Family:     family,
	}, nil
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptyMapAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func normalizeHotels(raw json.RawMessage, days []map[string]any) (map[string]any, []HotelUpsert, error) {
	outObj := map[string]any{}
	var upserts []HotelUpsert
	if len(raw) == 0 || string(raw) == "null" {
		return outObj, upserts, nil
	}

	// Dict form
	var asMap map[string]map[string]any
	if err := json.Unmarshal(raw, &asMap); err == nil && asMap != nil {
		for hotelID, hotelData := range asMap {
			outObj[hotelID] = hotelData
			dayNums := daysUsingHotel(days, hotelID)
			if len(dayNums) == 0 {
				continue
			}
			data := copyMap(hotelData)
			data["hotelId"] = hotelID
			data["dayNums"] = dayNums
			upserts = append(upserts, HotelUpsert{DayNum: dayNums[0], Data: data})
		}
		return outObj, upserts, nil
	}

	// Array form
	var asArr []map[string]any
	if err := json.Unmarshal(raw, &asArr); err != nil {
		return nil, nil, fmt.Errorf("hotels: %w", err)
	}
	for _, hotelData := range asArr {
		hotelID, _ := hotelData["id"].(string)
		if hotelID == "" {
			continue
		}
		outObj[hotelID] = hotelData
		dayNums := daysUsingHotel(days, hotelID)
		if len(dayNums) == 0 {
			continue
		}
		data := copyMap(hotelData)
		data["hotelId"] = hotelID
		data["dayNums"] = dayNums
		upserts = append(upserts, HotelUpsert{DayNum: dayNums[0], Data: data})
	}
	return outObj, upserts, nil
}

func daysUsingHotel(days []map[string]any, hotelID string) []int {
	var nums []int
	for _, d := range days {
		hid, _ := d["hotelId"].(string)
		if hid != hotelID {
			continue
		}
		switch n := d["day"].(type) {
		case float64:
			nums = append(nums, int(n))
		case int:
			nums = append(nums, n)
		}
	}
	return nums
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
