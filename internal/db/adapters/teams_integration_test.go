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

// Store-layer obligations of v0.3 spec §4 (migration 022), each with a test.
// Real PostgreSQL via testutil.NewTestDB — the depth and path CHECKs are
// database constraints and must be exercised against the database.

func newTeamFixture(t *testing.T) (*testutil.TestDB, testutil.Org, *adapters.TeamAdapter, *teams.Service) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewTeamAdapter(db.Pool)
	return db, org, adapter, teams.NewService(adapter)
}

func mkTeam(t *testing.T, svc *teams.Service, orgID uuid.UUID, parent *uuid.UUID, slug string) teams.Team {
	t.Helper()
	team, err := svc.Create(context.Background(), orgID, parent, slug, "Team "+slug, "")
	require.NoError(t, err, "creating team %s", slug)
	return team
}

// TestTeamCreate_PathIsParentPathPlusID: path is always parent.path || id,
// never hand-assembled (spec §4 store obligation).
func TestTeamCreate_PathIsParentPathPlusID(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	root := mkTeam(t, svc, org.ID, nil, "eng")
	require.Equal(t, []uuid.UUID{root.ID}, root.Path, "root path must be [own id]")

	child := mkTeam(t, svc, org.ID, &root.ID, "platform")
	require.Equal(t, []uuid.UUID{root.ID, child.ID}, child.Path, "child path must be parent.path || id")

	grandchild := mkTeam(t, svc, org.ID, &child.ID, "platform-core")
	require.Equal(t, []uuid.UUID{root.ID, child.ID, grandchild.ID}, grandchild.Path)
}

// TestTeamCreate_DepthLimit: five levels are allowed, the sixth is rejected
// before insert (and the database CHECK backstops it).
func TestTeamCreate_DepthLimit(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	parent := mkTeam(t, svc, org.ID, nil, "l1")
	for i, slug := range []string{"l2", "l3", "l4", "l5"} {
		parent = mkTeam(t, svc, org.ID, &parent.ID, slug)
		require.Len(t, parent.Path, i+2)
	}

	_, err := svc.Create(context.Background(), org.ID, &parent.ID, "l6", "Level 6", "")
	require.ErrorIs(t, err, teams.ErrDepthExceeded, "sixth level must be rejected")
}

// TestTeamCreate_SlugUniquePerOrg: two teams under different parents still
// collide on slug within one org; the same slug in another org is fine.
func TestTeamCreate_SlugUniquePerOrg(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)

	a := mkTeam(t, svc, org.ID, nil, "design")
	_ = a
	b := mkTeam(t, svc, org.ID, nil, "marketing")

	_, err := svc.Create(context.Background(), org.ID, &b.ID, "design", "Design under Marketing", "")
	require.ErrorIs(t, err, teams.ErrSlugTaken)

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	_, err = svc.Create(context.Background(), otherOrg.ID, nil, "design", "Other org design", "")
	require.NoError(t, err, "same slug in another org must be allowed")
}

// TestTeamCreate_ParentValidation: a parent from another org (or a deleted
// one) is rejected, not silently rooted.
func TestTeamCreate_ParentValidation(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreign := mkTeam(t, svc, otherOrg.ID, nil, "foreign")

	_, err := svc.Create(context.Background(), org.ID, &foreign.ID, "child", "Child", "")
	require.ErrorIs(t, err, teams.ErrParentNotFound, "cross-org parent must be rejected")

	missing := uuid.New()
	_, err = svc.Create(context.Background(), org.ID, &missing, "child2", "Child2", "")
	require.ErrorIs(t, err, teams.ErrParentNotFound)
}

// TestTeamReparent_RewritesWholeSubtree: moving a node rewrites the paths of
// the node and every descendant in one transaction (spec §4).
func TestTeamReparent_RewritesWholeSubtree(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	eng := mkTeam(t, svc, org.ID, nil, "eng")
	platform := mkTeam(t, svc, org.ID, &eng.ID, "platform")
	core := mkTeam(t, svc, org.ID, &platform.ID, "core")
	ops := mkTeam(t, svc, org.ID, nil, "ops")

	// Move platform (with descendant core) under ops.
	moved, err := svc.Reparent(context.Background(), org.ID, platform.ID, &ops.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ops.ID, platform.ID}, moved.Path)
	require.NotNil(t, moved.ParentID)
	require.Equal(t, ops.ID, *moved.ParentID)

	refreshedCore, err := svc.Get(context.Background(), core.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ops.ID, platform.ID, core.ID}, refreshedCore.Path,
		"descendant path must be rewritten in the same move")
}

// TestTeamReparent_ToRoot: a nil parent moves the subtree to the root.
func TestTeamReparent_ToRoot(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	eng := mkTeam(t, svc, org.ID, nil, "eng")
	platform := mkTeam(t, svc, org.ID, &eng.ID, "platform")

	moved, err := svc.Reparent(context.Background(), org.ID, platform.ID, nil)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{platform.ID}, moved.Path)
	require.Nil(t, moved.ParentID)
}

