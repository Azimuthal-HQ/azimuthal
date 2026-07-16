package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	notificationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	workflowsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/workflows"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// RouterConfig holds all the dependencies needed to build the API router.
type RouterConfig struct {
	Authenticator       *auth.Authenticator
	AuthHandler         *authapi.Handler
	TicketHandler       *ticketsapi.Handler
	WikiHandler         *wikiapi.Handler
	ProjectHandler      *projectsapi.Handler
	SpaceHandler        *spacesapi.Handler
	CommentHandler      *commentsapi.Handler
	NotificationHandler *notificationsapi.Handler
	WorkflowHandler     *workflowsapi.Handler
	SPAHandler          http.Handler // serves the embedded frontend; nil disables SPA serving
	// AllowedOrigins is the explicit CORS allow-list. nil falls back to the
	// permissive wildcard for backwards compatibility with existing tests.
	AllowedOrigins []string
	// QueueStatus is reported in the /health response: "ok", "disabled", or "error".
	QueueStatus string
	// SpaceOrgResolver backs the RequireSpaceInOrg middleware that enforces
	// the single /orgs/{orgID}/spaces/{spaceID}/... scoping convention.
	SpaceOrgResolver SpaceOrgResolver
}

// NewRouter builds the unified chi router with all routes and middleware.
func NewRouter(cfg RouterConfig) http.Handler { //nolint:funlen // router setup naturally grows with routes
	r := chi.NewRouter()

	// Global middleware stack
	r.Use(Recoverer)
	r.Use(RequestID)
	r.Use(Logging)
	if cfg.AllowedOrigins == nil {
		r.Use(CORS)
	} else {
		r.Use(NewCORS(cfg.AllowedOrigins))
	}

	// Public endpoints (no auth required)
	queueStatus := cfg.QueueStatus
	if queueStatus == "" {
		queueStatus = "disabled"
	}
	r.Get("/health", HandleHealthWithQueue(queueStatus))
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
			r.Patch("/me", cfg.AuthHandler.UpdateMe)
		})
	})

	// Protected API endpoints
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(cfg.Authenticator.RequireAuth)

		// Organization management
		r.Get("/orgs/{orgID}", cfg.SpaceHandler.GetOrg)
		r.Patch("/orgs/{orgID}", cfg.SpaceHandler.UpdateOrg)

		// The single scoping convention: every space resource lives under
		// /orgs/{orgID}/spaces/{spaceID}/... — spaceGuard 404s any request
		// whose space does not belong to the org in the URL. A nil resolver
		// (routing-only unit tests) disables the ownership check; every real
		// construction site wires one.
		spaceGuard := func(next http.Handler) http.Handler { return next }
		if cfg.SpaceOrgResolver != nil {
			spaceGuard = RequireSpaceInOrg(cfg.SpaceOrgResolver)
		}

		mountSpaceResources(r, cfg, spaceGuard)

		// Notifications (scoped to current user)
		if cfg.NotificationHandler != nil {
			r.Route("/notifications", func(r chi.Router) {
				r.Mount("/", cfg.NotificationHandler.Routes())
			})
		}

		// Labels (scoped by org)
		r.Route("/orgs/{orgID}/labels", func(r chi.Router) {
			r.Get("/", cfg.ProjectHandler.ListLabels)
			r.Post("/", cfg.ProjectHandler.CreateLabel)
			r.Delete("/{labelID}", cfg.ProjectHandler.DeleteLabel)
		})

		// Workflows (org-scoped CRUD)
		if cfg.WorkflowHandler != nil {
			r.Route("/orgs/{orgID}/workflows", func(r chi.Router) {
				r.Mount("/", cfg.WorkflowHandler.OrgRoutes())
			})
		}

	})

	// SPA frontend: serve static assets and fall back to index.html
	if cfg.SPAHandler != nil {
		r.NotFound(cfg.SPAHandler.ServeHTTP)
	}

	return r
}

// mountSpaceResources registers every space-scoped resource tree under the
// single /orgs/{orgID}/spaces/{spaceID}/... convention, with spaceGuard
// enforcing that the space belongs to the org. Comments hang off their
// resource's own path.
func mountSpaceResources(r chi.Router, cfg RouterConfig, spaceGuard func(http.Handler) http.Handler) {
	// Spaces (scoped by org)
	r.Route("/orgs/{orgID}/spaces", func(r chi.Router) {
		r.Mount("/", cfg.SpaceHandler.Routes(spaceGuard))
	})

	// Tickets
	r.Route("/orgs/{orgID}/spaces/{spaceID}/tickets", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Mount("/", cfg.TicketHandler.Routes())
		if cfg.WorkflowHandler != nil {
			r.Post("/{ticketID}/workflow-state", cfg.WorkflowHandler.ApplyWorkflowTransitionToTicket)
		}
		if cfg.CommentHandler != nil {
			r.Get("/{ticketID}/comments", cfg.CommentHandler.ListTicketComments)
			r.Post("/{ticketID}/comments", cfg.CommentHandler.CreateTicketComment)
		}
	})

	// Wiki pages
	r.Route("/orgs/{orgID}/spaces/{spaceID}/wiki", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Mount("/", cfg.WikiHandler.Routes())
		if cfg.CommentHandler != nil {
			r.Get("/{pageID}/comments", cfg.CommentHandler.ListPageComments)
			r.Post("/{pageID}/comments", cfg.CommentHandler.CreatePageComment)
		}
	})

	// Projects
	r.Route("/orgs/{orgID}/spaces/{spaceID}/projects", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Mount("/", cfg.ProjectHandler.Routes())
		if cfg.WorkflowHandler != nil {
			r.Post("/items/{itemID}/workflow-state", cfg.WorkflowHandler.ApplyWorkflowTransitionToItem)
		}
		if cfg.CommentHandler != nil {
			r.Get("/items/{itemID}/comments", cfg.CommentHandler.ListItemComments)
			r.Post("/items/{itemID}/comments", cfg.CommentHandler.CreateItemComment)
		}
	})

	// Space workflow (read-only)
	if cfg.WorkflowHandler != nil {
		r.Route("/orgs/{orgID}/spaces/{spaceID}/workflow", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Mount("/", cfg.WorkflowHandler.SpaceRoutes())
		})
	}
}
