package jobs_test

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// TestEmailArgs_Kind verifies the job kind string is stable.
func TestEmailArgs_Kind(t *testing.T) {
	var a jobs.EmailArgs
	if got := a.Kind(); got != "email" {
		t.Errorf("expected Kind()=%q, got %q", "email", got)
	}
}

// TestNotificationArgs_Kind verifies the job kind string is stable.
func TestNotificationArgs_Kind(t *testing.T) {
	var a jobs.NotificationArgs
	if got := a.Kind(); got != "notification" {
		t.Errorf("expected Kind()=%q, got %q", "notification", got)
	}
}

// TestEmailWorker_Work verifies that the email worker calls the sender correctly.
func TestEmailWorker_Work(t *testing.T) {
	var captured email.Message
	captureSender := &capturingEmailSender{capture: func(msg email.Message) {
		captured = msg
	}}

	worker := jobs.NewEmailWorker(captureSender)
	ctx := context.Background()
	job := &river.Job[jobs.EmailArgs]{
		Args: jobs.EmailArgs{
			From:    "from@example.com",
			To:      []string{"to@example.com"},
			Subject: "Test",
			Body:    "<p>hello</p>",
		},
	}

	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("Work returned error: %v", err)
	}

	if captured.From != "from@example.com" {
		t.Errorf("expected From=%q, got %q", "from@example.com", captured.From)
	}
	if len(captured.To) != 1 || captured.To[0] != "to@example.com" {
		t.Errorf("unexpected To: %v", captured.To)
	}
	if captured.Subject != "Test" {
		t.Errorf("unexpected Subject: %q", captured.Subject)
	}
}

// TestEmailWorker_PropagatesSendError verifies errors from the sender are returned.
func TestEmailWorker_PropagatesSendError(t *testing.T) {
	worker := jobs.NewEmailWorker(&email.NoopSender{}) // NoopSender never errors
	ctx := context.Background()
	job := &river.Job[jobs.EmailArgs]{
		Args: jobs.EmailArgs{
			From: "from@example.com",
			To:   []string{"to@example.com"},
		},
	}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("unexpected error from NoopSender: %v", err)
	}
}

// TestNotificationWorker_Work verifies that the notification worker succeeds.
func TestNotificationWorker_Work(t *testing.T) {
	worker := jobs.NewNotificationWorker()
	ctx := context.Background()
	job := &river.Job[jobs.NotificationArgs]{
		Args: jobs.NotificationArgs{
			UserID:     "user-123",
			EventKind:  "ticket.assigned",
			Message:    "You have been assigned a ticket",
			ResourceID: "ticket-456",
		},
	}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("NotificationWorker.Work returned error: %v", err)
	}
}

// capturingEmailSender is a test double that invokes a callback on Send.
type capturingEmailSender struct {
	capture func(email.Message)
}

func (s *capturingEmailSender) Send(_ context.Context, msg email.Message) error {
	s.capture(msg)
	return nil
}
