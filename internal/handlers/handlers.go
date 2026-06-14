// Package handlers provides HTTP handlers for the TripKit API.
package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _LOGGER = log.Default()

// Handler holds a reference to the database.
type Handler struct {
	db *gorm.DB
}

// New creates a new Handler with the given DB.
func New(db *gorm.DB) *Handler {
	return &Handler{db: db}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func parseJSONRaw(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(*s), &v); err != nil {
		return nil
	}
	return v
}

func tripResponse(t models.Trip, daysCount *int64) map[string]any {
	resp := map[string]any{
		"id":         t.ID,
		"name":       t.Name,
		"emoji":      t.Emoji,
		"start_date": t.StartDate,
		"end_date":   t.EndDate,
		"data":       parseJSONRaw(t.Data),
		"created_at": t.CreatedAt.Format(time.RFC3339),
		"updated_at": t.UpdatedAt.Format(time.RFC3339),
	}
	if daysCount != nil {
		resp["daysCount"] = *daysCount
	}
	return resp
}

func dayResponse(d models.Day) map[string]any {
	return map[string]any{
		"id":      d.ID,
		"trip_id": d.TripID,
		"day_num": d.DayNum,
		"data":    parseJSONRaw(&d.Data),
	}
}

func hotelResponse(h models.Hotel) map[string]any {
	return map[string]any{
		"id":      h.ID,
		"trip_id": h.TripID,
		"day_num": h.DayNum,
		"data":    parseJSONRaw(&h.Data),
	}
}

func listResponse(l models.List) map[string]any {
	return map[string]any{
		"id":         l.ID,
		"trip_id":    l.TripID,
		"type":       l.Type,
		"title":      l.Title,
		"data":       parseJSONRaw(&l.Data),
		"owner_user": l.OwnerUser,
		"created_at": l.CreatedAt.Format(time.RFC3339),
		"updated_at": l.UpdatedAt.Format(time.RFC3339),
	}
}

// ── Health ───────────────────────────────────────────────────────────────────

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
}

// ── Trips ────────────────────────────────────────────────────────────────────

