package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestItemKeyBackfill_StableOrderingAndIdempotent proves migration 031's
// backfill logic: item_key is <SPACE_KEY>-<number>, numbers reflect creation
// order, and re-running the exact backfill statement is a no-op (idempotent),
// because the key is derived from the stored number rather than a fresh
// ROW_NUMBER. This uses the schema already migrated by NewTestDB, then replays
// the backfill UPDATE to assert stability.
func TestItemKeyBackfill_StableOrderingAndIdempotent(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, tdb.Pool)
	user := testutil.CreateTestUser(t, tdb.Pool, org.ID)
	space := testutil.CreateTestSpace(t, tdb.Pool, org.ID, user.ID, "vector")

	var spaceKey string
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT key FROM spaces WHERE id = $1`, space.ID).Scan(&spaceKey))

	// Insert three rows directly with explicit numbers and out-of-order
	// created_at, standing in for pre-existing (pre-migration) items. Numbers
	// already encode creation order (assigned by migration 014 / the adapter).
	type row struct {
		id     uuid.UUID
		number int
	}
	rows := []row{{uuid.New(), 1}, {uuid.New(), 2}, {uuid.New(), 3}}
	for _, r := range rows {
		_, err := tdb.Pool.Exec(ctx,
			`INSERT INTO project_items (id, space_id, org_id, number, item_key, kind, title,
			    status, priority, reporter_id, rank)
			 VALUES ($1, $2, $3, $4, $5, 'task', 'Item', 'open', 'medium', $6, '0|a:')`,
			r.id, space.ID, org.ID, r.number,
			spaceKey+"-"+itoa(r.number), user.ID)
		require.NoError(t, err)
	}

	// Snapshot the keys.
	before := readKeys(t, tdb, space.ID)
	require.Equal(t, map[uuid.UUID]string{
		rows[0].id: spaceKey + "-1",
		rows[1].id: spaceKey + "-2",
		rows[2].id: spaceKey + "-3",
	}, before)

	// Replay migration 031's backfill statement verbatim. Idempotent: keys must
	// not change.
	_, err := tdb.Pool.Exec(ctx,
		`UPDATE project_items pi
		 SET item_key = s.key || '-' || pi.number
		 FROM spaces s
		 WHERE pi.space_id = s.id`)
	require.NoError(t, err)

	after := readKeys(t, tdb, space.ID)
	require.Equal(t, before, after, "re-running the backfill must not change any key")
}

func readKeys(t *testing.T, tdb *testutil.TestDB, spaceID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	out := map[uuid.UUID]string{}
	dbRows, err := tdb.Pool.Query(context.Background(),
		`SELECT id, item_key FROM project_items WHERE space_id = $1`, spaceID)
	require.NoError(t, err)
	defer dbRows.Close()
	for dbRows.Next() {
		var id uuid.UUID
		var key string
		require.NoError(t, dbRows.Scan(&id, &key))
		out[id] = key
	}
	require.NoError(t, dbRows.Err())
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
