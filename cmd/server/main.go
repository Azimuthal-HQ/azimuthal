// Package main is the single binary entrypoint for Azimuthal.
// It wires together config, database, background jobs, and the HTTP server.
//
// @title           Azimuthal API
// @version         1.0
// @description     The fully open-source alternative to Jira, Confluence, and Jira Service Desk.
// @description     Self-hostable, single binary, Apache 2.0 licensed.
// @description
// @description     ## Authentication
// @description     All endpoints except /auth/login and /auth/register require a Bearer JWT token.
// @description     Obtain a token via POST /api/v1/auth/login, then include it as:
// @description     `Authorization: Bearer <your-token>`
//
// @contact.name    Azimuthal HQ
// @contact.url     https://azimuthalhq.com
// @contact.email   hello@azimuthalhq.com
//
// @license.name    Apache 2.0
// @license.url     https://www.apache.org/licenses/LICENSE-2.0.html
// @host            localhost:8080
// @BasePath        /api/v1
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 JWT Bearer token. Format: "Bearer <token>". Obtain via POST /api/v1/auth/login
// @securityDefinitions.apikey  PortalSessionAuth
// @in                          header
// @name                        Authorization
// @description                 Customer-portal session token. Format: "Bearer <token>". Obtained via POST /api/v1/portal/auth/redeem. NOT interchangeable with BearerAuth: the two token families carry different audience claims and each parser refuses the other's tokens.
// @tag.name        auth
// @tag.description Authentication — login, logout, register, get current user
// @tag.name        spaces
// @tag.description Spaces — containers for service desks, wikis, and projects
// @tag.name        tickets
// @tag.description Service Desk — create and manage tickets and kanban items
// @tag.name        wiki
// @tag.description Wiki — create and manage documentation pages
// @tag.name        projects
// @tag.description Projects — manage backlogs, sprints, and roadmaps
// @tag.name        comments
// @tag.description Comments — unified comment system across tickets and wiki pages
// @tag.name        members
// @tag.description Members — space membership management
// @tag.name        labels
// @tag.description Labels — organization-scoped labels for items
// @tag.name        notifications
// @tag.description Notifications — in-app notification delivery and read state
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	adminapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/admin"
	attachmentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/attachments"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	avatarapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/avatar"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	grantsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/grants"
	invitesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/invites"
	notificationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	portalapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/portal"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	sharesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/shares"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	teamsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	viewsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/views"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	workflowsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/workflows"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/invites"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
	"github.com/Azimuthal-HQ/azimuthal/web"
)

// Version and BuildTime are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	Execute()
}

// serverDeps bundles shutdown hooks that must run in order before the DB pool closes.
type serverDeps struct {
	// stopQueue drains in-flight River jobs. Must be called before pool.Close().
	stopQueue func(ctx context.Context) error
}

// newServer builds an http.Server with the full API router backed by the database.
// The caller must call deps.stopQueue(ctx) then cleanup() during shutdown.
func newServer(cfg *config.Config) (*http.Server, *serverDeps, func(), error) { //nolint:funlen // server wiring naturally enumerates all dependencies; splitting would obscure the startup sequence
	ctx := context.Background()
	noop := func() {}
	deps := &serverDeps{stopQueue: func(_ context.Context) error { return nil }}

	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return nil, deps, noop, fmt.Errorf("connecting to database: %w", err)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, deps, noop, fmt.Errorf("running migrations: %w", err)
	}

	queries := generated.New(pool)

	// Build the email sender — NoopSender when SMTP host is not configured.
	var sender email.Sender
	if cfg.SMTPHost != "" {
		sender = email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom)
	} else {
		sender = &email.NoopSender{}
	}

	// Start the River background queue unless disabled by config.
	queueStatus := "disabled"
	var notifEnqueuer jobs.NotificationEnqueuer = jobs.NoopNotificationEnqueuer{}

	if cfg.QueueEnabled {
		q, err := jobs.NewQueue(ctx, pool, sender, queries)
		if err != nil {
			pool.Close()
			return nil, deps, noop, fmt.Errorf("creating job queue: %w", err)
		}

		queueCtx, queueCancel := context.WithCancel(context.Background())
		go func() {
			if startErr := q.Start(queueCtx); startErr != nil {
				slog.Error("job queue error", "error", startErr)
			}
		}()

		deps.stopQueue = func(ctx context.Context) error {
			queueCancel()
			return q.Stop(ctx)
		}
		notifEnqueuer = q
		queueStatus = "ok"
		slog.Info("job queue started")
	} else {
		slog.Warn("job queue disabled via AZIMUTHAL_QUEUE_ENABLED=false")
	}

	handler, err := buildRouter(cfg, pool, queries, notifEnqueuer, sender, queueStatus)
	if err != nil {
		pool.Close()
		return nil, deps, noop, err
	}

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.AppPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return srv, deps, func() { pool.Close() }, nil
}

