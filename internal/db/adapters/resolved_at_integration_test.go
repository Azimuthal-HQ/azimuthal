package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// resolved_at is written NOWHERE before D5 — three consumers read it and every
// one saw eternal null (TestViewResults_ResolvedAtRangeIsWiredEvenThoughNothingWritesIt
// pinned exactly that). These tests pin the wiring that fills it: set on entering
// a done-category state, cleared on leaving it, through EVERY status writer, plus
// the deliberate no-backfill and the "untouched by a non-status edit" property.
//
// The discriminator is workflow_states.category ('todo','in_progress','done';
// migration 016). The workflow path reads that category in SQL from the target
// state; the no-workflow path has no state to read and keys off the terminal
// status names instead (tickets.Status.IsDone / projects.IsDoneStatus).

type resolvedAtFixture struct {
	db     *testutil.TestDB
	q      *generated.Queries
	tx     *adapters.WorkflowTransitionTxAdapter
	ticket *adapters.TicketAdapter
	item   *adapters.ItemAdapter

	orgID, spaceID, userID uuid.UUID

	// A controlled workflow, so the categories are explicit rather than inherited
	// from whatever the seed produced. Two done states, so done -> done can be
	// exercised.
	sTodo, sProg, sDone, sDone2 uuid.UUID
}

const (
	nTodo  = "wf_todo"
	nProg  = "wf_prog"
	nDone  = "wf_done"
	nDone2 = "wf_done2"
)

