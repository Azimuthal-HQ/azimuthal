package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// The server's logger is built before its configuration is loaded, so LOG_LEVEL
// cannot be read at construction time. newLogger returns a LevelVar for exactly
// that reason, and runServe calls Set on it once the config is in hand.
//
// What this pins is the property the design depends on: re-levelling reaches a
// logger that was ALREADY HANDED OUT. If somebody "simplifies" newLogger to take
// a plain slog.Level, the LevelVar disappears, the re-levelling has nothing to
// act on, and LOG_LEVEL goes back to being the inert setting this replaced.
// That change does not fail to compile — it fails here.
func TestNewLogger_RelevelsALoggerAlreadyHandedOut(t *testing.T) {
	var buf bytes.Buffer
	logger, level := newLogger(&buf)

	// Derived before the level is known, exactly as slog.SetDefault's consumers
	// are: middleware and handlers take the default logger at wiring time.
	early := logger.With("component", "early")

	early.Debug("suppressed at the default level")
	if buf.Len() != 0 {
		t.Fatalf("a Debug line must not appear at the Info default, got %q", buf.String())
	}

	level.Set(slog.LevelDebug)

	early.Debug("emitted after re-levelling")
	got := buf.String()
	if !strings.Contains(got, "emitted after re-levelling") {
		t.Errorf("re-levelling must reach a logger derived before Set, got %q", got)
	}
	if !strings.Contains(got, `"component":"early"`) {
		t.Errorf("the derived logger's attributes must survive, got %q", got)
	}
}

// The other direction: raising the floor silences what was previously emitted.
// This is the case an operator actually asks for with LOG_LEVEL=error, and the
// one that used to do nothing.
func TestNewLogger_RaisingTheLevelSilencesLowerLines(t *testing.T) {
	var buf bytes.Buffer
	logger, level := newLogger(&buf)

	logger.Info("visible at the default")
	if !strings.Contains(buf.String(), "visible at the default") {
		t.Fatalf("Info must be emitted at the default level, got %q", buf.String())
	}

	buf.Reset()
	level.Set(slog.LevelError)

	logger.Info("should now be suppressed")
	logger.Warn("should also be suppressed")
	if buf.Len() != 0 {
		t.Errorf("LOG_LEVEL=error must suppress Info and Warn, got %q", buf.String())
	}

	logger.Error("errors still get through")
	if !strings.Contains(buf.String(), "errors still get through") {
		t.Errorf("Error must survive LOG_LEVEL=error, got %q", buf.String())
	}
}

// newLogger's default must be Info, because it is what the server logs at
// between process start and the config load — including the log line reporting
// that the config load FAILED.
func TestNewLogger_DefaultsToInfoBeforeConfigIsLoaded(t *testing.T) {
	var buf bytes.Buffer
	logger, level := newLogger(&buf)

	if level.Level() != slog.LevelInfo {
		t.Errorf("expected an Info default, got %v", level.Level())
	}
	logger.Info("starting azimuthal")
	if !strings.Contains(buf.String(), "starting azimuthal") {
		t.Errorf("the pre-config startup line must be emitted, got %q", buf.String())
	}
}
