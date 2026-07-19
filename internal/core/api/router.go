package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	grantsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/grants"
	notificationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	teamsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/teams"
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
	TeamHandler         *teamsapi.Handler
	GrantHandler        *grantsapi.Handler
	SPAHandler          http.Handler // serves the embedded frontend; nil disables SPA serving
	// AllowedOrigins is the explicit CORS allow-list. nil falls back to the
	// permissive wildcard for backwards compatibility with existing tests.
	AllowedOrigins []string
	// QueueStatus is reported in the /health response: "ok", "disabled", or "error".
	QueueStatus string
	// SpaceOrgResolver backs the RequireSpaceInOrg middleware that enforces
	// the single /orgs/{orgID}/spaces/{spaceID}/... scoping convention.
	SpaceOrgResolver SpaceOrgResolver
	// AccessResolver backs the per-request permission resolution middleware
	// (spec §5). nil (routing-only unit tests) leaves the middleware
	// unmounted; every real construction site wires one, and the capability
	// guards fail closed without a resolution on the context.
	AccessResolver *access.Resolver
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

		// Notifications (scoped to current user, not org-scoped)
		if cfg.NotificationHandler != nil {
			r.Route("/notifications", func(r chi.Router) {
				r.Mount("/", cfg.NotificationHandler.Routes())
			})
		}

		// Everything org-scoped lives under one group so the per-request
		// permission resolution (spec §5) mounts in exactly one place:
		// membership is checked (404 for non-members) and the readable
		// space set is resolved once and cached on the request context.
		r.Route("/orgs/{orgID}", func(r chi.Router) {
			if cfg.AccessResolver != nil {
				r.Use(ResolveAccess(cfg.AccessResolver))
			}

			// Organization management. Reads need membership (enforced
			// above); mutating the org needs an org admin.
			r.Get("/", cfg.SpaceHandler.GetOrg)
			r.With(orgAdminGuard(cfg)).Patch("/", cfg.SpaceHandler.UpdateOrg)

			// Teams (org admin administers; members read).
			if cfg.TeamHandler != nil {
				r.Route("/teams", func(r chi.Router) {
					r.Mount("/", cfg.TeamHandler.Routes(orgAdminGuard(cfg)))
				})
			}

			// The single scoping convention: every space resource lives
			// under /orgs/{orgID}/spaces/{spaceID}/... — spaceGuard 404s
			// any request whose space does not belong to the org in the
			// URL, then readableGuard 404s spaces outside the caller's
			// resolved readable set. nil resolvers (routing-only unit
			// tests) disable the corresponding check; every real
			// construction site wires both.
			spaceGuard := func(next http.Handler) http.Handler { return next }
			if cfg.SpaceOrgResolver != nil {
				spaceGuard = RequireSpaceInOrg(cfg.SpaceOrgResolver)
			}
			readableGuard := func(next http.Handler) http.Handler { return next }
			if cfg.AccessResolver != nil {
				readableGuard = RequireSpaceReadable()
			}

			mountSpaceResources(r, cfg, spaceGuard, readableGuard)

			// Labels (org-scoped metadata; any member).
			r.Route("/labels", func(r chi.Router) {
				r.Get("/", cfg.ProjectHandler.ListLabels)
				r.Post("/", cfg.ProjectHandler.CreateLabel)
				r.Delete("/{labelID}", cfg.ProjectHandler.DeleteLabel)
			})

			// Workflows (org-scoped: members read, org admins mutate).
			if cfg.WorkflowHandler != nil {
				r.Route("/workflows", func(r chi.Router) {
					r.Mount("/", cfg.WorkflowHandler.OrgRoutes(orgAdminGuard(cfg)))
				})
			}
		})
	})

	// SPA frontend: serve static assets and fall back to index.html
	if cfg.SPAHandler != nil {
		r.NotFound(cfg.SPAHandler.ServeHTTP)
	}

	return r
}

// orgAdminGuard returns the org-admin middleware, or a pass-through when no
// AccessResolver is wired (routing-only unit tests).
func orgAdminGuard(cfg RouterConfig) func(http.Handler) http.Handler {
	if cfg.AccessResolver == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return RequireOrgAdmin()
}

// mountSpaceResources registers every space-scoped resource tree under the
// single /orgs/{orgID}/spaces/{spaceID}/... convention (relative to the
// /orgs/{orgID} group), with spaceGuard enforcing that the space belongs to
// the org and readableGuard enforcing the caller's resolved readable set.
// Comments hang off their resource's own path.
func mountSpaceResources(r chi.Router, cfg RouterConfig, spaceGuard, readableGuard func(http.Handler) http.Handler) {
	// Spaces (org-level list/create plus {spaceID} resources).
	r.Route("/spaces", func(r chi.Router) {
		r.Mount("/", cfg.SpaceHandler.Routes(spaceGuard, readableGuard))
	})

	// Grants and effective-access (space-scoped, capability-guarded in the
	// handler: manage_grants).
	if cfg.GrantHandler != nil {
		r.Route("/spaces/{spaceID}/grants", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Use(readableGuard)
			r.Mount("/", cfg.GrantHandler.Routes())
		})
		r.With(spaceGuard, readableGuard).
			Get("/spaces/{spaceID}/effective-access", cfg.GrantHandler.EffectiveAccess)
	}

	// Tickets
	r.Route("/spaces/{spaceID}/tickets", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
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
	r.Route("/spaces/{spaceID}/wiki", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
		r.Mount("/", cfg.WikiHandler.Routes())
		if cfg.CommentHandler != nil {
			r.Get("/{pageID}/comments", cfg.CommentHandler.ListPageComments)
			r.Post("/{pageID}/comments", cfg.CommentHandler.CreatePageComment)
		}
	})

	// Projects
	r.Route("/spaces/{spaceID}/projects", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
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
		r.Route("/spaces/{spaceID}/workflow", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Use(readableGuard)
			r.Mount("/", cfg.WorkflowHandler.SpaceRoutes())
		})
	}
}
