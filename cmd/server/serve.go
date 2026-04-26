package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

// serveCmd starts the HTTP server. It is also the default action.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Azimuthal HTTP server",
	RunE:  runServe,
}

// runServe loads config, connects to the DB, runs migrations, and starts the
// HTTP server with graceful shutdown.
func runServe(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting azimuthal", "version", Version, "build_time", BuildTime)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	slog.Info("configuration loaded", "env", cfg.AppEnv, "port", cfg.AppPort)

	srv, cleanup, err := newServer(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("http server listening", "port", cfg.AppPort)
		if listenErr := srv.ListenAndServe(); listenErr != nil && listenErr != http.ErrServerClosed {
			slog.Error("http server error", "error", listenErr)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop accepting new HTTP requests, then drain in-flight jobs and close
	// the DB pool. Order matters: the queue must finish before the pool
	// closes since workers acquire connections from it.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		cleanup(shutdownCtx)
		return fmt.Errorf("shutting down server: %w", err)
	}
	cleanup(shutdownCtx)

	slog.Info("shutdown complete")
	return nil
}
