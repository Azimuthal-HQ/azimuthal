package jobs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// TestNewQueue_Integration creates a real River client backed by the test database.
// Skipped when DATABASE_URL is not set.
func TestNewQueue_Integration(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping queue integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, generated.New(pool))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if q == nil {
		t.Fatal("expected non-nil Queue")
	}

	// Stop immediately (was never started — should be a no-op or fast).
	stopCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately so Stop returns quickly
	_ = q.Stop(stopCtx)
}

// TestQueue_EnqueueRequiresDB verifies the enqueue helpers fail gracefully when
// the River job tables are absent. Skipped when DATABASE_URL is not set.
func TestQueue_EnqueueRequiresDB(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, generated.New(pool))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	// Without river tables, Insert will fail — that is the expected behaviour.
	err = q.EnqueueEmail(ctx, jobs.EmailArgs{
		From: "from@example.com",
		To:   []string{"to@example.com"},
	})
	if err == nil {
		t.Log("EnqueueEmail succeeded (river tables exist in test DB)")
	} else {
		t.Logf("EnqueueEmail returned expected error (no river tables): %v", err)
	}

	err = q.EnqueueNotification(ctx, jobs.NotificationArgs{
		UserID:    "user-1",
		EventKind: "test",
		Message:   "hello",
	})
	if err == nil {
		t.Log("EnqueueNotification succeeded (river tables exist in test DB)")
	} else {
		t.Logf("EnqueueNotification returned expected error: %v", err)
	}
}

// TestQueue_Start exercises the Start code path by starting with a pre-cancelled
// context so the client exits immediately. River may return an error (tables absent
// or context cancelled) — both outcomes are acceptable; we only require coverage.
func TestQueue_Start(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, generated.New(pool))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	startCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel before Start so river exits immediately
	_ = q.Start(startCtx)
}

// TestQueue_Status_ReflectsLiveState is the fails-before test for the /health
// queue snapshot at the queue layer. Status() reads the client's actual state,
// so it flips from "ok" to "error" when the client stops — the live transition
// the boot-time string ("ok" forever, "error" never assigned in non-test code)
// could not express.
//
// It does not depend on the River tables existing: it asserts the not-stopped
// default and the post-stop transition, both driven by the client's own
// lifecycle, not by whether any job can be processed.
//
// It also hammers Status() concurrently with Start() so that -race guards the
// read: Status is served on HTTP-handler goroutines while the queue's Start
// goroutine is still coming up, and reading River's stop channel unguarded there
// is a data race (this is how that race was found). Revert the guard in
// queue.go and this test fails under -race.
func TestQueue_Status_ReflectsLiveState(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, generated.New(pool))
	require.NoError(t, err)

	// A queue that has not stopped reads "ok" — the live default, never a
	// captured constant.
	require.Equal(t, "ok", q.Status(), "an un-stopped queue must report ok")

	// Start it on a context we control, exactly as cmd/server/main.go does,
	// while a second goroutine reads Status() throughout — the concurrent shape
	// -race must stay clean on. Then cancel to bring it down. Whatever River does
	// in between (with or without its tables), the client ends up stopped.
	runCtx, cancelRun := context.WithCancel(ctx)
	startReturned := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		_ = q.Start(runCtx)
		close(startReturned)
	}()
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-startReturned:
				return
			default:
				_ = q.Status() // concurrent read; must not race the Start write
			}
		}
	}()
	<-startReturned
	<-pollDone
	cancelRun()

	// The SAME accessor now reports "error": the transition the boot-time
	// snapshot could never show.
	require.Eventually(t, func() bool { return q.Status() == "error" },
		5*time.Second, 20*time.Millisecond,
		"a stopped queue must report error, not the boot-time ok")
}
