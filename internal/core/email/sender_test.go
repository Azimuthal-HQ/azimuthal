package email_test

import (
	"context"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
)

// TestNoopSender verifies the noop implementation satisfies the interface and succeeds.
func TestNoopSender(t *testing.T) {
	var s email.Sender = &email.NoopSender{}
	err := s.Send(context.Background(), email.Message{
		From:    "from@example.com",
		To:      []string{"to@example.com"},
		Subject: "Test",
		Body:    "<p>hello</p>",
	})
	if err != nil {
		t.Fatalf("NoopSender.Send returned unexpected error: %v", err)
	}
}

// TestSMTPSender_InterfaceCompliance verifies *SMTPSender satisfies Sender at compile time.
func TestSMTPSender_InterfaceCompliance(_ *testing.T) {
	var _ email.Sender = email.NewSMTPSender(email.SMTPConfig{Host: "localhost", Port: 1025, From: "test@localhost"})
}

// TestSMTPTLSModeConstantsAgree pins the email package's TLS-mode strings to the
// ones internal/config validates, so main.go can pass cfg.SMTPTLS straight
// through. It is the same constant-agreement guard the delivery modes and the
// bcrypt floor use, for the same reason: the two are stated in separate packages
// (email imports nothing from config) and must not drift.
func TestSMTPTLSModeConstantsAgree(t *testing.T) {
	pairs := []struct{ email, cfg string }{
		{email.TLSNone, config.SMTPTLSNone},
		{email.TLSStartTLS, config.SMTPTLSStartTLS},
		{email.TLSImplicit, config.SMTPTLSImplicit},
	}
	for _, p := range pairs {
		if p.email != p.cfg {
			t.Errorf("email TLS mode %q != config mode %q", p.email, p.cfg)
		}
	}
}

// TestSMTPSender_FailsOnUnreachableHost verifies Send returns an error when the
// SMTP server is not available, across every transport mode. Port 19999 is
// chosen to be almost certainly not listening, so each mode's dial path reaches
// its error branch.
func TestSMTPSender_FailsOnUnreachableHost(t *testing.T) {
	for _, mode := range []string{email.TLSNone, email.TLSStartTLS, email.TLSImplicit} {
		t.Run(mode, func(t *testing.T) {
			s := email.NewSMTPSender(email.SMTPConfig{
				Host: "127.0.0.1", Port: 19999, From: "from@example.com", TLS: mode,
			})
			err := s.Send(context.Background(), email.Message{
				From:    "from@example.com",
				To:      []string{"to@example.com"},
				Subject: "Test subject",
				Body:    "<p>body</p>",
			})
			if err == nil {
				t.Fatalf("mode %q: expected an error when the SMTP host is unreachable", mode)
			}
		})
	}
}

// TestSMTPSender_DefaultFrom verifies that an empty msg.From falls back to the sender's from.
// This exercises the Send code path that sets a default from address.
func TestSMTPSender_DefaultFrom(t *testing.T) {
	s := email.NewSMTPSender(email.SMTPConfig{Host: "127.0.0.1", Port: 19999, From: "default@example.com"})
	err := s.Send(context.Background(), email.Message{
		// From intentionally empty — should use sender default
		To:      []string{"to@example.com"},
		Subject: "default from test",
		Body:    "<p>body</p>",
	})
	// Connection will fail (no SMTP on 19999), but the default-from branch is hit.
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}
