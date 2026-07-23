package access_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Permission matrix (spec §2.5) at the resolution layer, against real
// PostgreSQL. The fixture mirrors ADR-0007's worked example:
//
//	eng (root)
//	├── platform
//	│   └── platform-core
//	└── design
//
// A user in eng acts with the authority of all four teams; a user in
// platform acts with platform (and platform-core) only — expansion is on
// the subject side, never the grant side.
type matrixFixture struct {
	db       *testutil.TestDB
	org      testutil.Org
	resolver *access.Resolver
	grants   *access.GrantService
	teamSvc  *teams.Service

	eng, platform, platformCore, design teams.Team

	// admin is an org admin; vp sits in eng; dev sits in platform;
	// designer sits in design; outsider belongs to another org.
	admin, vp, dev, designer, outsider testutil.User

	space testutil.Space // hidden by default fixture visibility: discoverable
}

func newMatrixFixture(t *testing.T) *matrixFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)

	teamAdapter := adapters.NewTeamAdapter(db.Pool)
	teamSvc := teams.NewService(teamAdapter)
	accessAdapter := adapters.NewAccessAdapter(db.Pool)

	f := &matrixFixture{
		db:       db,
		org:      org,
		resolver: access.NewResolver(accessAdapter),
		grants:   access.NewGrantService(accessAdapter),
		teamSvc:  teamSvc,
	}

	ctx := context.Background()
	mk := func(parent *uuid.UUID, slug string) teams.Team {
		team, err := teamSvc.Create(ctx, org.ID, parent, slug, slug, "")
		require.NoError(t, err)
		return team
	}
	f.eng = mk(nil, "eng")
	f.platform = mk(&f.eng.ID, "platform")
	f.platformCore = mk(&f.platform.ID, "platform-core")
	f.design = mk(&f.eng.ID, "design")

	f.admin = testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "owner")
	f.vp = testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	f.dev = testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	f.designer = testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	f.outsider = testutil.CreateTestUser(t, db.Pool, otherOrg.ID)

	join := func(team teams.Team, user testutil.User) {
		_, err := teamSvc.AddMember(ctx, team.ID, user.ID, org.ID, "member")
		require.NoError(t, err)
	}
	join(f.eng, f.vp)
	join(f.platform, f.dev)
	join(f.design, f.designer)

	f.space = testutil.CreateTestSpace(t, db.Pool, org.ID, f.admin.ID, "vector")
	return f
}

func (f *matrixFixture) resolve(t *testing.T, user testutil.User) *access.Resolution {
	t.Helper()
	res, err := f.resolver.Resolve(context.Background(), f.org.ID, user.ID)
	require.NoError(t, err)
	return res
}

func (f *matrixFixture) grantTeam(t *testing.T, team teams.Team, role access.Role) access.Grant {
	t.Helper()
	g, err := f.grants.Create(context.Background(), f.org.ID, f.space.ID,
		access.SubjectTeam, team.ID, role, f.admin.ID)
	require.NoError(t, err)
	return g
}

// Case 1 — direct user grant: access at the granted role.
func TestMatrix01_DirectUserGrant(t *testing.T) {
	f := newMatrixFixture(t)
	_, err := f.grants.Create(context.Background(), f.org.ID, f.space.ID,
		access.SubjectUser, f.dev.ID, access.RoleContributor, f.admin.ID)
	require.NoError(t, err)

	res := f.resolve(t, f.dev)
	require.True(t, res.CanReadSpace(f.space.ID))
	require.Equal(t, access.RoleContributor, res.RoleOn(f.space.ID))
	require.True(t, res.Can(access.CapCreateItems, f.space.ID))
	require.False(t, res.Can(access.CapEditAnyItem, f.space.ID), "contributor must not hold agent capabilities")
}

// Case 2 — grant to the user's own team: access at the granted role.
func TestMatrix02_OwnTeamGrant(t *testing.T) {
	f := newMatrixFixture(t)
	f.grantTeam(t, f.platform, access.RoleViewer)

	res := f.resolve(t, f.dev)
	require.True(t, res.CanReadSpace(f.space.ID))
	require.Equal(t, access.RoleViewer, res.RoleOn(f.space.ID))
}

