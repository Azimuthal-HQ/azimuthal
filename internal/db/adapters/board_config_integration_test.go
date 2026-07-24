package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Migration 035 pushes two invariants into the schema rather than trusting
// application code to remember them:
//
//   - board_column_statuses' primary key is (space_id, status), so a status
//     cannot appear in two columns.
//   - its foreign key to board_columns is ON DELETE RESTRICT, so a column
//     cannot be dropped while it still owns statuses.
//
// These tests hit real PostgreSQL, because a constraint that only exists in a
// migration file is a constraint nobody has verified.

func newBoardFixture(t *testing.T) (context.Context, *adapters.BoardConfigAdapter, uuid.UUID) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	return context.Background(), adapters.NewBoardConfigAdapter(db.Pool), space.ID
}

func boardCol(name string, position int, statuses ...string) projects.BoardColumn {
	return projects.BoardColumn{
		ID: uuid.New(), Name: name, Position: position, Statuses: statuses,
	}
}

func TestBoardConfigAdapter_ReplaceAndRead(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)
	limit := 4

	doing := boardCol("Doing", 1, "in_progress", "in_review")
	doing.WIPLimit = &limit
	want := []projects.BoardColumn{
		boardCol("To Do", 0, "open"),
		doing,
		boardCol("Done", 2, "done"),
	}
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, want))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// Ordered by position, carrying their statuses and limits.
	require.Equal(t, "To Do", got[0].Name)
	require.Equal(t, "Doing", got[1].Name)
	require.Equal(t, "Done", got[2].Name)
	require.ElementsMatch(t, []string{"in_progress", "in_review"}, got[1].Statuses)
	require.NotNil(t, got[1].WIPLimit)
	require.Equal(t, 4, *got[1].WIPLimit)
	require.Nil(t, got[0].WIPLimit, "a column with no limit must read back as no limit, not zero")
}

func TestBoardConfigAdapter_NoStoredConfigReadsEmpty(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Empty(t, got, "an untouched space must have no stored columns so the service derives the default")
}

func TestBoardConfigAdapter_ReplaceIsAWholeSwap(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{
		boardCol("Old A", 0, "open"),
		boardCol("Old B", 1, "done"),
	}))
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{
		boardCol("Only", 0, "open", "done"),
	}))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Len(t, got, 1, "the previous columns must be gone, not merged with the new ones")
	require.Equal(t, "Only", got[0].Name)
	require.ElementsMatch(t, []string{"open", "done"}, got[0].Statuses)
}

func TestBoardConfigAdapter_ReplaceWithNothingClearsTheConfig(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{
		boardCol("Something", 0, "open"),
	}))
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, nil))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Empty(t, got, "clearing the config must return the space to the derived default")
}

func TestBoardConfigAdapter_DeleteColumnRehomesStatuses(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	doing := boardCol("Doing", 0, "in_progress")
	done := boardCol("Done", 1, "done")
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{doing, done}))

	require.NoError(t, adapter.DeleteColumn(ctx, spaceID, doing.ID, done.ID))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Done", got[0].Name)
	require.ElementsMatch(t, []string{"in_progress", "done"}, got[0].Statuses,
		"the deleted column's status must have moved to the target, not vanished")
}

// The invariant stated directly at the storage layer: a raw DELETE of a column
// that still owns statuses is refused. This is ON DELETE RESTRICT doing the
// work — change it to CASCADE in migration 035 and this test fails, because the
// delete then succeeds and takes the status mappings down with it, leaving
// those items with no column to appear in.
//
// It goes through the generated query rather than the adapter deliberately:
// the adapter always re-homes first, so only a direct delete can show that the
// database, not the adapter, is what forbids this.
func TestBoardConfigAdapter_RawDeleteOfAColumnWithStatusesIsRefused(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	ctx := context.Background()
	adapter := adapters.NewBoardConfigAdapter(db.Pool)

	doing := boardCol("Doing", 0, "in_progress")
	done := boardCol("Done", 1, "done")
	require.NoError(t, adapter.ReplaceConfig(ctx, space.ID, []projects.BoardColumn{doing, done}))

	err := generated.New(db.Pool).DeleteBoardColumn(ctx, doing.ID)
	require.Error(t, err, "the schema must refuse to drop a column that still owns statuses")

	got, listErr := adapter.ListColumns(ctx, space.ID)
	require.NoError(t, listErr)
	require.Len(t, got, 2, "the refused delete must have changed nothing")
}

