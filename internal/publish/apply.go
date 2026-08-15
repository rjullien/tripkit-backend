package publish

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// ApplyResult summarizes a successful transactional apply.
type ApplyResult struct {
	Created     bool
	DataVersion int64
	Days        int
	Hotels      int
	Lists       int
	Assets      int
	ACLMembers  []string
	GroupID     string
}

// ApplyCanonical writes trip data + per-trip ACL in one DB transaction.
//
// Semantics:
//   - seed lists (owner_user="") are authoritative; personal lists preserved
//   - checks/custom/hidden for kept seed lists are preserved
//   - stale seed days/hotels/lists removed
//   - ACL group id = "trip-" + tripID; members replaced for that group only
//   - ownerLogins are always included in ACL members
func ApplyCanonical(db *gorm.DB, p CanonicalPayload, ownerLogins []string) (ApplyResult, error) {
	if p.TripID == "" {
		return ApplyResult{}, fmt.Errorf("trip id required")
	}
	groupID := "trip-" + p.TripID
	members := mergeMembers(p.ACLMembers, ownerLogins)

	var result ApplyResult
	result.GroupID = groupID
	result.ACLMembers = members

	err := db.Transaction(func(tx *gorm.DB) error {
		var existing models.Trip
		err := tx.First(&existing, "id = ?", p.TripID).Error
		created := err == gorm.ErrRecordNotFound
		if err != nil && !created {
			return err
		}
		result.Created = created

		// Seed without trip.construction must not wipe a phase written via
		// PUT /construction/phase. Quebec (and any inited trip) keeps its state
		// across a publish that only updates days/hotels.
		if !created && p.TripData["construction"] == nil && existing.Data != nil && *existing.Data != "" {
			var prev map[string]any
			if json.Unmarshal([]byte(*existing.Data), &prev) == nil {
				if c, ok := prev["construction"]; ok && c != nil {
					p.TripData["construction"] = c
				}
			}
		}

		dataBytes, err := json.Marshal(p.TripData)
		if err != nil {
			return err
		}
		dataStr := string(dataBytes)
		now := time.Now()

		if created {
			trip := models.Trip{
				ID:        p.TripID,
				Name:      p.Name,
				Emoji:     p.Emoji,
				StartDate: p.StartDate,
				EndDate:   p.EndDate,
				Data:      &dataStr,
			}
			if err := tx.Create(&trip).Error; err != nil {
				return fmt.Errorf("create trip: %w", err)
			}
		} else {
			updates := map[string]any{
				"name":       p.Name,
				"data":       dataStr,
				"updated_at": now,
			}
			if p.Emoji != nil {
				updates["emoji"] = *p.Emoji
			}
			if p.StartDate != nil {
				updates["start_date"] = *p.StartDate
			}
			if p.EndDate != nil {
				updates["end_date"] = *p.EndDate
			}
			if err := tx.Model(&existing).Updates(updates).Error; err != nil {
				return fmt.Errorf("update trip: %w", err)
			}
		}

		// Days
		keepDays := map[int]bool{}
		for _, day := range p.Days {
			dayNum, err := dayNumOf(day)
			if err != nil {
				return err
			}
			keepDays[dayNum] = true
			raw, err := json.Marshal(day)
			if err != nil {
				return err
			}
			var row models.Day
			q := tx.Where("trip_id = ? AND day_num = ?", p.TripID, dayNum).First(&row)
			if q.Error == gorm.ErrRecordNotFound {
				row = models.Day{TripID: p.TripID, DayNum: dayNum, Data: string(raw)}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("create day %d: %w", dayNum, err)
				}
			} else if q.Error != nil {
				return q.Error
			} else if err := tx.Model(&row).Update("data", string(raw)).Error; err != nil {
				return fmt.Errorf("update day %d: %w", dayNum, err)
			}
		}
		var allDays []models.Day
		if err := tx.Where("trip_id = ?", p.TripID).Find(&allDays).Error; err != nil {
			return err
		}
		for _, d := range allDays {
			if !keepDays[d.DayNum] {
				if err := tx.Delete(&d).Error; err != nil {
					return err
				}
			}
		}
		result.Days = len(keepDays)

		// Hotels — replace all for trip (seed authoritative)
		if err := tx.Where("trip_id = ?", p.TripID).Delete(&models.Hotel{}).Error; err != nil {
			return err
		}
		for _, h := range p.Hotels {
			raw, err := json.Marshal(h.Data)
			if err != nil {
				return err
			}
			row := models.Hotel{TripID: p.TripID, DayNum: h.DayNum, Data: string(raw)}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create hotel day %d: %w", h.DayNum, err)
			}
		}
		result.Hotels = len(p.Hotels)

		// Lists — sync seed lists (owner_user=""); keep personal lists
		var existingLists []models.List
		if err := tx.Where("trip_id = ?", p.TripID).Find(&existingLists).Error; err != nil {
			return err
		}
		keepLists := map[string]bool{}
		for id := range p.Lists {
			keepLists[id] = true
		}
		for _, l := range existingLists {
			if l.OwnerUser != "" {
				continue // personal
			}
			if !keepLists[l.ID] {
				// delete seed list + state
				if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListCheck{}).Error; err != nil {
					return err
				}
				if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListCustomItem{}).Error; err != nil {
					return err
				}
				if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListHidden{}).Error; err != nil {
					return err
				}
				if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListCustomDeleted{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&l).Error; err != nil {
					return err
				}
			}
		}
		for id, list := range p.Lists {
			// Collision: list id belongs to another trip
			var other models.List
			err := tx.Where("id = ? AND trip_id <> ?", id, p.TripID).First(&other).Error
			if err == nil {
				return fmt.Errorf("list id %q already attached to trip %q", id, other.TripID)
			}
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}

			raw, err := json.Marshal(list.Data)
			if err != nil {
				return err
			}
			var row models.List
			q := tx.Where("id = ?", id).First(&row)
			if q.Error == gorm.ErrRecordNotFound {
				row = models.List{
					ID:        id,
					TripID:    p.TripID,
					Type:      list.Type,
					Title:     list.Title,
					Data:      string(raw),
					OwnerUser: "",
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("create list %s: %w", id, err)
				}
			} else if q.Error != nil {
				return q.Error
			} else {
				if err := tx.Model(&row).Updates(map[string]any{
					"trip_id": p.TripID,
					"type":    list.Type,
					"title":   list.Title,
					"data":    string(raw),
					// preserve OwnerUser (should be "")
				}).Error; err != nil {
					return fmt.Errorf("update list %s: %w", id, err)
				}
			}
		}
		result.Lists = len(p.Lists)

		// Assets
		for _, a := range p.Assets {
			if a.Filename == "" || len(a.Data) == 0 {
				continue
			}
			ct := a.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			var row models.Asset
			q := tx.Where("trip_id = ? AND filename = ?", p.TripID, a.Filename).First(&row)
			if q.Error == gorm.ErrRecordNotFound {
				row = models.Asset{
					TripID:      p.TripID,
					Filename:    a.Filename,
					ContentType: ct,
					Size:        int64(len(a.Data)),
					Data:        a.Data,
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("create asset %s: %w", a.Filename, err)
				}
			} else if q.Error != nil {
				return q.Error
			} else {
				if err := tx.Model(&row).Updates(map[string]any{
					"content_type": ct,
					"size":         int64(len(a.Data)),
					"data":         a.Data,
				}).Error; err != nil {
					return fmt.Errorf("update asset %s: %w", a.Filename, err)
				}
			}
			result.Assets++
		}

		// Per-trip ACL group
		g := models.Group{ID: groupID, Name: "Trip " + p.TripID}
		if err := tx.Save(&g).Error; err != nil {
			return fmt.Errorf("save group: %w", err)
		}
		if err := tx.Where("group_id = ?", groupID).Delete(&models.GroupMember{}).Error; err != nil {
			return err
		}
		for _, u := range members {
			if err := tx.Create(&models.GroupMember{GroupID: groupID, Username: u}).Error; err != nil {
				return err
			}
		}
		// Ensure trip access for this group (do not wipe other groups' access)
		var ta models.TripAccess
		q := tx.Where("trip_id = ? AND group_id = ?", p.TripID, groupID).First(&ta)
		if q.Error == gorm.ErrRecordNotFound {
			if err := tx.Create(&models.TripAccess{TripID: p.TripID, GroupID: groupID}).Error; err != nil {
				return err
			}
		} else if q.Error != nil {
			return q.Error
		}

		// Final bump
		if err := tx.Model(&models.Trip{}).Where("id = ?", p.TripID).Update("updated_at", now).Error; err != nil {
			return err
		}
		var trip models.Trip
		if err := tx.First(&trip, "id = ?", p.TripID).Error; err != nil {
			return err
		}
		result.DataVersion = trip.UpdatedAt.UnixMilli()
		return nil
	})

	return result, err
}

func dayNumOf(day map[string]any) (int, error) {
	switch n := day["day"].(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	default:
		return 0, fmt.Errorf("day.day missing or invalid")
	}
}

func mergeMembers(fromSeed, owners []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range append(append([]string{}, fromSeed...), owners...) {
		u = strings.ToLower(strings.TrimSpace(u))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}