func setupResolvedAt(t *testing.T) *resolvedAtFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	q := generated.New(db.Pool)

	f := &resolvedAtFixture{
		db: db, q: q,
		tx:     adapters.NewWorkflowTransitionTxAdapter(db.Pool),
		ticket: adapters.NewTicketAdapter(q),
		item:   adapters.NewItemAdapter(q),
		orgID:  org.ID, spaceID: space.ID, userID: user.ID,
		sTodo: uuid.New(), sProg: uuid.New(), sDone: uuid.New(), sDone2: uuid.New(),
	}

	ctx := context.Background()
	var wfID uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx,
		`INSERT INTO workflows (org_id, name, description, is_default, applies_to)
		 VALUES ($1,'resolved-at-wf','',false,'both') RETURNING id`, org.ID).Scan(&wfID))
	for _, s := range []struct {
		id       uuid.UUID
		name     string
		category string
		pos      int
	}{
		{f.sTodo, nTodo, "todo", 0},
		{f.sProg, nProg, "in_progress", 1},
		{f.sDone, nDone, "done", 2},
		{f.sDone2, nDone2, "done", 3},
	} {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO workflow_states (id, workflow_id, name, category, position)
			 VALUES ($1,$2,$3,$4,$5)`, s.id, wfID, s.name, s.category, s.pos)
		require.NoError(t, err)
	}
	return f
}

// createTicket makes a ticket in the given status/state with resolved_at unset
// (CreateTicket never writes the column — that is the point of the no-backfill).
func (f *resolvedAtFixture) createTicket(t *testing.T, number int32, status string, stateID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.q.CreateTicket(context.Background(), generated.CreateTicketParams{
		ID: id, SpaceID: f.spaceID, Number: number, Title: "T", Description: "d",
		Status: status, Priority: "medium",
		ReporterID:      pgtype.UUID{Bytes: f.userID, Valid: true},
		Rank:            "a",
		WorkflowStateID: optState(stateID),
	})
	require.NoError(t, err)
	return id
}

func (f *resolvedAtFixture) createItem(t *testing.T, status string, stateID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.q.CreateProjectItem(context.Background(), generated.CreateProjectItemParams{
		ID: id, SpaceID: f.spaceID, Kind: "task", Title: "I", Description: "d",
		Status: status, Priority: "medium", ReporterID: f.userID, Rank: "a",
		WorkflowStateID: optState(stateID),
	})
	require.NoError(t, err)
	return id
}

func optState(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func (f *resolvedAtFixture) applyTicket(t *testing.T, id uuid.UUID, toStatus string, toState uuid.UUID, from string) {
	t.Helper()
	require.NoError(t, f.tx.ApplyTransition(context.Background(), workflow.ApplyInput{
		EntityType: workflow.ApprovalEntityTicket, EntityID: id,
		OrgID: f.orgID, SpaceID: f.spaceID, ActorID: f.userID,
		ToStatus: toStatus, ToStateID: &toState, ExpectFromStatus: from,
	}))
}

func (f *resolvedAtFixture) applyItem(t *testing.T, id uuid.UUID, toStatus string, toState uuid.UUID, from string) {
	t.Helper()
	require.NoError(t, f.tx.ApplyTransition(context.Background(), workflow.ApplyInput{
		EntityType: workflow.ApprovalEntityItem, EntityID: id,
		OrgID: f.orgID, SpaceID: f.spaceID, ActorID: f.userID,
		ToStatus: toStatus, ToStateID: &toState, ExpectFromStatus: from,
	}))
}

func (f *resolvedAtFixture) ticketResolvedAt(t *testing.T, id uuid.UUID) pgtype.Timestamptz {
	t.Helper()
	row, err := f.q.GetTicketByID(context.Background(), id)
	require.NoError(t, err)
	return row.ResolvedAt
}

func (f *resolvedAtFixture) itemResolvedAt(t *testing.T, id uuid.UUID) pgtype.Timestamptz {
	t.Helper()
	row, err := f.q.GetProjectItemByID(context.Background(), id)
	require.NoError(t, err)
	return row.ResolvedAt
}

// TestResolvedAt_SetOnDone_ClearedOnReopen_AllWriters is the table over every
// status writer found for D5. Each row starts a fresh entity in a non-done
// state (resolved_at null), transitions it INTO a done-category state (must set
// resolved_at), then OUT again (must clear it). A writer that fails either half
// is the residual bug the phase was about.
func TestResolvedAt_SetOnDone_ClearedOnReopen_AllWriters(t *testing.T) {
	cases := []struct {
		name     string
		create   func(f *resolvedAtFixture) uuid.UUID
		toDone   func(f *resolvedAtFixture, id uuid.UUID)
		toReopen func(f *resolvedAtFixture, id uuid.UUID)
		read     func(f *resolvedAtFixture, id uuid.UUID) pgtype.Timestamptz
	}{
		{
			name:   "ticket workflow transition (UpdateTicketWorkflowState, category-driven)",
			create: func(f *resolvedAtFixture) uuid.UUID { return f.createTicket(t, 1, nTodo, f.sTodo) },
			toDone: func(f *resolvedAtFixture, id uuid.UUID) { f.applyTicket(t, id, nDone, f.sDone, nTodo) },
			toReopen: func(f *resolvedAtFixture, id uuid.UUID) {
				f.applyTicket(t, id, nTodo, f.sTodo, nDone)
			},
			read: func(f *resolvedAtFixture, id uuid.UUID) pgtype.Timestamptz { return f.ticketResolvedAt(t, id) },
		},
		{
			name:   "item workflow transition (UpdateProjectItemWorkflowState, category-driven)",
			create: func(f *resolvedAtFixture) uuid.UUID { return f.createItem(t, nTodo, f.sTodo) },
			toDone: func(f *resolvedAtFixture, id uuid.UUID) { f.applyItem(t, id, nDone, f.sDone, nTodo) },
			toReopen: func(f *resolvedAtFixture, id uuid.UUID) {
				f.applyItem(t, id, nTodo, f.sTodo, nDone)
			},
			read: func(f *resolvedAtFixture, id uuid.UUID) pgtype.Timestamptz { return f.itemResolvedAt(t, id) },
		},
		{
			name:   "ticket no-workflow status (UpdateTicketStatus, name-driven)",
			create: func(f *resolvedAtFixture) uuid.UUID { return f.createTicket(t, 2, "open", uuid.Nil) },
			toDone: func(f *resolvedAtFixture, id uuid.UUID) {
				_, err := f.ticket.UpdateStatus(context.Background(), id, tickets.StatusResolved)
				require.NoError(t, err)
			},
			toReopen: func(f *resolvedAtFixture, id uuid.UUID) {
				_, err := f.ticket.UpdateStatus(context.Background(), id, tickets.StatusOpen)
				require.NoError(t, err)
			},
			read: func(f *resolvedAtFixture, id uuid.UUID) pgtype.Timestamptz { return f.ticketResolvedAt(t, id) },
		},
		{
			name:   "item no-workflow status (UpdateProjectItemStatus, name-driven)",
			create: func(f *resolvedAtFixture) uuid.UUID { return f.createItem(t, "todo", uuid.Nil) },
			toDone: func(f *resolvedAtFixture, id uuid.UUID) {
				_, err := f.item.UpdateStatus(context.Background(), id, "done")
				require.NoError(t, err)
			},
			toReopen: func(f *resolvedAtFixture, id uuid.UUID) {
				_, err := f.item.UpdateStatus(context.Background(), id, "todo")
				require.NoError(t, err)
			},
			read: func(f *resolvedAtFixture, id uuid.UUID) pgtype.Timestamptz { return f.itemResolvedAt(t, id) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupResolvedAt(t)
			id := tc.create(f)
			require.False(t, tc.read(f, id).Valid, "fresh non-done entity must have null resolved_at")

			tc.toDone(f, id)
			done := tc.read(f, id)
			require.True(t, done.Valid, "entering a done state must SET resolved_at")

			tc.toReopen(f, id)
			require.False(t, tc.read(f, id).Valid, "leaving a done state must CLEAR resolved_at")
		})
	}
}

// TestResolvedAt_NoBackfill_PreExistingDoneStaysNull pins the honest
// consequence: a ticket that is already in a done state but was never
// transitioned there by this wiring keeps a null resolved_at. Creation is not a
// transition, and inventing a resolution moment would poison metrics, so the
// column stays null until the entity's next real transition.
func TestResolvedAt_NoBackfill_PreExistingDoneStaysNull(t *testing.T) {
	f := setupResolvedAt(t)

	// Born directly into a done state, as a pre-D5 ticket effectively is.
	tk := f.createTicket(t, 1, nDone, f.sDone)
	require.False(t, f.ticketResolvedAt(t, tk).Valid,
		"a pre-existing done ticket must NOT be backfilled at creation")

	it := f.createItem(t, nDone, f.sDone)
	require.False(t, f.itemResolvedAt(t, it).Valid,
		"a pre-existing done item must NOT be backfilled at creation")

	// Its next transition is where the column starts telling the truth: a
	// done -> done move stamps the moment it was next observed in done.
	f.applyTicket(t, tk, nDone2, f.sDone2, nDone)
	require.True(t, f.ticketResolvedAt(t, tk).Valid,
		"the next transition into done stamps resolved_at (no longer null)")
}

// TestResolvedAt_UntouchedByNonStatusEdit pins that an ordinary field edit — the
// PATCH path through UpdateTicket, which rewrites the status column to its
// existing value but never touches resolved_at — leaves a resolved ticket's
// resolution moment exactly where it was.
func TestResolvedAt_UntouchedByNonStatusEdit(t *testing.T) {
	f := setupResolvedAt(t)
	tk := f.createTicket(t, 1, nTodo, f.sTodo)
	f.applyTicket(t, tk, nDone, f.sDone, nTodo)
	before := f.ticketResolvedAt(t, tk)
	require.True(t, before.Valid)

	// A title edit through the PATCH adapter. Status is carried unchanged.
	full, err := f.ticket.GetByIDInSpace(context.Background(), f.spaceID, tk)
	require.NoError(t, err)
	full.Title = "renamed, still resolved"
	require.NoError(t, f.ticket.Update(context.Background(), full))

	after := f.ticketResolvedAt(t, tk)
	require.True(t, after.Valid, "a non-status edit must not clear resolved_at")
	require.WithinDuration(t, before.Time, after.Time, 0,
		"a non-status edit must not move resolved_at")
}

// TestResolvedAt_PreservedAcrossDoneToDone pins the COALESCE semantics: a
// done -> done move (resolved -> closed) keeps the moment the entity FIRST
// reached done rather than restamping. The column answers "when did this reach
// done", and it has not left done. A reopen (tested above) is what clears it.
func TestResolvedAt_PreservedAcrossDoneToDone(t *testing.T) {
	f := setupResolvedAt(t)
	tk := f.createTicket(t, 1, nTodo, f.sTodo)

	f.applyTicket(t, tk, nDone, f.sDone, nTodo)
	first := f.ticketResolvedAt(t, tk)
	require.True(t, first.Valid)

	// A measurable gap, then a done -> done move.
	time.Sleep(10 * time.Millisecond)
	f.applyTicket(t, tk, nDone2, f.sDone2, nDone)

	second := f.ticketResolvedAt(t, tk)
	require.True(t, second.Valid)
	require.WithinDuration(t, first.Time, second.Time, 0,
		"done -> done must preserve the first-reached moment, not restamp it")
}
