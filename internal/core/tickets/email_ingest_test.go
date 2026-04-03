package tickets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseInboundEmail(t *testing.T) {
	t.Run("plain text email", func(t *testing.T) {
		raw := "From: user@example.com\r\nSubject: Help needed\r\n\r\nI cannot log in to the dashboard."
		email, err := ParseInboundEmail(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if email.From != "user@example.com" {
			t.Errorf("expected from user@example.com, got %q", email.From)
		}
		if email.Subject != "Help needed" {
			t.Errorf("expected subject 'Help needed', got %q", email.Subject)
		}
		if email.Body != "I cannot log in to the dashboard." {
			t.Errorf("unexpected body: %q", email.Body)
		}
	})

	t.Run("multipart email", func(t *testing.T) {
		raw := "From: user@example.com\r\n" +
			"Subject: Multipart test\r\n" +
			"Content-Type: multipart/alternative; boundary=boundary123\r\n\r\n" +
			"--boundary123\r\n" +
			"Content-Type: text/plain\r\n\r\n" +
			"Plain text body\r\n" +
			"--boundary123\r\n" +
			"Content-Type: text/html\r\n\r\n" +
			"<p>HTML body</p>\r\n" +
			"--boundary123--\r\n"
		email, err := ParseInboundEmail(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if email.Body != "Plain text body" {
			t.Errorf("expected plain text body, got %q", email.Body)
		}
	})

	t.Run("missing from", func(t *testing.T) {
		raw := "Subject: No from\r\n\r\nBody text"
		_, err := ParseInboundEmail(strings.NewReader(raw))
		if !errors.Is(err, ErrEmailParseFailure) {
			t.Errorf("expected ErrEmailParseFailure, got %v", err)
		}
	})

	t.Run("missing subject", func(t *testing.T) {
		raw := "From: user@example.com\r\n\r\nBody text"
		_, err := ParseInboundEmail(strings.NewReader(raw))
		if !errors.Is(err, ErrEmailParseFailure) {
			t.Errorf("expected ErrEmailParseFailure, got %v", err)
		}
	})

	t.Run("invalid email", func(t *testing.T) {
		_, err := ParseInboundEmail(strings.NewReader("not a valid email"))
		// Should either parse with missing fields or fail
		if err == nil {
			t.Error("expected error for invalid email")
		}
	})
}

func TestCreateFromEmail(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	spaceID := uuid.New()
	reporterID := uuid.New()

	t.Run("success", func(t *testing.T) {
		email := &InboundEmail{
			From:    "customer@example.com",
			Subject: "Cannot access account",
			Body:    "I forgot my password and the reset link is broken.",
		}
		ticket, err := svc.CreateFromEmail(context.Background(), email, spaceID, reporterID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ticket.Title != "Cannot access account" {
			t.Errorf("expected title from subject, got %q", ticket.Title)
		}
		if ticket.Description != "I forgot my password and the reset link is broken." {
			t.Errorf("expected description from body, got %q", ticket.Description)
		}
		if ticket.Priority != PriorityMedium {
			t.Errorf("expected medium priority for email tickets, got %q", ticket.Priority)
		}
	})

	t.Run("nil email", func(t *testing.T) {
		_, err := svc.CreateFromEmail(context.Background(), nil, spaceID, reporterID)
		if !errors.Is(err, ErrEmailParseFailure) {
			t.Errorf("expected ErrEmailParseFailure, got %v", err)
		}
	})
}
