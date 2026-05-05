// Package handlers — asset management (trip map images, etc.)
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
)

var safeFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$`)

func assetsDir() string {
	dir := os.Getenv("ASSETS_DIR")
	if dir == "" {
		dir = "/data/assets"
	}
	return dir
}

// GetAsset serves a static file for a trip.
// GET /trips/{tripId}/assets/{filename}
func (h *Handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	filename := chi.URLParam(r, "filename")

	// Validate filename (no path traversal)
	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	// Check trip exists
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	fpath := filepath.Join(assetsDir(), tripID, filename)
	f, err := os.Open(fpath)
	if err != nil {
		writeError(w, http.StatusNotFound, "Asset not found")
		return
	}
	defer f.Close()

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

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// UploadAsset stores a file for a trip.
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

	dir := filepath.Join(assetsDir(), tripID)
	os.MkdirAll(dir, 0755)

	fpath := filepath.Join(dir, filename)
	out, err := os.Create(fpath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create file")
		return
	}
	defer out.Close()

	n, err := io.Copy(out, r.Body)
	if err != nil {
		os.Remove(fpath)
		writeError(w, http.StatusBadRequest, "Upload failed (max 5MB)")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"filename": filename,
		"trip_id":  tripID,
		"size":     n,
		"url":      fmt.Sprintf("/trips/%s/assets/%s", tripID, filename),
	})
}

// ListAssets returns all assets for a trip.
// GET /trips/{tripId}/assets
func (h *Handler) ListAssets(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")
	if !h.tripExists(tripID) {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	dir := filepath.Join(assetsDir(), tripID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	assets := []map[string]any{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		assets = append(assets, map[string]any{
			"filename": e.Name(),
			"size":     info.Size(),
			"url":      fmt.Sprintf("/trips/%s/assets/%s", tripID, e.Name()),
		})
	}

	writeJSON(w, http.StatusOK, assets)
}
