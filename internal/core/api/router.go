package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// RouterConfig holds all the dependencies needed to build the API router.
type RouterConfig struct {
	Authenticator  *auth.Authenticator
	AuthHandler    *authapi.Handler
	TicketHandler  *ticketsapi.Handler
	WikiHandler    *wikiapi.Handler
	ProjectHandler *projectsapi.Handler
	SpaceHandler   *spacesapi.Handler
	SPAHandler     http.Handler // serves the embedded frontend; nil disables SPA serving
}

// NewRouter builds the unified chi router with all routes and middleware.
func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Global middleware stack
	r.Use(Recoverer)
	r.Use(RequestID)
	r.Use(Logging)
	r.Use(CORS)

	// Public endpoints (no auth required)
	r.Get("/health", HandleHealth)
	r.Get("/ready", HandleReady)

	// Auth endpoints (mostly public)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Mount("/", cfg.AuthHandler.Routes())
	})

	// Protected API endpoints
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(cfg.Authenticator.RequireAuth)

		// Spaces (scoped by org)
		r.Route("/orgs/{orgID}/spaces", func(r chi.Router) {
			r.Mount("/", cfg.SpaceHandler.Routes())
		})

		// Labels (scoped by org)
		r.Route("/orgs/{orgID}/labels", func(r chi.Router) {
			r.Get("/", cfg.ProjectHandler.ListLabels)
			r.Post("/", cfg.ProjectHandler.CreateLabel)
			r.Delete("/{labelID}", cfg.ProjectHandler.DeleteLabel)
		})

		// Tickets (scoped by space)
		r.Route("/spaces/{spaceID}/tickets", func(r chi.Router) {
			r.Mount("/", cfg.TicketHandler.Routes())
		})

		// Wiki pages (scoped by space)
		r.Route("/spaces/{spaceID}/wiki", func(r chi.Router) {
			r.Mount("/", cfg.WikiHandler.Routes())
		})

		// Projects (scoped by space)
		r.Route("/spaces/{spaceID}/projects", func(r chi.Router) {
			r.Mount("/", cfg.ProjectHandler.Routes())
		})
	})

	// SPA frontend: serve static assets and fall back to index.html
	if cfg.SPAHandler != nil {
		r.NotFound(cfg.SPAHandler.ServeHTTP)
	}

	return r
}
