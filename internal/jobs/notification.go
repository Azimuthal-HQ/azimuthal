package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// NotificationArgs holds the arguments for an in-app notification job.
// It implements river.JobArgs.
type NotificationArgs struct {
	// UserID is the recipient of the notification.
	UserID string `json:"user_id"`
	// Kind is the notification type (e.g. "ticket.assigned", "comment.added").
	EventKind string `json:"kind"`
	// Message is the human-readable notification text.
	Message string `json:"message"`
	// ResourceID is the ID of the entity the notification relates to.
	ResourceID string `json:"resource_id,omitempty"`
}

// Kind returns the unique job kind identifier used by River.
func (NotificationArgs) Kind() string { return "notification" }

// NotificationWorker processes NotificationArgs jobs by persisting in-app
// notifications. The actual persistence is delegated to the notifications
// package once it is implemented in Phase 2.
type NotificationWorker struct {
	river.WorkerDefaults[NotificationArgs]
}

// NewNotificationWorker creates a NotificationWorker.
func NewNotificationWorker() *NotificationWorker {
	return &NotificationWorker{}
}

// Work records the in-app notification.
// Phase 1 implementation logs the notification; Phase 2 (notifications package)
// will wire in real persistence.
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationArgs]) error {
	args := job.Args
	slog.InfoContext(ctx, "in-app notification queued",
		"user_id", args.UserID,
		"kind", args.EventKind,
		"resource_id", args.ResourceID,
	)
	// TODO(phase-2): persist via internal/core/notifications package.
	return nil
}
