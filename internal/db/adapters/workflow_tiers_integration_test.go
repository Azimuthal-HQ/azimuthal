package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// These tests assert the half of the tier model that lives in PostgreSQL:
// the CHECK constraints that mirror the Go vocabulary, the ON DELETE rules that
// decide whether a deleted referent removes a restriction, and the partial
// unique index that closes the concurrent-request window. Every one of them is
// a claim migration 046 or 047 makes in prose, tested against the database
// rather than against the migration that was supposed to create it.

type tierFixture struct {
	db         *testutil.TestDB
	orgID      uuid.UUID
	spaceID    uuid.UUID
	userID     uuid.UUID
	teamID     uuid.UUID
	workflowID uuid.UUID
	// openToInProgress is a real seeded edge of the default ticket workflow.
	openToInProgress uuid.UUID
	openStateID      uuid.UUID
	inProgressID     uuid.UUID
	tier             *adapters.WorkflowTierAdapter
	q                *generated.Queries
}

func setupTiers(t *testing.T) *tierFixture {
	t.Helper()

	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	q := generated.New(db.Pool)

	wf := adapters.NewWorkflowAdapter(q)
	ctx := context.Background()
	require.NoError(t, wf.SeedDefaultWorkflows(ctx, org.ID))

	def, err := wf.GetDefaultWorkflow(ctx, org.ID, "tickets")
	require.NoError(t, err)

	states, err := wf.ListStates(ctx, def.ID)
	require.NoError(t, err)
	byName := map[string]uuid.UUID{}
	for _, s := range states {
		byName[s.Name] = s.ID
	}

	transitions, err := wf.ListTransitions(ctx, def.ID)
	require.NoError(t, err)
	var edge uuid.UUID
	for _, tr := range transitions {
		if tr.FromStateID == byName["open"] && tr.ToStateID == byName["in_progress"] {
			edge = tr.ID
		}
	}
	require.NotEqual(t, uuid.Nil, edge, "the seeded ticket workflow must carry open -> in_progress")

	return &tierFixture{
		db: db, orgID: org.ID, spaceID: space.ID, userID: user.ID,
		teamID:           testutil.DefaultTeamID(t, db.Pool, org.ID),
		workflowID:       def.ID,
		openToInProgress: edge,
		openStateID:      byName["open"],
		inProgressID:     byName["in_progress"],
		tier:             adapters.NewWorkflowTierAdapter(q),
		q:                q,
	}
}

func ptr[T any](v T) *T { return &v }

// disposableTeam creates a team that nothing else references, so a test can
// hard-delete it. The org's default team cannot be used: spaces.owner_team_id
// points at it, and the FK is RESTRICT.
func disposableTeam(t *testing.T, f *tierFixture, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.db.Pool.Exec(context.Background(), `
		INSERT INTO teams (id, org_id, path, slug, name)
		VALUES ($1, $2, ARRAY[$1]::uuid[], $3, $3)`, id, f.orgID, slug)
	require.NoError(t, err)
	return id
}

// ─── The seeded default carries nothing ───────────────────────────────────────

// TestSeededWorkflow_HasNoTiers is the byte-identical guarantee at the data
// layer: migrations 046 and 047 seed nothing, so every transition of every
// workflow that existed before them is still unguarded, unapproved and
// side-effect-free. If this fails, existing installs changed behaviour on
// upgrade.
func TestSeededWorkflow_HasNoTiers(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	transitions, err := adapters.NewWorkflowAdapter(f.q).ListTransitions(ctx, f.workflowID)
	require.NoError(t, err)
	require.NotEmpty(t, transitions)

	for _, tr := range transitions {
		guards, err := f.tier.GuardsForTransition(ctx, tr.ID)
		require.NoError(t, err)
		require.Empty(t, guards, "seeded transition %q gained a guard", tr.Name)

		approvers, err := f.tier.ApproversForTransition(ctx, tr.ID)
		require.NoError(t, err)
		require.Empty(t, approvers, "seeded transition %q gained an approver", tr.Name)

		pfs, err := f.tier.PostFunctionsForTransition(ctx, tr.ID)
		require.NoError(t, err)
		require.Empty(t, pfs, "seeded transition %q gained a post-function", tr.Name)
	}
}

// ─── The CHECK constraints mirror the Go vocabulary ───────────────────────────

