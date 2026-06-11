// Package handlers — auth handlers for magic link flow.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rjullien/tripkit-backend/internal/config"
	"github.com/rjullien/tripkit-backend/internal/models"
)

// jwtSecret returns the signing key from shared config.
func jwtSecret() []byte {
	return config.JWTSecret()
}

// CreateInvite generates a magic link token (admin only).
// POST /auth/invite  { "name": "Alex", "trip_id": "usa-2026", "role": "viewer", "expires_hours": 168 }
func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	// Only admin can create invites (checked via existing TRIPKIT_API_TOKEN)
	adminToken := os.Getenv("TRIPKIT_API_TOKEN")
	if adminToken == "" {
		writeError(w, http.StatusForbidden, "Admin endpoint disabled: TRIPKIT_API_TOKEN not configured")
		return
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+adminToken {
		writeError(w, http.StatusForbidden, "Admin token required to create invites")
		return
	}

	var req struct {
		Name         string `json:"name"`
		TripID       string `json:"trip_id"`
		Role         string `json:"role"`
		ExpiresHours int    `json:"expires_hours"`
	}
	if err := parseBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if req.Name == "" || req.TripID == "" {
		writeError(w, http.StatusBadRequest, "name and trip_id are required")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if req.ExpiresHours <= 0 {
		req.ExpiresHours = 168 // 7 days default
	}

	// Generate secure random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	mt := models.MagicToken{
		Token:     token,
		Name:      req.Name,
		Role:      req.Role,
		TripID:    req.TripID,
		ExpiresAt: time.Now().Add(time.Duration(req.ExpiresHours) * time.Hour),
	}

	if err := h.db.Create(&mt).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create invite")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"name":       req.Name,
		"role":       req.Role,
		"trip_id":    req.TripID,
		"expires_at": mt.ExpiresAt,
	})
}

// LoginMagicLink exchanges a magic token for a JWT.
// POST /auth/login  { "token": "abc123..." }
func (h *Handler) LoginMagicLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := parseBody(r, &req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	var mt models.MagicToken
	if err := h.db.First(&mt, "token = ?", req.Token).Error; err != nil {
		writeError(w, http.StatusNotFound, "Invalid or expired token")
		return
	}

	if time.Now().After(mt.ExpiresAt) {
		writeError(w, http.StatusGone, "Token already used or expired")
		return
	}

	// Atomically mark the token as used. The `used_at IS NULL` guard makes the
	// token genuinely single-use even under concurrent requests: only one
	// UPDATE can win the race.
	now := time.Now()
	res := h.db.Model(&models.MagicToken{}).
		Where("token = ? AND used_at IS NULL", req.Token).
		Updates(map[string]any{"used_by": mt.Name, "used_at": now})
	if res.Error != nil {
		writeError(w, http.StatusInternalServerError, "Failed to consume token")
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, http.StatusGone, "Token already used or expired")
		return
	}

	// Generate JWT (30 days)
	claims := jwt.MapClaims{
		"sub":     mt.Name,
		"role":    mt.Role,
		"trip_id": mt.TripID,
		"iat":     now.Unix(),
		"exp":     now.Add(30 * 24 * time.Hour).Unix(),
	}
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := jwtToken.SignedString(jwtSecret())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate JWT")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jwt":     signed,
		"name":    mt.Name,
		"role":    mt.Role,
		"trip_id": mt.TripID,
	})
}

// ListInvites lists all magic tokens (admin only).
// GET /auth/invites
func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	adminToken := os.Getenv("TRIPKIT_API_TOKEN")
	if adminToken == "" {
		writeError(w, http.StatusForbidden, "Admin endpoint disabled: TRIPKIT_API_TOKEN not configured")
		return
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+adminToken {
		writeError(w, http.StatusForbidden, "Admin token required")
		return
	}

	var tokens []models.MagicToken
	h.db.Order("created_at DESC").Find(&tokens)
	writeJSON(w, http.StatusOK, tokens)
}
