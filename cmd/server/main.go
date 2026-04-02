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

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
)

// Version, BuildTime, and Edition are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
	Edition   = "community"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting azimuthal",
		"version", Version,
		"build_time", BuildTime,
		"edition", Edition,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", api.HandleHealth)
	mux.HandleFunc("/ready", api.HandleReady)

	portStr := os.Getenv("APP_PORT")
	if portStr == "" {
		portStr = "8080"
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum < 1 || portNum > 65535 {
		slog.Error("APP_PORT must be an integer between 1 and 65535")
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(portNum),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("http server listening", "port", portNum)
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
