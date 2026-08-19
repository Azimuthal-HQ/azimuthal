package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	adminapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/admin"
	attachmentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/attachments"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	avatarapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/avatar"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	credlinksapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/credlinks"
	dashboardsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/dashboards"
	grantsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/grants"
	invitesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/invites"
	notificationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	portalapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/portal"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	relationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/relations"
	searchapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/search"
	sharesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/shares"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	teamsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/teams"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	viewsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/views"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	workflowsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/workflows"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
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
	// RelationHandler serves the entity-generic relation satellite: one core,
	// mounted per entity subtree (projects items, tickets, wiki pages) the way
	// comments are — the from side of a relation comes from which route was
	// hit. nil leaves every relation route unmounted, including the item ones
	// that used to live inside ProjectHandler.Routes(); the harness wires it,
	// and TestHarness_NoDarkDependencies fails on a nil.
	RelationHandler     *relationsapi.Handler
	NotificationHandler *notificationsapi.Handler
	WorkflowHandler     *workflowsapi.Handler
	TeamHandler         *teamsapi.Handler
	GrantHandler        *grantsapi.Handler
	// ShareHandler serves entity shares (P3): the /shares management family
	// (manage_shares in-handler) and the /shared read family (share-
	// authorised, not space-authorised). nil leaves both unmounted.
	ShareHandler *sharesapi.Handler
	// AttachmentHandler serves entity attachments (P3): the space-scoped
	// upload/read family and the share-authorised read family. nil leaves
	// both unmounted.
	AttachmentHandler *attachmentsapi.Handler
	// AdminHandler serves the P2.5 administration surface (people, matrix,
	// audit viewer) behind RequireOrgAdmin404, plus the member-visible
	// picker search. nil leaves the surface unmounted.
	AdminHandler *adminapi.Handler
	// InviteHandler serves the invite lifecycle: admin routes behind
	// RequireOrgAdmin404 and the public token-authenticated acceptance
	// routes. nil leaves both unmounted.
	InviteHandler *invitesapi.Handler
	// CredentialLinkHandler serves the internal-user credential links (D1): the
	// public forgot-password / inspect / consume routes (token is the
	// credential), the authenticated email-change request mounted beside /me, and
	// the org-admin issuance routes behind RequireOrgAdmin404. nil leaves them
	// all unmounted.
	CredentialLinkHandler *credlinksapi.Handler
	// AvatarHandler serves user avatar upload (self + admin) and the
	// org-member-readable serve endpoint. nil leaves the routes unmounted
	// (e.g. when object storage is unavailable).
	AvatarHandler *avatarapi.Handler
	// ViewHandler serves saved views (P4, ADR-0009): the org-scoped /views
	// family. Cross-container by nature, so it has no {spaceID} to hang off
	// (ADR-0010).
	ViewHandler *viewsapi.Handler
	// PortalHandler serves the customer portal: the unauthenticated sign-in
	// routes and the requester-authenticated request routes. nil leaves the
	// whole surface unmounted, which is the correct default for a deployment
	// that has not opted any space in.
	PortalHandler *portalapi.Handler
	// PortalService backs RequirePortalSession. It is separate from
	// PortalHandler because the guard is middleware the ROUTER mounts, not
	// something the handler can apply to itself — and mounting the handler
	// without it would leave every requester route unauthenticated.
	// TestHarness_PortalGuardIsMounted fails if the two ever disagree.
	PortalService *portal.Service
	// DashboardHandler serves dashboards and gadgets (P5, ADR-0009): the
	// org-scoped /dashboards family. Org-scoped for the same reason /views is
	// — a dashboard arranges gadgets that cross containers.
	DashboardHandler *dashboardsapi.Handler
	// SearchHandler serves cross-module search (P6, spec §5/§7): the
	// org-scoped /search route. Org-scoped for the same reason /views is — a
	// search spans containers by definition, so there is no {spaceID} to
	// scope it to, and the per-viewer access set replaces the space guard.
	SearchHandler *searchapi.Handler
	SPAHandler    http.Handler // serves the embedded frontend; nil disables SPA serving
	// AllowedOrigins is the explicit CORS allow-list, and nil or empty is the
	// safe default: no CORS headers are emitted and the browser enforces
	// same-origin. Cross-origin callers are admitted only by an operator
	// setting AZIMUTHAL_ALLOWED_ORIGINS at boot.
	//
	// This field used to fail open — nil selected a permissive middleware that
	// echoed Access-Control-Allow-Origin: * on every response. Leave it unset
	// and you now get the restrictive behaviour, not the permissive one.
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
	r.Use(SecurityHeaders)
	// Unconditional: an unset allow-list means "emit no CORS headers", not
	// "allow everything". See RouterConfig.AllowedOrigins.
	r.Use(NewCORS(cfg.AllowedOrigins))

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

		// /me and /logout require authentication — the same JWT middleware as
		// all other protected endpoints, to avoid redirect loops.
		//
		// /logout moved in here from AuthHandler.Routes() in the v0.4.1 trust
		// patch, and this is a repair rather than a tightening. Out there it
		// refused EVERY caller, not just anonymous ones: nothing in this router
		// mounts OptionalAuth, so no middleware ever put claims on the context
		// at that path, ClaimsFromContext returned nil, and the handler's own
		// nil-claims branch answered 401 to a valid bearer token exactly as to
		// a stranger. The endpoint was unreachable.
		//
		// Inside the group it also gains the middleware's live-state read,
		// which is a genuine tightening: a token whose generation had already
		// been revoked, or whose account had been deactivated, is now refused
		// here rather than reaching a handler that would have to trust it.
		r.Group(func(r chi.Router) {
			r.Use(cfg.Authenticator.RequireAuth)
			r.Post("/logout", cfg.AuthHandler.Logout)
			// Logout-all is the org-wide sign-out plain logout used to be
			// before B1: same RequireAuth as logout, bumps the generation and
			// revokes every session row rather than just this device's.
			r.Post("/logout-all", cfg.AuthHandler.LogoutAll)
			r.Get("/me", cfg.AuthHandler.Me)
			r.Patch("/me", cfg.AuthHandler.UpdateMe)
			if cfg.AvatarHandler != nil {
				r.Put("/me/avatar", cfg.AvatarHandler.SelfUpload)
			}
			// Email change is a credential action, not a profile edit: it
			// reauthenticates and routes through a confirmation link (C.2-c), so
			// it lives on the credential-link handler even though it hangs off
			// /me. UpdateMe no longer touches email at all.
			if cfg.CredentialLinkHandler != nil {
				r.Post("/me/email-change", cfg.CredentialLinkHandler.RequestEmailChange)
			}
		})
	})

	// Invite acceptance (public): possession of the raw token is the
	// credential, exactly like a password-reset link. No auth middleware.
	if cfg.InviteHandler != nil {
		r.Route("/api/v1/invites", func(r chi.Router) {
			r.Mount("/", cfg.InviteHandler.PublicRoutes())
		})
	}

	// The customer portal. Mounted OUTSIDE the /api/v1 group on purpose: that
	// group's first statement is r.Use(RequireAuth), there is no per-route
	// opt-out of it, and an external requester holds no internal credential to
	// satisfy it with. Same placement as the public invite subtree above, for
	// the same reason.
	//
	// Nothing here is org-scoped by URL, so ResolveAccess never runs and no
	// access.Resolution reaches the context. That is correct rather than
	// missing: a requester has no membership to resolve (migration 044), and
	// every capability guard fails closed on a nil resolution anyway.
	// Authorisation for this subtree is RequirePortalSession plus the
	// requester-scoped queries behind it.
	//
	// The session subtree is a nested r.Route so the guard applies to the
	// whole of it via r.Use. Since #64 a route added to a guarded GROUP
	// inherits nothing unless it is inside that closure — the sibling public
	// routes above are outside it and are meant to be.
	if cfg.PortalHandler != nil {
		r.Route("/api/v1/portal", func(r chi.Router) {
			r.Mount("/", cfg.PortalHandler.PublicRoutes())
			r.Route("/{portalKey}/my", func(r chi.Router) {
				r.Use(portalapi.RequirePortalSession(cfg.PortalService))
				r.Mount("/", cfg.PortalHandler.SessionRoutes())
			})
		})
	}

	// Internal-user credential links (D1). Mounted OUTSIDE the /api/v1 group for
	// the same reason as the public invite and portal subtrees: possession of the
	// raw token is the credential, and forgot-password is reached by someone who
	// is by definition signed out — neither can satisfy RequireAuth. The admin
	// issuance routes live under /orgs/{orgID} (mountAdminSurface); the
	// authenticated email-change request hangs off /auth/me above.
	if cfg.CredentialLinkHandler != nil {
		r.Route("/api/v1/credential-links", func(r chi.Router) {
			r.Mount("/", cfg.CredentialLinkHandler.PublicRoutes())
		})
	}

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

			// Boot-time deployment flags the UI needs. Org-scoped for the
			// membership 404 this subtree gets from ResolveAccess, NOT because
			// the values are per-org — they are process-wide. What may appear
			// on that wire is decided by the BootConfig struct itself; read
			// the comment on it before adding a field
			// (internal/core/api/spaces/config.go).
			r.Get("/config", cfg.SpaceHandler.BootConfig)

			// Teams (org admin administers; members read).
			if cfg.TeamHandler != nil {
				r.Route("/teams", func(r chi.Router) {
					r.Mount("/", cfg.TeamHandler.Routes(orgAdminGuard(cfg)))
				})
			}

			mountAdminSurface(r, cfg)

			// The single scoping convention: every space resource lives
			// under /orgs/{orgID}/spaces/{spaceID}/... — spaceGuard 404s
			// any request whose space does not belong to the org in the
			// URL, then readableGuard 404s spaces outside the caller's
			// resolved readable set.
			spaceGuard, readableGuard, writeFloor := buildSpaceGuards(cfg)
			mountSpaceResources(r, cfg, spaceGuard, readableGuard, writeFloor)

			// Entity shares (P3, ADR-0008). Management is org-scoped and
			// capability-checked in-handler (manage_shares on the entity's
			// space). The read family bypasses the space guards by design —
			// it is authorised by a share, not by space access — so it
			// carries its own ResolveShares middleware and lives outside
			// mountSpaceResources.
			mountShareResources(r, cfg)

			// Saved views (P4, ADR-0009/ADR-0010). Org-scoped: a view spans
			// containers, so there is no {spaceID} to scope it to. The two
			// result routes carry ResolveShares of their own — a view is the
			// sanctioned ADR-0008 exception and unions the caller's shared
			// entities into its results.
			mountViewResources(r, cfg)

			// Dashboards (P5, ADR-0009). Org-scoped beside the views they
			// arrange. Deliberately NO share resolver: not one route here
			// reads a ticket or an item — the response hands the client the
			// query each gadget should run, and the client resolves it
			// through /views/preview and /views/aggregate, which carry
			// ResolveShares themselves.
			mountDashboardResources(r, cfg)

			// Cross-module search (P6, ADR-0009/ADR-0010). Org-scoped beside
			// the views it complements. It DOES carry ResolveShares, unlike
			// dashboards: search reads pages, tickets and items directly, and
			// a share is the only way an entity outside the caller's readable
			// spaces can legitimately appear in results.
			mountSearchResources(r, cfg)

			// Ticket-reference typeahead (A1). Org-scoped rather than
			// space-scoped: the ticket_ref field it fills names a ticket
			// anywhere in the organisation, not one in whichever space the
			// operator happens to be looking at. Deliberately outside the
			// admin guard, for the same reason the person picker is — the
			// panels that carry a ticket_ref are operated by space admins
			// who need not be org admins. The handler cuts results to the
			// caller's own resolved readable set, so this reveals nothing an
			// ordinary ticket list would not.
			r.Get("/tickets/suggest", cfg.TicketHandler.SuggestRefs)

			// Page typeahead (A4), beside the ticket one and org-scoped for
			// the same reason: a relation target may be a page anywhere in
			// the organisation, not one in whichever space the operator is
			// looking at. The handler cuts results to the caller's own
			// resolved readable set, so this reveals nothing a page list
			// would not.
			r.Get("/pages/suggest", cfg.WikiHandler.SuggestPages)

			// Item types (org-scoped: any member reads for pickers/filters;
			// org admins define, rename, archive, and delete).
			r.Route("/item-types", func(r chi.Router) {
				r.Get("/", cfg.ProjectHandler.ListItemTypes)
				r.With(orgAdminGuard(cfg)).Post("/", cfg.ProjectHandler.CreateItemType)
				r.With(orgAdminGuard(cfg)).Patch("/{typeID}", cfg.ProjectHandler.UpdateItemType)
				r.With(orgAdminGuard(cfg)).Delete("/{typeID}", cfg.ProjectHandler.DeleteItemType)
			})

			// Entity tags (org-scoped; migrations 040, 055). Read-only here,
			// and deliberately so: tags have no administration surface in
			// this phase — one comes into existence because somebody tagged a
			// page, a ticket or a project item, or typed `#foo` into a page
			// body, and all of those happen on the space-scoped write routes
			// where the entity's own edit permission already applies.
			// Renaming and merging are future work, and will want the
			// org-admin guard the item-type routes carry.
			//
			// Any member reads the tag list; it backs the tag autocomplete and
			// a tag NAME reveals nothing about which entities carry it. The
			// entities carrying a tag are a cross-space read, so that route
			// filters against the caller's own resolved readable set
			// in-handler — ADR-0010's rule for every cross-space endpoint.
			r.Route("/tags", func(r chi.Router) {
				r.Get("/", cfg.WikiHandler.ListOrgTags)
				r.Get("/{slug}/entities", cfg.WikiHandler.ListEntitiesWithTag)
			})

			// Custom fields (org-scoped: any member reads definitions for
			// entity forms; org admins define, rename, archive, and delete).
			// Scopes — which spaces and entity types a field is attached to,
			// and whether it is required there — are org-admin in BOTH
			// directions: the rows carry space ids, and listing them to any
			// member would disclose which private spaces a field is attached
			// to. Forms never read raw scopes; they read the composed
			// per-entity render, which carries no space beyond the URL's own.
			//
			// The /forms/* pair is the same data pivoted the other way — one
			// form's rows across fields, rather than one field's rows across
			// spaces — so the same both-directions org-admin rule applies.
			r.Route("/custom-fields", func(r chi.Router) {
				r.Get("/", cfg.ProjectHandler.ListCustomFields)
				r.With(orgAdminGuard(cfg)).Post("/", cfg.ProjectHandler.CreateCustomField)
				r.With(orgAdminGuard(cfg)).Patch("/{fieldID}", cfg.ProjectHandler.UpdateCustomField)
				r.With(orgAdminGuard(cfg)).Delete("/{fieldID}", cfg.ProjectHandler.DeleteCustomField)
				r.With(orgAdminGuard(cfg)).Get("/{fieldID}/scopes", cfg.ProjectHandler.ListFieldScopes)
				r.With(orgAdminGuard(cfg)).Put("/{fieldID}/scopes/{spaceID}/{entityType}", cfg.ProjectHandler.SetFieldScope)
				r.With(orgAdminGuard(cfg)).Delete("/{fieldID}/scopes/{spaceID}/{entityType}", cfg.ProjectHandler.RemoveFieldScope)
				r.With(orgAdminGuard(cfg)).Get("/forms/{spaceID}/{entityType}", cfg.ProjectHandler.ListFormFieldScopes)
				r.With(orgAdminGuard(cfg)).Put("/forms/{spaceID}/{entityType}/order", cfg.ProjectHandler.ReorderFormFields)
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

// buildSpaceGuards constructs the space-subtree middleware chain. nil
// resolvers (routing-only unit tests) disable the corresponding check;
// every real construction site wires both.
func buildSpaceGuards(cfg RouterConfig) (spaceGuard, readableGuard, writeFloor func(http.Handler) http.Handler) {
	spaceGuard = func(next http.Handler) http.Handler { return next }
	if cfg.SpaceOrgResolver != nil {
		spaceGuard = RequireSpaceInOrg(cfg.SpaceOrgResolver)
	}
	readableGuard = func(next http.Handler) http.Handler { return next }
	writeFloor = func(next http.Handler) http.Handler { return next }
	if cfg.AccessResolver != nil {
		readableGuard = RequireSpaceReadable()
		// Mutating a space resource needs at least create_items
		// (contributor); viewers read only. Handlers refine above the
		// floor (edit_own/edit_any, transitions, queue).
		writeFloor = RequireWriteFloor(access.CapCreateItems)
	}
	return spaceGuard, readableGuard, writeFloor
}

// mountAdminSurface registers the P2.5 administration surface under the
// org group: 404 for non-admins — the surface does not exist as far as
// they can tell. The picker search is the one member-visible route (space
// admins use it on the grants panel without being org admins).
func mountAdminSurface(r chi.Router, cfg RouterConfig) {
	if cfg.AdminHandler != nil {
		admin404 := orgAdmin404Guard(cfg)
		r.Route("/users", func(r chi.Router) {
			// admin404 is applied per-route (not r.Use) so the avatar serve
			// route below can be org-member instead of org-admin.
			r.With(admin404).Get("/", cfg.AdminHandler.ListPeople)
			r.With(admin404).Patch("/{userID}", cfg.AdminHandler.UpdatePerson)
			r.With(admin404).Delete("/{userID}", cfg.AdminHandler.RemovePerson)
			r.With(admin404).Post("/{userID}/deactivate", cfg.AdminHandler.DeactivatePerson)
			r.With(admin404).Post("/{userID}/reactivate", cfg.AdminHandler.ReactivatePerson)
			r.With(admin404).Post("/{userID}/force-logout", cfg.AdminHandler.ForceLogoutPerson)
			if cfg.AvatarHandler != nil {
				// Admin sets another member's avatar (org-admin only)...
				r.With(admin404).Put("/{userID}/avatar", cfg.AvatarHandler.AdminUpload)
				// ...but any org member may read an avatar (shown org-wide).
				r.Get("/{userID}/avatar", cfg.AvatarHandler.Serve)
			}
		})
		r.With(admin404).Get("/access-matrix", cfg.AdminHandler.AccessMatrix)
		r.Route("/grants", func(r chi.Router) {
			r.Use(admin404)
			r.Post("/bulk-preview", cfg.AdminHandler.BulkPreview)
			r.Post("/bulk-apply", cfg.AdminHandler.BulkApply)
		})
		r.Route("/audit-log", func(r chi.Router) {
			r.Use(admin404)
			r.Get("/", cfg.AdminHandler.ListAuditLog)
			r.Get("/batches/{batchID}", cfg.AdminHandler.AuditLogBatch)
		})
		r.Get("/members/search", cfg.AdminHandler.SearchMembers)
	}
	if cfg.InviteHandler != nil {
		r.Route("/invites", func(r chi.Router) {
			r.Use(orgAdmin404Guard(cfg))
			r.Mount("/", cfg.InviteHandler.AdminRoutes())
		})
	}
	if cfg.CredentialLinkHandler != nil {
		// Admin credential-link issuance: create a member behind a sign-in link,
		// mint a reset link. Same org-admin-404 posture as the rest of this
		// surface — a non-admin cannot tell the routes exist.
		r.Route("/credential-links", func(r chi.Router) {
			r.Use(orgAdmin404Guard(cfg))
			r.Mount("/", cfg.CredentialLinkHandler.AdminRoutes())
		})
	}
}

// mountShareResources registers the P3 share families under the org group.
//
// The /shares management family is org-scoped: any member reaches it, and
// the handler enforces manage_shares on the shared entity's space (with a
// read check first, so an unreadable space 404s rather than 403-leaking the
// entity). The /shared read family and the shared attachment path are
// authorised by share coverage alone — they cannot use the space guards,
// because the whole point is access without space access — so they carry
// their own ResolveShares middleware, mounted here and nowhere else, which
// keeps the P2 per-request query budget on space routes untouched.
func mountShareResources(r chi.Router, cfg RouterConfig) {
	if cfg.ShareHandler == nil {
		return
	}
	r.Route("/shares", func(r chi.Router) {
		r.Mount("/", cfg.ShareHandler.ManagementRoutes())
	})
	// The shared read family is registered explicitly (not mounted as a
	// sub-router) so the standalone read route and the attachment routes can
	// coexist under one /shared subtree that shares the ResolveShares
	// middleware — chi forbids Mount("/") beside sibling routes.
	r.Route("/shared", func(r chi.Router) {
		if cfg.AccessResolver != nil {
			r.Use(ResolveShares(cfg.AccessResolver))
		}
		r.Get("/{entityType}/{entityID}", cfg.ShareHandler.ReadShared)
		if cfg.AttachmentHandler != nil {
			r.Get("/{entityType}/{entityID}/attachments", cfg.AttachmentHandler.ListShared)
			r.Get("/{entityType}/{entityID}/attachments/{attachmentID}", cfg.AttachmentHandler.DownloadShared)
		}
	})
}

// mountViewResources registers the P4 saved-view family under the org group.
//
// Org-scoped per ADR-0010: a saved view is cross-container by nature and has
// no {spaceID} to hang off. Every route is org-member — any member may keep
// private views — and who may see or change one is decided by the view's own
// ownership and visibility rather than by a space capability.
//
// The share resolver is applied to the two RESULT routes only, and is passed
// into the handler rather than mounted on the whole family. A saved view is
// the sanctioned ADR-0008 exception and must union the caller's shared
// entities into its results, but listing or editing a view has no use for
// share coverage and must not pay a query for it. That is the same reasoning
// that keeps ResolveShares off every space-scoped route (spec §5): mount it
// per route family — here, per route — and re-run the case-23 tracer.
func mountViewResources(r chi.Router, cfg RouterConfig) {
	if cfg.ViewHandler == nil {
		return
	}
	// Same nil-resolver pass-through convention as the admin guards, so
	// routing-only unit tests can build a router without an access resolver.
	shareResolver := func(next http.Handler) http.Handler { return next }
	if cfg.AccessResolver != nil {
		shareResolver = ResolveShares(cfg.AccessResolver)
	}
	r.Route("/views", func(r chi.Router) {
		r.Mount("/", cfg.ViewHandler.Routes(shareResolver))
	})
}

// mountSearchResources registers the P6 cross-module search route under the org
// group.
//
// It carries ResolveShares because search reads the three entity tables
// directly and unions the caller's shared entities — including, for pages, the
// cascade SUBTREES that D46's paired accessor makes reachable. Without the
// middleware the share and subtree arrays are empty on every request, which has
// no symptom other than shared things quietly never being found.
func mountSearchResources(r chi.Router, cfg RouterConfig) {
	if cfg.SearchHandler == nil {
		return
	}
	// Same nil-resolver pass-through convention as the admin guards and
	// mountViewResources, so routing-only unit tests can build a router
	// without an access resolver.
	shareResolver := func(next http.Handler) http.Handler { return next }
	if cfg.AccessResolver != nil {
		shareResolver = ResolveShares(cfg.AccessResolver)
	}
	r.Route("/search", func(r chi.Router) {
		r.Mount("/", cfg.SearchHandler.Routes(shareResolver))
	})
}

// mountDashboardResources registers the P5 dashboard family under the org
// group.
//
// Its own function rather than a block inside NewRouter, matching
// mountShareResources and mountViewResources — and because NewRouter is
// already at its cyclomatic ceiling.
//
// No middleware of its own. The org group's ResolveAccess has already
// established membership, and a dashboard read needs nothing further: it
// returns an arrangement plus, per gadget, the query the client should run.
// The gadget's DATA is fetched separately through the two view endpoints that
// do carry ResolveShares. Adding it here would make every dashboard read pay
// for a share query no route in this family uses, which is what per-family
// mounting (spec §5, matrix case 23) exists to prevent.
func mountDashboardResources(r chi.Router, cfg RouterConfig) {
	if cfg.DashboardHandler == nil {
		return
	}
	r.Route("/dashboards", func(r chi.Router) {
		r.Mount("/", cfg.DashboardHandler.Routes())
	})
}

// mountQueueResources registers the P4 PR-B queue family under a space.
//
// Its own function rather than a block inside mountSpaceResources, matching
// mountShareResources and mountViewResources. That is also what keeps
// mountSpaceResources under the cyclomatic limit: every space-scoped family
// added inline costs it another branch, and it already carries eleven.
//
// Space-scoped because reading a queue needs only space-readability, which is
// exactly the audience a queue has (visibility 'space'). The guards are the
// ordinary three; every MUTATION is refined above the write floor by an
// in-handler CapManageQueue check, which is where that capability lands.
//
// The share resolver reaches the results route only, for the same reason it
// does on the saved-view family: a queue is a saved view, and its results
// union the caller's shared entities.
func mountQueueResources(r chi.Router, cfg RouterConfig, spaceGuard, readableGuard, writeFloor func(http.Handler) http.Handler) {
	if cfg.ViewHandler == nil {
		return
	}
	shareResolver := func(next http.Handler) http.Handler { return next }
	if cfg.AccessResolver != nil {
		shareResolver = ResolveShares(cfg.AccessResolver)
	}
	r.Route("/spaces/{spaceID}/queues", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
		r.Use(writeFloor)
		r.Mount("/", cfg.ViewHandler.QueueRoutes(shareResolver))
	})
}

// orgAdminGuard returns the org-admin middleware, or a pass-through when no
// AccessResolver is wired (routing-only unit tests).
func orgAdminGuard(cfg RouterConfig) func(http.Handler) http.Handler {
	if cfg.AccessResolver == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return RequireOrgAdmin()
}

// orgAdmin404Guard is the 404-variant guard for the administration surface,
// with the same nil-resolver pass-through convention.
func orgAdmin404Guard(cfg RouterConfig) func(http.Handler) http.Handler {
	if cfg.AccessResolver == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return RequireOrgAdmin404()
}

// mountSpaceResources registers every space-scoped resource tree under the
// single /orgs/{orgID}/spaces/{spaceID}/... convention (relative to the
// /orgs/{orgID} group). Guard order on every subtree: spaceGuard (the space
// belongs to the org, 404), readableGuard (the space is in the caller's
// resolved readable set, 404), writeFloor (mutating methods need at least
// create_items, 403). Comments hang off their resource's own path.
func mountSpaceResources(r chi.Router, cfg RouterConfig, spaceGuard, readableGuard, writeFloor func(http.Handler) http.Handler) { //nolint:funlen // route registration naturally grows with resources, like NewRouter above
	// Spaces (org-level list/create plus {spaceID} resources). The space
	// handler carries its own capability checks (manage_space) — the
	// create_items floor does not apply to space governance.
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

	mountQueueResources(r, cfg, spaceGuard, readableGuard, writeFloor)

	// Customer-portal configuration (agent side). Ordinary space-scoped
	// routes: org membership, space readability, and manage_space enforced in
	// the handler. Deliberately NOT under writeFloor — the floor is
	// create_items, and manage_space sits well above it, so adding the floor
	// would make the capability check unreachable for anybody it would refuse.
	if cfg.PortalHandler != nil {
		r.Route("/spaces/{spaceID}/portal", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Use(readableGuard)
			r.Mount("/", cfg.PortalHandler.AdminRoutes())
		})
	}

	// Tickets
	r.Route("/spaces/{spaceID}/tickets", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
		r.Use(writeFloor)
		r.Mount("/", cfg.TicketHandler.Routes())
		if cfg.WorkflowHandler != nil {
			r.Post("/{ticketID}/workflow-state", cfg.WorkflowHandler.ApplyWorkflowTransitionToTicket)
		}
		if cfg.CommentHandler != nil {
			r.Get("/{ticketID}/comments", cfg.CommentHandler.ListTicketComments)
			r.Post("/{ticketID}/comments", cfg.CommentHandler.CreateTicketComment)
		}
		mountRelationRoutes(r, cfg.RelationHandler, "/{ticketID}",
			(*relationsapi.Handler).ListTicketRelations, (*relationsapi.Handler).CreateTicketRelation, false)
	})

	// Wiki pages
	r.Route("/spaces/{spaceID}/wiki", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
		r.Use(writeFloor)
		r.Mount("/", cfg.WikiHandler.Routes())
		if cfg.CommentHandler != nil {
			r.Get("/{pageID}/comments", cfg.CommentHandler.ListPageComments)
			r.Post("/{pageID}/comments", cfg.CommentHandler.CreatePageComment)
		}
		mountRelationRoutes(r, cfg.RelationHandler, "/{pageID}",
			(*relationsapi.Handler).ListPageRelations, (*relationsapi.Handler).CreatePageRelation, false)
	})

	// Projects
	r.Route("/spaces/{spaceID}/projects", func(r chi.Router) {
		r.Use(spaceGuard)
		r.Use(readableGuard)
		r.Use(writeFloor)
		r.Mount("/", cfg.ProjectHandler.Routes())
		if cfg.WorkflowHandler != nil {
			r.Post("/items/{itemID}/workflow-state", cfg.WorkflowHandler.ApplyWorkflowTransitionToItem)
		}
		if cfg.CommentHandler != nil {
			r.Get("/items/{itemID}/comments", cfg.CommentHandler.ListItemComments)
			r.Post("/items/{itemID}/comments", cfg.CommentHandler.CreateItemComment)
		}
		// The item URLs predate the entity-generic mount and did not move;
		// only their registration did, out of ProjectHandler.Routes() and
		// into the same per-subtree convention comments use.
		mountRelationRoutes(r, cfg.RelationHandler, "/items/{itemID}",
			(*relationsapi.Handler).ListItemRelations, (*relationsapi.Handler).CreateItemRelation, true)
	})

	// Space workflow (read-only routes plus the transition POST above).
	if cfg.WorkflowHandler != nil {
		r.Route("/spaces/{spaceID}/workflow", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Use(readableGuard)
			r.Use(writeFloor)
			r.Mount("/", cfg.WorkflowHandler.SpaceRoutes())
		})
	}

	// Attachments (P3): space members upload/read/delete; the write floor
	// gates uploads and deletes, reads need only space-readability. The
	// share-authorised read counterparts live under /shared.
	if cfg.AttachmentHandler != nil {
		r.Route("/spaces/{spaceID}/attachments", func(r chi.Router) {
			r.Use(spaceGuard)
			r.Use(readableGuard)
			r.Use(writeFloor)
			r.Mount("/", cfg.AttachmentHandler.SpaceRoutes())
		})
	}
}

// mountRelationRoutes registers one entity subtree's relation routes (A4):
// the satellite is ONE handler mounted per subtree, so each call fixes only
// the id pattern and the two wrappers. Method expressions rather than bound
// values, because h may legitimately be nil — a nil handler leaves the
// subtree's relation routes unmounted, exactly like a nil CommentHandler one
// block up, and the harness's dark-dependency walk is what keeps that state
// out of the test server. withDelete adds the single relation-addressed
// DELETE, which the projects subtree carries for URL continuity; a relation
// is addressed by its own id, so one delete serves all three mounts.
func mountRelationRoutes(
	r chi.Router,
	h *relationsapi.Handler,
	idPattern string,
	list, create func(*relationsapi.Handler, http.ResponseWriter, *http.Request),
	withDelete bool,
) {
	if h == nil {
		return
	}
	r.Get(idPattern+"/relations", func(w http.ResponseWriter, req *http.Request) { list(h, w, req) })
	r.Post(idPattern+"/relations", func(w http.ResponseWriter, req *http.Request) { create(h, w, req) })
	if withDelete {
		r.Delete("/relations/{relationID}", h.DeleteRelation)
	}
}