func (h *Handler) ListTrips(w http.ResponseWriter, r *http.Request) {
	// Prefer JWT auth user, fallback to Authelia Remote-User
	user := middleware.GetAuthUser(r)
	if user == "anonymous" {
		user = middleware.GetUser(r)
	}
	allowedIDs := middleware.AllowedTripIDs(h.db, user)

	log.Printf("[ListTrips] user=%q allowedIDs=%v", user, allowedIDs)

	// Use raw table scan with explicit columns to avoid any GORM/Postgres driver
	// issues with json columns or model associations.
	type tripRow struct {
		ID        string  `gorm:"column:id"`
		Name      string  `gorm:"column:name"`
		Emoji     *string `gorm:"column:emoji"`
		StartDate *string `gorm:"column:start_date"`
		EndDate   *string `gorm:"column:end_date"`
		CreatedAt time.Time `gorm:"column:created_at"`
	}

	var rows []tripRow
	query := h.db.Table("trips").Select("id, name, emoji, start_date, end_date, created_at").Order("created_at DESC")
	if allowedIDs != nil {
		query = query.Where("id IN ?", allowedIDs)
	}
	if err := query.Find(&rows).Error; err != nil {
		log.Printf("[ListTrips] ERROR: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to list trips")
		return
	}

	log.Printf("[ListTrips] found %d trips", len(rows))

	result := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var count int64
		h.db.Model(&models.Day{}).Where("trip_id = ?", r.ID).Count(&count)
		result = append(result, map[string]any{
			"id":         r.ID,
			"name":       r.Name,
			"emoji":      r.Emoji,
			"start_date": r.StartDate,
			"end_date":   r.EndDate,
			"daysCount":  count,
			"created_at": r.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateTrip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Emoji     *string `json:"emoji"`
		StartDate *string `json:"start_date"`
		EndDate   *string `json:"end_date"`
		Data      any     `json:"data"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	tripID := body.ID
	if tripID == "" {
		tripID = uuid.NewString()
	}

	var dataStr *string
	if body.Data != nil {
		b, _ := json.Marshal(body.Data)
		s := string(b)
		dataStr = &s
	}

	trip := models.Trip{
		ID:        tripID,
		Name:      body.Name,
		Emoji:     body.Emoji,
		StartDate: body.StartDate,
		EndDate:   body.EndDate,
		Data:      dataStr,
	}
	if err := h.db.Create(&trip).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create trip")
		return
	}

	writeJSON(w, http.StatusCreated, tripResponse(trip, nil))
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	var count int64
	h.db.Model(&models.Day{}).Where("trip_id = ?", tripID).Count(&count)
	writeJSON(w, http.StatusOK, tripResponse(trip, &count))
}

func (h *Handler) UpdateTrip(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	var body struct {
		Name      *string `json:"name"`
		Emoji     *string `json:"emoji"`
		StartDate *string `json:"start_date"`
		EndDate   *string `json:"end_date"`
		Data      any     `json:"data"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updates := map[string]any{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Emoji != nil {
		updates["emoji"] = *body.Emoji
	}
	if body.StartDate != nil {
		updates["start_date"] = *body.StartDate
	}
	if body.EndDate != nil {
		updates["end_date"] = *body.EndDate
	}
	if body.Data != nil {
		b, _ := json.Marshal(body.Data)
		updates["data"] = string(b)
	}

	if len(updates) > 0 {
		h.db.Model(&trip).Updates(updates)
		h.db.First(&trip, "id = ?", tripID)
	}

	writeJSON(w, http.StatusOK, tripResponse(trip, nil))
}

func (h *Handler) DeleteTrip(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	// Cascade delete in a transaction for atomicity
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var lists []models.List
		tx.Where("trip_id = ?", tripID).Find(&lists)
		for _, l := range lists {
			if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListCheck{}).Error; err != nil {
				return err
			}
			if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListCustomItem{}).Error; err != nil {
				return err
			}
			if err := tx.Where("list_id = ?", l.ID).Delete(&models.ListHidden{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("trip_id = ?", tripID).Delete(&models.List{}).Error; err != nil {
			return err
		}
		if err := tx.Where("trip_id = ?", tripID).Delete(&models.Hotel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("trip_id = ?", tripID).Delete(&models.Day{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&trip).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		_LOGGER.Printf("DeleteTrip transaction failed for %s: %v", tripID, err)
		writeError(w, http.StatusInternalServerError, "Failed to delete trip")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Version check (lightweight) ─────────────────────────────────────────────

func (h *Handler) TripVersion(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	// Compute version = max updated_at across trip + all days
	// Trip.UpdatedAt is already set by GORM on every update.
	// For days, we pick the max of the trip's last day upsert.
	latestAt := trip.UpdatedAt

	var maxDayRow struct {
		MaxID uint
	}
	// Day has no UpdatedAt, so we use max(id) as proxy (auto-increment = latest upsert)
	// Better: we use the trip's own UpdatedAt which we bump on seed-import

	// Also check max hotel update
	var hotelCount int64
	h.db.Model(&models.Hotel{}).Where("trip_id = ?", tripID).Count(&hotelCount)
	_ = maxDayRow

	// Version = unix timestamp of last update (stable, monotonic)
	version := latestAt.UnixMilli()

	// Set cache headers — short TTL so app checks often but CDN/proxy can cache
	if os.Getenv("TRIPKIT_NO_CACHE") != "" {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=30")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":    version,
		"updated_at": latestAt.Format(time.RFC3339),
	})
}

func (h *Handler) SeedTrip(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	var days []models.Day
	h.db.Where("trip_id = ?", tripID).Order("day_num").Find(&days)
	var hotels []models.Hotel
	h.db.Where("trip_id = ?", tripID).Order("day_num").Find(&hotels)
	var dbLists []models.List
	h.db.Where("trip_id = ?", tripID).Find(&dbLists)

	daysResp := make([]map[string]any, len(days))
	for i, d := range days {
		daysResp[i] = dayResponse(d)
	}
	hotelsResp := make([]map[string]any, len(hotels))
	for i, ho := range hotels {
		hotelsResp[i] = hotelResponse(ho)
	}
	listsResp := make([]map[string]any, len(dbLists))
	for i, l := range dbLists {
		listsResp[i] = listResponse(l)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"trip":   tripResponse(trip, nil),
		"days":   daysResp,
		"hotels": hotelsResp,
		"lists":  listsResp,
	})
}

// ── Days ─────────────────────────────────────────────────────────────────────

func (h *Handler) ListDays(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	var days []models.Day
	h.db.Where("trip_id = ?", tripID).Order("day_num").Find(&days)

	result := make([]map[string]any, len(days))
	for i, d := range days {
		result[i] = dayResponse(d)
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetDay(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid day number")
		return
	}

	var day models.Day
	if err := h.db.Where("trip_id = ? AND day_num = ?", tripID, dayNum).First(&day).Error; err != nil {
		writeError(w, http.StatusNotFound, "Day not found")
		return
	}
	writeJSON(w, http.StatusOK, dayResponse(day))
}

func (h *Handler) UpsertDay(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid day number")
		return
	}

	var body any
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	dataBytes, _ := json.Marshal(body)

	var day models.Day
	result := h.db.Where("trip_id = ? AND day_num = ?", tripID, dayNum).First(&day)
	if result.Error != nil {
		day = models.Day{TripID: tripID, DayNum: dayNum, Data: string(dataBytes)}
		if err := h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "trip_id"}, {Name: "day_num"}},
			DoUpdates: clause.AssignmentColumns([]string{"data"}),
		}).Create(&day).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save day")
			return
		}
	} else {
		if err := h.db.Model(&day).Update("data", string(dataBytes)).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update day")
			return
		}
	}

	writeJSON(w, http.StatusOK, dayResponse(day))
}

