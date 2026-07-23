package adapters_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestItemTypeAdapter_SeedIdempotentAndActiveFilter proves DB-specific behaviour:
// SeedDefaults is idempotent (ON CONFLICT DO NOTHING), archived types drop out of
// the active list, and CountItemsOfType tracks references.
func TestItemTypeAdapter_SeedIdempotentAndActiveFilter(t *testing.T) {
	db := testutil.NewTestDB(t)
	// CreateTestOrg already seeds the four defaults.
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewItemTypeAdapter(generated.New(db.Pool))
	ctx := context.Background()

	// Re-seeding must not duplicate.
	require.NoError(t, adapter.SeedDefaults(ctx, org.ID))
	require.NoError(t, adapter.SeedDefaults(ctx, org.ID))
	all, err := adapter.ListByOrg(ctx, org.ID)
	require.NoError(t, err)
	require.Len(t, all, 4, "seeding is idempotent — still four defaults")

	// Ordered by position: task, story, bug, epic.
	require.Equal(t, []string{"task", "story", "bug", "epic"},
		[]string{all[0].Slug, all[1].Slug, all[2].Slug, all[3].Slug})

	// Archive 'bug' → drops from the active list but stays in the full list.
	var bug *itemtypes.ItemType
	for _, tp := range all {
		if tp.Slug == "bug" {
			bug = tp
		}
	}
	require.NotNil(t, bug)
	_, err = adapter.SetArchived(ctx, bug.ID, true)
	require.NoError(t, err)

	active, err := adapter.ListActiveByOrg(ctx, org.ID)
	require.NoError(t, err)
	require.Len(t, active, 3)
	for _, tp := range active {
		require.NotEqual(t, "bug", tp.Slug, "archived type must not appear in the active list")
	}

	// NextPosition continues past the seeded max (4).
	pos, err := adapter.NextPosition(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, 5, pos)

	// CountItemsOfType is zero with no items.
	n, err := adapter.CountItemsOfType(ctx, org.ID, "task")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
