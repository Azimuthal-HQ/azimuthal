package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// --- ItemAdapter extended tests ---

func TestItemAdapter_ListByStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)
	ctx := context.Background()

	open := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Open item", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	done := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Done item", Status: "done", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, open))
	require.NoError(t, adapter.Create(ctx, done))

	result, err := adapter.ListByStatus(ctx, space.ID, "open")
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, open.ID, result[0].ID)
}

func TestItemAdapter_ListByAssignee(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)
	ctx := context.Background()

	assignee := user.ID
	assigned := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Assigned item", Status: "open", Priority: "medium",
		ReporterID: user.ID, AssigneeID: &assignee,
	}
	unassigned := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Unassigned item", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, assigned))
	require.NoError(t, adapter.Create(ctx, unassigned))

	result, err := adapter.ListByAssignee(ctx, space.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, assigned.ID, result[0].ID)
}

func TestItemAdapter_UpdateSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	sprint := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Sprint 1", Status: "planned", CreatedBy: user.ID,
	}
	require.NoError(t, sprintAdapter.Create(ctx, sprint))

	item := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Item for sprint", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, itemAdapter.Create(ctx, item))

	// Assign to sprint.
	require.NoError(t, itemAdapter.UpdateSprint(ctx, item.ID, &sprint.ID))

	inSprint, err := itemAdapter.ListBySprint(ctx, sprint.ID)
	require.NoError(t, err)
	require.Len(t, inSprint, 1)
	require.Equal(t, item.ID, inSprint[0].ID)

	// Remove from sprint.
	require.NoError(t, itemAdapter.UpdateSprint(ctx, item.ID, nil))

	inSprint, err = itemAdapter.ListBySprint(ctx, sprint.ID)
	require.NoError(t, err)
	require.Empty(t, inSprint)
}

func TestItemAdapter_Search(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)
	ctx := context.Background()

	item := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Database migration task", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, item))

	// An item that must NOT match. The previous version of this test ended at
	// `_ = results` under the comment "Result count may vary by search
	// implementation; just check no error" — which is exactly the reasoning
	// §2 forbids: it stays green if the `search_vector @@ ...` predicate is
	// deleted, and it stayed green through migration 049's rewrite of the
	// column it depends on.
	other := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Payment gateway outage", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, other))

	results, err := adapter.Search(ctx, space.ID, "migration", 10)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	require.Equal(t, []uuid.UUID{item.ID}, ids,
		"search must return exactly the matching item — not the non-matching one, and not nothing")

	results, err = adapter.Search(ctx, space.ID, "gateway", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, other.ID, results[0].ID)
}

// --- SprintAdapter extended tests ---

func TestSprintAdapter_Update(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	adapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	sprint := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Sprint 1", Goal: "Initial goal", Status: "planned", CreatedBy: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, sprint))

	sprint.Name = "Sprint 1 Updated"
	sprint.Goal = "New goal"
	require.NoError(t, adapter.Update(ctx, sprint))

	fetched, err := adapter.GetByID(ctx, sprint.ID)
	require.NoError(t, err)
	require.Equal(t, "Sprint 1 Updated", fetched.Name)
	require.Equal(t, "New goal", fetched.Goal)
}

func TestSprintAdapter_UpdateStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	adapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	start := time.Now()
	end := start.Add(14 * 24 * time.Hour)
	sprint := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Sprint 2", Status: "planned", CreatedBy: user.ID,
		StartsAt: &start, EndsAt: &end,
	}
	require.NoError(t, adapter.Create(ctx, sprint))

	updated, err := adapter.UpdateStatus(ctx, sprint.ID, "active")
	require.NoError(t, err)
	require.Equal(t, "active", updated.Status)
}

// --- RelationAdapter tests ---

func TestRelationAdapter_CreateListDelete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	queries := generated.New(db.Pool)
	itemAdapter := adapters.NewItemAdapter(queries)
	ticketAdapter := adapters.NewTicketAdapter(queries)
	relationAdapter := adapters.NewRelationAdapter(queries)
	ctx := context.Background()

	item := &projects.Item{
		ID: uuid.New(), SpaceID: spaceA.ID,
		Kind: "task", Title: "Source item", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, itemAdapter.Create(ctx, item))

	reporterPtr := user.ID
	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: spaceB.ID,
		Title: "Target ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: &reporterPtr,
	}
	require.NoError(t, ticketAdapter.Create(ctx, tkt))

	relID := uuid.New()
	rel := &projects.NewRelation{
		FromID:    item.ID,
		FromType:  "project_item",
		ToID:      tkt.ID,
		ToType:    "ticket",
		Kind:      "blocks",
		CreatedBy: user.ID,
	}
	require.NoError(t, relationAdapter.Create(ctx, relID, rel))

	readable := []uuid.UUID{spaceA.ID, spaceB.ID}

	rels, err := relationAdapter.ListForEntity(ctx, item.ID, "project_item", readable)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	require.Equal(t, relID, rels[0].ID)
	require.Equal(t, projects.DirectionOutgoing, rels[0].Direction)
	require.True(t, rels[0].FarReadable)
	require.Equal(t, "Target ticket", *rels[0].FarTitle)

	// Delete.
	require.NoError(t, relationAdapter.Delete(ctx, relID))

	rels3, err := relationAdapter.ListForEntity(ctx, item.ID, "project_item", readable)
	require.NoError(t, err)
	require.Empty(t, rels3)
}

// TestRelationAdapter_ListForEntity_TypeIsExplicit replaces a test of the
// removed ListByItem shim, which ran the query as 'project_item' and, on an
// empty result, ran it again as 'ticket' — inferring the entity's type by
// guessing rather than being told.
//
// The type is now a required argument, so this asserts the property that
// replaced the fallback: a ticket-typed entity lists its own relations, and the
// same id queried under the wrong type lists nothing.
func TestRelationAdapter_ListForEntity_TypeIsExplicit(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	ticketAdapter := adapters.NewTicketAdapter(queries)
	itemAdapter := adapters.NewItemAdapter(queries)
	relationAdapter := adapters.NewRelationAdapter(queries)
	ctx := context.Background()

	reporterPtr := user.ID
	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: spaceA.ID,
		Title: "Source ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: &reporterPtr,
	}
	require.NoError(t, ticketAdapter.Create(ctx, tkt))

	targetItem := &projects.Item{
		ID: uuid.New(), SpaceID: spaceB.ID,
		Kind: "task", Title: "Target item", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, itemAdapter.Create(ctx, targetItem))

	require.NoError(t, relationAdapter.Create(ctx, uuid.New(), &projects.NewRelation{
		FromID:    tkt.ID,
		FromType:  "ticket",
		ToID:      targetItem.ID,
		ToType:    "project_item",
		Kind:      "relates_to",
		CreatedBy: user.ID,
	}))

	readable := []uuid.UUID{spaceA.ID, spaceB.ID}

	rels, err := relationAdapter.ListForEntity(ctx, tkt.ID, "ticket", readable)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	require.Equal(t, "Target item", *rels[0].FarTitle)

	wrongType, err := relationAdapter.ListForEntity(ctx, tkt.ID, "project_item", readable)
	require.NoError(t, err)
	require.Empty(t, wrongType, "the same id under the wrong entity type must not match")
}