// Case 3 — grant to a team BELOW the user's team: access (subject-side
// expansion). The VP in eng inherits platform's grant.
func TestMatrix03_GrantToTeamBelow_Access(t *testing.T) {
	f := newMatrixFixture(t)
	f.grantTeam(t, f.platform, access.RoleAgent)

	res := f.resolve(t, f.vp)
	require.True(t, res.CanReadSpace(f.space.ID),
		"a user in the parent team must inherit a grant to the child team")
	require.Equal(t, access.RoleAgent, res.RoleOn(f.space.ID))
}

// Case 4 — grant to a team ABOVE the user's team: NO access. Fails closed.
// A grant to eng must not reach the dev sitting in platform.
func TestMatrix04_GrantToTeamAbove_NoAccess(t *testing.T) {
	f := newMatrixFixture(t)
	f.grantTeam(t, f.eng, access.RoleAgent)

	res := f.resolve(t, f.dev)
	require.False(t, res.CanReadSpace(f.space.ID),
		"a grant must never reach downward to sub-teams")
	require.Equal(t, access.RoleNone, res.RoleOn(f.space.ID))
	require.False(t, res.Can(access.CapReadItems, f.space.ID))
	require.Empty(t, res.ReadableSpaceIDs(), "no readable spaces may leak from an upward grant")
}

// Case 5 — grant to a sibling team: NO access. Fails closed.
func TestMatrix05_SiblingTeamGrant_NoAccess(t *testing.T) {
	f := newMatrixFixture(t)
	f.grantTeam(t, f.design, access.RoleViewer)

	res := f.resolve(t, f.dev)
	require.False(t, res.CanReadSpace(f.space.ID), "a sibling team's grant must not leak")
	require.Equal(t, access.RoleNone, res.RoleOn(f.space.ID))
	require.Empty(t, res.ReadableSpaceIDs())
}

// Case 6 — user in two teams with different roles on one space: highest wins.
func TestMatrix06_TwoTeamsHighestRoleWins(t *testing.T) {
	f := newMatrixFixture(t)
	_, err := f.teamSvc.AddMember(context.Background(), f.design.ID, f.dev.ID, f.org.ID, "member")
	require.NoError(t, err)

	f.grantTeam(t, f.platform, access.RoleViewer)
	f.grantTeam(t, f.design, access.RoleAgent)

	res := f.resolve(t, f.dev)
	require.Equal(t, access.RoleAgent, res.RoleOn(f.space.ID), "highest role across matching grants wins")
	require.True(t, res.Can(access.CapEditAnyItem, f.space.ID))
}