// The database is the second line of defence behind ValidateGuard. These insert
// through the adapter, bypassing the API's validation, to prove the constraints
// themselves refuse — not merely that the layer above them does.
func TestGuardConstraints_RefuseWhatTheVocabularyDoesNot(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		guard workflow.Guard
	}{
		{"unknown kind", workflow.Guard{Kind: "actor_is_lucky", Class: workflow.GuardConditionClass}},
		{"unknown class", workflow.Guard{Kind: workflow.GuardActorIsAssignee, Class: "sometimes"}},
		{"unknown field key", workflow.Guard{
			Kind: workflow.GuardFieldRequired, Class: workflow.GuardValidatorClass,
			FieldKey: ptr(workflow.FieldKey("story_points")),
		}},
		{"priority is deliberately not requirable", workflow.Guard{
			Kind: workflow.GuardFieldRequired, Class: workflow.GuardValidatorClass,
			FieldKey: ptr(workflow.FieldKey("priority")),
		}},
		{"field_required with no field", workflow.Guard{
			Kind: workflow.GuardFieldRequired, Class: workflow.GuardValidatorClass,
		}},
		{"actor_has_capability with no capability", workflow.Guard{
			Kind: workflow.GuardActorHasCapability, Class: workflow.GuardConditionClass,
		}},
		{"parameterless kind carrying a parameter", workflow.Guard{
			Kind: workflow.GuardActorIsAssignee, Class: workflow.GuardConditionClass,
			FieldKey: ptr(workflow.FieldDueAt),
		}},
		{"two parameters at once", workflow.Guard{
			Kind: workflow.GuardActorInTeam, Class: workflow.GuardConditionClass,
			TeamID: ptr(uuid.New()), FieldKey: ptr(workflow.FieldDueAt),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.guard
			g.TransitionID = f.openToInProgress
			_, err := f.tier.CreateGuard(ctx, g)
			require.Error(t, err, "the database must refuse this guard even when the API does not")
		})
	}
}

func TestPostFunctionConstraints_RefuseWhatTheVocabularyDoesNot(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	cases := []struct {
		name string
		pf   workflow.PostFunction
	}{
		// Both are named by ADR-0011 and neither has a target in this schema.
		// The CHECK is what stops one being written before it does.
		{"add_comment is not built here", workflow.PostFunction{Kind: "add_comment"}},
		{"transition_linked_item has no link model", workflow.PostFunction{Kind: "transition_linked_item"}},
		{"description is readable but not writable", workflow.PostFunction{
			Kind: workflow.PostSetField, FieldKey: ptr(workflow.PostFieldKey("description")), FieldValue: ptr("x"),
		}},
		{"assign_to owns the assignee", workflow.PostFunction{
			Kind: workflow.PostSetField, FieldKey: ptr(workflow.PostFieldKey("assignee_id")), FieldValue: ptr("x"),
		}},
		{"set_field with no field", workflow.PostFunction{Kind: workflow.PostSetField}},
		{"assign_to carrying a field", workflow.PostFunction{
			Kind: workflow.PostAssignTo, FieldKey: ptr(workflow.PostFieldDueAt), FieldValue: ptr("x"),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.pf
			p.TransitionID = f.openToInProgress
			_, err := f.tier.CreatePostFunction(ctx, p)
			require.Error(t, err, "the database must refuse this post-function even when the API does not")
		})
	}
}

func TestApproverConstraints_RefuseUnrepresentableSubjectKinds(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	// ADR-0011 names a third approver kind. It has no representation in this
	// product, and the CHECK is what keeps it from being written as though it
	// did.
	_, err := f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.openToInProgress,
		SubjectType:  "role",
		SubjectID:    uuid.New(),
	})
	require.Error(t, err, "approver-by-role is not representable and the database must refuse it")
}

// ─── ON DELETE rules decide whether a restriction survives ────────────────────

