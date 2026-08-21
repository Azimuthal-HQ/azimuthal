// Package jobs provides background job queue setup and worker registration
// using the River job queue backed by PostgreSQL.
package jobs

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// NotificationEnqueuer is the interface for enqueuing in-app notification jobs.
// Implemented by *Queue; use NoopNotificationEnqueuer in tests or when the
// queue is disabled.
type NotificationEnqueuer interface {
	EnqueueNotification(ctx context.Context, args NotificationArgs) error
}

// NoopNotificationEnqueuer silently discards notification jobs.
type NoopNotificationEnqueuer struct{}

// EnqueueNotification is a no-op.
func (NoopNotificationEnqueuer) EnqueueNotification(_ context.Context, _ NotificationArgs) error {
	return nil
}

// Queue wraps a River client and exposes helpers for enqueueing jobs.
type Queue struct {
	client *river.Client[pgx.Tx]

	// stoppedMu guards stopped: the client's stop-signal channel, captured once
	// Start has returned. Status reads it from HTTP-handler goroutines, so it
	// cannot read river.Client.Stopped() directly — River sets that channel
	// inside Start under its own lock, and a concurrent read during boot is a
	// data race (proven with -race). Capturing it here, in the goroutine that
	// called Start and only after Start returned, and serving Status from this
	// guarded copy, keeps the read race-free while still reflecting a live stop.
	stoppedMu sync.Mutex
	stopped   <-chan struct{}
}

// NewQueue creates a River client wired to the given pgxpool and registers
// all application workers. Call Start on the returned Queue to begin
// processing jobs.
//
// The pool must be open and healthy before calling NewQueue.
func NewQueue(ctx context.Context, pool *pgxpool.Pool, sender email.Sender, queries *generated.Queries) (*Queue, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewEmailWorker(sender))
	river.AddWorker(workers, NewNotificationWorker(queries))

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("creating river client: %w", err)
	}

	_ = ctx // ctx reserved for future use (e.g. schema migration on startup)
	return &Queue{client: client}, nil
}

// Start begins processing background jobs. River's client runs in its own
// background goroutines; Start returns once they are up (or once startup fails).
func (q *Queue) Start(ctx context.Context) error {
	startErr := q.client.Start(ctx)

	// Capture the client's stop-signal channel now — in this goroutine, after
	// Start has returned, so River's internal write to it has already happened
	// and this read cannot race it. On a start failure River has already closed
	// the channel (it fires its stopped signal on the error path), so Status
	// then reports "error". Held under stoppedMu because Status reads it from
	// other goroutines.
	q.stoppedMu.Lock()
	q.stopped = q.client.Stopped()
	q.stoppedMu.Unlock()

	if startErr != nil {
		return fmt.Errorf("starting job queue: %w", startErr)
	}
	return nil
}

// Stop gracefully stops job processing, waiting for in-flight jobs to complete.
func (q *Queue) Stop(ctx context.Context) error {
	if err := q.client.Stop(ctx); err != nil {
		return fmt.Errorf("stopping job queue: %w", err)
	}
	return nil
}

// Status reports the queue's LIVE state, read at the moment it is called rather
// than captured at boot. It is what /health now reports for the queue, so a
// queue that stops after startup is visible instead of frozen at its boot-time
// value.
//
//   - "error" the River client has stopped — its Stopped() channel is closed.
//   - "ok"    the client has NOT stopped: it is running, or (a negligible boot
//     window) has been created and is about to be started.
//
// The read mirrors what the old boot-time snapshot stood in for — that snapshot
// was set to "ok" right after Start was invoked and never revisited — but asks
// the client whether it is still up rather than trusting a frozen value. River's
// client closes a stop-signal channel once it has stopped; Start captures that
// channel (see the stopped field), and a non-blocking receive here distinguishes
// not-stopped (would block → default → "ok") from stopped (ready → closed →
// "error"). The distinction the old snapshot could not make is precisely the one
// that matters: a client that stops AFTER boot now reports "error", not a stale
// "ok".
//
// "disabled" is deliberately not a value here: a disabled queue has no *Queue
// to call this on. cmd/server/main.go maps the absent queue to "disabled" by
// leaving the /health status function nil.
func (q *Queue) Status() string {
	q.stoppedMu.Lock()
	stopped := q.stopped
	q.stoppedMu.Unlock()

	if stopped == nil {
		// Start has not published the stop channel yet: the queue is enabled and
		// coming up. Report "ok" — the window is a negligible sliver of boot, and
		// in the real wiring /health is not serving during it.
		return "ok"
	}
	select {
	case <-stopped:
		return "error"
	default:
		return "ok"
	}
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