// buildRouter constructs all domain services with DB-backed adapters and
// returns the fully wired API router.
func buildRouter(cfg *config.Config, pool *pgxpool.Pool, queries *generated.Queries, notifEnqueuer jobs.NotificationEnqueuer, sender email.Sender, queueStatus string) (http.Handler, error) { //nolint:funlen // router wiring naturally enumerates all dependencies, like newServer above
	// The signing key lives in the database so restarts never invalidate
	// tokens. JWTPrivateKeyPath is only consulted as a one-time import for
	// deployments upgrading from the legacy file-based key.
	privateKey, err := auth.EnsureSigningKey(context.Background(), adapters.NewSigningKeyAdapter(queries), cfg.JWTPrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading RSA signing key: %w", err)
	}

	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  cfg.JWTExpiry,
		RefreshTTL: cfg.JWTExpiry * 7,
		Issuer:     "azimuthal",
		// The customer portal signs a second token family with this same key
		// (auth_signing_keys holds one row by construction), so the audience
		// claim is the boundary between an agent's credential and an external
		// requester's. See auth.AudienceInternal.
		Audience: auth.AudienceInternal,
	})

	userAdapter := adapters.NewUserAdapter(pool, uuid.Nil)
	userSvc := auth.NewUserService(userAdapter)
	sessionSvc := auth.NewSessionService(adapters.NewSessionAdapter(queries), auth.SessionConfig{TTL: cfg.JWTExpiry})
	// The user adapter doubles as the per-request auth-state store: the
	// token_generation + is_active check that makes deactivation and force
	// logout effective on the very next request (P2.5 session control).
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc, userAdapter)
	membershipResolver := adapters.NewMembershipAdapter(queries)
	workflowAdapter := adapters.NewWorkflowAdapter(queries)
	workflowEngine := workflow.NewDBEngine(workflowAdapter)
	orgProvisioner := adapters.NewOrgProvisionerAdapterWithWorkflows(queries, workflowAdapter)

	// The content-transaction adapter carries the ADR-0008 share invariants:
	// deleting an entity, or moving a page across spaces, revokes the
	// affected shares in the SAME transaction (with the share.revoked audit
	// rows in that transaction). One instance satisfies the delete/move seam
	// of all three module services.
	contentTx := adapters.NewContentTxAdapter(pool)

	ticketAdapter := adapters.NewTicketAdapter(queries)
	ticketSvc := tickets.NewTicketService(ticketAdapter, contentTx)
	// The ticket_ref typeahead reads across every space the caller can see,
	// which no space-scoped ticket read does. It hangs off its own store seam
	// rather than widening TicketRepository, whose every method is scoped to
	// a single space id.
	ticketSuggestSvc := tickets.NewSuggestionService(ticketAdapter)

	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(pool)
	itemSvc := projects.NewItemService(itemAdapter, contentTx)
	sprintSvc := projects.NewSprintService(sprintAdapter)
	// Board configuration validates against the same workflow-state vocabulary
	// the board has always derived its columns from, so a configuration cannot
	// be valid on one surface and wrong on the other.
	boardConfigSvc := projects.NewBoardConfigService(
		adapters.NewBoardConfigAdapter(pool),
		adapters.NewWorkflowStatusAdapter(pool),
	)
	itemTypeSvc := itemtypes.NewService(adapters.NewItemTypeAdapter(queries))
	customFieldSvc := customfields.NewService(
		adapters.NewCustomFieldDefAdapter(queries),
		adapters.NewCustomFieldValueAdapter(queries),
	)

	wikiSvc := wiki.NewService(queries, contentTx)

	spaHandler, err := newSPAHandler()
	if err != nil {
		return nil, fmt.Errorf("creating SPA handler: %w", err)
	}

	auditLog := audit.NewDBLogger(queries)

	// v0.3 access control (ADR-0006/0007): teams, grants, per-request
	// resolution. The org provisioner seeds the default team and enrols new
	// users so nobody is ever teamless.
	teamAdapter := adapters.NewTeamAdapter(pool)
	teamSvc := teams.NewService(teamAdapter)
	accessAdapter := adapters.NewAccessAdapter(pool)
	shareAdapter := adapters.NewShareAdapter(pool)
	// The resolver gains the share store so ResolveShares can union entity
	// shares into cross-space reads (P3). Space-scoped resolution is
	// untouched — shares are resolved only on the /shared subtree.
	accessResolver := access.NewResolver(accessAdapter).WithShareStore(shareAdapter)
	grantSvc := access.NewGrantService(accessAdapter)
	spaceCreateAdapter := adapters.NewSpaceCreateAdapter(pool)
	shareSvc := access.NewShareService(shareAdapter)
	explainer := access.NewExplainer(accessAdapter, accessAdapter)
	orgProvisioner.WithTeamSeeder(teamAdapter)
	orgProvisioner.WithItemTypeSeeder(adapters.NewItemTypeAdapter(queries))

	// Entity attachments (P3): the first production consumer of the object
	// store (known-issues #16). A misconfigured or unreachable store leaves
	// the attachment handler nil — the feature is absent, nothing else
	// breaks — exactly the pre-P3 behaviour.
	var attachmentHandler *attachmentsapi.Handler
	var blobStore storage.ObjectStore
	// The Codex document surface needs an image store as a REQUIRED dependency,
	// so a deployment without object storage gets one that refuses loudly rather
	// than a nil that would disable the whole editor, text included.
	var pageImages wiki.ImageStore = wiki.UnavailableImageStore{}
	if bs, err := newObjectStore(context.Background(), cfg); err != nil {
		slog.Warn("object storage disabled: attachments, avatars and page images unavailable", "error", err)
	} else {
		blobStore = bs
		attachmentSvc := attachments.NewService(adapters.NewAttachmentAdapter(pool), blobStore)
		attachmentHandler = attachmentsapi.NewHandler(attachmentSvc, shareSvc)
		pageImages = attachmentSvc
	}
	// Codex tags (migration 040). One service serves both callers: the tag
	// endpoints on the wiki handler, and the publish path's aggregation of a
	// document's inline `#tag` tokens.
	tagSvc := tags.NewService(adapters.NewTagAdapter(queries, pool))
	wikiDocs := wiki.NewDocumentService(queries, contentTx, pageImages, tagSvc)

	// Read once, at boot, and handed to every handler that accepts a ticket
	// reference. Deliberately not a runtime settings row: turning this on
	// changes what every administrative action requires, and a restart is the
	// honest cost of that. One value shared by all six handlers is also what
	// stops the surfaces disagreeing about whether a reference is mandatory.
	ticketRefPolicy := ticketref.Policy{Required: cfg.TicketRefRequired}

	// The shared-entity reader projects each module entity into a
	// container-free view (no space, tree, siblings, or comments).
	sharedReader := sharesapi.NewServiceReader(wikiSvc, ticketSvc, itemSvc)
	shareHandler := sharesapi.NewHandler(shareSvc, sharedReader).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy)

	// Saved views (P4, ADR-0009). One adapter satisfies both seams: the view
	// rows and the two cross-space result fan-outs.
	savedViewAdapter := adapters.NewSavedViewAdapter(pool)
	viewHandler := viewsapi.NewHandler(
		views.NewService(savedViewAdapter, savedViewAdapter),
		views.NewQueueService(savedViewAdapter),
	)

	// P2.5 administration: people lifecycle, invites, bulk grants, audit
	// viewer. Invite delivery follows config — link mode returns the URL to
	// the admin; email mode sends it (SMTP validated at startup).
	peopleAdapter := adapters.NewPeopleAdapter(pool)
	peopleSvc := people.NewService(peopleAdapter)

	// Avatars reuse the shared object store; the handler is nil (feature
	// absent) when object storage is unavailable, exactly like attachments.
	var avatarHandler *avatarapi.Handler
	if blobStore != nil {
		avatarHandler = avatarapi.NewHandler(people.NewAvatarService(peopleAdapter, blobStore)).WithAuditLogger(auditLog)
	}
	var inviteSender invites.Sender
	if cfg.InviteDelivery == config.InviteDeliveryEmail {
		inviteSender = adapters.NewInviteEmailSender(sender, queries)
	}
	inviteSvc := invites.NewService(adapters.NewInviteAdapter(pool), inviteSender, invites.Config{
		TTL:            cfg.InviteTTL,
		DeliverByEmail: cfg.InviteDelivery == config.InviteDeliveryEmail,
		BaseURL:        cfg.AppBaseURL,
	})
	// Customer portal. The token service shares the RSA key with the internal
	// family and separates on the audience claim — see auth.AudienceInternal
	// and portal.AudiencePortal, which are verified against each other in
	// both directions.
	portalTokens := portal.NewTokenService(portal.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		SessionTTL: cfg.PortalSessionTTL,
		Issuer:     "azimuthal",
	})
	var portalSender portal.Sender
	if cfg.PortalLinkDelivery == config.PortalLinkDeliveryEmail {
		portalSender = adapters.NewPortalLinkSender(sender)
	}
	portalSvc := portal.NewService(adapters.NewPortalAdapter(pool), portalTokens, portalSender, portal.Config{
		LinkTTL:        cfg.PortalLinkTTL,
		DeliverByEmail: cfg.PortalLinkDelivery == config.PortalLinkDeliveryEmail,
		// Disclosing the sign-in URL to an unauthenticated caller is an
		// authentication bypass, so it is gated on the delivery mode that
		// config.validate refuses in production — belt as well as braces,
		// because the config check is the only thing preventing it and a
		// second reader of that decision here makes the coupling visible.
		DiscloseLink: cfg.PortalLinkDelivery == config.PortalLinkDeliveryLink && !cfg.IsProduction(),
		BaseURL:      cfg.AppBaseURL,
	})
	portalHandler := portalapi.NewHandler(portalSvc).
		WithAuditLogger(auditLog).
		WithSpaceTypes(func(ctx context.Context, spaceID uuid.UUID) (string, error) {
			sp, err := queries.GetSpaceByID(ctx, spaceID)
			if err != nil {
				return "", fmt.Errorf("resolving space type: %w", err)
			}
			return sp.Type, nil
		}).
		WithNotifier(func(args jobs.NotificationArgs) {
			if err := notifEnqueuer.EnqueueNotification(context.Background(), args); err != nil {
				slog.Warn("portal reply notification not enqueued", "error", err)
			}
		})

	bulkSvc := access.NewBulkService(adapters.NewBulkGrantAdapter(pool))
	auditReader := audit.NewReader(adapters.NewAuditReaderAdapter(queries))

	return api.NewRouter(api.RouterConfig{
		Authenticator: authenticator,
		AuthHandler: authapi.NewHandler(userSvc, jwtSvc, sessionSvc, membershipResolver, orgProvisioner, userAdapter).
			WithAuditLogger(auditLog).
			WithRegistrationPolicy(cfg.AllowRegistration),
		TicketHandler:       ticketsapi.NewHandler(ticketSvc).WithAuditLogger(auditLog).WithNotificationEnqueuer(notifEnqueuer).WithSuggestions(ticketSuggestSvc),
		WikiHandler:         wikiapi.NewHandler(wikiSvc, wikiDocs, tagSvc).WithAuditLogger(auditLog).WithShareQueries(shareAdapter),
		ProjectHandler:      projectsapi.NewHandler(itemSvc, sprintSvc, projects.NewBacklogService(itemAdapter, sprintAdapter), projects.NewRoadmapService(itemAdapter, sprintAdapter), projects.NewRelationService(adapters.NewRelationAdapter(queries)), projects.NewLabelService(adapters.NewLabelAdapter(queries))).WithAuditLogger(auditLog).WithItemTypes(itemTypeSvc).WithCustomFields(customFieldSvc).WithBoardConfig(boardConfigSvc),
		SpaceHandler:        spacesapi.NewHandler(queries).WithWorkflowAssigner(workflowAdapter).WithTeamService(teamSvc).WithGrantService(grantSvc).WithSpaceCreateTx(spaceCreateAdapter).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy),
		CommentHandler:      commentsapi.NewHandler(queries).WithAuditLogger(auditLog).WithNotificationEnqueuer(notifEnqueuer),
		NotificationHandler: notificationsapi.NewHandler(queries),
		WorkflowHandler:     workflowsapi.NewHandler(queries, workflowAdapter, workflowEngine),
		TeamHandler:         teamsapi.NewHandler(teamSvc).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy),
		GrantHandler:        grantsapi.NewHandler(grantSvc, explainer).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy),
		ShareHandler:        shareHandler,
		AttachmentHandler:   attachmentHandler,
		AdminHandler:        adminapi.NewHandler(peopleSvc, bulkSvc, auditReader).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy),
		InviteHandler:       invitesapi.NewHandler(inviteSvc, jwtSvc).WithAuditLogger(auditLog).WithTicketRefPolicy(ticketRefPolicy),
		AvatarHandler:       avatarHandler,
		ViewHandler:         viewHandler,
		PortalHandler:       portalHandler,
		PortalService:       portalSvc,
		SPAHandler:          spaHandler,
		AllowedOrigins:      cfg.AllowedOrigins,
		QueueStatus:         queueStatus,
		SpaceOrgResolver:    spaceOrgResolver(queries),
		AccessResolver:      accessResolver,
	}), nil
}

