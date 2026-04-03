// Package main is the single binary entrypoint for Azimuthal.
// It wires together config, database, background jobs, and the HTTP server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
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

	srv := newServer(cfg.AppPort)

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

// newServer builds an http.Server with the chi router, middleware stack, and
// public endpoints. The full API router (api.NewRouter) requires DB-backed
// repository adapters; see docs/known-issues.md.
func newServer(port int) *http.Server {
	r := chi.NewRouter()
	r.Use(api.Recoverer)
	r.Use(api.RequestID)
	r.Use(api.Logging)
	r.Use(api.CORS)
	r.Get("/health", api.HandleHealth)
	r.Get("/ready", api.HandleReady)

	return &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}