// TestDeletingATeam_DegradesTheGuardRatherThanRemovingIt is the security claim
// migration 046's header makes. Under CASCADE the guard row would be deleted
// and the transition would become UNGUARDED — an unrelated administrative
// action silently removing a restriction. SET NULL keeps the row, and the
// evaluator refuses it.
//
// Change the migration to ON DELETE CASCADE and this test dies on the first
// assertion.
func TestDeletingATeam_DegradesTheGuardRatherThanRemovingIt(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	teamID := disposableTeam(t, f, "guarded-team")

	created, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.openToInProgress,
		Class:        workflow.GuardConditionClass,
		Kind:         workflow.GuardActorInTeam,
		TeamID:       &teamID,
	})
	require.NoError(t, err)
	require.NotNil(t, created.TeamID)

	_, err = f.db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID)
	require.NoError(t, err)

	guards, err := f.tier.GuardsForTransition(ctx, f.openToInProgress)
	require.NoError(t, err)
	require.Len(t, guards, 1, "deleting a team must not delete the guard — that would silently remove a restriction")
	require.Nil(t, guards[0].TeamID, "the guard survives in its degraded, unsatisfiable state")

	// And the degraded guard denies rather than permits.
	refusal := workflow.Evaluate(guards, workflow.GuardConditionClass,
		workflow.Actor{UserID: f.userID, TeamIDs: map[uuid.UUID]struct{}{teamID: {}}},
		workflow.EntitySnapshot{})
	require.NotNil(t, refusal, "a guard whose team was deleted must refuse everyone")
}

// A guard has no meaning without the edge it guards, so deleting the edge does
// cascade — that removes the permission the edge carried rather than granting
// one.
func TestDeletingATransition_RemovesItsGuards(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	_, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.openToInProgress,
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardActorIsAssignee,
	})
	require.NoError(t, err)

	require.NoError(t, adapters.NewWorkflowAdapter(f.q).DeleteTransition(ctx, f.openToInProgress))

	guards, err := f.tier.GuardsForTransition(ctx, f.openToInProgress)
	require.NoError(t, err)
	require.Empty(t, guards)
}

// ─── The pending-approval index closes the concurrency window ─────────────────

// One pending approval per item, enforced by the partial unique index rather
// than by a read-then-write. Two people pressing the same guarded transition at
// the same moment is the ordinary case, not the exotic one.
func TestApprovals_OnlyOnePendingPerEntity(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	entityID := uuid.New()
	request := workflow.Approval{
		TransitionID: &f.openToInProgress,
		EntityType:   workflow.ApprovalEntityTicket,
		EntityID:     entityID,
		SpaceID:      f.spaceID,
		FromStateID:  &f.openStateID,
		ToStateID:    &f.inProgressID,
		FromStatus:   "open",
		ToStatus:     "in_progress",
		RequestedBy:  f.userID,
	}

	first, err := f.tier.CreateApproval(ctx, request)
	require.NoError(t, err)
	require.True(t, first.IsPending())

	_, err = f.tier.CreateApproval(ctx, request)
	require.ErrorIs(t, err, workflow.ErrApprovalPending,
		"the second concurrent request must lose on the index, not create a duplicate")

	// Deciding the first frees the item to request again — the index excludes
	// decided rows precisely so an item can accumulate history.
	decided, err := f.tier.DecideApproval(ctx, first.ID, f.userID, workflow.DecisionDeclined, ptr("not this sprint"))
	require.NoError(t, err)
	require.False(t, decided.IsPending())

	_, err = f.tier.CreateApproval(ctx, request)
	require.NoError(t, err, "a decided approval must not block the next request")
}

// A second approver deciding concurrently updates zero rows rather than
// overwriting the first decision.
func TestApprovals_ASecondDecisionIsRefused(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	created, err := f.tier.CreateApproval(ctx, workflow.Approval{
		TransitionID: &f.openToInProgress,
		EntityType:   workflow.ApprovalEntityItem,
		EntityID:     uuid.New(),
		SpaceID:      f.spaceID,
		FromStatus:   "open",
		ToStatus:     "in_progress",
		RequestedBy:  f.userID,
	})
	require.NoError(t, err)

	_, err = f.tier.DecideApproval(ctx, created.ID, f.userID, workflow.DecisionApproved, nil)
	require.NoError(t, err)

	_, err = f.tier.DecideApproval(ctx, created.ID, f.userID, workflow.DecisionDeclined, ptr("too late"))
	require.ErrorIs(t, err, workflow.ErrApprovalAlreadyDecided,
		"a decided approval must not be re-decided, in either direction")

	// The first decision stands.
	got, err := f.tier.GetApproval(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Decision)
	require.Equal(t, workflow.DecisionApproved, *got.Decision)
}