// newObjectStore constructs the attachment object store from config and
// ensures its bucket exists. STORAGE_ENDPOINT may carry an http(s):// scheme
// (as .env.test does); minio-go wants a bare host:port with Secure set
// separately, so the scheme is stripped and used to seed useSSL (an explicit
// STORAGE_USE_SSL still wins). A blank endpoint means "no object store".
func newObjectStore(ctx context.Context, cfg *config.Config) (storage.ObjectStore, error) {
	if cfg.StorageEndpoint == "" {
		return nil, fmt.Errorf("STORAGE_ENDPOINT not set")
	}
	endpoint := cfg.StorageEndpoint
	useSSL := cfg.StorageUseSSL
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		endpoint = strings.TrimPrefix(endpoint, "https://")
		useSSL = true
	case strings.HasPrefix(endpoint, "http://"):
		endpoint = strings.TrimPrefix(endpoint, "http://")
	}
	store, err := storage.NewS3Store(endpoint, cfg.StorageAccessKey, cfg.StorageSecretKey, cfg.StorageBucket, useSSL)
	if err != nil {
		return nil, fmt.Errorf("creating object store: %w", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return nil, fmt.Errorf("ensuring bucket: %w", err)
	}
	return store, nil
}

// spaceOrgResolver returns the org that owns a space, backing the router's
// org+space scoping guard.
func spaceOrgResolver(queries *generated.Queries) api.SpaceOrgResolver {
	return func(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error) {
		s, err := queries.GetSpaceByID(ctx, spaceID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("resolving space org: %w", err)
		}
		return s.OrgID, nil
	}
}

// newSPAHandler returns an http.Handler that serves the embedded frontend
// assets. For any request that doesn't match a static file, it falls back
// to index.html so the React Router can handle client-side routing.
func newSPAHandler() (http.Handler, error) {
	distFS, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("creating sub filesystem: %w", err)
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Never intercept API routes — return a proper 404 so clients
		// get JSON errors instead of index.html.
		if strings.HasPrefix(path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly
		if path != "/" {
			cleanPath := strings.TrimPrefix(path, "/")
			if f, err := distFS.(fs.ReadFileFS).ReadFile(cleanPath); err == nil {
				_ = f // file exists, let the file server handle it
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}), nil
}
