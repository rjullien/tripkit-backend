// Package main — TripKit API server
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

func main() {
	port := 3001
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	db, err := database.Connect()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	h := handlers.New(db)

	r := chi.NewRouter()

	// Middleware
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)
	r.Use(chiMiddleware.RealIP)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health (always at root, no auth)
	r.Get("/health", h.Health)

	// Auth routes (no auth required for login, admin-protected for invite/list)
	r.Post("/auth/invite", h.CreateInvite)
	r.Post("/auth/login", h.LoginMagicLink)
	r.Get("/auth/invites", h.ListInvites)

	// BASE_PATH — configurable route prefix (default: empty = routes at root)
	// Examples: BASE_PATH="" → GET /trips, BASE_PATH="/api" → GET /api/trips
	basePath := os.Getenv("BASE_PATH")
	// Clean: ensure leading slash if non-empty, strip trailing slash
	if basePath != "" {
		if !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		basePath = strings.TrimRight(basePath, "/")
	}

	apiRoute := basePath
	if apiRoute == "" {
		apiRoute = "/" // mount at root
	}

	// API routes — auth via JWT (magic link) or static token or Authelia forwardAuth
	r.Route(apiRoute, func(r chi.Router) {
		// JWT + static token auth middleware
		r.Use(middleware.Auth)
		// Legacy: also extract Remote-User from Authelia forwardAuth header
		r.Use(middleware.UserIdentity)

		// Trips
		r.Get("/trips", h.ListTrips)
		r.Post("/trips", h.CreateTrip)
		r.Get("/trips/{tripId}", h.GetTrip)
		r.Put("/trips/{tripId}", h.UpdateTrip)
		r.Delete("/trips/{tripId}", h.DeleteTrip)
		r.Get("/trips/{tripId}/seed", h.SeedTrip)
		r.Get("/trips/{tripId}/version", h.TripVersion)

		// Days
		r.Get("/trips/{tripId}/days", h.ListDays)
		r.Get("/trips/{tripId}/days/{dayNum}", h.GetDay)
		r.Put("/trips/{tripId}/days/{dayNum}", h.UpsertDay)

		// Hotels
		r.Get("/trips/{tripId}/hotels", h.ListHotels)
		r.Put("/trips/{tripId}/hotels/{dayNum}", h.UpsertHotel)

		// Lists
		r.Get("/trips/{tripId}/lists", h.ListLists)
		r.Get("/trips/{tripId}/lists/{listId}", h.GetList)
		r.Put("/trips/{tripId}/lists/{listId}", h.UpsertList)
		r.Delete("/trips/{tripId}/lists/{listId}", h.DeleteList)
		r.Patch("/trips/{tripId}/lists/{listId}/sync", h.SyncList)

		// Weather
		r.Get("/trips/{tripId}/weather", h.GetWeather)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("TripKit backend (Go) listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
