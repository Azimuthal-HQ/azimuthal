package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// NotificationArgs holds the arguments for an in-app notification job.
// It implements river.JobArgs.
type NotificationArgs struct {
	// UserID is the recipient of the notification.
	UserID string `json:"user_id"`
	// EventKind is the notification type (e.g. "ticket.assigned", "comment.added").
	EventKind string `json:"kind"`
	// Message is the human-readable notification text.
	Message string `json:"message"`
	// ResourceID is the ID of the entity the notification relates to.
	ResourceID string `json:"resource_id,omitempty"`
	// EntityKind is the type of entity (e.g. "ticket", "page").
	EntityKind string `json:"entity_kind,omitempty"`
	// SpaceID is the space the entity lives in, captured so the bell can build
	// a space-scoped route to it. Empty when unknown (the notification then
	// stays non-navigable).
	SpaceID string `json:"space_id,omitempty"`
}

// Kind returns the unique job kind identifier used by River.
func (NotificationArgs) Kind() string { return "notification" }

// NotificationWorker processes NotificationArgs jobs by persisting in-app
// notifications to the database.
type NotificationWorker struct {
	river.WorkerDefaults[NotificationArgs]
	queries *generated.Queries
}

// NewNotificationWorker creates a NotificationWorker backed by the given queries.
func NewNotificationWorker(queries *generated.Queries) *NotificationWorker {
	return &NotificationWorker{queries: queries}
}

// Work inserts the notification into the notifications table.
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationArgs]) error {
	args := job.Args

	userID, err := uuid.Parse(args.UserID)
	if err != nil {
		slog.Warn("notification worker: invalid user_id, skipping", "user_id", args.UserID)
		return nil
	}

	entityKind := args.EntityKind
	var entityID *string
	if args.ResourceID != "" {
		entityID = &args.ResourceID
	}

	params := generated.CreateNotificationParams{
		ID:     uuid.New(),
		UserID: userID,
		Kind:   args.EventKind,
		Title:  args.Message,
	}
	if entityKind != "" {
		params.EntityKind = &entityKind
	}
	if entityID != nil {
		parsed, err := uuid.Parse(*entityID)
		if err == nil {
			params.EntityID.Bytes = parsed
			params.EntityID.Valid = true
		}
	}
	if args.SpaceID != "" {
		if parsed, err := uuid.Parse(args.SpaceID); err == nil {
			params.EntitySpaceID.Bytes = parsed
			params.EntitySpaceID.Valid = true
		}
	}

	if _, err := w.queries.CreateNotification(ctx, params); err != nil {
		return fmt.Errorf("persisting notification for user %s: %w", args.UserID, err)
	}

	slog.DebugContext(ctx, "notification persisted",
		"user_id", args.UserID,
		"kind", args.EventKind,
	)
	return nil
}