// Deleting a transition must not destroy the record that someone once asked to
// traverse it — migration 047 uses SET NULL for exactly this.
func TestDeletingATransition_KeepsItsApprovalRecord(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	created, err := f.tier.CreateApproval(ctx, workflow.Approval{
		TransitionID: &f.openToInProgress,
		EntityType:   workflow.ApprovalEntityTicket,
		EntityID:     uuid.New(),
		SpaceID:      f.spaceID,
		FromStatus:   "open",
		ToStatus:     "in_progress",
		RequestedBy:  f.userID,
	})
	require.NoError(t, err)

	n, err := f.tier.PendingApprovalCountForTransition(ctx, f.openToInProgress)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the count that lets an administrator be warned before deleting the edge")

	require.NoError(t, adapters.NewWorkflowAdapter(f.q).DeleteTransition(ctx, f.openToInProgress))

	got, err := f.tier.GetApproval(ctx, created.ID)
	require.NoError(t, err, "the approval record must outlive the edge it referenced")
	require.Nil(t, got.TransitionID)
	require.Equal(t, "open", got.FromStatus, "the captured source status is what a decline restores")
}

// ─── Round trips ──────────────────────────────────────────────────────────────

func TestTierAdapter_GuardRoundTrip(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	for _, g := range []workflow.Guard{
		{Class: workflow.GuardConditionClass, Kind: workflow.GuardActorIsAssignee},
		{Class: workflow.GuardValidatorClass, Kind: workflow.GuardActorInTeam, TeamID: &f.teamID},
		{Class: workflow.GuardConditionClass, Kind: workflow.GuardActorHasCapability, Capability: ptr(access.CapManageSpace)},
		{Class: workflow.GuardValidatorClass, Kind: workflow.GuardFieldRequired, FieldKey: ptr(workflow.FieldLabels)},
	} {
		g.TransitionID = f.openToInProgress
		created, err := f.tier.CreateGuard(ctx, g)
		require.NoError(t, err, "kind %q", g.Kind)
		require.Equal(t, g.Kind, created.Kind)
		require.Equal(t, g.Class, created.Class)
	}

	all, err := f.tier.GuardsForWorkflow(ctx, f.workflowID)
	require.NoError(t, err)
	require.Len(t, all, 4)

	require.NoError(t, f.tier.DeleteGuard(ctx, f.openToInProgress, all[0].ID))
	require.ErrorIs(t, f.tier.DeleteGuard(ctx, f.openToInProgress, all[0].ID), workflow.ErrNotFound,
		"a repeated delete must not read as success")

	// The delete is scoped to the transition, not just the id: naming a
	// DIFFERENT transition must not remove this guard. Without the
	// transition_id predicate an admin could delete a guard belonging to any
	// other transition — including one in another organisation — by pairing a
	// transition of their own with a foreign guard id.
	require.ErrorIs(t, f.tier.DeleteGuard(ctx, uuid.New(), all[1].ID), workflow.ErrNotFound,
		"a guard must not be deletable through a transition it does not belong to")

	survivors, err := f.tier.GuardsForWorkflow(ctx, f.workflowID)
	require.NoError(t, err)
	require.Len(t, survivors, 3, "the mis-scoped delete must have removed nothing")
}

func TestTierAdapter_ApproverSubjectNamesAndMissingFlag(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	_, err := f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.openToInProgress, SubjectType: workflow.ApproverUser, SubjectID: f.userID,
	})
	require.NoError(t, err)
	_, err = f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.openToInProgress, SubjectType: workflow.ApproverTeam, SubjectID: f.teamID,
	})
	require.NoError(t, err)

	// The same subject twice is a conflict, not a duplicate row.
	_, err = f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.openToInProgress, SubjectType: workflow.ApproverUser, SubjectID: f.userID,
	})
	require.ErrorIs(t, err, workflow.ErrApproverExists)

	approvers, err := f.tier.ApproversForTransition(ctx, f.openToInProgress)
	require.NoError(t, err)
	require.Len(t, approvers, 2)
	for _, ap := range approvers {
		require.NotEmpty(t, ap.SubjectName, "a live subject must resolve a display name")
		require.False(t, ap.SubjectMissing)
	}

	// A deleted team is reported as missing rather than rendering blank — the
	// same pair access.Grant carries.
	_, err = f.db.Pool.Exec(ctx, `UPDATE teams SET deleted_at = now() WHERE id = $1`, f.teamID)
	require.NoError(t, err)

	approvers, err = f.tier.ApproversForTransition(ctx, f.openToInProgress)
	require.NoError(t, err)
	var sawMissing bool
	for _, ap := range approvers {
		if ap.SubjectType == workflow.ApproverTeam {
			sawMissing = ap.SubjectMissing
		}
	}
	require.True(t, sawMissing, "a soft-deleted approver team must be reported missing, not silently dropped")
}

