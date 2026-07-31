package adapters_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// spaceKey reads the org-unique key assigned to a space by the fixtures.
func spaceKey(t *testing.T, db *testutil.TestDB, spaceID uuid.UUID) string {
	t.Helper()
	var key string
	err := db.Pool.QueryRow(context.Background(),
		`SELECT key FROM spaces WHERE id = $1`, spaceID).Scan(&key)
	require.NoError(t, err)
	return key
}

func newItem(spaceID, reporterID uuid.UUID, title string) *projects.Item {
	return &projects.Item{
		ID:         uuid.New(),
		SpaceID:    spaceID,
		Kind:       "task",
		Title:      title,
		Status:     "open",
		Priority:   "medium",
		ReporterID: reporterID,
	}
}

// TestItemKey_AssignedAtCreation proves that Create assigns a <SPACE_KEY>-<n>
// key and per-space number, writes them back onto the item, and that the number
// increments monotonically from 1.
func TestItemKey_AssignedAtCreation(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	key := spaceKey(t, db, space.ID)
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	first := newItem(space.ID, user.ID, "First")
	require.NoError(t, adapter.Create(context.Background(), first))
	require.Equal(t, 1, first.Number, "first item in a space gets number 1")
	require.Equal(t, fmt.Sprintf("%s-1", key), first.ItemKey)

	second := newItem(space.ID, user.ID, "Second")
	require.NoError(t, adapter.Create(context.Background(), second))
	require.Equal(t, 2, second.Number)
	require.Equal(t, fmt.Sprintf("%s-2", key), second.ItemKey)

	// The key round-trips through a fresh read.
	fetched, err := adapter.GetByID(context.Background(), second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ItemKey, fetched.ItemKey)
	require.Equal(t, 2, fetched.Number)
}

// TestItemKey_ConcurrentCreatesNoDuplicateNoSkip is the concurrency guarantee:
// N parallel creations in one space produce N distinct, contiguous keys with no
// duplicate and no skipped-then-reused number. Run with -race in CI.
func TestItemKey_ConcurrentCreatesNoDuplicateNoSkip(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	key := spaceKey(t, db, space.ID)
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	const n = 40
	var wg sync.WaitGroup
	keys := make([]string, n)
	nums := make([]int, n)
	errs := make([]error, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			item := newItem(space.ID, user.ID, fmt.Sprintf("Item %d", idx))
			<-start // maximise contention: everyone races the counter at once
			if err := adapter.Create(context.Background(), item); err != nil {
				errs[idx] = err
				return
			}
			keys[idx] = item.ItemKey
			nums[idx] = item.Number
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "create %d must not fail under contention", i)
	}

	// No duplicate keys, and the set of numbers is exactly {1..n} — contiguous,
	// so nothing was skipped and nothing reused.
	seen := make(map[string]bool, n)
	numSeen := make(map[int]bool, n)
	for i := 0; i < n; i++ {
		require.NotEmptyf(t, keys[i], "item %d got no key", i)
		require.Falsef(t, seen[keys[i]], "duplicate key %s", keys[i])
		seen[keys[i]] = true
		numSeen[nums[i]] = true
	}
	for want := 1; want <= n; want++ {
		require.Truef(t, numSeen[want], "number %d missing — a number was skipped", want)
		require.Containsf(t, seen, fmt.Sprintf("%s-%d", key, want), "key %s-%d missing", key, want)
	}

	// The database agrees: n distinct numbers persisted.
	var count, distinct int
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT count(number), count(DISTINCT number) FROM project_items WHERE space_id = $1`,
		space.ID).Scan(&count, &distinct))
	require.Equal(t, n, count)
	require.Equal(t, n, distinct, "every persisted number is distinct")
}

// TestItemKey_DuplicateInsertRejected is the negative test: the (org_id,
// item_key) unique index rejects a second row with the same key. Deleting the
// check (the unique index) would let this pass, so the test dies with the check.
func TestItemKey_DuplicateInsertRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	item := newItem(space.ID, user.ID, "Original")
	require.NoError(t, adapter.Create(context.Background(), item))

	// Force a second row with the same org_id + item_key directly (bypassing the
	// counter) — the unique index must reject it.
	var orgID uuid.UUID
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT org_id FROM project_items WHERE id = $1`, item.ID).Scan(&orgID))
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO project_items (id, space_id, org_id, number, item_key, kind, title,
		    status, priority, reporter_id, rank)
		 VALUES ($1, $2, $3, 999, $4, 'task', 'Dup', 'open', 'medium', $5, '0|zzz:')`,
		uuid.New(), space.ID, orgID, item.ItemKey, user.ID)
	require.Error(t, err, "duplicate (org_id, item_key) must be rejected by the unique index")
}

// TestItemKey_OrgUniqueNotGlobal proves keys are org-scoped, not global: two
// orgs may each hold the same key without collision.
func TestItemKey_OrgUniqueNotGlobal(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	makeKey := func() string {
		org := testutil.CreateTestOrg(t, db.Pool)
		user := testutil.CreateTestUser(t, db.Pool, org.ID)
		// Give both spaces the same key so the derived item_key collides across orgs.
		space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
		_, err := db.Pool.Exec(context.Background(),
			`UPDATE spaces SET key = 'SHARED' WHERE id = $1`, space.ID)
		require.NoError(t, err)
		item := newItem(space.ID, user.ID, "Item")
		require.NoError(t, adapter.Create(context.Background(), item))
		return item.ItemKey
	}

	require.Equal(t, "SHARED-1", makeKey())
	require.Equal(t, "SHARED-1", makeKey(), "the same key in a different org must be allowed")
}

// TestItemKey_ResolveByKey exercises the importer-facing resolution path.
func TestItemKey_ResolveByKey(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	item := newItem(space.ID, user.ID, "Resolve me")
	require.NoError(t, adapter.Create(context.Background(), item))

	got, err := adapter.GetByOrgKey(context.Background(), org.ID, item.ItemKey)
	require.NoError(t, err)
	require.Equal(t, item.ID, got.ID)

	_, err = adapter.GetByOrgKey(context.Background(), org.ID, "NOPE-404")
	require.ErrorIs(t, err, projects.ErrNotFound)

	// Soft-deleted items do not resolve.
	require.NoError(t, adapter.SoftDeleteInSpace(context.Background(), item.ID, space.ID))
	_, err = adapter.GetByOrgKey(context.Background(), org.ID, item.ItemKey)
	require.ErrorIs(t, err, projects.ErrNotFound)
}

// TestItemKey_NumberNotReusedAfterDelete proves the counter is monotonic: a
// deleted item's number is never handed to a later item.
func TestItemKey_NumberNotReusedAfterDelete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	adapter := adapters.NewItemAdapter(generated.New(db.Pool))

	first := newItem(space.ID, user.ID, "First")
	require.NoError(t, adapter.Create(context.Background(), first))
	require.Equal(t, 1, first.Number)
	require.NoError(t, adapter.SoftDeleteInSpace(context.Background(), first.ID, space.ID))

	second := newItem(space.ID, user.ID, "Second")
	require.NoError(t, adapter.Create(context.Background(), second))
	require.Equal(t, 2, second.Number, "number 1 must not be reused after delete")
}