// Case 7 — org admin with zero grant rows: full access via the middleware
// bypass. The zero-grant-rows premise is asserted against the database.
func TestMatrix07_OrgAdminZeroGrantRows(t *testing.T) {
	f := newMatrixFixture(t)

	var grantRows int
	require.NoError(t, f.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM space_grants WHERE org_id = $1`, f.org.ID).Scan(&grantRows))
	require.Zero(t, grantRows, "premise: the org admin holds zero grant rows")

	res := f.resolve(t, f.admin)
	require.True(t, res.IsOrgAdmin)
	require.True(t, res.CanReadSpace(f.space.ID))
	require.Equal(t, access.RoleSpaceAdmin, res.RoleOn(f.space.ID))
	for _, c := range []access.Capability{
		access.CapReadItems, access.CapCreateItems, access.CapEditAnyItem,
		access.CapManageSpace, access.CapManageGrants, access.CapManageWorkflow,
		access.CapSetVisibility,
	} {
		require.True(t, res.Can(c, f.space.ID), "org admin must hold %s", c)
	}
}

// set_visibility is org-admin-only: no space role holds it, space_admin
// included. Visibility changes what the whole organisation sees, so the
// capability lives outside minRoleFor and only the bypass grants it.
func TestMatrix_SetVisibilityOrgAdminOnly(t *testing.T) {
	f := newMatrixFixture(t)
	_, err := f.grants.Create(context.Background(), f.org.ID, f.space.ID,
		access.SubjectUser, f.dev.ID, access.RoleSpaceAdmin, f.admin.ID)
	require.NoError(t, err)

	res := f.resolve(t, f.dev)
	require.Equal(t, access.RoleSpaceAdmin, res.RoleOn(f.space.ID))
	require.True(t, res.Can(access.CapManageSpace, f.space.ID),
		"premise: the space_admin grant is live — the denial below is set_visibility, not a broken grant")
	require.False(t, res.Can(access.CapSetVisibility, f.space.ID),
		"space_admin must not hold set_visibility")
	require.False(t, access.RoleSpaceAdmin.Grants(access.CapSetVisibility),
		"no space role holds set_visibility — it must stay out of minRoleFor")

	admin := f.resolve(t, f.admin)
	require.True(t, admin.Can(access.CapSetVisibility, f.space.ID),
		"org admin holds set_visibility via the bypass")

	// The bypass reaches only spaces inside the org: a foreign space grants
	// nothing, set_visibility included.
	otherOrg := testutil.CreateTestOrg(t, f.db.Pool)
	foreignAdmin := testutil.CreateTestUserWithRole(t, f.db.Pool, otherOrg.ID, "owner")
	foreignSpace := testutil.CreateTestSpace(t, f.db.Pool, otherOrg.ID, foreignAdmin.ID, "vector")
	require.False(t, admin.Can(access.CapSetVisibility, foreignSpace.ID),
		"the bypass must not cross org boundaries")
}

// Case 8 — visibility = org: every org member reads with no grant rows.
func TestMatrix08_OrgVisibilityReadableByMembers(t *testing.T) {
	f := newMatrixFixture(t)
	testutil.SetSpaceVisibility(t, f.db.Pool, f.space.ID, "org")

	res := f.resolve(t, f.designer)
	require.True(t, res.CanReadSpace(f.space.ID))
	require.Equal(t, access.RoleViewer, res.RoleOn(f.space.ID), "org visibility grants exactly viewer")
	require.True(t, res.Can(access.CapReadItems, f.space.ID))
	require.False(t, res.Can(access.CapCreateItems, f.space.ID), "org visibility must not grant writes")
}

// Non-members resolve to ErrNotOrgMember — the middleware turns this into
// 404 so org existence never leaks.
func TestMatrix_NonMemberFailsClosed(t *testing.T) {
	f := newMatrixFixture(t)
	_, err := f.resolver.Resolve(context.Background(), f.org.ID, f.outsider.ID)
	require.ErrorIs(t, err, access.ErrNotOrgMember)
}

// Case 22 — deleting a user removes their grants in the same transaction.
func TestMatrix22_UserDeleteRemovesGrants(t *testing.T) {
	f := newMatrixFixture(t)
	ctx := context.Background()

	_, err := f.grants.Create(ctx, f.org.ID, f.space.ID,
		access.SubjectUser, f.dev.ID, access.RoleViewer, f.admin.ID)
	require.NoError(t, err)

	userAdapter := adapters.NewUserAdapter(f.db.Pool, f.org.ID)
	require.NoError(t, userAdapter.Delete(ctx, f.dev.ID))

	var grantRows int
	require.NoError(t, f.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM space_grants WHERE subject_type = 'user' AND subject_id = $1`,
		f.dev.ID).Scan(&grantRows))
	require.Zero(t, grantRows, "the user's grants must be deleted with the user")

	var deletedAt *string
	require.NoError(t, f.db.Pool.QueryRow(ctx,
		`SELECT deleted_at::text FROM users WHERE id = $1`, f.dev.ID).Scan(&deletedAt))
	require.NotNil(t, deletedAt, "the user row must be soft-deleted in the same transaction")
}

// A grant whose team is soft-deleted stops matching at resolution — nothing
// is materialised anywhere, so "left the team / team deleted, kept the
// access" cannot happen.
func TestMatrix_DeletedTeamGrantStopsMatching(t *testing.T) {
	f := newMatrixFixture(t)
	ctx := context.Background()

	squad, err := f.teamSvc.Create(ctx, f.org.ID, nil, "squad", "Squad", "")
	require.NoError(t, err)
	_, err = f.teamSvc.AddMember(ctx, squad.ID, f.dev.ID, f.org.ID, "member")
	require.NoError(t, err)
	f.grantTeam(t, squad, access.RoleAgent)

	res := f.resolve(t, f.dev)
	require.True(t, res.CanReadSpace(f.space.ID))

	// Leaving the team removes the access on the very next resolution.
	require.NoError(t, f.teamSvc.RemoveMember(ctx, squad.ID, f.dev.ID, f.org.ID))
	res = f.resolve(t, f.dev)
	require.False(t, res.CanReadSpace(f.space.ID), "left the team must mean lost the access")
}
