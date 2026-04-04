// Package main is the single binary entrypoint for Azimuthal.
// It wires together config, database, background jobs, and the HTTP server.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

// newServer builds an http.Server with the full API router backed by the
// database. It connects to the database, runs migrations, constructs all
// services with their DB-backed adapters, and calls api.NewRouter with the
// full RouterConfig.
func newServer(cfg *config.Config) (*http.Server, func(), error) {
	ctx := context.Background()
	noop := func() {}

	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return nil, noop, fmt.Errorf("connecting to database: %w", err)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("running migrations: %w", err)
	}

	queries := generated.New(pool)

	orgID, err := ensureDefaultOrg(ctx, queries)
	if err != nil {
		pool.Close()
		return nil, noop, fmt.Errorf("bootstrapping default org: %w", err)
	}

	handler, err := buildRouter(cfg, queries, orgID)
	if err != nil {
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

	return srv, func() { pool.Close() }, nil
}

// buildRouter constructs all domain services with DB-backed adapters and
// returns the fully wired API router.
func buildRouter(cfg *config.Config, queries *generated.Queries, orgID uuid.UUID) (http.Handler, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating RSA key: %w", err)
	}

	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  cfg.JWTExpiry,
		RefreshTTL: cfg.JWTExpiry * 7,
		Issuer:     "azimuthal",
	})

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, orgID))
	sessionSvc := auth.NewSessionService(adapters.NewSessionAdapter(queries), auth.SessionConfig{TTL: cfg.JWTExpiry})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)

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
		Authenticator:  authenticator,
		AuthHandler:    authapi.NewHandler(userSvc, jwtSvc, sessionSvc),
		TicketHandler:  ticketsapi.NewHandler(ticketSvc),
		WikiHandler:    wikiapi.NewHandler(wikiSvc),
		ProjectHandler: projectsapi.NewHandler(itemSvc, sprintSvc, projects.NewBacklogService(itemAdapter, sprintAdapter), projects.NewRoadmapService(itemAdapter, sprintAdapter), projects.NewRelationService(adapters.NewRelationAdapter(queries)), projects.NewLabelService(adapters.NewLabelAdapter(queries))),
		SpaceHandler:   spacesapi.NewHandler(queries),
		SPAHandler:     spaHandler,
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
