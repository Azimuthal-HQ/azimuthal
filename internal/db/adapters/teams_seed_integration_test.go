package adapters_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Default-team seeding must be safe to call concurrently for the same org
// (T3/T4).
//
// SeedDefaultTeam is reached by every org-creation path and by `admin
// create-user` for an org that may already exist. It used to read the team
// and then insert it, which is a check-then-act race: two callers both see no
// row, both insert, and the loser fails on teams_org_id_slug_key.
//
// This was not theoretical. Playwright runs four workers, and against a fresh
// database each worker's `admin create-user` raced the others through this
// function; admin.spec.ts:136 and :185 failed every run on a clean database
// with:
//
//	seeding default team: ERROR: duplicate key value violates unique
//	constraint "teams_org_id_slug_key" (SQLSTATE 23505)
//
// CI hid it behind workers:2 and retries:1, which is why it read as a flaky
// spec rather than the product defect it was.
//
// Real PostgreSQL, because the defect IS the constraint — an in-memory fake
// would not have one.

// TestSeedDefaultTeam_ConcurrentTransactionsDoNotCollide is the deterministic
// regression test, and the one that actually fails before the fix.
//
// Spawning goroutines is not enough to reproduce this: against a warm local
// pool the first caller commits before the others reach their read, so every
// later caller takes the fast path and the window never opens. Two explicit
// transactions remove the timing question entirely — both are past the read
// and neither has committed, which is exactly the state four `admin
// create-user` processes reach against a fresh database.
//
// Before the fix (a plain INSERT), tx2 blocks on tx1's uncommitted row and
// then fails with SQLSTATE 23505 on teams_org_id_slug_key. After it, tx2's
// ON CONFLICT DO NOTHING makes it a no-op.
func TestSeedDefaultTeam_ConcurrentTransactionsDoNotCollide(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	ctx := context.Background()

	q := generated.New(db.Pool)

	tx1, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback(ctx) }()

	tx2, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()

	seed := func(q *generated.Queries) error {
		return q.InsertDefaultTeamIfAbsent(ctx, generated.InsertDefaultTeamIfAbsentParams{
			ID:          uuid.New(),
			OrgID:       org.ID,
			Description: "Org default team. Every member belongs here until assigned elsewhere.",
		})
	}

	// tx1 inserts and commits. Both transactions were open at this point, so
	// neither could have observed the other's row.
	require.NoError(t, seed(q.WithTx(tx1)))
	require.NoError(t, tx1.Commit(ctx))

	// tx2 is the loser of the race. It must not error.
	require.NoError(t, seed(q.WithTx(tx2)),
		"the second concurrent seed must be a no-op, not a duplicate-key failure")
	require.NoError(t, tx2.Commit(ctx))

	var count int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
		org.ID).Scan(&count))
	require.Equal(t, 1, count, "exactly one default team must survive the race")
}

// TestSeedDefaultTeam_IsIdempotentUnderConcurrency exercises the real
// SeedDefaultTeam entry point under goroutine pressure. It is a smoke test
// rather than the proof — see the deterministic test above for that.
func TestSeedDefaultTeam_IsIdempotentUnderConcurrency(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewTeamAdapter(db.Pool)
	ctx := context.Background()

	// Playwright runs four workers, but four goroutines against a warm local
	// pool routinely serialise — the first inserts and commits before the rest
	// reach their read, so every later caller takes the fast path and the race
	// never opens. Widening on both axes makes the window reliable: more
	// racers per org, and several fresh orgs so a single lucky interleaving
	// cannot carry the whole test.
	const (
		racers = 16
		orgs   = 5
	)

	for range orgs {
		org := testutil.CreateTestOrg(t, db.Pool)

		var wg sync.WaitGroup
		errs := make([]error, racers)
		start := make(chan struct{})

		for i := range racers {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start // release everyone at once
				errs[idx] = adapter.SeedDefaultTeam(ctx, org.ID)
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			require.NoErrorf(t, err, "racer %d failed; concurrent seeding must not error", i)
		}

		// Exactly one team, not sixteen and not zero. A seed that swallowed
		// its own error would pass the loop above while leaving nothing behind.
		var count int
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT count(*) FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
			org.ID).Scan(&count))
		require.Equal(t, 1, count, "exactly one default team must exist after concurrent seeding")

		// And it must be a usable row, not a husk: the invariant
		// teams_path_ends_self enforces has to hold for whichever racer won.
		var id uuid.UUID
		var path []uuid.UUID
		var slug string
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT id, path, slug FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
			org.ID).Scan(&id, &path, &slug))
		require.Equal(t, "default", slug)
		require.Len(t, path, 1, "a root team's path is just itself")
		require.Equal(t, id, path[0], "teams_path_ends_self: the path must end with the row's own id")
	}
}

// TestSeedDefaultTeam_RepeatedCallsAreNoOps covers the sequential case the
// concurrency test cannot distinguish: calling twice in a row must not create
// a second team or error.
func TestSeedDefaultTeam_RepeatedCallsAreNoOps(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewTeamAdapter(db.Pool)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultTeam(ctx, org.ID))

	var first uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT id FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
		org.ID).Scan(&first))

	require.NoError(t, adapter.SeedDefaultTeam(ctx, org.ID))

	var second uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT id FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
		org.ID).Scan(&second))

	require.Equal(t, first, second, "a second seed must not replace the team")
}