// TestTeamReparent_DepthAccountsForSubtreeHeight — matrix case 19, and the
// single most likely miss called out by the spec: the check must be
// depth(new_parent) + height(moved_subtree) <= 5, not just the moved node's
// own depth.
func TestTeamReparent_DepthAccountsForSubtreeHeight(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	// deep chain: d1 > d2 > d3 > d4 (depth 4)
	d1 := mkTeam(t, svc, org.ID, nil, "d1")
	d2 := mkTeam(t, svc, org.ID, &d1.ID, "d2")
	d3 := mkTeam(t, svc, org.ID, &d2.ID, "d3")
	d4 := mkTeam(t, svc, org.ID, &d3.ID, "d4")
	_ = d4

	// subtree of height 2: top > bottom
	top := mkTeam(t, svc, org.ID, nil, "top")
	bottom := mkTeam(t, svc, org.ID, &top.ID, "bottom")

	// Moving top under d4 would put top at depth 5 (legal on its own) but
	// bottom at depth 6 — must be rejected because of the subtree height.
	_, err := svc.Reparent(context.Background(), org.ID, top.ID, &d4.ID)
	require.ErrorIs(t, err, teams.ErrDepthExceeded,
		"depth check must account for subtree height, not just the moved node")

	// Moving top under d3 puts bottom exactly at depth 5 — allowed.
	moved, err := svc.Reparent(context.Background(), org.ID, top.ID, &d3.ID)
	require.NoError(t, err)
	require.Len(t, moved.Path, 4)

	refreshedBottom, err := svc.Get(context.Background(), bottom.ID)
	require.NoError(t, err)
	require.Len(t, refreshedBottom.Path, 5)
}

// TestTeamReparent_CycleRejected — matrix case 20: a parent assignment that
// creates a cycle (own subtree, or itself) is rejected.
func TestTeamReparent_CycleRejected(t *testing.T) {
	_, org, _, svc := newTeamFixture(t)

	a := mkTeam(t, svc, org.ID, nil, "a")
	b := mkTeam(t, svc, org.ID, &a.ID, "b")
	c := mkTeam(t, svc, org.ID, &b.ID, "c")

	_, err := svc.Reparent(context.Background(), org.ID, a.ID, &c.ID)
	require.ErrorIs(t, err, teams.ErrCycle, "moving a under its own descendant must be rejected")

	_, err = svc.Reparent(context.Background(), org.ID, a.ID, &a.ID)
	require.ErrorIs(t, err, teams.ErrCycle, "moving a under itself must be rejected")

	// The tree is untouched after the rejections.
	refreshedA, err := svc.Get(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{a.ID}, refreshedA.Path)
}

// TestTeamDelete_RestrictedByChildrenAndSpaces: RESTRICT while children or
// owned spaces exist; the default team is never deletable.
func TestTeamDelete_RestrictedByChildrenAndSpaces(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	parent := mkTeam(t, svc, org.ID, nil, "parent")
	child := mkTeam(t, svc, org.ID, &parent.ID, "child")

	err := svc.Delete(context.Background(), org.ID, parent.ID)
	require.ErrorIs(t, err, teams.ErrHasChildren)

	// Give the child an owned space; deletion must be blocked.
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	_, err = db.Pool.Exec(context.Background(),
		`UPDATE spaces SET owner_team_id = $1 WHERE id = $2`, child.ID, space.ID)
	require.NoError(t, err)

	err = svc.Delete(context.Background(), org.ID, child.ID)
	require.ErrorIs(t, err, teams.ErrOwnsSpaces)

	// The org default team is protected outright.
	def, err := svc.GetDefault(context.Background(), org.ID)
	require.NoError(t, err)
	err = svc.Delete(context.Background(), org.ID, def.ID)
	require.ErrorIs(t, err, teams.ErrDefaultTeam)
}

