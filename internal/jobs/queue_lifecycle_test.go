// Phase 1 (P1.1) integration coverage for the River queue lifecycle.
package jobs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestQueue_Lifecycle_RunsJob migrates the test schema (River + Azimuthal),
// starts the queue, enqueues a notification job, waits for it to be
// processed, and asserts the notifications row exists.
//
// Audit ref: P1 Definition of Done — "River queue starts at boot, drains on
// shutdown" + "River queue unit test: enqueue a no-op job in test mode,
// assert it executes within timeout."
//
// Test name is intentionally short — its sanitized form is the Postgres
// schema and River prefixes it with "<schema>.river_leadership" (16 chars),
// limited to 63 bytes by Postgres.
func TestQueue_Lifecycle_RunsJob(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set — skipping queue lifecycle test")
	}

	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	// River requires its own schema migrations.
	require.NoError(t, jobs.Migrate(context.Background(), db.Pool))

	queries := generated.New(db.Pool)
	notifySvc := notifications.NewService(queries)

	queue, err := jobs.NewQueue(context.Background(), db.Pool, &email.NoopSender{}, notifySvc)
	require.NoError(t, err)

	// Run the queue in a cancellable context. River.Start returns once the
	// internals are running; cancelling the context is the supported way to
	// initiate shutdown alongside Stop.
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	require.NoError(t, queue.Start(startCtx))
	defer func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelStop()
		_ = queue.Stop(stopCtx)
	}()

	// Enqueue a notification job.
	entity := uuid.New()
	require.NoError(t, queue.EnqueueNotification(context.Background(), jobs.NotificationArgs{
		UserID:     user.ID.String(),
		EventKind:  string(notifications.KindAssigned),
		Title:      "Queued assignment",
		EntityKind: string(notifications.EntityTicket),
		EntityID:   entity.String(),
	}))

	// Poll the notifications table — the row should appear after the worker runs.
	deadline := time.Now().Add(15 * time.Second)
	for {
		count, err := notifySvc.CountUnread(context.Background(), user.ID)
		require.NoError(t, err)
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("notification was not persisted within timeout (count=%d)", count)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestQueue_Stop_DrainsEvenWithoutStart verifies Stop is safe before Start.
func TestQueue_Stop_DrainsEvenWithoutStart(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer pool.Close()

	queue, err := jobs.NewQueue(context.Background(), pool, &email.NoopSender{}, nil)
	require.NoError(t, err)
	require.NoError(t, queue.Stop(context.Background()))
}
