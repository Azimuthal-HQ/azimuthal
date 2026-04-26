package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
)

// NotificationArgs holds the arguments for an in-app notification job.
// It implements river.JobArgs.
type NotificationArgs struct {
	// UserID is the recipient of the notification.
	UserID string `json:"user_id"`
	// EventKind is the notification type (e.g. "assigned", "commented").
	EventKind string `json:"kind"`
	// Title is the human-readable headline.
	Title string `json:"title"`
	// Body is optional supporting text.
	Body string `json:"body,omitempty"`
	// EntityKind classifies the linked entity ("ticket", "item", "page", "comment").
	EntityKind string `json:"entity_kind,omitempty"`
	// EntityID is the ID of the entity the notification relates to.
	EntityID string `json:"entity_id,omitempty"`
}

// Kind returns the unique job kind identifier used by River.
func (NotificationArgs) Kind() string { return "notification" }

// NotificationWorker processes NotificationArgs jobs by persisting in-app
// notifications via a notifications.Recorder.
type NotificationWorker struct {
	river.WorkerDefaults[NotificationArgs]
	recorder notifications.Recorder
}

// NewNotificationWorker creates a NotificationWorker that writes through the
// given recorder. A nil recorder is allowed — the worker becomes a logging
// no-op, which keeps the queue startable in environments where notifications
// are explicitly disabled.
func NewNotificationWorker(recorder notifications.Recorder) *NotificationWorker {
	return &NotificationWorker{recorder: recorder}
}

// Work persists the notification described by the job arguments.
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationArgs]) error {
	args := job.Args
	if w.recorder == nil {
		slog.WarnContext(ctx, "notification job dropped: no recorder configured",
			"user_id", args.UserID, "kind", args.EventKind)
		return nil
	}

	userID, err := uuid.Parse(args.UserID)
	if err != nil {
		return fmt.Errorf("parsing notification user_id %q: %w", args.UserID, err)
	}

	input := notifications.CreateInput{
		UserID:     userID,
		Kind:       notifications.Kind(args.EventKind),
		Title:      args.Title,
		Body:       args.Body,
		EntityKind: notifications.EntityKind(args.EntityKind),
	}
	if args.EntityID != "" {
		entityID, err := uuid.Parse(args.EntityID)
		if err != nil {
			return fmt.Errorf("parsing notification entity_id %q: %w", args.EntityID, err)
		}
		input.EntityID = entityID
	}
	if _, err := w.recorder.Create(ctx, input); err != nil {
		return fmt.Errorf("recording notification: %w", err)
	}
	return nil
}
