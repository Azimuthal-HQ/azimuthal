package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// ADR-0006 point 4: NOBODY IS EVER TEAMLESS, and nobody ever holds two primary
// teams.
//
// Both are maintained by the adapter rather than by a constraint, because the
// window in which they are violated is inside a transaction that also does
// something else — deleting a team, or removing a membership. That makes them
// exactly the kind of invariant a test has to assert directly: no CHECK will
// catch a regression, and the symptom of losing either one is a person who
// silently drops out of every team-scoped read.

// tmiPrimaryCount counts a user's primary memberships in one org, straight
// from the table — the invariant is about rows, so it is asserted on rows.
func tmiPrimaryCount(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID) (total, primary int) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE tm.is_primary)
		   FROM team_members tm
		   JOIN teams t ON t.id = tm.team_id AND t.deleted_at IS NULL
		  WHERE tm.org_id = $1 AND tm.user_id = $2`, orgID, userID).Scan(&total, &primary)
	require.NoError(t, err)
	return total, primary
}

func tmiTeam(orgID uuid.UUID, name, slug string) teams.Team {
	// Two details the adapter does not fill in for you: the id is
	// CALLER-supplied (teams.path is `parent.path || id`, so it has to be known
	// before the insert), and `source` is CHECK-constrained to
	// manual/scim/oidc by migration 022 rather than defaulted.
	return teams.Team{ID: uuid.New(), OrgID: orgID, Name: name, Slug: slug, Source: "manual"}
}

// Deleting a team re-homes its members onto the org default rather than
// orphaning them, and anybody whose PRIMARY it was gets a new primary in the
// same transaction.
//
// Fails-before: drop migrateMembersToDefault from Delete and the member is
// left in no live team at all; drop the primary half and they hold a
// membership with no primary, which every "my teams" read then answers empty.
func TestTeamMembershipInvariants_DeletingATeamRehomesItsMembers(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	store := adapters.NewTeamAdapter(db.Pool)

	created, err := store.Create(ctx, tmiTeam(org.ID, "Platform", "platform"))
	require.NoError(t, err)

	_, err = store.AddMember(ctx, created.ID, member.ID, org.ID, "lead")
	require.NoError(t, err)
	require.NoError(t, store.SetPrimary(ctx, created.ID, member.ID, org.ID))

	got, err := store.GetMember(ctx, created.ID, member.ID)
	require.NoError(t, err)
	require.True(t, got.IsPrimary, "premise: the new team is their primary")

	require.NoError(t, store.Delete(ctx, org.ID, created.ID))

	total, primary := tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.NotZero(t, total, "a deleted team must not leave its members teamless")
	require.Equal(t, 1, primary, "exactly one primary, always — never zero and never two")
}

// Moving a primary is a swap, not an addition: the old one is stood down in
// the same transaction.
//
// Fails-before: drop the demotion from SetPrimary and the member holds two,
// which makes "their primary team" a question with two answers.
func TestTeamMembershipInvariants_SettingAPrimaryStandsTheOldOneDown(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	store := adapters.NewTeamAdapter(db.Pool)

	first, err := store.Create(ctx, tmiTeam(org.ID, "First", "first"))
	require.NoError(t, err)
	second, err := store.Create(ctx, tmiTeam(org.ID, "Second", "second"))
	require.NoError(t, err)

	_, err = store.AddMember(ctx, first.ID, member.ID, org.ID, "member")
	require.NoError(t, err)
	_, err = store.AddMember(ctx, second.ID, member.ID, org.ID, "member")
	require.NoError(t, err)

	require.NoError(t, store.SetPrimary(ctx, first.ID, member.ID, org.ID))
	_, primary := tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.Equal(t, 1, primary)

	require.NoError(t, store.SetPrimary(ctx, second.ID, member.ID, org.ID))
	_, primary = tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.Equal(t, 1, primary, "one primary, not two")

	got, err := store.GetMember(ctx, second.ID, member.ID)
	require.NoError(t, err)
	require.True(t, got.IsPrimary)

	old, err := store.GetMember(ctx, first.ID, member.ID)
	require.NoError(t, err)
	require.False(t, old.IsPrimary, "the previous primary is stood down, not left alongside")
}

// Removing somebody's last remaining membership must not leave them teamless
// either — the default team catches them.
//
// Fails-before: drop restoreMembershipInvariants from RemoveMember and the
// person ends the call in no team, invisible to every team-scoped read.
func TestTeamMembershipInvariants_RemovingTheLastMembershipFallsBackToTheDefault(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	store := adapters.NewTeamAdapter(db.Pool)
	def := testutil.DefaultTeamID(t, db.Pool, org.ID)

	// A fixture user is enrolled in the org default as their primary
	// (ADR-0006 point 4), so the interesting removal is that one.
	total, primary := tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.Equal(t, 1, total, "premise: the fixture user starts in exactly the default team")
	require.Equal(t, 1, primary)

	require.NoError(t, store.RemoveMember(ctx, def, member.ID, org.ID))

	total, primary = tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.NotZero(t, total, "removing the last membership must re-home, not orphan")
	require.Equal(t, 1, primary)
}

// EnsureDefaultMembership is idempotent: it runs on paths that may or may not
// have already run, and a second call must not enrol somebody twice.
func TestTeamMembershipInvariants_EnsureDefaultIsIdempotent(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	store := adapters.NewTeamAdapter(db.Pool)

	require.NoError(t, store.EnsureDefaultMembership(ctx, org.ID, member.ID))
	require.NoError(t, store.EnsureDefaultMembership(ctx, org.ID, member.ID))

	total, primary := tmiPrimaryCount(t, db.Pool, org.ID, member.ID)
	require.Equal(t, 1, total, "calling it twice must not enrol somebody twice")
	require.Equal(t, 1, primary)
}

// The default team is seeded once per org, and seeding again is a no-op rather
// than a second default — two defaults would make "the org default" ambiguous
// at exactly the moment it is used as a fallback.
func TestTeamMembershipInvariants_SeedDefaultTeamIsIdempotent(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	store := adapters.NewTeamAdapter(db.Pool)

	require.NoError(t, store.SeedDefaultTeam(ctx, org.ID))
	require.NoError(t, store.SeedDefaultTeam(ctx, org.ID))

	var defaults int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
		org.ID).Scan(&defaults))
	require.Equal(t, 1, defaults, "an org has exactly one default team")

	got, err := store.GetDefault(ctx, org.ID)
	require.NoError(t, err)
	require.True(t, got.IsDefault)
}

// IsOrgMember answers the referential question every subject-side write asks,
// and it must answer NO across an organisation boundary — that is the check
// whose absence known-issue #23 records on the ticket-assign path.
func TestTeamMembershipInvariants_IsOrgMemberIsOrgScoped(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	other := testutil.CreateTestOrg(t, db.Pool)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	store := adapters.NewTeamAdapter(db.Pool)

	in, err := store.IsOrgMember(ctx, org.ID, member.ID)
	require.NoError(t, err)
	require.True(t, in)

	in, err = store.IsOrgMember(ctx, other.ID, member.ID)
	require.NoError(t, err)
	require.False(t, in, "a member of one organisation is not a member of another")

	in, err = store.IsOrgMember(ctx, org.ID, uuid.New())
	require.NoError(t, err)
	require.False(t, in, "a user id that names nobody is not a member")
}