func (h *Handler) DeleteDay(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid day number")
		return
	}
	result := h.db.Where("trip_id = ? AND day_num = ?", tripID, dayNum).Delete(&models.Day{})
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "Day not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Hotels ───────────────────────────────────────────────────────────────────

func (h *Handler) ListHotels(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	var hotels []models.Hotel
	h.db.Where("trip_id = ?", tripID).Order("day_num").Find(&hotels)

	result := make([]map[string]any, len(hotels))
	for i, ho := range hotels {
		result[i] = hotelResponse(ho)
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) UpsertHotel(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	dayNum, err := strconv.Atoi(chi.URLParam(r, "dayNum"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid day number")
		return
	}

	var body any
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	dataBytes, _ := json.Marshal(body)

	var hotel models.Hotel
	result := h.db.Where("trip_id = ? AND day_num = ?", tripID, dayNum).First(&hotel)
	if result.Error != nil {
		hotel = models.Hotel{TripID: tripID, DayNum: dayNum, Data: string(dataBytes)}
		if err := h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "trip_id"}, {Name: "day_num"}},
			DoUpdates: clause.AssignmentColumns([]string{"data"}),
		}).Create(&hotel).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save hotel")
			return
		}
	} else {
		if err := h.db.Model(&hotel).Update("data", string(dataBytes)).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update hotel")
			return
		}
	}

	writeJSON(w, http.StatusOK, hotelResponse(hotel))
}

// ── Lists ────────────────────────────────────────────────────────────────────

