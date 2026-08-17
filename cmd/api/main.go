// Package main — TripKit API server
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rjullien/tripkit-backend/internal/bifrost"
	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/dailybrief"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/discovery"
	"github.com/rjullien/tripkit-backend/internal/formalities"
	"github.com/rjullien/tripkit-backend/internal/geocode"
	"github.com/rjullien/tripkit-backend/internal/handlers"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
	"github.com/rjullien/tripkit-backend/internal/nuisance"
	"github.com/rjullien/tripkit-backend/internal/pluschat"
	"github.com/rjullien/tripkit-backend/internal/polarsteps"
	"github.com/rjullien/tripkit-backend/internal/publish"
	"github.com/rjullien/tripkit-backend/internal/seedgit"
	"github.com/rjullien/tripkit-backend/internal/weather"
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

	reg, sourcesLoader, regOrigin, err := publish.LoadRegistry()
	if err != nil {
		log.Fatalf("Failed to load publish registry: %v", err)
	}
	log.Printf("publish registry: origin=%s sources=%d", regOrigin, reg.Len())
	h.SetPublishRegistry(reg)
	manifest := publish.NewManifestResolverFromEnv()
	h.SetPublishManifestResolver(manifest)
	publish.StartRegistryRefresh(reg, sourcesLoader)

	// Centralized weather service (routes US→NWS, CA→MSC, default→Open-Meteo).
	weatherSvc := weather.New()
	h.SetWeather(weatherSvc)
	log.Printf("weather: centralized service ready (providers: nws, msc, open-meteo)")

	briefLoader := dailybrief.NewLoaderFromEnv()
	briefCfg := briefLoader.Bootstrap()
	log.Printf("dailybrief config: origin=%s model=%s gowa=%s", briefCfg.Origin, briefCfg.BriefModel, briefCfg.GowaBaseURL)
	briefSvc := &dailybrief.Service{DB: db, Loader: briefLoader, Weather: &weather.DailyBriefAdapter{Svc: weatherSvc}}
	h.SetDailyBrief(briefSvc)
	(&dailybrief.Worker{DB: db, Service: briefSvc}).Start()

	plusLoader := pluschat.NewLoaderFromEnv()
	plusCfg := plusLoader.Bootstrap()
	log.Printf("pluschat config: origin=%s model=%s enabled=%v", plusCfg.Origin, plusCfg.ChatModel, plusCfg.Enabled)
	h.SetPlusChat(plusLoader)

	psLoader := polarsteps.NewLoaderFromEnv()
	psCfg := psLoader.Bootstrap()
	log.Printf("polarsteps config: origin=%s model=%s enabled=%v", psCfg.Origin, psCfg.CaptionModel, psCfg.Enabled)
	h.SetPolarsteps(&polarsteps.Service{DB: db, Loader: psLoader})

	discLoader := discovery.NewLoaderFromEnv()
	discCfg := discLoader.Bootstrap()
	log.Printf("discovery config: origin=%s themes=%d overpass=%s", discCfg.Origin, len(discCfg.Themes), discCfg.Overpass.BaseURL)

	constructionOps := construction.NewLoaderFromEnv()
	consCfg := constructionOps.Bootstrap()
	log.Printf("construction ops: origin=%s enabled=%t bifrost=%s models=[admin=%s health=%s nuisance=%s]",
		consCfg.Origin, consCfg.Enabled, consCfg.BifrostBaseURL,
		consCfg.ModelFor("adminCheck"), consCfg.ModelFor("healthCheck"), consCfg.ModelFor("nuisance"))
	h.SetConstructionOps(constructionOps)

	h.SetDiscovery(&discovery.Service{
		DB:        db,
		Loader:    discLoader,
		Editorial: discovery.NewLeoEditorialFromEnv(),
		CorridorSampleKm: func() float64 {
			return constructionOps.Get().QA.CorridorSampleKm
		},
	})

	leoOps := leo.NewOpsLoaderFromEnv()
	leoCfg := leoOps.Bootstrap()
	log.Printf("leo ops: origin=%s default=%s models=%d", leoCfg.Origin, leoCfg.DefaultModel, len(leoCfg.Models))
	h.SetLeoOps(leoOps)

	// One Bifrost completer per check, rebuilt from Loader.Get() so a commit
	// to ops/construction.json takes effect within the 2 min TTL (not only
	// at the next pod restart).
	newCompleter := func(feature string) bifrost.Completer {
		cfg := constructionOps.Get()
		if !cfg.Ready() {
			return nil
		}
		model := cfg.ModelFor(feature)
		if model == "" {
			return nil
		}
		return bifrost.NewClient(cfg.BifrostBaseURL, cfg.BifrostAPIKey, model).AsCompleter()
	}
	if !consCfg.Ready() {
		log.Printf("construction ops: LLM formatting disabled (enabled=%t bifrostBaseUrl=%q) — checks return deterministic results only",
			consCfg.Enabled, consCfg.BifrostBaseURL)
	}

	h.SetConstruction(&construction.Service{
		DB:  db,
		Ops: constructionOps,
		SeedGit: &seedgit.Service{
			Registry: reg,
			Manifest: manifest,
			Files:    seedgit.GitHubFiles{Client: publish.NewGitHubClientFromEnv()},
		},
	})

	discOverpass := discovery.NewClient(discCfg.Overpass)

	// A booked hotel is analysed at its OWN address, and a seed hotel carries an
	// address, never coordinates: the nuisance check needs a geocoder. Public
	// Nominatim by default (1 req/s, enforced client-side); override the
	// endpoint with TRIPKIT_NOMINATIM_URL and set TRIPKIT_NOMINATIM_EMAIL as the
	// contact its usage policy asks for.
	geocoder := geocode.NewClient(os.Getenv("TRIPKIT_NOMINATIM_URL"), os.Getenv("TRIPKIT_NOMINATIM_EMAIL"))
	log.Printf("nuisance: geocoder=%s (booked hotels analysed at their own address)", geocoder.BaseURL)

	h.SetNuisance(&nuisance.Service{
		DB:        db,
		Overpass:  discOverpass,
		Geocoder:  geocoder,
		BifrostFn: func() bifrost.Completer { return newCompleter("nuisance") },
		Hub:       h.LeoHub(),
	})

	h.SetFormalities(&formalities.Service{
		DB:                db,
		CompleterFn:       func() bifrost.Completer { return newCompleter("adminCheck") },
		HealthCompleterFn: func() bifrost.Completer { return newCompleter("healthCheck") },
		RulesOverride:     func() map[string]json.RawMessage { return constructionOps.Get().Formalities.Overrides },
	})

	// In-process worker: auto-on when TRIPKIT_GITHUB_TOKEN is set; override via TRIPKIT_PUBLISH_WORKER.
	if publish.WorkerEnabled() {
		w := &publish.Worker{DB: db, Reg: reg}
		w.Start()
	}

	r := chi.NewRouter()

	// Middleware
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)
	r.Use(chiMiddleware.RealIP)
	// CORS — use explicit origins in production
	allowedOrigins := []string{"*"}
	if origins := os.Getenv("TRIPKIT_CORS_ORIGINS"); origins != "" {
		allowedOrigins = strings.Split(origins, ",")
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
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
		// Trip-level ACL (group-based access control)
		r.Use(middleware.TripACL(db))

		// User info
		r.Get("/me", h.Me)
		r.Get("/my/trips", h.MyTrips)

		// Groups (admin only)
		r.Get("/groups", h.ListGroups)
		r.Put("/groups/{groupId}", h.UpsertGroup)
		r.Delete("/groups/{groupId}", h.DeleteGroup)

		// Publish (family seed → prod). Not under /trips/{id} (ACL chicken/egg).
		r.Get("/publish/sources", h.ListPublishSources)
		r.Post("/publish/jobs", h.CreatePublishJob)
		r.Get("/publish/jobs/{jobId}", h.GetPublishJob)

		// Léo / Hermes chat proxy (secrets stay on the server).
		r.Get("/leo/status", h.LeoStatus)
		// Deprecated for Plus UI — keep for curl/debug (same SystemPrompt as stream).
		r.Post("/leo/chat", h.LeoChat)
		r.Post("/leo/chat/stream", h.LeoChatStream) // Plus UI — live SSE + detached job
		r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
		r.Post("/leo/jobs/{jobId}/cancel", h.LeoJobCancel)

		// Plus Assistant — Bifrost direct (ops/plus-chat.json model).
		r.Get("/plus/chat/status", h.PlusChatStatus)
		r.Post("/plus/chat/stream", h.PlusChatStream)

		// Polarsteps journal draft (Plus box, text only — no GoWA).
		r.Get("/trips/{tripId}/polarsteps/status", h.PolarstepsStatus)
		r.Get("/trips/{tripId}/polarsteps/caption", h.PolarstepsCaption)
		r.Post("/trips/{tripId}/polarsteps/caption", h.GeneratePolarstepsCaption)

		r.Get("/discovery/themes", h.DiscoveryCatalog)
		r.Get("/trips/{tripId}/discovery/themes", h.DiscoveryThemes)
		r.Post("/trips/{tripId}/discovery/search", h.DiscoverySearch)
		r.Get("/trips/{tripId}/discovery/results", h.DiscoveryResults)

		// Construction
		r.Get("/trips/{tripId}/construction", h.GetConstruction)
		r.Put("/trips/{tripId}/construction/phase", h.TransitionPhase)
		r.Post("/trips/{tripId}/construction/qa", h.RunConstructionQA)
		r.Get("/trips/{tripId}/construction/qa", h.GetConstructionQA)
		r.Get("/trips/{tripId}/travel-profile", h.GetTravelProfile)
		r.Post("/trips/{tripId}/travel-profile/request", h.CreateProfileRequest)

		// Formalities (admin-check, health-check)
		r.Post("/trips/{tripId}/admin-check", h.RunAdminCheck)
		r.Get("/trips/{tripId}/admin-check", h.GetAdminCheck)
		r.Post("/trips/{tripId}/health-check", h.RunHealthCheck)
		r.Get("/trips/{tripId}/health-check", h.GetHealthCheck)

		// Discovery retain (add item to seed via Leo)
		r.Post("/trips/{tripId}/discovery/retain", h.RetainDiscoveryItem)

		// Nuisance analysis
		r.Post("/trips/{tripId}/nuisance-check", h.RunNuisanceCheck)
		r.Get("/trips/{tripId}/nuisance-check", h.GetNuisanceCheck)
		r.Get("/trips/{tripId}/nuisance-check/{locationId}", h.GetNuisanceCheck)
		r.Post("/trips/{tripId}/nuisance-check/pin", h.PinNuisanceToSeed)

		// Trips
		r.Get("/trips", h.ListTrips)
		r.Get("/debug/trips", h.DebugListTrips)
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
		r.Delete("/trips/{tripId}/days/{dayNum}", h.DeleteDay)
		r.Get("/trips/{tripId}/days/{dayNum}/brief", h.GetDayBrief)
		r.Post("/trips/{tripId}/days/{dayNum}/brief/send", h.SendDayBrief)

		// Hotels
		r.Get("/trips/{tripId}/hotels", h.ListHotels)
		r.Put("/trips/{tripId}/hotels/{dayNum}", h.UpsertHotel)
		r.Delete("/trips/{tripId}/hotels/{dayNum}", h.DeleteHotel)

		// Lists
		r.Get("/trips/{tripId}/lists", h.ListLists)
		r.Get("/trips/{tripId}/lists/{listId}", h.GetList)
		r.Put("/trips/{tripId}/lists/{listId}", h.UpsertList)
		r.Delete("/trips/{tripId}/lists/{listId}", h.DeleteList)
		r.Patch("/trips/{tripId}/lists/{listId}/sync", h.SyncList)

		// Weather
		r.Get("/trips/{tripId}/weather", h.GetWeather)
		r.Get("/weather/forecast", h.GetWeatherForecast)

		// Assets (map images, etc.)
		r.Get("/trips/{tripId}/assets", h.ListAssets)
		r.Get("/trips/{tripId}/assets/{filename}", h.GetAsset)
		r.Put("/trips/{tripId}/assets/{filename}", h.UploadAsset)
		r.Delete("/trips/{tripId}/assets/{filename}", h.DeleteAsset)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("TripKit backend (Go) listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
