package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// serveCmd starts the HTTP server. It is also the default action.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Azimuthal HTTP server",
	RunE:  runServe,
}

// newLogger builds the server's logger and returns it alongside the LevelVar
// that controls it.
//
// THE LEVEL IS A LevelVar RATHER THAN A FIXED Level because the logger has to
// exist before the config does: loading the config can itself fail, and that
// failure has to be logged somewhere. So the handler starts at Info and is
// re-levelled IN PLACE once LOG_LEVEL is known — the package default, and any
// logger already derived from this one, follow the change without being
// rebuilt or re-installed.
//
// This is what makes LOG_LEVEL mean anything at all. It was parsed into
// Config.LogLevel and read by nobody, while this handler hardcoded
// slog.LevelInfo, so both env tables promised debug/warn/error worked and
// none of them did.
//
// It is a separate function so the re-levelling can be tested. Inline, the
// only way to reach it would be to start a server.
func newLogger(w io.Writer) (*slog.Logger, *slog.LevelVar) {
	level := new(slog.LevelVar) // Info until told otherwise
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})), level
}

// runServe loads config, connects to the DB, runs migrations, and starts the
// HTTP server with graceful shutdown.
func runServe(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true // runtime failure, not a usage error — see TestCommands_SilenceUsageOnRuntimeFailure
	logger, logLevel := newLogger(os.Stdout)
	slog.SetDefault(logger)

	slog.Info("starting azimuthal", "version", Version, "build_time", BuildTime)

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	logLevel.Set(cfg.LogLevel)

	// Deliberately after the re-levelling: at LOG_LEVEL=warn or error this line
	// is meant to disappear, and an operator who asked for a quiet server and
	// still got a startup banner would reasonably conclude the setting is
	// inert — which is the exact bug being fixed.
	slog.Info("configuration loaded", "env", cfg.AppEnv, "port", cfg.AppPort, "log_level", cfg.LogLevel)

	srv, deps, cleanup, err := newServer(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

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

	// Drain in-flight jobs before closing the DB pool.
	if err := deps.stopQueue(shutdownCtx); err != nil {
		slog.Warn("job queue did not drain cleanly", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}

	slog.Info("shutdown complete")
	return nil
}
