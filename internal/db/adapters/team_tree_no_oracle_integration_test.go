package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// A REPARENT REFUSAL MUST NOT SAY WHICH KIND OF WRONG THE PARENT WAS.
//
// `lockReparentTarget` in internal/db/adapters/teams.go reaches
// teams.ErrParentNotFound down two separate statements — pgx.ErrNoRows when the
// id names nothing, and the org comparison when it names another tenant's team.
// Collapsing them is the point. A caller who could tell the two apart could
// probe team ids across the org boundary and learn which ones exist.
//
// The rest of the tree is already asserted, and asserted harder, so this file
// deliberately does not repeat it. All four of these live in
// internal/db/adapters:
//
//   - TestTeamReparent_CycleRejected — the cycle case, the self-parent case, and
//     the tree left untouched afterwards.
//   - TestTeamReparent_DepthAccountsForSubtreeHeight — the subtree-height bound,
//     the allowed counter-case one level up, and the descendant's rewritten path.
//   - TestTeamDelete_RestrictedByChildrenAndSpaces — ErrHasChildren,
//     ErrOwnsSpaces and ErrDefaultTeam.
//   - TestAdapterNeg_TeamReparent_UnknownForeignAndDefaultAreRefused — the
//     unknown team, the foreign team, the immovable default team and the foreign
//     parent, constructed through adapters.NewTeamAdapter directly, so it is
//     already at this layer rather than the service's.
//
// And one layer up, TestTeamsAPI_ReparentErrorsMapTo400 in
// internal/core/api/teams_api_negative_integration_test.go covers the cycle and
// depth refusals through the route. It is worth naming because it disproves the
// premise this file was first written on: deleting the adapter's cycle guard
// fails that API test too, so the handler tests do NOT "pass just as well if the
// adapter let the write through."
//
// What none of them asserts is the EQUIVALENCE. Each checks that a refusal
// happens, not that the two refusals are indistinguishable from one another, so
// a refactor giving the absent parent its own sentinel would pass all five.
//
// Only the parent id is worth asserting this way. The team-id arms — unknown
// team and foreign team, on reparent and on delete alike — both fall through the
// single `len(subtree) == 0 || subtree[0].ID != teamID` line, so there is no
// mutation that makes them diverge and an equivalence assertion there could
// never fail.
//
// Fails-before: in lockReparentTarget, return teams.ErrNotFound instead of
// teams.ErrParentNotFound on pgx.ErrNoRows. The two refusals then differ, this
// test fails, and nothing else in the suite notices.
func TestTeamTreeNoOracle_AbsentAndForeignParentsRefuseIdentically(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	store := adapters.NewTeamAdapter(db.Pool)

	moved := edgeMakeTeam(t, db.Pool, org.ID, "movable")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreignParent := edgeMakeTeam(t, db.Pool, otherOrg.ID, "foreign-parent")
	absentParent := uuid.New()

	_, absentErr := store.Reparent(ctx, org.ID, moved, &absentParent)
	_, foreignErr := store.Reparent(ctx, org.ID, moved, &foreignParent)

	// Each refusal is the documented one.
	require.ErrorIs(t, absentErr, teams.ErrParentNotFound,
		"a parent id naming nothing is refused as a missing parent")
	require.ErrorIs(t, foreignErr, teams.ErrParentNotFound,
		"a parent in another org is refused as a missing parent")

	// And — the part nothing else in the suite asserts — they are the SAME
	// refusal. If these ever diverge, the caller can separate "no such team"
	// from "not your team", which is an existence oracle over the id space.
	require.Equal(t, absentErr.Error(), foreignErr.Error(),
		"an absent parent and another org's parent must be indistinguishable to the caller")

	// Neither refusal may have partially applied: both checks run inside the
	// transaction that would otherwise have rewritten the subtree.
	after, err := store.Get(ctx, moved)
	require.NoError(t, err)
	require.Nil(t, after.ParentID)
	require.Equal(t, []uuid.UUID{moved}, after.Path)
}