// An unrecognised guard kind written by a newer build must survive the read so
// the evaluator can refuse it by name. Dropping it here would permit; erroring
// here would 500 every transition through the edge during a rolling deploy.
func TestTierAdapter_CarriesUnknownKindsThrough(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	// Written the way a future migration would, past the current CHECK.
	// Both constraints enumerate the kind, so a future migration widening the
	// vocabulary would relax both. Dropping only one leaves the other refusing.
	for _, c := range []string{"workflow_transition_guards_kind_valid", "workflow_transition_guards_shape_valid"} {
		_, err := f.db.Pool.Exec(ctx, `ALTER TABLE workflow_transition_guards DROP CONSTRAINT `+c)
		require.NoError(t, err)
	}
	var err error
	_, err = f.db.Pool.Exec(ctx, `
		INSERT INTO workflow_transition_guards (transition_id, guard_class, kind)
		VALUES ($1, 'validator', 'from_a_future_release')`, f.openToInProgress)
	require.NoError(t, err)

	guards, err := f.tier.GuardsForTransition(ctx, f.openToInProgress)
	require.NoError(t, err, "an unrecognised kind must not fail the read")
	require.Len(t, guards, 1)
	require.Equal(t, workflow.GuardKind("from_a_future_release"), guards[0].Kind)

	refusal := workflow.Evaluate(guards, workflow.GuardValidatorClass,
		workflow.Actor{UserID: f.userID}, workflow.EntitySnapshot{AssigneeID: &f.userID})
	require.NotNil(t, refusal, "and the evaluator must then refuse it")
}

// ─── Workflow-level reads ─────────────────────────────────────────────────────

// The *ForWorkflow reads exist so one round trip populates the whole admin
// editor. They have no HTTP consumer until PR-B, so they are covered here at the
// store layer, where the contract actually lives.
func TestTierAdapter_WorkflowLevelReadsSpanEveryTransition(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	transitions, err := adapters.NewWorkflowAdapter(f.q).ListTransitions(ctx, f.workflowID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(transitions), 2, "the seeded ticket workflow has several edges")

	// One row of each kind, on two DIFFERENT edges, so a read that returned only
	// the first transition's rows would fail.
	first, second := transitions[0].ID, transitions[1].ID

	_, err = f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: first, Class: workflow.GuardValidatorClass, Kind: workflow.GuardActorIsAssignee,
	})
	require.NoError(t, err)
	_, err = f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: second, Class: workflow.GuardConditionClass,
		Kind: workflow.GuardFieldRequired, FieldKey: ptr(workflow.FieldDueAt),
	})
	require.NoError(t, err)

	_, err = f.tier.CreatePostFunction(ctx, workflow.PostFunction{
		TransitionID: first, Kind: workflow.PostAssignTo, AssigneeUserID: &f.userID,
	})
	require.NoError(t, err)
	_, err = f.tier.CreatePostFunction(ctx, workflow.PostFunction{
		TransitionID: second, Kind: workflow.PostSetField,
		FieldKey: ptr(workflow.PostFieldLabels), FieldValue: ptr("escalated"),
	})
	require.NoError(t, err)

	_, err = f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: first, SubjectType: workflow.ApproverUser, SubjectID: f.userID,
	})
	require.NoError(t, err)
	_, err = f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: second, SubjectType: workflow.ApproverTeam, SubjectID: f.teamID,
	})
	require.NoError(t, err)

	guards, err := f.tier.GuardsForWorkflow(ctx, f.workflowID)
	require.NoError(t, err)
	require.Len(t, guards, 2)

	pfs, err := f.tier.PostFunctionsForWorkflow(ctx, f.workflowID)
	require.NoError(t, err)
	require.Len(t, pfs, 2)

	approvers, err := f.tier.ApproversForWorkflow(ctx, f.workflowID)
	require.NoError(t, err)
	require.Len(t, approvers, 2)
	for _, ap := range approvers {
		require.NotEmpty(t, ap.SubjectName, "the workflow-level read resolves display names too")
		require.False(t, ap.SubjectMissing)
	}

	// A different workflow's rows must not leak in.
	other, err := adapters.NewWorkflowAdapter(f.q).GetDefaultWorkflow(ctx, f.orgID, "project_items")
	require.NoError(t, err)
	guards, err = f.tier.GuardsForWorkflow(ctx, other.ID)
	require.NoError(t, err)
	require.Empty(t, guards, "the read must be scoped to the workflow it names")
}