func (h *Handler) ListLists(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	var dbLists []models.List
	owner := r.URL.Query().Get("owner")
	if owner != "" {
		// Return lists owned by this user OR shared lists (owner_user = "")
		h.db.Where("trip_id = ? AND (owner_user = ? OR owner_user = '')", tripID, owner).Order("created_at").Find(&dbLists)
	} else {
		h.db.Where("trip_id = ?", tripID).Order("created_at").Find(&dbLists)
	}

	result := make([]map[string]any, len(dbLists))
	for i, l := range dbLists {
		result[i] = listResponse(l)
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetList(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	listID := chi.URLParam(r, "listId")

	var list models.List
	if err := h.db.Where("id = ? AND trip_id = ?", listID, tripID).First(&list).Error; err != nil {
		writeError(w, http.StatusNotFound, "List not found")
		return
	}

	state := h.getListState(listID)
	resp := listResponse(list)
	resp["state"] = state

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpsertList(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}
	listID := chi.URLParam(r, "listId")

	var body struct {
		Type      string `json:"type"`
		Title     string `json:"title"`
		Data      any    `json:"data"`
		OwnerUser string `json:"owner_user"`
	}
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	dataBytes, _ := json.Marshal(body.Data)
	if body.Data == nil {
		dataBytes = []byte("{}")
	}

	var list models.List
	result := h.db.Where("id = ?", listID).First(&list)
	if result.Error != nil {
		list = models.List{
			ID:        listID,
			TripID:    tripID,
			Type:      body.Type,
			Title:     body.Title,
			Data:      string(dataBytes),
			OwnerUser: body.OwnerUser,
		}
		if err := h.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"type", "title", "data", "owner_user"}),
		}).Create(&list).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save list")
			return
		}
	} else {
		updates := map[string]any{
			"type":       body.Type,
			"title":      body.Title,
			"data":       string(dataBytes),
			"owner_user": body.OwnerUser,
		}
		if err := h.db.Model(&list).Updates(updates).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update list")
			return
		}
		h.db.First(&list, "id = ?", listID)
	}

	writeJSON(w, http.StatusOK, listResponse(list))
}

