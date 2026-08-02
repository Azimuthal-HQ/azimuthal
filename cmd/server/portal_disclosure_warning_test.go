package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

// AZIMUTHAL_PORTAL_DISCLOSE_LINK=true on a production server is SAFE and
// SILENT, and the silence is the defect. config.PortalLinkDisclosureAllowed
// discards the flag, nothing is logged, and an operator who set it, restarted,
// and saw a clean startup would reasonably conclude it was in force.
//
// The ruling was to warn rather than refuse: refusing would turn an already-safe
// misconfiguration into an outage. So the warning IS the whole remedy, which
// makes "is it actually emitted" the assertion the ruling stands or falls on.
//
// Captured through newLogger's io.Writer, the way this package's other logger
// tests do, rather than through slog.SetDefault — warnIfDisclosureFlagIgnored
// takes its logger for exactly this reason.

// warnOutput runs the warning against a config and returns what it logged.
func warnOutput(t *testing.T, cfg *config.Config) string {
	t.Helper()
	var buf bytes.Buffer
	logger, _ := newLogger(&buf)
	warnIfDisclosureFlagIgnored(logger, cfg)
	return buf.String()
}

// TestWarnIfDisclosureFlagIgnored_EmittedOnTheIgnoredCombination is the
// emission test. Delete the logger.Warn call in serve.go and it fails.
//
// It asserts on the VARIABLE NAME rather than the full sentence. The name is the
// stable, greppable part — it is what an operator pastes into a search — while
// the prose around it should be free to improve without a test standing in the
// way. Asserting the whole sentence would make this a change-detector.
func TestWarnIfDisclosureFlagIgnored_EmittedOnTheIgnoredCombination(t *testing.T) {
	got := warnOutput(t, &config.Config{AppEnv: "production", PortalDiscloseLink: true})

	if got == "" {
		t.Fatal("nothing was logged for AZIMUTHAL_PORTAL_DISCLOSE_LINK=true on a production " +
			"server. That combination is silently ignored, and the silence is the whole " +
			"defect this warning closes — see warnIfDisclosureFlagIgnored.")
	}
	if !strings.Contains(got, "AZIMUTHAL_PORTAL_DISCLOSE_LINK") {
		t.Errorf("the warning must name the variable an operator set, got %q", got)
	}
	if !strings.Contains(got, "APP_ENV") {
		t.Errorf("the warning must name the setting that overrode it, or an operator cannot "+
			"tell what to change, got %q", got)
	}
	// A warning, not an error: nothing is broken and nothing is unsafe. Logged
	// at Error this would page somebody at 3am over a server that is behaving
	// exactly as designed.
	if !strings.Contains(got, `"level":"WARN"`) {
		t.Errorf("expected a WARN line, got %q", got)
	}
}

// TestWarnIfDisclosureFlagIgnored_SilentOnEveryOtherCombination is the half that
// keeps the warning worth reading.
//
// Deleting the `if !cfg.PortalDisclosureFlagIgnored() { return }` guard makes
// the warning unconditional, and every row here fails. That matters more than it
// looks: a line that appears on a correctly configured development server
// teaches operators that this warning is noise, and the one time it is real they
// will scroll past it.
func TestWarnIfDisclosureFlagIgnored_SilentOnEveryOtherCombination(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		why  string
	}{
		{
			name: "development with the flag",
			cfg:  config.Config{AppEnv: "development", PortalDiscloseLink: true},
			why:  "this configuration discloses — the operator got what they asked for",
		},
		{
			name: "production without the flag",
			cfg:  config.Config{AppEnv: "production", PortalDiscloseLink: false},
			why:  "nobody asked for anything, so nothing is being ignored",
		},
		{
			name: "development without the flag",
			cfg:  config.Config{AppEnv: "development", PortalDiscloseLink: false},
			why:  "the ordinary developer default",
		},
		{
			name: "test with the flag — what the E2E harness runs",
			cfg:  config.Config{AppEnv: "test", PortalDiscloseLink: true},
			why: "the browser suite sets exactly this; a warning here would appear in " +
				"every E2E run's server output",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := warnOutput(t, &tc.cfg); got != "" {
				t.Errorf("expected silence for APP_ENV=%q flag=%v, got %q\n%s",
					tc.cfg.AppEnv, tc.cfg.PortalDiscloseLink, got, tc.why)
			}
		})
	}
}

// TestRunServeCallsTheDisclosureWarning closes the last gap, and it is the same
// gap this whole pull request is about.
//
// Every test above proves warnIfDisclosureFlagIgnored behaves correctly. Not one
// of them notices if runServe stops calling it — deleting that single line
// leaves the entire file green while no operator ever sees the warning again.
// That is exactly the shape of the original defect: a correct rule with nobody
// calling it, living in a function no test can execute.
//
// runServe cannot be invoked here — it loads config, opens a pool, runs
// migrations and blocks on a signal — so the call is asserted by reading the
// source, the same technique portal_disclosure_wiring_test.go uses against
// main.go and dockerfile_parity_test.go uses against the Dockerfiles.
func TestRunServeCallsTheDisclosureWarning(t *testing.T) {
	b, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("reading serve.go: %v", err)
	}
	// Normalise line endings: this repository is developed on Windows as well as
	// Linux, and a CRLF checkout must not fail this for a reason unrelated to
	// the call.
	src := strings.ReplaceAll(string(b), "\r\n", "\n")

	runServeBody := src[strings.Index(src, "func runServe("):]
	if !strings.Contains(runServeBody, "warnIfDisclosureFlagIgnored(") {
		t.Fatal("runServe does not call warnIfDisclosureFlagIgnored. The warning is the " +
			"entire remedy for a silently-ignored setting, so a version of it that is " +
			"never invoked is the same silence with more code. Do not delete this guard " +
			"to make it pass.")
	}

	// It must come AFTER the LOG_LEVEL re-levelling, or an operator who set
	// LOG_LEVEL=error still gets the line — the inverse of the bug the
	// "configuration loaded" comment above it records.
	if strings.Index(runServeBody, "warnIfDisclosureFlagIgnored(") <
		strings.Index(runServeBody, "logLevel.Set(") {
		t.Error("the warning is emitted before logLevel.Set, so LOG_LEVEL cannot silence it")
	}
}

// TestWarnIfDisclosureFlagIgnored_DoesNotContradictTheRule closes the gap
// between "a warning is emitted" and "the warning is TRUE".
//
// The sentence claims the URL is never disclosed on a production server. If that
// stopped being so — if PortalLinkDisclosureAllowed were widened — the warning
// would become a confident lie, and both tests above would still pass, because
// neither of them consults the rule. This one does.
func TestWarnIfDisclosureFlagIgnored_DoesNotContradictTheRule(t *testing.T) {
	cfg := &config.Config{AppEnv: "production", PortalDiscloseLink: true}

	if cfg.PortalLinkDisclosureAllowed() {
		t.Fatal("the warning says production never discloses. It does here, so the " +
			"warning is now false — fix the rule, not the sentence.")
	}
	if warnOutput(t, cfg) == "" {
		t.Error("disclosure is refused and the operator asked for it, so this must warn")
	}
}