// ApprovalsForEntity is the item's own history: every request ever made about
// it, decided and pending alike, newest first. A declined request is kept, so
// the record of who asked and who refused survives the item moving on.
func TestTierAdapter_ApprovalsForEntityKeepsHistory(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()
	entityID := uuid.New()

	request := workflow.Approval{
		TransitionID: &f.openToInProgress,
		EntityType:   workflow.ApprovalEntityTicket,
		EntityID:     entityID,
		SpaceID:      f.spaceID,
		FromStatus:   "open",
		ToStatus:     "in_progress",
		RequestedBy:  f.userID,
	}

	first, err := f.tier.CreateApproval(ctx, request)
	require.NoError(t, err)
	_, err = f.tier.DecideApproval(ctx, first.ID, f.userID, workflow.DecisionDeclined, ptr("blocked on release"))
	require.NoError(t, err)

	second, err := f.tier.CreateApproval(ctx, request)
	require.NoError(t, err)

	history, err := f.tier.ApprovalsForEntity(ctx, f.spaceID, workflow.ApprovalEntityTicket, entityID)
	require.NoError(t, err)
	require.Len(t, history, 2, "a declined request is kept, not replaced")

	// Newest first.
	require.Equal(t, second.ID, history[0].ID)
	require.True(t, history[0].IsPending())
	require.Equal(t, first.ID, history[1].ID)
	require.False(t, history[1].IsPending())
	require.Equal(t, workflow.DecisionDeclined, *history[1].Decision)
	require.NotEmpty(t, history[1].DecidedByName, "the decider is resolved for display")

	// Another entity's history is empty.
	other, err := f.tier.ApprovalsForEntity(ctx, f.spaceID, workflow.ApprovalEntityTicket, uuid.New())
	require.NoError(t, err)
	require.Empty(t, other)

	// And so is the SAME entity's history read through a different space: the
	// query is reconciled against space_id, so an approval history cannot be
	// reached by naming its entity from a space the caller happens to hold.
	wrongSpace, err := f.tier.ApprovalsForEntity(ctx, uuid.New(), workflow.ApprovalEntityTicket, entityID)
	require.NoError(t, err)
	require.Empty(t, wrongSpace, "an approval history must not be readable through another space")
}

// Deciding an approval that does not exist reports not-found rather than
// "already decided" — the follow-up read is what tells the two apart.
func TestTierAdapter_DecidingAMissingApprovalIsNotFound(t *testing.T) {
	f := setupTiers(t)
	_, err := f.tier.DecideApproval(context.Background(), uuid.New(), f.userID, workflow.DecisionApproved, nil)
	require.ErrorIs(t, err, workflow.ErrNotFound)
}

// A space with no workflow reports ErrNotFound, which the gate reads as
// "nothing applies" rather than as a failure.
func TestTierAdapter_WorkflowIDForSpaceReportsNoneDistinctly(t *testing.T) {
	f := setupTiers(t)
	ctx := context.Background()

	// setupTiers deliberately does NOT bind a workflow to its space — assignment
	// is a separate, best-effort step in production — so the unassigned case is
	// the fixture's starting state, and it must be distinguishable from a lookup
	// failure rather than surfacing as a zero id.
	_, err := f.tier.WorkflowIDForSpace(ctx, f.spaceID)
	require.ErrorIs(t, err, workflow.ErrNotFound,
		"an unassigned space must report ErrNotFound, which the gate reads as \"nothing applies\"")

	require.NoError(t, adapters.NewWorkflowAdapter(f.q).
		AssignDefaultWorkflowToSpace(ctx, f.orgID, "beacon", f.spaceID))

	got, err := f.tier.WorkflowIDForSpace(ctx, f.spaceID)
	require.NoError(t, err)
	require.Equal(t, f.workflowID, got)
}