func (h *Handler) DeleteList(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	listID := chi.URLParam(r, "listId")

	var list models.List
	if err := h.db.Where("id = ? AND trip_id = ?", listID, tripID).First(&list).Error; err != nil {
		writeError(w, http.StatusNotFound, "List not found")
		return
	}

	// Personal list: only the owner can delete
	if list.OwnerUser != "" {
		currentUser := middleware.GetUser(r)
		if list.OwnerUser != currentUser {
			writeError(w, http.StatusForbidden, "Cannot delete another user's personal list")
			return
		}
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ?", listID).Delete(&models.ListCheck{}).Error; err != nil {
			return err
		}
		if err := tx.Where("list_id = ?", listID).Delete(&models.ListCustomItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("list_id = ?", listID).Delete(&models.ListHidden{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&list).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_LOGGER.Printf("DeleteList transaction failed for %s: %v", listID, err)
		writeError(w, http.StatusInternalServerError, "Failed to delete list")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Sync (the critical endpoint) ─────────────────────────────────────────────

type syncRequest struct {
	DeviceID   string                    `json:"deviceId"`
	LastSyncAt int64                     `json:"lastSyncAt"`
	Checks     map[string]syncCheckItem  `json:"checks"`
	Custom     map[string]syncCustomItem `json:"custom"`
	Hidden     []string                  `json:"hidden"`
}

type syncCheckItem struct {
	Checked   bool  `json:"checked"`
	UpdatedAt int64 `json:"updatedAt"`
}

type syncCustomItem struct {
	Text      string `json:"text"`
	Section   int    `json:"section"`
	CreatedAt int64  `json:"createdAt"`
}

func (h *Handler) SyncList(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	listID := chi.URLParam(r, "listId")

	var list models.List
	if err := h.db.Where("id = ? AND trip_id = ?", listID, tripID).First(&list).Error; err != nil {
		writeError(w, http.StatusNotFound, "List not found")
		return
	}

	// Personal list: only the owner can sync
	if list.OwnerUser != "" {
		currentUser := middleware.GetUser(r)
		if list.OwnerUser != currentUser {
			writeError(w, http.StatusForbidden, "Cannot sync another user's personal list")
			return
		}
	}

	var body syncRequest
	if err := parseBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "deviceId is required")
		return
	}

	conflicts := 0

	// All sync writes in a single transaction for atomicity
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// Step 1: Merge checks (last-write-wins by updatedAt)
		for itemID, incoming := range body.Checks {
			check := models.ListCheck{
				ListID:    listID,
				ItemID:    itemID,
				Checked:   incoming.Checked,
				UpdatedAt: incoming.UpdatedAt,
			}
			// Try to find existing
			var existing models.ListCheck
			findErr := tx.Where("list_id = ? AND item_id = ?", listID, itemID).First(&existing).Error
			if findErr != nil {
				// New item — insert
				if err := tx.Create(&check).Error; err != nil {
					return err
				}
			} else if incoming.UpdatedAt > existing.UpdatedAt {
				conflicts++
				if err := tx.Model(&existing).Updates(map[string]any{
					"checked":    incoming.Checked,
					"updated_at": incoming.UpdatedAt,
				}).Error; err != nil {
					return err
				}
			} else if incoming.UpdatedAt == existing.UpdatedAt && incoming.Checked != existing.Checked {
				if err := tx.Model(&existing).Update("checked", incoming.Checked).Error; err != nil {
					return err
				}
			}
		}

		// Step 2: Merge custom items (union — never delete)
		for itemID, incoming := range body.Custom {
			var existing models.ListCustomItem
			if tx.Where("id = ? AND list_id = ?", itemID, listID).First(&existing).Error != nil {
				createdAt := incoming.CreatedAt
				if createdAt == 0 {
					createdAt = time.Now().UnixMilli()
				}
				if err := tx.Create(&models.ListCustomItem{
					ID:           itemID,
					ListID:       listID,
					Text:         incoming.Text,
					SectionIndex: incoming.Section,
					CreatedAt:    createdAt,
				}).Error; err != nil {
					return err
				}
			}
		}

		// Step 3: Hidden items — replace per device (atomic delete+re-insert)
		if err := tx.Where("list_id = ? AND device_id = ?", listID, body.DeviceID).Delete(&models.ListHidden{}).Error; err != nil {
			return err
		}
		for _, itemID := range body.Hidden {
			if err := tx.Create(&models.ListHidden{
				ListID:   listID,
				DeviceID: body.DeviceID,
				ItemID:   itemID,
			}).Error; err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		_LOGGER.Printf("SyncList transaction failed for list %s: %v", listID, err)
		writeError(w, http.StatusInternalServerError, "Sync failed")
		return
	}

	// Step 4: Return merged state (read outside tx — consistent after commit)
	mergedState := h.getListState(listID)
	deviceHidden := h.getDeviceHidden(listID, body.DeviceID)
	serverSyncAt := time.Now().UnixMilli()

	writeJSON(w, http.StatusOK, map[string]any{
		"merged":       mergedState,
		"hidden":       deviceHidden,
		"conflicts":    conflicts,
		"serverSyncAt": serverSyncAt,
	})
}

// ── Internal helpers ─────────────────────────────────────────────────────────

func (h *Handler) tripExists(tripID string) bool {
	var count int64
	h.db.Model(&models.Trip{}).Where("id = ?", tripID).Count(&count)
	return count > 0
}

func (h *Handler) getListState(listID string) map[string]any {
	var checks []models.ListCheck
	h.db.Where("list_id = ?", listID).Find(&checks)
	var customItems []models.ListCustomItem
	h.db.Where("list_id = ?", listID).Find(&customItems)

	checksMap := map[string]any{}
	for _, c := range checks {
		checksMap[c.ItemID] = map[string]any{
			"checked":   c.Checked,
			"updatedAt": c.UpdatedAt,
		}
	}

	customMap := map[string]any{}
	for _, ci := range customItems {
		customMap[ci.ID] = map[string]any{
			"text":      ci.Text,
			"section":   ci.SectionIndex,
			"createdAt": ci.CreatedAt,
		}
	}

	return map[string]any{
		"checks": checksMap,
		"custom": customMap,
	}
}

func (h *Handler) getDeviceHidden(listID, deviceID string) []string {
	var rows []models.ListHidden
	h.db.Where("list_id = ? AND device_id = ?", listID, deviceID).Find(&rows)
	result := make([]string, len(rows))
	for i, r := range rows {
		result[i] = r.ItemID
	}
	return result
}

// Me returns the current authenticated user identity (from Authelia Remote-User header).
// GET /api/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	writeJSON(w, http.StatusOK, map[string]string{
		"user": user,
	})
}
