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

	reporterPtr := user.ID
	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "To delete", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: &reporterPtr,
	}
	require.NoError(t, adapter.Create(ctx, tkt))

	// Verify it exists.
	_, err := adapter.GetByID(ctx, tkt.ID)
	require.NoError(t, err)

	// A space that does not own the ticket deletes nothing and says nothing,
	// so the assertion below cannot pass against an unscoped statement.
	strangerSpace := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	require.NoError(t, adapter.DeleteInSpace(ctx, tkt.ID, strangerSpace.ID))
	_, err = adapter.GetByID(ctx, tkt.ID)
	require.NoError(t, err, "a stranger space must not delete the ticket")

	require.NoError(t, adapter.DeleteInSpace(ctx, tkt.ID, space.ID))

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
	reporterID := user.ID
	assigned := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Assigned ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: &reporterID, AssigneeID: &assignee,
	}
	reporterPtr := user.ID
	unassigned := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Unassigned ticket", Status: tickets.StatusOpen, Priority: tickets.PriorityMedium,
		ReporterID: &reporterPtr,
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

	reporterPtr := user.ID
	tkt := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Login button broken", Status: tickets.StatusOpen, Priority: tickets.PriorityHigh,
		ReporterID: &reporterPtr,
	}
	require.NoError(t, adapter.Create(ctx, tkt))

	// A second ticket that must NOT match. Without it this test asserted
	// nothing: it ended at `_ = results`, which stays green if the
	// `search_vector @@ ...` predicate is deleted outright — and stayed green
	// through migration 049's rewrite of the very column it depends on.
	other := &tickets.Ticket{
		ID: uuid.New(), SpaceID: space.ID,
		Title: "Payment gateway outage", Status: tickets.StatusOpen, Priority: tickets.PriorityHigh,
		ReporterID: &reporterPtr,
	}
	require.NoError(t, adapter.Create(ctx, other))

	results, err := adapter.Search(ctx, space.ID, "login", 10)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	require.Equal(t, []uuid.UUID{tkt.ID}, ids,
		"search must return exactly the matching ticket — not the non-matching one, and not nothing")

	// The title half of the vector, proven separately: a word that appears
	// only in the other ticket's title must find that ticket and only it.
	results, err = adapter.Search(ctx, space.ID, "gateway", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, other.ID, results[0].ID)
}
