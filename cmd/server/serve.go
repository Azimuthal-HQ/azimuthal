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

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
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

// warnIfDisclosureFlagIgnored tells an operator that a setting they went out of
// their way to turn on is doing nothing.
//
// AZIMUTHAL_PORTAL_DISCLOSE_LINK=true outside a development environment is safe —
// the portal discloses only on the APP_ENV safelist, by
// config.Config.PortalLinkDisclosureAllowed — but it was also SILENT, and
// silence is the failure this closes. An operator who set the flag, restarted,
// and saw a clean startup would reasonably conclude it was in force. That is
// most likely on a staging host, whose name is neither "production" nor a
// development environment: under the blocklist this replaced, staging disclosed
// with no warning at all. internal/config states the principle at parseLogLevel:
// a value the server ignores is worse than one it rejects.
//
// A warning rather than a boot refusal, by ruling: refusing would turn an
// already-safe misconfiguration into an outage, which is a bad trade to ship
// inside a security patch.
//
// It takes the logger rather than reaching for the package default so that the
// emission can be tested without a server or a global — the same reason
// newLogger is its own function. The message names the actual APP_ENV so the
// staging operator sees their own value, not a production-only sentence that
// would read as "not about me".
func warnIfDisclosureFlagIgnored(logger *slog.Logger, cfg *config.Config) {
	if !cfg.PortalDisclosureFlagIgnored() {
		return
	}
	logger.Warn(fmt.Sprintf("AZIMUTHAL_PORTAL_DISCLOSE_LINK=true has no effect when APP_ENV=%q; "+
		"the portal sign-in URL is disclosed only in a development environment "+
		"(APP_ENV=development or test) and is never disclosed otherwise", cfg.AppEnv))
}

// warnIfSMTPAuthWithoutTLS tells an operator that their SMTP credentials will
// travel in the clear.
//
// SMTP_USERNAME/SMTP_PASSWORD set with SMTP_TLS=none is a WARN, not a boot
// refusal — an operator's lab against a local relay is a legitimate use, and a
// hard refusal shipped in a security patch could lock out a working setup over a
// combination that is disclosed rather than dangerous (the same
// WARN-not-refuse call the portal disclosure flag makes above). But it is never
// SILENT: an operator who configured auth expecting it to be protected must be
// told it is not. See config.Config.SMTPAuthWithoutTLS.
func warnIfSMTPAuthWithoutTLS(logger *slog.Logger, cfg *config.Config) {
	if !cfg.SMTPAuthWithoutTLS() {
		return
	}
	logger.Warn("SMTP_USERNAME/SMTP_PASSWORD are set but SMTP_TLS=none: the credentials will be " +
		"sent over an unencrypted connection and can be read on the wire. Set SMTP_TLS=starttls or " +
		"SMTP_TLS=implicit for anything but a trusted local relay.")
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
	warnIfDisclosureFlagIgnored(logger, cfg)
	warnIfSMTPAuthWithoutTLS(logger, cfg)

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
