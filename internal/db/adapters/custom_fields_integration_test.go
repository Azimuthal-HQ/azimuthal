package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestCustomFieldDefAdapter_RoundTrip exercises every definition adapter method
// against a real database, including the JSONB options round-trip.
func TestCustomFieldDefAdapter_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewCustomFieldDefAdapter(generated.New(db.Pool))
	ctx := context.Background()

	// NextPosition on an empty org.
	pos, err := adapter.NextPosition(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, 1, pos)

	// Create a single-select field (options round-trip through JSONB).
	def := &customfields.FieldDef{
		ID: uuid.New(), OrgID: org.ID, Slug: "tier", Name: "Tier",
		Type: customfields.TypeSingleSelect, Options: []string{"gold", "silver"}, Position: pos,
	}
	require.NoError(t, adapter.Create(ctx, def))

	// GetByID and GetByOrgSlug return the stored options.
	got, err := adapter.GetByID(ctx, def.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"gold", "silver"}, got.Options)
	require.Equal(t, customfields.TypeSingleSelect, got.Type)

	bySlug, err := adapter.GetByOrgSlug(ctx, org.ID, "tier")
	require.NoError(t, err)
	require.Equal(t, def.ID, bySlug.ID)

	// A text field carries empty options.
	txt := &customfields.FieldDef{ID: uuid.New(), OrgID: org.ID, Slug: "squad", Name: "Squad", Type: customfields.TypeText, Options: []string{}, Position: 2}
	require.NoError(t, adapter.Create(ctx, txt))
	gotTxt, err := adapter.GetByID(ctx, txt.ID)
	require.NoError(t, err)
	require.Empty(t, gotTxt.Options)

	// ListByOrg returns both; ListActiveByOrg excludes archived.
	all, err := adapter.ListByOrg(ctx, org.ID)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Update: rename + replace options.
	updated, err := adapter.Update(ctx, def.ID, "Level", []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Equal(t, "Level", updated.Name)
	require.Equal(t, []string{"a", "b", "c"}, updated.Options)
	require.Equal(t, "tier", updated.Slug, "slug is immutable")

	// Archive → drops from the active list, remains in the full list.
	_, err = adapter.SetArchived(ctx, def.ID, true)
	require.NoError(t, err)
	active, err := adapter.ListActiveByOrg(ctx, org.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, "squad", active[0].Slug)

	// NextPosition advances past the max.
	pos, err = adapter.NextPosition(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, 3, pos)

	// Delete removes the definition.
	require.NoError(t, adapter.Delete(ctx, txt.ID))
	_, err = adapter.GetByID(ctx, txt.ID)
	require.ErrorIs(t, err, customfields.ErrNotFound)

	// Not-found lookups map to the sentinel.
	_, err = adapter.GetByOrgSlug(ctx, org.ID, "nope")
	require.ErrorIs(t, err, customfields.ErrNotFound)
}

// TestCustomFieldValueAdapter_RoundTrip exercises the value adapter: upsert
// (insert then update), list, and delete against a real item.
func TestCustomFieldValueAdapter_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	q := generated.New(db.Pool)

	item := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Has fields",
		Status: "open", Priority: "medium", ReporterID: user.ID,
	}
	require.NoError(t, adapters.NewItemAdapter(q).Create(context.Background(), item))

	values := adapters.NewCustomFieldValueAdapter(q)
	ctx := context.Background()

	// Insert.
	require.NoError(t, values.Upsert(ctx, item.ID, "points", "5"))
	list, err := values.ListByItemInSpace(ctx, space.ID, item.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "points", list[0].FieldSlug)
	require.Equal(t, "5", list[0].Value)

	// Upsert again → updates in place (still one row).
	require.NoError(t, values.Upsert(ctx, item.ID, "points", "8"))
	list, err = values.ListByItemInSpace(ctx, space.ID, item.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "8", list[0].Value)

	// Delete.
	require.NoError(t, values.Delete(ctx, item.ID, "points"))
	list, err = values.ListByItemInSpace(ctx, space.ID, item.ID)
	require.NoError(t, err)
	require.Empty(t, list)
}
