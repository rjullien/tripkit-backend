package seedgit

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PatchActivity inserts or replaces one entry under top-level activities.
func PatchActivity(src string, activity map[string]any) (string, error) {
	id, _ := activity["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("activity id required")
	}
	raw, err := json.Marshal(activity)
	if err != nil {
		return "", err
	}
	objStart, objEnd, err := seedObjectSpan(src)
	if err != nil {
		return "", err
	}
	root := src[objStart:objEnd]
	acts, actsErr := findDepth1Object(root, "activities")
	if actsErr != nil {
		quoted, _ := json.Marshal(id)
		block := "\n  activities: {\n    " + string(quoted) + ": " + string(raw) + "\n  },"
		newRoot := "{" + block + root[1:]
		return splice(src, objStart, objEnd, newRoot), nil
	}
	updated, err := setDepth1JSON(acts.value, id, string(raw))
	if err != nil {
		return "", err
	}
	newRoot := splice(root, acts.start, acts.end, updated)
	return splice(src, objStart, objEnd, newRoot), nil
}

// PatchPin writes trip.construction.lastQa and hotels[id].nuisance.
func PatchPin(src string, lastQa map[string]any, hotelNuisance map[string]map[string]any) (string, error) {
	objStart, objEnd, err := seedObjectSpan(src)
	if err != nil {
		return "", err
	}
	root := src[objStart:objEnd]

	if lastQa != nil {
		raw, err := json.Marshal(lastQa)
		if err != nil {
			return "", err
		}
		trip, err := findDepth1Object(root, "trip")
		if err != nil {
			return "", fmt.Errorf("seed trip: %w", err)
		}
		cons, consErr := findDepth1Object(trip.value, "construction")
		if consErr != nil {
			block := "\n    \"construction\": {\n      \"lastQa\": " + string(raw) + "\n    },"
			newTrip := "{" + block + trip.value[1:]
			root = splice(root, trip.start, trip.end, newTrip)
		} else {
			updatedCons, err := setDepth1JSON(cons.value, "lastQa", string(raw))
			if err != nil {
				return "", err
			}
			newTrip := splice(trip.value, cons.start, cons.end, updatedCons)
			root = splice(root, trip.start, trip.end, newTrip)
		}
	}

	if len(hotelNuisance) > 0 {
		hotels, err := findDepth1Object(root, "hotels")
		if err == nil {
			updatedHotels := hotels.value
			for id, nui := range hotelNuisance {
				if nui == nil {
					continue
				}
				raw, err := json.Marshal(nui)
				if err != nil {
					return "", err
				}
				hotel, herr := findDepth1Object(updatedHotels, id)
				if herr != nil {
					continue
				}
				patchedHotel, err := setDepth1JSON(hotel.value, "nuisance", string(raw))
				if err != nil {
					return "", err
				}
				updatedHotels = splice(updatedHotels, hotel.start, hotel.end, patchedHotel)
			}
			root = splice(root, hotels.start, hotels.end, updatedHotels)
		}
	}

	return splice(src, objStart, objEnd, root), nil
}

func setDepth1JSON(obj, key, rawJSON string) (string, error) {
	loc, err := findDepth1Key(obj, key)
	if err != nil {
		return insertDepth1JSON(obj, key, rawJSON)
	}
	end, err := skipValue(obj, loc.valueStart)
	if err != nil {
		return "", err
	}
	return splice(obj, loc.valueStart, end, rawJSON), nil
}

func insertDepth1JSON(obj, key, rawJSON string) (string, error) {
	if len(obj) < 2 || obj[0] != '{' {
		return "", fmt.Errorf("expected object")
	}
	quoted, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	block := "\n    " + string(quoted) + ": " + rawJSON + ","
	return "{" + block + obj[1:], nil
}
