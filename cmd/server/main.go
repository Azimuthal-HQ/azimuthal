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
// @tag.description Notifications — in-app alerts for the current user
// @tag.name        health
// @tag.description Health — liveness and readiness probes
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	notifyapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
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

// queueHealthProvider tracks the current state of the background job queue
// for /health. It is safe for concurrent reads via atomic.Value.
type queueHealthProvider struct {
	state atomic.Value // api.QueueStatus
}

func newQueueHealthProvider(initial api.QueueStatus) *queueHealthProvider {
	p := &queueHealthProvider{}
	p.state.Store(initial)
	return p
}

func (p *queueHealthProvider) set(s api.QueueStatus) { p.state.Store(s) }

// QueueStatus returns the most recently observed queue state.
func (p *queueHealthProvider) QueueStatus() api.QueueStatus {
	if v, ok := p.state.Load().(api.QueueStatus); ok {
		return v
	}
	return api.QueueStatusError
}

// newServer builds an http.Server with the full API router backed by the
// database. It connects to the database, runs migrations, constructs all
// services with their DB-backed adapters, and calls api.NewRouter with the
// full RouterConfig. The returned cleanup function closes the pool and
// drains the background job queue.
func newServer(cfg *config.Config) (*http.Server, func(context.Context), error) {
	ctx := context.Background()
	noop := func(context.Context) {}

	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return nil, noop, fmt.Errorf("connecting to database: %w", err)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("running migrations: %w", err)
	}

	queries := generated.New(pool)
	auditRecorder := audit.NewDBLogger(queries)
	notifyService := notifications.NewService(queries)

	healthProvider := newQueueHealthProvider(api.QueueStatusDisabled)
	queue, queueErr := startQueue(ctx, cfg, pool, notifyService, healthProvider)
	if queueErr != nil {
		// Queue failure is logged and surfaced via /health, but does not
		// abort startup — sync paths (audit, notifications API) still work.
		slog.Error("queue startup failed", "error", queueErr)
		healthProvider.set(api.QueueStatusError)
	}

	handler, err := buildRouter(cfg, queries, auditRecorder, notifyService, healthProvider)
	if err != nil {
		if queue != nil {
			_ = queue.Stop(ctx)
		}
		pool.Close()
		return nil, noop, err
	}

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.AppPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	cleanup := func(shutdownCtx context.Context) {
		if queue != nil {
			if err := queue.Stop(shutdownCtx); err != nil {
				slog.Error("queue drain failed", "error", err)
			}
		}
		pool.Close()
	}

	return srv, cleanup, nil
}

// startQueue applies River's schema migrations and starts the background
// queue when AZIMUTHAL_QUEUE_ENABLED is true (the default). When disabled,
// the function logs a warning and returns (nil, nil) so the caller proceeds
// without async workers. Any failure leaves the queue unstarted but does
// not abort startup.
func startQueue(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, notifier notifications.Recorder, health *queueHealthProvider) (*jobs.Queue, error) {
	if !cfg.QueueEnabled {
		slog.Warn("background job queue disabled (AZIMUTHAL_QUEUE_ENABLED=false)")
		health.set(api.QueueStatusDisabled)
		return nil, nil
	}

	if err := jobs.Migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("river schema migrations: %w", err)
	}

	queue, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, notifier)
	if err != nil {
		return nil, fmt.Errorf("creating queue: %w", err)
	}
	if err := queue.Start(ctx); err != nil {
		return nil, fmt.Errorf("starting queue: %w", err)
	}
	health.set(api.QueueStatusOK)
	slog.Info("background job queue started")
	return queue, nil
}

// buildRouter constructs all domain services with DB-backed adapters and
// returns the fully wired API router.
func buildRouter(cfg *config.Config, queries *generated.Queries, recorder audit.Recorder, notifyService *notifications.Service, health api.HealthProvider) (http.Handler, error) {
	privateKey, err := auth.LoadOrGenerateRSAKey(cfg.JWTPrivateKeyPath)
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

	userAdapter := adapters.NewUserAdapter(queries, uuid.Nil)
	userSvc := auth.NewUserService(userAdapter)
	sessionSvc := auth.NewSessionService(adapters.NewSessionAdapter(queries), auth.SessionConfig{TTL: cfg.JWTExpiry})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)
	membershipResolver := adapters.NewMembershipAdapter(queries)
	orgProvisioner := adapters.NewOrgProvisionerAdapter(queries)

	ticketSvc := tickets.NewTicketService(adapters.NewTicketAdapter(queries))

	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(queries)
	itemSvc := projects.NewItemService(itemAdapter)
	sprintSvc := projects.NewSprintService(sprintAdapter)

	wikiSvc := wiki.NewService(queries)

	spaHandler, err := newSPAHandler()
	if err != nil {
		return nil, fmt.Errorf("creating SPA handler: %w", err)
	}

	return api.NewRouter(api.RouterConfig{
		Authenticator:       authenticator,
		AuthHandler:         authapi.NewHandler(userSvc, jwtSvc, sessionSvc, membershipResolver, orgProvisioner, recorder),
		TicketHandler:       ticketsapi.NewHandler(ticketSvc, recorder, notifyService),
		WikiHandler:         wikiapi.NewHandler(wikiSvc, recorder),
		ProjectHandler:      projectsapi.NewHandler(itemSvc, sprintSvc, projects.NewBacklogService(itemAdapter, sprintAdapter), projects.NewRoadmapService(itemAdapter, sprintAdapter), projects.NewRelationService(adapters.NewRelationAdapter(queries)), projects.NewLabelService(adapters.NewLabelAdapter(queries)), recorder, notifyService),
		SpaceHandler:        spacesapi.NewHandler(queries),
		CommentHandler:      commentsapi.NewHandler(queries, recorder, notifyService),
		NotificationHandler: notifyapi.NewHandler(notifyService),
		SPAHandler:          spaHandler,
		AllowedOrigins:      cfg.AllowedOrigins,
		HealthProvider:      health,
	}), nil
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
