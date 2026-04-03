package email_test

import (
	"context"
	"testing"

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
	var _ email.Sender = email.NewSMTPSender("localhost", 1025, "test@localhost")
}

// TestSMTPSender_FailsOnUnreachableHost verifies Send returns an error when the
// SMTP server is not available.
func TestSMTPSender_FailsOnUnreachableHost(t *testing.T) {
	// Port 19999 is chosen to be almost certainly not listening.
	s := email.NewSMTPSender("127.0.0.1", 19999, "from@example.com")
	err := s.Send(context.Background(), email.Message{
		From:    "from@example.com",
		To:      []string{"to@example.com"},
		Subject: "Test subject",
		Body:    "<p>body</p>",
	})
	if err == nil {
		t.Fatal("expected error when SMTP host is unreachable, got nil")
	}
}
