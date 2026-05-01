package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

func setupAuthTest(t *testing.T) (*Handler, chi.Router) {
	t.Helper()
	os.Setenv("TRIPKIT_API_TOKEN", "admin-secret")
	os.Setenv("TRIPKIT_JWT_SECRET", "test-jwt-secret")
	t.Cleanup(func() {
		os.Unsetenv("TRIPKIT_API_TOKEN")
		os.Unsetenv("TRIPKIT_JWT_SECRET")
	})

	db, err := database.Init(":memory:")
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	h := New(db)

	r := chi.NewRouter()
	// Auth routes (no middleware)
	r.Post("/auth/invite", h.CreateInvite)
	r.Post("/auth/login", h.LoginMagicLink)
	r.Get("/auth/invites", h.ListInvites)
	// Protected routes
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Auth)
		r.Get("/trips", h.ListTrips)
	})

	return h, r
}

func TestCreateInvite_AdminOnly(t *testing.T) {
	_, r := setupAuthTest(t)

	// Without admin token → 403
	body := `{"name":"Alex","trip_id":"usa-2026","role":"viewer"}`
	req := httptest.NewRequest("POST", "/auth/invite", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected 403, got %d", w.Code)
	}

	// With admin token → 201
	req = httptest.NewRequest("POST", "/auth/invite", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer admin-secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Fatal("Expected non-empty token")
	}
	if resp["name"] != "Alex" {
		t.Fatalf("Expected name=Alex, got %v", resp["name"])
	}
}

func TestMagicLinkFlow(t *testing.T) {
	_, r := setupAuthTest(t)

	// 1. Create invite
	body := `{"name":"Dinah","trip_id":"usa-2026","role":"viewer"}`
	req := httptest.NewRequest("POST", "/auth/invite", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Create invite failed: %d %s", w.Code, w.Body.String())
	}

	var inviteResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &inviteResp)
	token := inviteResp["token"].(string)

	// 2. Login with token → get JWT
	loginBody, _ := json.Marshal(map[string]string{"token": token})
	req = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Login failed: %d %s", w.Code, w.Body.String())
	}

	var loginResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwtStr, ok := loginResp["jwt"].(string)
	if !ok || jwtStr == "" {
		t.Fatal("Expected JWT in response")
	}
	if loginResp["name"] != "Dinah" {
		t.Fatalf("Expected name=Dinah, got %v", loginResp["name"])
	}

	// 3. Use JWT to access protected endpoint
	req = httptest.NewRequest("GET", "/api/trips", nil)
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Protected endpoint with JWT failed: %d %s", w.Code, w.Body.String())
	}

	// 4. Token already used → 410
	req = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("Expected 410 for reused token, got %d", w.Code)
	}
}

func TestProtectedEndpoint_NoToken(t *testing.T) {
	_, r := setupAuthTest(t)

	// No auth header → 401
	req := httptest.NewRequest("GET", "/api/trips", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", w.Code)
	}
}

func TestProtectedEndpoint_StaticToken(t *testing.T) {
	_, r := setupAuthTest(t)

	// Admin static token → 200
	req := httptest.NewRequest("GET", "/api/trips", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 with static token, got %d", w.Code)
	}
}
