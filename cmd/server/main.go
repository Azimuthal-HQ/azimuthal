// Package main is the single binary entrypoint for Azimuthal.
// It wires together config, database, background jobs, and the HTTP server.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Version and BuildTime are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting azimuthal", "version", Version, "build_time", BuildTime)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("configuration loaded", "env", cfg.AppEnv, "port", cfg.AppPort)

	srv, cleanup, err := newServer(cfg)
	if err != nil {
		slog.Error("failed to initialise server", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("http server listening", "port", cfg.AppPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	slog.Info("shutdown complete")
}

// newServer builds an http.Server with the full API router backed by the
// database. It connects to the database, runs migrations, constructs all
// services with their DB-backed adapters, and calls api.NewRouter with the
// full RouterConfig.
func newServer(cfg *config.Config) (*http.Server, func(), error) {
	ctx := context.Background()
	noop := func() {}

	// 1. Database connection
	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return nil, noop, fmt.Errorf("connecting to database: %w", err)
	}

	// 2. Run migrations
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("running migrations: %w", err)
	}

	queries := generated.New(pool)

	// 3. Bootstrap default organisation
	orgID, err := ensureDefaultOrg(ctx, queries)
	if err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("bootstrapping default org: %w", err)
	}

	// 4. Generate RSA key pair for JWT signing
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("generating RSA key: %w", err)
	}

	// 5. Construct auth services
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  cfg.JWTExpiry,
		RefreshTTL: cfg.JWTExpiry * 7,
		Issuer:     "azimuthal",
	})

	userAdapter := adapters.NewUserAdapter(queries, orgID)
	userSvc := auth.NewUserService(userAdapter)

	sessionAdapter := adapters.NewSessionAdapter(queries)
	sessionSvc := auth.NewSessionService(sessionAdapter, auth.SessionConfig{
		TTL: cfg.JWTExpiry,
	})

	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)

	// 6. Construct ticket service
	ticketAdapter := adapters.NewTicketAdapter(queries)
	ticketSvc := tickets.NewTicketService(ticketAdapter)

	// 7. Construct project services
	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(queries)
	relationAdapter := adapters.NewRelationAdapter(queries)
	labelAdapter := adapters.NewLabelAdapter(queries)

	itemSvc := projects.NewItemService(itemAdapter)
	sprintSvc := projects.NewSprintService(sprintAdapter)
	backlogSvc := projects.NewBacklogService(itemAdapter, sprintAdapter)
	roadmapSvc := projects.NewRoadmapService(itemAdapter, sprintAdapter)
	relationSvc := projects.NewRelationService(relationAdapter)
	labelSvc := projects.NewLabelService(labelAdapter)

	// 8. Construct wiki service (PageStore is satisfied by *generated.Queries directly)
	wikiSvc := wiki.NewService(queries)

	// 9. Construct API handlers
	authHandler := authapi.NewHandler(userSvc, jwtSvc, sessionSvc)
	ticketHandler := ticketsapi.NewHandler(ticketSvc)
	projectHandler := projectsapi.NewHandler(itemSvc, sprintSvc, backlogSvc, roadmapSvc, relationSvc, labelSvc)
	wikiHandler := wikiapi.NewHandler(wikiSvc)
	spaceHandler := spacesapi.NewHandler(queries)

	// 10. Build the full API router
	handler := api.NewRouter(api.RouterConfig{
		Authenticator:  authenticator,
		AuthHandler:    authHandler,
		TicketHandler:  ticketHandler,
		WikiHandler:    wikiHandler,
		ProjectHandler: projectHandler,
		SpaceHandler:   spaceHandler,
	})

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.AppPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	cleanup := func() {
		pool.Close()
	}

	return srv, cleanup, nil
}

// ensureDefaultOrg creates or retrieves the default organization. In a
// single-tenant deployment this is the only org; multi-tenant support can
// be added later.
func ensureDefaultOrg(ctx context.Context, q *generated.Queries) (uuid.UUID, error) {
	org, err := q.GetOrganizationBySlug(ctx, "default")
	if err == nil {
		return org.ID, nil
	}

	desc := "Default organisation"
	org, err = q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:          uuid.New(),
		Slug:        "default",
		Name:        "Default",
		Description: &desc,
		Plan:        "free",
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("creating default org: %w", err)
	}
	slog.Info("created default organisation", "org_id", org.ID)
	return org.ID, nil
}
