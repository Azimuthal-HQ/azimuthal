package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

// SMTP_USERNAME/SMTP_PASSWORD with SMTP_TLS=none sends the credentials over the
// wire in the clear. config refuses to boot on contradictory SMTP settings but
// deliberately does NOT refuse this one — a local relay is a legitimate lab
// setup — so, exactly like the portal disclosure flag, the whole remedy is a
// startup warning, and "is it actually emitted" is the assertion it stands on.

// smtpWarnOutput runs the warning against a config and returns what it logged,
// captured through newLogger's io.Writer like this package's other logger tests.
func smtpWarnOutput(t *testing.T, cfg *config.Config) string {
	t.Helper()
	var buf bytes.Buffer
	logger, _ := newLogger(&buf)
	warnIfSMTPAuthWithoutTLS(logger, cfg)
	return buf.String()
}

// TestWarnIfSMTPAuthWithoutTLS_EmittedWhenCredentialsRidePlaintext is the
// emission test. Delete the logger.Warn call in serve.go and it fails.
func TestWarnIfSMTPAuthWithoutTLS_EmittedWhenCredentialsRidePlaintext(t *testing.T) {
	got := smtpWarnOutput(t, &config.Config{
		SMTPUsername: "mailer", SMTPPassword: "s3cret", SMTPTLS: config.SMTPTLSNone,
	})
	if got == "" {
		t.Fatal("nothing was logged for SMTP auth with SMTP_TLS=none. That combination sends " +
			"credentials in the clear and is accepted at boot, so the warning is the whole remedy.")
	}
	// Assert on the greppable variable names, not the full sentence, so the prose
	// can improve without a change-detector standing in the way.
	for _, name := range []string{"SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_TLS"} {
		if !strings.Contains(got, name) {
			t.Errorf("the warning must name %s so the operator knows what to change, got %q", name, got)
		}
	}
	// A warning, not an error: the server behaves exactly as configured.
	if !strings.Contains(got, `"level":"WARN"`) {
		t.Errorf("expected a WARN line, got %q", got)
	}
}

// TestWarnIfSMTPAuthWithoutTLS_SilentOnEveryOtherCombination is the half that
// keeps the warning worth reading. Delete the guard in warnIfSMTPAuthWithoutTLS
// and every row here fails.
func TestWarnIfSMTPAuthWithoutTLS_SilentOnEveryOtherCombination(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		why  string
	}{
		{
			name: "auth over starttls",
			cfg:  config.Config{SMTPUsername: "u", SMTPPassword: "p", SMTPTLS: config.SMTPTLSStartTLS},
			why:  "the credentials are encrypted — the operator did the right thing",
		},
		{
			name: "auth over implicit TLS",
			cfg:  config.Config{SMTPUsername: "u", SMTPPassword: "p", SMTPTLS: config.SMTPTLSImplicit},
			why:  "also encrypted",
		},
		{
			name: "no auth, plaintext relay",
			cfg:  config.Config{SMTPTLS: config.SMTPTLSNone},
			why:  "nothing secret is being sent, so there is nothing to warn about",
		},
		{
			name: "no auth over starttls",
			cfg:  config.Config{SMTPTLS: config.SMTPTLSStartTLS},
			why:  "no credentials at all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := smtpWarnOutput(t, &tc.cfg); got != "" {
				t.Errorf("expected silence, got %q\n%s", got, tc.why)
			}
		})
	}
}

// TestRunServeCallsTheSMTPAuthWarning closes the same gap the disclosure warning
// has its own test for: every test above proves the function behaves, but none
// notices if runServe stops calling it. Asserted by reading the source, since
// runServe cannot be invoked in a unit test.
func TestRunServeCallsTheSMTPAuthWarning(t *testing.T) {
	b, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("reading serve.go: %v", err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	runServeBody := src[strings.Index(src, "func runServe("):]
	if !strings.Contains(runServeBody, "warnIfSMTPAuthWithoutTLS(") {
		t.Fatal("runServe does not call warnIfSMTPAuthWithoutTLS. A warning nobody invokes is the " +
			"same silence with more code.")
	}
	if strings.Index(runServeBody, "warnIfSMTPAuthWithoutTLS(") <
		strings.Index(runServeBody, "logLevel.Set(") {
		t.Error("the warning is emitted before logLevel.Set, so LOG_LEVEL cannot silence it")
	}
}
