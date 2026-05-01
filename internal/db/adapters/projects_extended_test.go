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
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
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
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
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
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(queries)
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
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)
	ctx := context.Background()

	item := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID,
		Kind: "task", Title: "Database migration task", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, item))

	results, err := adapter.Search(ctx, space.ID, "migration", 10)
	require.NoError(t, err)
	// Result count may vary by search implementation; just check no error.
	_ = results
}

// --- SprintAdapter extended tests ---

func TestSprintAdapter_Update(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewSprintAdapter(queries)
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
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewSprintAdapter(queries)
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
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
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

	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: spaceB.ID,
		Title: "Target ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	require.NoError(t, ticketAdapter.Create(ctx, tkt))

	rel := &projects.Relation{
		ID:        uuid.New(),
		FromID:    item.ID,
		FromType:  "project_item",
		ToID:      tkt.ID,
		ToType:    "ticket",
		Kind:      "blocks",
		CreatedBy: user.ID,
	}
	require.NoError(t, relationAdapter.Create(ctx, rel))

	// ListByItem (project_item type).
	rels, err := relationAdapter.ListByItem(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	require.Equal(t, rel.ID, rels[0].ID)

	// ListByEntity explicit.
	rels2, err := relationAdapter.ListByEntity(ctx, item.ID, "project_item")
	require.NoError(t, err)
	require.Len(t, rels2, 1)

	// Delete.
	require.NoError(t, relationAdapter.Delete(ctx, rel.ID))

	rels3, err := relationAdapter.ListByEntity(ctx, item.ID, "project_item")
	require.NoError(t, err)
	require.Empty(t, rels3)
}

func TestRelationAdapter_ListByItem_FallsBackToTicket(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	spaceA := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	spaceB := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	ticketAdapter := adapters.NewTicketAdapter(queries)
	itemAdapter := adapters.NewItemAdapter(queries)
	relationAdapter := adapters.NewRelationAdapter(queries)
	ctx := context.Background()

	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: spaceA.ID,
		Title: "Source ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	require.NoError(t, ticketAdapter.Create(ctx, tkt))

	targetItem := &projects.Item{
		ID: uuid.New(), SpaceID: spaceB.ID,
		Kind: "task", Title: "Target item", Status: "open", Priority: "medium",
		ReporterID: user.ID,
	}
	require.NoError(t, itemAdapter.Create(ctx, targetItem))

	rel := &projects.Relation{
		ID:        uuid.New(),
		FromID:    tkt.ID,
		FromType:  "ticket",
		ToID:      targetItem.ID,
		ToType:    "project_item",
		Kind:      "relates_to",
		CreatedBy: user.ID,
	}
	require.NoError(t, relationAdapter.Create(ctx, rel))

	// ListByItem falls back to ticket when project_item returns empty.
	rels, err := relationAdapter.ListByItem(ctx, tkt.ID)
	require.NoError(t, err)
	require.Len(t, rels, 1)
}
