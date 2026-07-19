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
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
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

	handler, err := buildRouter(cfg, pool, queries, notifEnqueuer, queueStatus)
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
func buildRouter(cfg *config.Config, pool *pgxpool.Pool, queries *generated.Queries, notifEnqueuer jobs.NotificationEnqueuer, queueStatus string) (http.Handler, error) { //nolint:funlen // router wiring naturally enumerates all dependencies, like newServer above
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
	})

	userAdapter := adapters.NewUserAdapter(pool, uuid.Nil)
	userSvc := auth.NewUserService(userAdapter)
	sessionSvc := auth.NewSessionService(adapters.NewSessionAdapter(queries), auth.SessionConfig{TTL: cfg.JWTExpiry})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)
	membershipResolver := adapters.NewMembershipAdapter(queries)
	workflowAdapter := adapters.NewWorkflowAdapter(queries)
	workflowEngine := workflow.NewDBEngine(workflowAdapter)
	orgProvisioner := adapters.NewOrgProvisionerAdapterWithWorkflows(queries, workflowAdapter)

	ticketAdapter := adapters.NewTicketAdapter(queries)
	ticketSvc := tickets.NewTicketService(ticketAdapter)

	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(queries)
	itemSvc := projects.NewItemService(itemAdapter)
	sprintSvc := projects.NewSprintService(sprintAdapter)

	wikiSvc := wiki.NewService(queries)
	wikiLocks := wiki.NewLockService(queries)

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
	accessResolver := access.NewResolver(accessAdapter)
	grantSvc := access.NewGrantService(accessAdapter)
	explainer := access.NewExplainer(accessAdapter, accessAdapter)
	orgProvisioner.WithTeamSeeder(teamAdapter)

	return api.NewRouter(api.RouterConfig{
		Authenticator:       authenticator,
		AuthHandler:         authapi.NewHandler(userSvc, jwtSvc, sessionSvc, membershipResolver, orgProvisioner).WithAuditLogger(auditLog),
		TicketHandler:       ticketsapi.NewHandler(ticketSvc).WithAuditLogger(auditLog).WithNotificationEnqueuer(notifEnqueuer),
		WikiHandler:         wikiapi.NewHandler(wikiSvc, wikiLocks).WithAuditLogger(auditLog),
		ProjectHandler:      projectsapi.NewHandler(itemSvc, sprintSvc, projects.NewBacklogService(itemAdapter, sprintAdapter), projects.NewRoadmapService(itemAdapter, sprintAdapter), projects.NewRelationService(adapters.NewRelationAdapter(queries)), projects.NewLabelService(adapters.NewLabelAdapter(queries))).WithAuditLogger(auditLog),
		SpaceHandler:        spacesapi.NewHandler(queries).WithWorkflowAssigner(workflowAdapter).WithTeamService(teamSvc).WithGrantService(grantSvc).WithAuditLogger(auditLog),
		CommentHandler:      commentsapi.NewHandler(queries).WithAuditLogger(auditLog).WithNotificationEnqueuer(notifEnqueuer),
		NotificationHandler: notificationsapi.NewHandler(queries),
		WorkflowHandler:     workflowsapi.NewHandler(queries, workflowAdapter, workflowEngine),
		TeamHandler:         teamsapi.NewHandler(teamSvc).WithAuditLogger(auditLog),
		GrantHandler:        grantsapi.NewHandler(grantSvc, explainer).WithAuditLogger(auditLog),
		SPAHandler:          spaHandler,
		AllowedOrigins:      cfg.AllowedOrigins,
		QueueStatus:         queueStatus,
		SpaceOrgResolver:    spaceOrgResolver(queries),
		AccessResolver:      accessResolver,
	}), nil
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
