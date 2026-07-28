package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/spaces"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// S9 — creating a space is one transaction.
//
// Before the fix the handler wrote the space row, then the creator's
// space_members row, then the creator's grant, as three separate pool calls. A
// failure in the second or third returned 500 and left the space row behind:
// an orphan in the org directory holding the slug and the key, so the obvious
// remedy — try again — fails on a conflict caused by the previous attempt's
// wreckage. The grant case is worse than untidy: a lead ends up owning a space
// they cannot open, and cannot grant themselves into, because granting
// requires reaching it.
//
// Both tests below inject a real failure after the first write and assert the
// absence of the orphan. Each fails before the fix: without the transaction,
// the space row is committed by the time the later write fails.

type spaceCreateFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	adapter *adapters.SpaceCreateAdapter
	q       *generated.Queries
	orgID   uuid.UUID
	teamID  uuid.UUID
	creator uuid.UUID
}

func newSpaceCreateFixture(t *testing.T) *spaceCreateFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	return &spaceCreateFixture{
		ctx:     context.Background(),
		pool:    db.Pool,
		adapter: adapters.NewSpaceCreateAdapter(db.Pool),
		q:       generated.New(db.Pool),
		orgID:   org.ID,
		teamID:  testutil.DefaultTeamID(t, db.Pool, org.ID),
		creator: user.ID,
	}
}

func (f *spaceCreateFixture) input(slug, key string) spaces.CreateInput {
	return spaces.CreateInput{
		Space: generated.CreateSpaceParams{
			ID: uuid.New(), OrgID: f.orgID, Slug: slug, Name: "Test Space",
			Type: "codex", CreatedBy: f.creator, Key: key,
			OwnerTeamID: f.teamID, Visibility: access.VisibilityDiscoverable,
		},
		MemberRowID:       uuid.New(),
		CreatorID:         f.creator,
		CreatorNeedsGrant: false,
	}
}

// countRows is the assertion that matters: after a failed creation, nothing
// this transaction touched may still be there.
func (f *spaceCreateFixture) countRows(t *testing.T, table, column string, value uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		"SELECT count(*) FROM "+table+" WHERE "+column+" = $1", value).Scan(&n))
	return n
}

// The ordinary path: space, membership and grant all land.
func TestCreateSpaceTx_WritesSpaceMembershipAndGrantTogether(t *testing.T) {
	f := newSpaceCreateFixture(t)

	in := f.input("ok-space", "OK1")
	in.CreatorNeedsGrant = true

	space, err := f.adapter.CreateSpaceTx(f.ctx, in)
	require.NoError(t, err)
	require.Equal(t, "ok-space", space.Slug)

	require.Equal(t, 1, f.countRows(t, "spaces", "id", space.ID))
	require.Equal(t, 1, f.countRows(t, "space_members", "space_id", space.ID))
	require.Equal(t, 1, f.countRows(t, "space_grants", "space_id", space.ID))

	grants, err := f.q.ListGrantsBySpace(f.ctx, space.ID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, string(access.SubjectUser), grants[0].SubjectType)
	require.Equal(t, f.creator, grants[0].SubjectID)
	require.Equal(t, access.RoleSpaceAdmin.String(), grants[0].Role)
}

// An org admin gets no grant row — the bypass is a middleware rule, never
// grant rows (ADR-0007) — and the rest of the transaction still commits.
func TestCreateSpaceTx_OrgAdminCreatorGetsNoGrantRow(t *testing.T) {
	f := newSpaceCreateFixture(t)

	space, err := f.adapter.CreateSpaceTx(f.ctx, f.input("admin-space", "ADM1"))
	require.NoError(t, err)

	require.Equal(t, 1, f.countRows(t, "space_members", "space_id", space.ID))
	require.Equal(t, 0, f.countRows(t, "space_grants", "space_id", space.ID),
		"an org admin must hold zero grant rows")
}

// Failure at the SECOND write: the creator's space_members row.
//
// Injected through the schema rather than a fake — space_members.user_id is
// NOT NULL REFERENCES users (id) (migration 003), so a creator id with no user
// behind it fails on the foreign key, after the space row has been written
// inside the transaction.
func TestCreateSpaceTx_MembershipFailureLeavesNoOrphanedSpace(t *testing.T) {
	f := newSpaceCreateFixture(t)

	in := f.input("orphan-space", "ORPH1")
	in.CreatorID = uuid.New() // no such user

	_, err := f.adapter.CreateSpaceTx(f.ctx, in)
	require.Error(t, err)

	require.Equal(t, 0, f.countRows(t, "spaces", "id", in.Space.ID),
		"a failed creation must leave no space row: the orphan holds the slug and key, "+
			"so retrying the same request fails on a conflict it caused itself")
	require.Equal(t, 0, f.countRows(t, "space_members", "space_id", in.Space.ID))

	// And the slug and key really are free again — the point of the rollback.
	retried := f.input("orphan-space", "ORPH1")
	_, err = f.adapter.CreateSpaceTx(f.ctx, retried)
	require.NoError(t, err, "the retry must not collide with the rolled-back attempt")
}

// Failure at the THIRD write: the creator's space_admin grant.
//
// Injected through the grant service's own rule rather than the schema:
// space_grants.subject_id has no foreign key (which is why access.GrantService
// checks membership itself), so the failure that matters here is a subject who
// is not an org member — ErrSubjectNotOrgMember. That the rule fires at all is
// itself worth asserting: the grant inside the transaction goes through the
// real service, not a hand-written INSERT that would skip it.
func TestCreateSpaceTx_GrantFailureLeavesNoOrphanedSpaceOrMembership(t *testing.T) {
	f := newSpaceCreateFixture(t)

	// A real user — so the space_members foreign key is satisfied — who
	// belongs to a different org, so the grant subject check refuses them.
	otherOrg := testutil.CreateTestOrg(t, f.pool)
	outsider := testutil.CreateTestUser(t, f.pool, otherOrg.ID)

	in := f.input("grantless-space", "GRNT1")
	in.CreatorID = outsider.ID
	in.CreatorNeedsGrant = true

	_, err := f.adapter.CreateSpaceTx(f.ctx, in)
	require.ErrorIs(t, err, access.ErrSubjectNotOrgMember)

	require.Equal(t, 0, f.countRows(t, "spaces", "id", in.Space.ID),
		"a space whose creator could not be granted access must not exist: it would be "+
			"owned by somebody who cannot open it and cannot grant themselves in")
	require.Equal(t, 0, f.countRows(t, "space_members", "space_id", in.Space.ID),
		"the membership written before the grant must roll back with it")
}

// A key collision still reports the constraint name the handler retries on.
// The transaction must not have swallowed it into an opaque internal error.
func TestCreateSpaceTx_KeyCollisionStaysDistinguishable(t *testing.T) {
	f := newSpaceCreateFixture(t)

	_, err := f.adapter.CreateSpaceTx(f.ctx, f.input("first", "DUP1"))
	require.NoError(t, err)

	_, err = f.adapter.CreateSpaceTx(f.ctx, f.input("second", "DUP1"))
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr,
		"the handler tells a key collision from a slug collision by reading the constraint "+
			"name off a *pgconn.PgError; losing it turns a retryable derived-key conflict into a 500")
	require.Equal(t, "23505", pgErr.Code)
	require.Equal(t, "idx_spaces_org_key", pgErr.ConstraintName)
}