// The adapter's own guard on the same ground: re-homing onto a column that does
// not exist fails the foreign key, so the whole transaction rolls back rather
// than leaving the statuses orphaned.
func TestBoardConfigAdapter_DeleteRollsBackWhenTargetIsMissing(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	doing := boardCol("Doing", 0, "in_progress")
	done := boardCol("Done", 1, "done")
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{doing, done}))

	err := adapter.DeleteColumn(ctx, spaceID, doing.ID, uuid.New())
	require.Error(t, err, "re-homing onto a nonexistent column must fail rather than orphan the statuses")

	got, listErr := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, listErr)
	require.Len(t, got, 2, "the failed deletion must have rolled back completely")
}

// A status belongs to exactly one column: the (space_id, status) primary key
// says so. Without it a save could quietly map "done" into two columns and the
// same item would render twice.
func TestBoardConfigAdapter_StatusCannotBeMappedTwice(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	// ReplaceConfig upserts, so the second mapping of "done" overwrites the
	// first rather than duplicating it — the row count is the proof.
	a := boardCol("A", 0, "done")
	b := boardCol("B", 1, "done")
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{a, b}))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)

	total := 0
	for _, c := range got {
		total += len(c.Statuses)
	}
	require.Equal(t, 1, total, "the status must exist in exactly one column, never two")
}

func TestBoardConfigAdapter_ConfigIsPerSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	ctx := context.Background()
	adapter := adapters.NewBoardConfigAdapter(db.Pool)

	require.NoError(t, adapter.ReplaceConfig(ctx, spaceA.ID, []projects.BoardColumn{
		boardCol("A Only", 0, "open"),
	}))

	gotB, err := adapter.ListColumns(ctx, spaceB.ID)
	require.NoError(t, err)
	require.Empty(t, gotB, "one space's board configuration must not leak into another's")

	gotA, err := adapter.ListColumns(ctx, spaceA.ID)
	require.NoError(t, err)
	require.Len(t, gotA, 1)
}

// Two columns may not share a name in one space, but the same name in two
// different spaces is fine.
func TestBoardConfigAdapter_NamesAreUniquePerSpaceOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	ctx := context.Background()
	adapter := adapters.NewBoardConfigAdapter(db.Pool)

	require.NoError(t, adapter.ReplaceConfig(ctx, spaceA.ID, []projects.BoardColumn{boardCol("Doing", 0, "open")}))
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceB.ID, []projects.BoardColumn{boardCol("Doing", 0, "open")}))

	err := adapter.ReplaceConfig(ctx, spaceA.ID, []projects.BoardColumn{
		boardCol("Doing", 0, "open"),
		boardCol("Doing", 1, "done"),
	})
	require.Error(t, err, "two columns in one space may not share a name")
}

// Reordering renumbers every column inside one transaction. The deferred
// unique constraint on (space_id, position) is what lets that happen without
// shuffling through temporary positions.
func TestBoardConfigAdapter_ReorderRenumbersWithoutCollision(t *testing.T) {
	ctx, adapter, spaceID := newBoardFixture(t)

	first := boardCol("First", 0, "open")
	second := boardCol("Second", 1, "done")
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{first, second}))

	// Swap them, keeping ids.
	first.Position, second.Position = 1, 0
	require.NoError(t, adapter.ReplaceConfig(ctx, spaceID, []projects.BoardColumn{second, first}))

	got, err := adapter.ListColumns(ctx, spaceID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "Second", got[0].Name)
	require.Equal(t, "First", got[1].Name)
}
