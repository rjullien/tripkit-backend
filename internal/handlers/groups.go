package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/models"
)

// ListGroups returns all groups with their members and trip access.
// GET /api/groups
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	var groups []models.Group
	h.db.Find(&groups)

	type groupResponse struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Members []string `json:"members"`
		Trips   []string `json:"trips"`
	}

	result := make([]groupResponse, 0, len(groups))
	for _, g := range groups {
		var members []models.GroupMember
		h.db.Where("group_id = ?", g.ID).Find(&members)
		var access []models.TripAccess
		h.db.Where("group_id = ?", g.ID).Find(&access)

		memberNames := make([]string, 0, len(members))
		for _, m := range members {
			memberNames = append(memberNames, m.Username)
		}
		tripIDs := make([]string, 0, len(access))
		for _, a := range access {
			tripIDs = append(tripIDs, a.TripID)
		}

		result = append(result, groupResponse{
			ID:      g.ID,
			Name:    g.Name,
			Members: memberNames,
			Trips:   tripIDs,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// UpsertGroup creates or updates a group with members and trip access.
// PUT /api/groups/{groupId}
func (h *Handler) UpsertGroup(w http.ResponseWriter, r *http.Request) {
	type request struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
		Trips   []string `json:"trips"`
	}

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	groupID := extractPathParam(r, "groupId")
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "Missing group ID")
		return
	}

	// Upsert group
	group := models.Group{ID: groupID, Name: req.Name}
	h.db.Save(&group)

	// Replace members
	h.db.Where("group_id = ?", groupID).Delete(&models.GroupMember{})
	for _, username := range req.Members {
		h.db.Create(&models.GroupMember{GroupID: groupID, Username: username})
	}

	// Replace trip access
	h.db.Where("group_id = ?", groupID).Delete(&models.TripAccess{})
	for _, tripID := range req.Trips {
		h.db.Create(&models.TripAccess{TripID: tripID, GroupID: groupID})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "group_id": groupID})
}

// DeleteGroup removes a group and its associations.
// DELETE /api/groups/{groupId}
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID := extractPathParam(r, "groupId")
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "Missing group ID")
		return
	}

	h.db.Where("group_id = ?", groupID).Delete(&models.GroupMember{})
	h.db.Where("group_id = ?", groupID).Delete(&models.TripAccess{})
	h.db.Where("id = ?", groupID).Delete(&models.Group{})

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// MyTrips returns trips the current user has access to.
// GET /api/my/trips
func (h *Handler) MyTrips(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	allowedIDs := middleware.AllowedTripIDs(h.db, user)

	var trips []models.Trip
	if allowedIDs == nil {
		// Open mode — return all
		h.db.Find(&trips)
	} else {
		h.db.Where("id IN ?", allowedIDs).Find(&trips)
	}

	writeJSON(w, http.StatusOK, trips)
}

// extractPathParam gets a path segment after a known prefix.
func extractPathParam(r *http.Request, param string) string {
	// Use chi URLParam
	return chi.URLParam(r, param)
}
