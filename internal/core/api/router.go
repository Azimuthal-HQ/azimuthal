package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
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
	CommentHandler *commentsapi.Handler
	SPAHandler     http.Handler // serves the embedded frontend; nil disables SPA serving
}

// NewRouter builds the unified chi router with all routes and middleware.
func NewRouter(cfg RouterConfig) http.Handler { //nolint:funlen // router setup naturally grows with routes
	r := chi.NewRouter()

	// Global middleware stack
	r.Use(Recoverer)
	r.Use(RequestID)
	r.Use(Logging)
	r.Use(CORS)

	// Public endpoints (no auth required)
	r.Get("/health", HandleHealth)
	r.Get("/ready", HandleReady)

	// API documentation (no auth required)
	RegisterDocsRoutes(r)

	// Auth endpoints (mostly public, /me is protected)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Mount("/", cfg.AuthHandler.Routes())

		// /me requires authentication — uses the same JWT middleware as
		// all other protected endpoints to avoid redirect loops.
		r.Group(func(r chi.Router) {
			r.Use(cfg.Authenticator.RequireAuth)
			r.Get("/me", cfg.AuthHandler.Me)
		})
	})

	// Protected API endpoints
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(cfg.Authenticator.RequireAuth)

		// Organization management
		r.Get("/orgs/{orgID}", cfg.SpaceHandler.GetOrg)
		r.Patch("/orgs/{orgID}", cfg.SpaceHandler.UpdateOrg)

		// Spaces (scoped by org)
		r.Route("/orgs/{orgID}/spaces", func(r chi.Router) {
			r.Mount("/", cfg.SpaceHandler.Routes())
		})

		// Comments (scoped by org, space, and item)
		r.Route("/orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments", func(r chi.Router) {
			if cfg.CommentHandler != nil {
				r.Mount("/", cfg.CommentHandler.Routes())
			}
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
