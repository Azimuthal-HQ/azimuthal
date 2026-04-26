// Package jobs provides background job queue setup and worker registration
// using the River job queue backed by PostgreSQL.
package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
)

// Queue wraps a River client and exposes helpers for enqueueing jobs.
type Queue struct {
	client *river.Client[pgx.Tx]
}

// NewQueue creates a River client wired to the given pgxpool and registers
// all application workers. Call Start on the returned Queue to begin
// processing jobs.
//
// The pool must be open and healthy before calling NewQueue. NewQueue does
// NOT run River's own schema migrations — call Migrate first.
func NewQueue(_ context.Context, pool *pgxpool.Pool, sender email.Sender, notifier notifications.Recorder) (*Queue, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewEmailWorker(sender))
	river.AddWorker(workers, NewNotificationWorker(notifier))

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("creating river client: %w", err)
	}

	return &Queue{client: client}, nil
}

// Migrate applies River's own schema migrations (river_job, river_leader, etc.).
// Safe to call repeatedly; only missing migrations are applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("creating river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("applying river migrations: %w", err)
	}
	return nil
}

// Start begins processing background jobs. It returns once the workers are
// running. Use Stop to drain in-flight jobs before shutdown.
func (q *Queue) Start(ctx context.Context) error {
	if err := q.client.Start(ctx); err != nil {
		return fmt.Errorf("starting job queue: %w", err)
	}
	return nil
}

// Stop gracefully stops job processing, waiting for in-flight jobs to complete.
// Returns nil if the client was never started — River's Stop is a no-op in
// that case, so it is always safe to defer.
func (q *Queue) Stop(ctx context.Context) error {
	if err := q.client.Stop(ctx); err != nil {
		return fmt.Errorf("stopping job queue: %w", err)
	}
	return nil
}

// EnqueueEmail inserts an email delivery job into the queue.
func (q *Queue) EnqueueEmail(ctx context.Context, args EmailArgs) error {
	if _, err := q.client.Insert(ctx, args, nil); err != nil {
		return fmt.Errorf("enqueueing email job: %w", err)
	}
	return nil
}

// EnqueueNotification inserts an in-app notification job into the queue.
func (q *Queue) EnqueueNotification(ctx context.Context, args NotificationArgs) error {
	if _, err := q.client.Insert(ctx, args, nil); err != nil {
		return fmt.Errorf("enqueueing notification job: %w", err)
	}
	return nil
}