// TestTeamDelete_MovesMembersToDefault: members of a deleted team land in
// the org default team; anyone whose primary it was gets the default as
// primary; the team's grants are removed in the same transaction.
func TestTeamDelete_MovesMembersToDefault(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	ctx := context.Background()

	userA := testutil.CreateTestUser(t, db.Pool, org.ID) // primary: doomed team
	userB := testutil.CreateTestUser(t, db.Pool, org.ID) // primary: default team

	doomed := mkTeam(t, svc, org.ID, nil, "doomed")
	_, err := svc.AddMember(ctx, doomed.ID, userA.ID, org.ID, "member")
	require.NoError(t, err)
	_, err = svc.AddMember(ctx, doomed.ID, userB.ID, org.ID, "member")
	require.NoError(t, err)
	require.NoError(t, svc.SetPrimary(ctx, doomed.ID, userA.ID, org.ID))

	// A grant to the doomed team, to prove grant cleanup.
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO space_grants (org_id, space_id, subject_type, subject_id, role)
		 VALUES ($1, $2, 'team', $3, 'viewer')`, org.ID, space.ID, doomed.ID)
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, org.ID, doomed.ID))

	def, err := svc.GetDefault(ctx, org.ID)
	require.NoError(t, err)

	// Both users are in the default team; userA's primary moved there.
	memberA, err := svc.GetMember(ctx, def.ID, userA.ID)
	require.NoError(t, err)
	require.True(t, memberA.IsPrimary, "primary must move to the default team")
	_, err = svc.GetMember(ctx, def.ID, userB.ID)
	require.NoError(t, err)

	// No leftover membership rows for the deleted team.
	var members int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM team_members WHERE team_id = $1`, doomed.ID).Scan(&members))
	require.Zero(t, members)

	// The team's grants went with it, in the same transaction.
	var grants int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM space_grants WHERE subject_type = 'team' AND subject_id = $1`,
		doomed.ID).Scan(&grants))
	require.Zero(t, grants, "deleting a team must remove its grants")
}

// TestTeamRemoveMember_NeverTeamless — ADR-0006 point 4: a user removed from
// their last team is added back to the org default team as primary.
func TestTeamRemoveMember_NeverTeamless(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	ctx := context.Background()

	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	def, err := svc.GetDefault(ctx, org.ID)
	require.NoError(t, err)

	// Move the user wholly into a squad: join squad, make it primary, leave default.
	squad := mkTeam(t, svc, org.ID, nil, "squad")
	_, err = svc.AddMember(ctx, squad.ID, user.ID, org.ID, "member")
	require.NoError(t, err)
	require.NoError(t, svc.SetPrimary(ctx, squad.ID, user.ID, org.ID))
	require.NoError(t, svc.RemoveMember(ctx, def.ID, user.ID, org.ID))

	// Removing the last membership re-adds the default as primary.
	require.NoError(t, svc.RemoveMember(ctx, squad.ID, user.ID, org.ID))

	member, err := svc.GetMember(ctx, def.ID, user.ID)
	require.NoError(t, err, "user must be re-added to the default team")
	require.True(t, member.IsPrimary)

	var count int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM team_members WHERE user_id = $1 AND org_id = $2`,
		user.ID, org.ID).Scan(&count))
	require.Equal(t, 1, count, "exactly the default membership must remain")
}

// TestTeamRemoveMember_PrimaryFallsBack: removing a primary membership (not
// the last one) moves primary to the default team when the user is in it.
func TestTeamRemoveMember_PrimaryFallsBack(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	ctx := context.Background()

	user := testutil.CreateTestUser(t, db.Pool, org.ID) // enrolled in default (primary)
	def, err := svc.GetDefault(ctx, org.ID)
	require.NoError(t, err)

	squad := mkTeam(t, svc, org.ID, nil, "squad")
	_, err = svc.AddMember(ctx, squad.ID, user.ID, org.ID, "member")
	require.NoError(t, err)
	require.NoError(t, svc.SetPrimary(ctx, squad.ID, user.ID, org.ID))

	require.NoError(t, svc.RemoveMember(ctx, squad.ID, user.ID, org.ID))

	member, err := svc.GetMember(ctx, def.ID, user.ID)
	require.NoError(t, err)
	require.True(t, member.IsPrimary, "primary must fall back to the default team")
}

// TestTeamAddMember_RequiresOrgMembership: enrolling a non-org-member is
// rejected (grants integrity depends on the same rule).
func TestTeamAddMember_RequiresOrgMembership(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	ctx := context.Background()

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	outsider := testutil.CreateTestUser(t, db.Pool, otherOrg.ID)

	squad := mkTeam(t, svc, org.ID, nil, "squad")
	_, err := svc.AddMember(ctx, squad.ID, outsider.ID, org.ID, "member")
	require.ErrorIs(t, err, teams.ErrNotOrgMember)
}

// TestEnsureDefaultMembership_NewUserJoinsAsPrimary: the provisioning hook
// enrols a fresh user in the default team with is_primary = true, and is
// idempotent.
func TestEnsureDefaultMembership_NewUserJoinsAsPrimary(t *testing.T) {
	db, org, adapter, svc := newTeamFixture(t)
	ctx := context.Background()

	// A user with no team enrolment at all (bypass the fixture's enrolment).
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	_, err := db.Pool.Exec(ctx, `DELETE FROM team_members WHERE user_id = $1`, user.ID)
	require.NoError(t, err)

	require.NoError(t, adapter.EnsureDefaultMembership(ctx, org.ID, user.ID))
	require.NoError(t, adapter.EnsureDefaultMembership(ctx, org.ID, user.ID), "must be idempotent")

	def, err := svc.GetDefault(ctx, org.ID)
	require.NoError(t, err)
	member, err := svc.GetMember(ctx, def.ID, user.ID)
	require.NoError(t, err)
	require.True(t, member.IsPrimary, "new users join the default team as primary")
}

// TestTeamMetadataRole_Validated: only member|lead pass validation.
func TestTeamMetadataRole_Validated(t *testing.T) {
	db, org, _, svc := newTeamFixture(t)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	squad := mkTeam(t, svc, org.ID, nil, "squad")

	_, err := svc.AddMember(context.Background(), squad.ID, user.ID, org.ID, "overlord")
	require.ErrorIs(t, err, teams.ErrInvalidMemberRole)

	m, err := svc.AddMember(context.Background(), squad.ID, user.ID, org.ID, "lead")
	require.NoError(t, err)
	require.True(t, m.IsLead())
}
