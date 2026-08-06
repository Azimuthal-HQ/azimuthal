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

// fieldValueFixture is the two-space, two-entity-type bed the polymorphic
// value tests share: one Vector space with an item, one Beacon space with a
// ticket, both in one org.
type fieldValueFixture struct {
	db     *testutil.TestDB
	q      *generated.Queries
	values *adapters.CustomFieldValueAdapter
	org    testutil.Org
	user   testutil.User
	vector testutil.Space
	beacon testutil.Space
	itemID uuid.UUID
	tickID uuid.UUID
}

func newFieldValueFixture(t *testing.T) *fieldValueFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	f := &fieldValueFixture{db: db, q: generated.New(db.Pool)}
	f.values = adapters.NewCustomFieldValueAdapter(f.q)
	f.org = testutil.CreateTestOrg(t, db.Pool)
	f.user = testutil.CreateTestUser(t, db.Pool, f.org.ID)
	f.vector = testutil.CreateTestSpace(t, db.Pool, f.org.ID, f.user.ID, "vector")
	f.beacon = testutil.CreateTestSpace(t, db.Pool, f.org.ID, f.user.ID, "beacon")

	item := &projects.Item{
		ID: uuid.New(), SpaceID: f.vector.ID, Kind: "task", Title: "Has fields",
		Status: "open", Priority: "medium", ReporterID: f.user.ID,
	}
	require.NoError(t, adapters.NewItemAdapter(f.q).Create(context.Background(), item))
	f.itemID = item.ID

	f.tickID = uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO tickets (id, space_id, number, title, description, status, priority, reporter_id, rank)
		 VALUES ($1, $2, 1, 'Ticket with fields', '', 'open', 'medium', $3, 'a')`,
		f.tickID, f.beacon.ID, f.user.ID)
	require.NoError(t, err)
	return f
}

// TestCustomFieldValueAdapter_RoundTrip exercises upsert (insert then update),
// list, and clear for both an item and a ticket — the polymorphism migration
// 053 exists for.
func TestCustomFieldValueAdapter_RoundTrip(t *testing.T) {
	f := newFieldValueFixture(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		spaceID    uuid.UUID
		entityType string
		entityID   uuid.UUID
	}{
		{"project_item", f.vector.ID, customfields.EntityTypeProjectItem, f.itemID},
		{"ticket", f.beacon.ID, customfields.EntityTypeTicket, f.tickID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Insert.
			ok, err := f.values.UpsertInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID, "points", "5")
			require.NoError(t, err)
			require.True(t, ok)
			list, err := f.values.ListForEntityInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, "points", list[0].FieldSlug)
			require.Equal(t, "5", list[0].Value)

			// Upsert again → updates in place (still one row).
			ok, err = f.values.UpsertInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID, "points", "8")
			require.NoError(t, err)
			require.True(t, ok)
			list, err = f.values.ListForEntityInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, "8", list[0].Value)

			// Clear.
			require.NoError(t, f.values.DeleteInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID, "points"))
			list, err = f.values.ListForEntityInSpace(ctx, tc.spaceID, tc.entityType, tc.entityID)
			require.NoError(t, err)
			require.Empty(t, list)
		})
	}
}

// The write path carries its own authorization. UpsertItemFieldValue and
// DeleteItemFieldValue — the statements these replaced — had no space
// predicate and no org predicate at all: nothing in either statement
// constrained the row to the caller's space, and the entire write-path
// authorization was the unenforced convention that the one calling handler
// resolved the item first. These tests call the STORE directly, exactly the
// call a handler that skipped the convention would make, and prove the
// statement itself refuses: fails-before, passes-after the predicate moved
// into the query.
func TestCustomFieldValueAdapter_WritesRefuseEntitiesOutsideTheSpace(t *testing.T) {
	f := newFieldValueFixture(t)
	ctx := context.Background()

	// A genuine value to try to overwrite/delete from the wrong space.
	ok, err := f.values.UpsertInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID, "points", "8")
	require.NoError(t, err)
	require.True(t, ok)

	otherSpace := testutil.CreateTestSpace(t, f.db.Pool, f.org.ID, f.user.ID, "vector")
	otherOrg := testutil.CreateTestOrg(t, f.db.Pool)
	otherOrgUser := testutil.CreateTestUser(t, f.db.Pool, otherOrg.ID)
	otherOrgSpace := testutil.CreateTestSpace(t, f.db.Pool, otherOrg.ID, otherOrgUser.ID, "vector")

	refusedUpsert := func(name string, spaceID uuid.UUID, entityType string) {
		t.Run("upsert/"+name, func(t *testing.T) {
			ok, err := f.values.UpsertInSpace(ctx, spaceID, entityType, f.itemID, "points", "666")
			require.NoError(t, err)
			require.False(t, ok, "the statement must match no entity and write nothing")
			list, err := f.values.ListForEntityInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, "8", list[0].Value, "the stored value must be untouched")
		})
	}
	// The item addressed through a sibling space the caller could write.
	refusedUpsert("cross-space", otherSpace.ID, customfields.EntityTypeProjectItem)
	// The item addressed through another organisation's space entirely.
	refusedUpsert("cross-org", otherOrgSpace.ID, customfields.EntityTypeProjectItem)
	// The right space with the wrong entity type: the discriminator is part
	// of the predicate, not a label.
	refusedUpsert("wrong-entity-type", f.vector.ID, customfields.EntityTypeTicket)
	// The zero space. There is no readable-set array in these statements —
	// the space is a scalar — so this is the fail-closed analogue of the
	// empty readable set: nothing, not everything.
	refusedUpsert("zero-space", uuid.Nil, customfields.EntityTypeProjectItem)

	t.Run("delete cross-space affects zero rows", func(t *testing.T) {
		require.NoError(t, f.values.DeleteInSpace(ctx, otherSpace.ID, customfields.EntityTypeProjectItem, f.itemID, "points"))
		list, err := f.values.ListForEntityInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID)
		require.NoError(t, err)
		require.Len(t, list, 1, "a cross-space delete must not remove the value")
	})
	t.Run("delete in the right space removes it", func(t *testing.T) {
		require.NoError(t, f.values.DeleteInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID, "points"))
		list, err := f.values.ListForEntityInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID)
		require.NoError(t, err)
		require.Empty(t, list)
	})
}

// A soft-deleted entity's values stop being readable AND writable: every arm
// of the predicate carries deleted_at IS NULL, so the values neither list nor
// accept writes once the entity is gone — they are simply unreachable, like
// the entity.
func TestCustomFieldValueAdapter_SoftDeletedEntityIsUnreachable(t *testing.T) {
	f := newFieldValueFixture(t)
	ctx := context.Background()

	ok, err := f.values.UpsertInSpace(ctx, f.beacon.ID, customfields.EntityTypeTicket, f.tickID, "env", "prod")
	require.NoError(t, err)
	require.True(t, ok)

	_, err = f.db.Pool.Exec(ctx, `UPDATE tickets SET deleted_at = now() WHERE id = $1`, f.tickID)
	require.NoError(t, err)

	list, err := f.values.ListForEntityInSpace(ctx, f.beacon.ID, customfields.EntityTypeTicket, f.tickID)
	require.NoError(t, err)
	require.Empty(t, list, "a soft-deleted ticket's values must stop being readable")

	ok, err = f.values.UpsertInSpace(ctx, f.beacon.ID, customfields.EntityTypeTicket, f.tickID, "env", "dev")
	require.NoError(t, err)
	require.False(t, ok, "a soft-deleted ticket must not accept new values")
}

// CountByOrgSlug counts across all three entity types and stays inside the
// org: items carry org_id directly, tickets and pages reach it through their
// space, and another org's values under the same slug are invisible.
func TestCustomFieldValueAdapter_CountByOrgSlugSpansEntityTypes(t *testing.T) {
	f := newFieldValueFixture(t)
	ctx := context.Background()

	ok, err := f.values.UpsertInSpace(ctx, f.vector.ID, customfields.EntityTypeProjectItem, f.itemID, "points", "8")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.values.UpsertInSpace(ctx, f.beacon.ID, customfields.EntityTypeTicket, f.tickID, "points", "13")
	require.NoError(t, err)
	require.True(t, ok)

	// Another org holding the same slug must not be counted.
	otherOrg := testutil.CreateTestOrg(t, f.db.Pool)
	otherUser := testutil.CreateTestUser(t, f.db.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, f.db.Pool, otherOrg.ID, otherUser.ID, "vector")
	otherItem := &projects.Item{
		ID: uuid.New(), SpaceID: otherSpace.ID, Kind: "task", Title: "Other org",
		Status: "open", Priority: "medium", ReporterID: otherUser.ID,
	}
	require.NoError(t, adapters.NewItemAdapter(f.q).Create(ctx, otherItem))
	ok, err = f.values.UpsertInSpace(ctx, otherSpace.ID, customfields.EntityTypeProjectItem, otherItem.ID, "points", "99")
	require.NoError(t, err)
	require.True(t, ok)

	n, err := f.values.CountByOrgSlug(ctx, f.org.ID, "points")
	require.NoError(t, err)
	require.Equal(t, 2, n, "one item value + one ticket value, and nothing from the other org")

	// A soft-deleted entity's value drops out of the count.
	_, err = f.db.Pool.Exec(ctx, `UPDATE tickets SET deleted_at = now() WHERE id = $1`, f.tickID)
	require.NoError(t, err)
	n, err = f.values.CountByOrgSlug(ctx, f.org.ID, "points")
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

// TestCustomFieldScopeAdapter_RoundTrip exercises the scope adapter: upsert
// (attach then re-flag), list by field and by form, get, delete — and the
// org predicate that is in the statement rather than the caller.
func TestCustomFieldScopeAdapter_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	vector := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	q := generated.New(db.Pool)
	defs := adapters.NewCustomFieldDefAdapter(q)
	scopes := adapters.NewCustomFieldScopeAdapter(q)
	ctx := context.Background()

	def := &customfields.FieldDef{
		ID: uuid.New(), OrgID: org.ID, Slug: "env", Name: "Env",
		Type: customfields.TypeText, Options: []string{}, Position: 4,
	}
	require.NoError(t, defs.Create(ctx, def))

	// Attach.
	saved, err := scopes.Upsert(ctx, org.ID, &customfields.FieldScope{
		FieldID: def.ID, SpaceID: vector.ID, EntityType: customfields.EntityTypeProjectItem,
		Required: false, Position: def.Position,
	})
	require.NoError(t, err)
	require.False(t, saved.Required)
	require.Equal(t, 4, saved.Position)

	// Re-attach flips required and keeps position (the statement's DO UPDATE
	// touches required alone).
	saved, err = scopes.Upsert(ctx, org.ID, &customfields.FieldScope{
		FieldID: def.ID, SpaceID: vector.ID, EntityType: customfields.EntityTypeProjectItem,
		Required: true, Position: 99,
	})
	require.NoError(t, err)
	require.True(t, saved.Required)
	require.Equal(t, 4, saved.Position, "re-attach must not reshuffle the form")

	byField, err := scopes.ListByField(ctx, def.ID)
	require.NoError(t, err)
	require.Len(t, byField, 1)
	byForm, err := scopes.ListForSpaceEntity(ctx, vector.ID, customfields.EntityTypeProjectItem)
	require.NoError(t, err)
	require.Len(t, byForm, 1)
	got, err := scopes.Get(ctx, def.ID, vector.ID, customfields.EntityTypeProjectItem)
	require.NoError(t, err)
	require.True(t, got.Required)

	// The org predicate is in the statement: another org's space matches
	// nothing, writes nothing, and answers not-found.
	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	otherUser := testutil.CreateTestUser(t, db.Pool, otherOrg.ID)
	foreignSpace := testutil.CreateTestSpace(t, db.Pool, otherOrg.ID, otherUser.ID, "vector")
	_, err = scopes.Upsert(ctx, org.ID, &customfields.FieldScope{
		FieldID: def.ID, SpaceID: foreignSpace.ID, EntityType: customfields.EntityTypeProjectItem,
	})
	require.ErrorIs(t, err, customfields.ErrSpaceNotFound)
	byField, err = scopes.ListByField(ctx, def.ID)
	require.NoError(t, err)
	require.Len(t, byField, 1, "the refused attach must not have written a row")

	// A soft-deleted space refuses the attach the same way.
	_, err = db.Pool.Exec(ctx, `UPDATE spaces SET deleted_at = now() WHERE id = $1`, foreignSpace.ID)
	require.NoError(t, err)
	_, err = scopes.Upsert(ctx, otherOrg.ID, &customfields.FieldScope{
		FieldID: def.ID, SpaceID: foreignSpace.ID, EntityType: customfields.EntityTypeProjectItem,
	})
	require.ErrorIs(t, err, customfields.ErrSpaceNotFound)

	// Get on an absent triple maps to the sentinel.
	_, err = scopes.Get(ctx, def.ID, vector.ID, customfields.EntityTypeTicket)
	require.ErrorIs(t, err, customfields.ErrScopeNotFound)

	// Delete reports found / not found.
	found, err := scopes.Delete(ctx, def.ID, vector.ID, customfields.EntityTypeProjectItem)
	require.NoError(t, err)
	require.True(t, found)
	found, err = scopes.Delete(ctx, def.ID, vector.ID, customfields.EntityTypeProjectItem)
	require.NoError(t, err)
	require.False(t, found)

	// SpaceOrgType resolves live spaces and refuses deleted ones.
	gotOrg, gotType, err := scopes.SpaceOrgType(ctx, vector.ID)
	require.NoError(t, err)
	require.Equal(t, org.ID, gotOrg)
	require.Equal(t, "vector", gotType)
	_, _, err = scopes.SpaceOrgType(ctx, foreignSpace.ID)
	require.ErrorIs(t, err, customfields.ErrSpaceNotFound)

	// Deleting the definition cascades its scope rows (an attachment of
	// nothing governs nothing) — while values would survive; that pairing is
	// proved end to end in the API integration tests.
	saved, err = scopes.Upsert(ctx, org.ID, &customfields.FieldScope{
		FieldID: def.ID, SpaceID: vector.ID, EntityType: customfields.EntityTypeProjectItem,
	})
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.NoError(t, defs.Delete(ctx, def.ID))
	byForm, err = scopes.ListForSpaceEntity(ctx, vector.ID, customfields.EntityTypeProjectItem)
	require.NoError(t, err)
	require.Empty(t, byForm, "scope rows die with their definition")
}
