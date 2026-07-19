package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func TestTicketAdapter_Delete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)
	ctx := context.Background()

	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "To delete", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, tkt))

	// Verify it exists.
	_, err := adapter.GetByID(ctx, tkt.ID)
	require.NoError(t, err)

	require.NoError(t, adapter.Delete(ctx, tkt.ID))

	// After soft delete, GetByID should fail.
	_, err = adapter.GetByID(ctx, tkt.ID)
	require.Error(t, err)
}

func TestTicketAdapter_ListByAssignee(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)
	ctx := context.Background()

	assignee := user.ID
	assigned := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Assigned ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: user.ID, AssigneeID: &assignee,
	}
	unassigned := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Unassigned ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, assigned))
	require.NoError(t, adapter.Create(ctx, unassigned))

	result, err := adapter.ListByAssignee(ctx, space.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, assigned.ID, result[0].ID)
}

func TestTicketAdapter_Search(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)
	ctx := context.Background()

	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Login button broken", Status: tickets.StatusOpen, Priority: tickets.PriorityHigh,
		ReporterID: user.ID,
	}
	require.NoError(t, adapter.Create(ctx, tkt))

	results, err := adapter.Search(ctx, space.ID, "login", 10)
	require.NoError(t, err)
	_ = results
}
