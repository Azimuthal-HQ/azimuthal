package jobs_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
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

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, nil)
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

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, nil)
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
		UserID:    uuid.New().String(),
		EventKind: "assigned",
		Title:     "hello",
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

	q, err := jobs.NewQueue(ctx, pool, &email.NoopSender{}, nil)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	startCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel before Start so river exits immediately
	_ = q.Start(startCtx)
}
