package jobs_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
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

// TestNotificationWorker_Work_NoRecorder verifies that the worker returns
// nil when no recorder is configured (logging-only mode).
func TestNotificationWorker_Work_NoRecorder(t *testing.T) {
	worker := jobs.NewNotificationWorker(nil)
	ctx := context.Background()
	job := &river.Job[jobs.NotificationArgs]{
		Args: jobs.NotificationArgs{
			UserID:    uuid.New().String(),
			EventKind: "assigned",
			Title:     "You have been assigned a ticket",
		},
	}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("NotificationWorker.Work returned error: %v", err)
	}
}

// TestNotificationWorker_Work_WithRecorder verifies that the worker
// forwards the job arguments through the recorder.
func TestNotificationWorker_Work_WithRecorder(t *testing.T) {
	rec := &capturingRecorder{}
	worker := jobs.NewNotificationWorker(rec)
	user := uuid.New()
	entity := uuid.New()
	job := &river.Job[jobs.NotificationArgs]{
		Args: jobs.NotificationArgs{
			UserID:     user.String(),
			EventKind:  "assigned",
			Title:      "Assigned to you",
			Body:       "Ticket X",
			EntityKind: "ticket",
			EntityID:   entity.String(),
		},
	}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("NotificationWorker.Work returned error: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("expected 1 recorder call, got %d", rec.calls)
	}
	if rec.last.UserID != user || rec.last.EntityID != entity {
		t.Errorf("recorder did not receive expected ids: %+v", rec.last)
	}
	if rec.last.Kind != notifications.KindAssigned {
		t.Errorf("expected Kind=%q, got %q", notifications.KindAssigned, rec.last.Kind)
	}
}

// TestNotificationWorker_RejectsInvalidUserID verifies that a malformed
// user_id is reported as an error so River retries with backoff.
func TestNotificationWorker_RejectsInvalidUserID(t *testing.T) {
	worker := jobs.NewNotificationWorker(&capturingRecorder{})
	job := &river.Job[jobs.NotificationArgs]{
		Args: jobs.NotificationArgs{UserID: "not-a-uuid", EventKind: "assigned", Title: "x"},
	}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("expected error from invalid user_id, got nil")
	}
}

// capturingRecorder is a test double that records the most recent Create call.
type capturingRecorder struct {
	calls int
	last  notifications.CreateInput
}

func (r *capturingRecorder) Create(_ context.Context, input notifications.CreateInput) (*notifications.Notification, error) {
	r.calls++
	r.last = input
	return &notifications.Notification{ID: uuid.New(), UserID: input.UserID, Kind: input.Kind, Title: input.Title}, nil
}

// capturingEmailSender is a test double that invokes a callback on Send.
type capturingEmailSender struct {
	capture func(email.Message)
}

func (s *capturingEmailSender) Send(_ context.Context, msg email.Message) error {
	s.capture(msg)
	return nil
}
