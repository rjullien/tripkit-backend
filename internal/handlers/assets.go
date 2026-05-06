// Package handlers — asset management (trip map images, etc.)
// Assets stored in database (BLOB) for persistence across pod restarts.
package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/models"
)

var safeFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$`)

// GetAsset serves a file from the database.
// GET /trips/{tripId}/assets/{filename}
func (h *Handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	filename := chi.URLParam(r, "filename")

	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	var asset models.Asset
	if err := h.db.Where("trip_id = ? AND filename = ?", tripID, filename).First(&asset).Error; err != nil {
		writeError(w, http.StatusNotFound, "Asset not found")
		return
	}

	w.Header().Set("Content-Type", asset.ContentType)
	if os.Getenv("TRIPKIT_NO_CACHE") != "" || r.URL.Query().Get("nocache") == "1" {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(asset.Data)))
	w.WriteHeader(http.StatusOK)
	w.Write(asset.Data)
}

// UploadAsset stores a file in the database.
// PUT /trips/{tripId}/assets/{filename}
func (h *Handler) UploadAsset(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	filename := chi.URLParam(r, "filename")

	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	// Limit to 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Upload failed (max 5MB)")
		return
	}

	// Detect content type
	ext := strings.ToLower(filepath.Ext(filename))
	ct := "application/octet-stream"
	switch ext {
	case ".png":
		ct = "image/png"
	case ".jpg", ".jpeg":
		ct = "image/jpeg"
	case ".webp":
		ct = "image/webp"
	case ".gif":
		ct = "image/gif"
	case ".svg":
		ct = "image/svg+xml"
	}

	// Upsert asset
	var existing models.Asset
	if err := h.db.Where("trip_id = ? AND filename = ?", tripID, filename).First(&existing).Error; err == nil {
		// Update existing
		existing.Data = data
		existing.ContentType = ct
		existing.Size = int64(len(data))
		h.db.Save(&existing)
	} else {
		// Create new
		asset := models.Asset{
			TripID:      tripID,
			Filename:    filename,
			ContentType: ct,
			Size:        int64(len(data)),
			Data:        data,
		}
		h.db.Create(&asset)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"filename": filename,
		"trip_id":  tripID,
		"size":     len(data),
		"url":      fmt.Sprintf("/trips/%s/assets/%s", tripID, filename),
	})
}

// ListAssets returns all assets for a trip (without data blob).
// GET /trips/{tripId}/assets
func (h *Handler) ListAssets(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	var assets []models.Asset
	h.db.Select("trip_id, filename, content_type, size").Where("trip_id = ?", tripID).Find(&assets)

	result := make([]map[string]any, 0, len(assets))
	for _, a := range assets {
		result = append(result, map[string]any{
			"filename": a.Filename,
			"size":     a.Size,
			"url":      fmt.Sprintf("/trips/%s/assets/%s", tripID, a.Filename),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// DeleteAsset removes a single asset from the database.
// DELETE /trips/{tripId}/assets/{filename}
func (h *Handler) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	filename := chi.URLParam(r, "filename")

	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	result := h.db.Where("trip_id = ? AND filename = ?", tripID, filename).Delete(&models.Asset{})
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "Asset not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":  filename,
		"trip_id":  tripID,
	})
}

