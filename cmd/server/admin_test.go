package main

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestAdminCreateUser_SeedsDefaultWorkflows: every org — regardless of which
// path created it (register endpoint OR admin CLI) — must have the two
// seeded default workflows, otherwise AssignDefaultWorkflowToSpace fails on
// every space created in that org.
func TestAdminCreateUser_SeedsDefaultWorkflows(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	orgID, _, err := ensureOrgForUser(ctx, tdb.Pool, "Workflow Seeded User")
	require.NoError(t, err)

	for _, appliesTo := range []string{"tickets", "project_items"} {
		wf, err := queries.GetDefaultWorkflow(ctx, generated.GetDefaultWorkflowParams{
			OrgID:     orgID,
			AppliesTo: appliesTo,
		})
		require.NoError(t, err, "org created via admin CLI must have a default %s workflow", appliesTo)
		require.True(t, wf.IsDefault)
	}
}

// Audit ref: testing-audit.md §6 — v0.1.3 admin create-user chain was uncovered.
// This test exercises the same query sequence that runCreateUser executes,
// verifying that a user, organization, and owner-role membership all land
// in the database in a single command.
func TestAdminCreateUser_CreatesUserOrgAndOwnerMembership(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	const (
		email       = "admin-cli@azimuthal.dev"
		displayName = "Admin CLI User"
		password    = "AdminCliPass123!"
	)

	orgID, orgSlug, err := ensureOrgForUser(ctx, tdb.Pool, displayName)
	require.NoError(t, err, "ensureOrgForUser must succeed")
	require.NotEqual(t, uuid.Nil, orgID, "org must have an ID")
	require.Equal(t, "admin-cli-user", orgSlug, "slug must come from display name")

	userSvc := auth.NewUserService(adapters.NewUserAdapter(tdb.Pool, orgID))
	user, err := userSvc.CreateUser(ctx, email, displayName, password)
	require.NoError(t, err, "CreateUser must succeed")
	require.Equal(t, email, user.Email)
	require.Equal(t, displayName, user.DisplayName)

	_, err = queries.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     orgID,
		UserID:    user.ID,
		Role:      "owner",
		InvitedBy: pgtype.UUID{},
	})
	require.NoError(t, err, "CreateMembership must succeed")

	// Verify all three rows landed in the database.
	gotUser, err := queries.GetUserByID(ctx, user.ID)
	require.NoError(t, err, "user row must be readable")
	require.Equal(t, email, gotUser.Email)
	require.Equal(t, orgID, gotUser.OrgID, "user must be persisted into the new org")

	gotOrg, err := queries.GetOrganizationByID(ctx, orgID)
	require.NoError(t, err, "org row must be readable")
	require.Equal(t, orgSlug, gotOrg.Slug)

	membership, err := queries.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  orgID,
		UserID: user.ID,
	})
	require.NoError(t, err, "membership row must be readable")
	require.Equal(t, "owner", membership.Role, "membership role must be owner")

	// Authentication via the same UserService must succeed end-to-end.
	authed, err := userSvc.Authenticate(ctx, email, password)
	require.NoError(t, err, "user must be able to authenticate after creation")
	require.Equal(t, user.ID, authed.ID)
}

// TestAdminCreateUser_OrgReusedWhenSlugAlreadyExists verifies that calling
// ensureOrgForUser twice with the same display name reuses the existing org
// instead of duplicating it — matching the production runCreateUser behavior.
func TestAdminCreateUser_OrgReusedWhenSlugAlreadyExists(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	ctx := context.Background()

	first, slug1, err := ensureOrgForUser(ctx, tdb.Pool, "Shared Org")
	require.NoError(t, err)

	second, slug2, err := ensureOrgForUser(ctx, tdb.Pool, "Shared Org")
	require.NoError(t, err)

	require.Equal(t, first, second, "second call must return the same org ID")
	require.Equal(t, slug1, slug2)
}

// N2 — concurrent `admin create-user` for the same display name.
//
// The E2E harness issues exactly this from four parallel Playwright workers
// against a freshly reset database, and it used to lose: ensureOrgForUser read,
// then inserted, with nothing between, so every caller but one died on
// organizations_slug_key. The failure surfaced in whichever spec lost the race,
// which is why it read as an unexplained flake in a different test on each run
// rather than as one bug.
//
// Fails before the fix, deterministically at this concurrency: one goroutine
// wins the insert and the rest return
// `duplicate key value violates unique constraint "organizations_slug_key"`.
func TestAdminCreateUser_ConcurrentCallersShareOneOrg(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	ctx := context.Background()

	const callers = 8
	ids := make([]uuid.UUID, callers)
	errs := make([]error, callers)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(callers)
	for i := range callers {
		go func() {
			defer done.Done()
			start.Wait() // release them together, so they really do collide
			ids[i], _, errs[i] = ensureOrgForUser(ctx, tdb.Pool, "Race Org")
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "caller %d must not fail on a slug another caller just took", i)
		require.Equal(t, ids[0], ids[i], "every caller must end up in the same org")
	}

	var orgs int
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT count(*) FROM organizations WHERE slug = 'race-org'`).Scan(&orgs))
	require.Equal(t, 1, orgs, "exactly one org row for the slug")

	// And the org that everybody got is seeded. This is the half a bare
	// ON CONFLICT DO NOTHING would have missed: the losers re-read the winner's
	// row, so the row must not become visible before its seeds exist.
	queries := generated.New(tdb.Pool)
	for _, appliesTo := range []string{"tickets", "project_items"} {
		_, err := queries.GetDefaultWorkflow(ctx, generated.GetDefaultWorkflowParams{
			OrgID: ids[0], AppliesTo: appliesTo,
		})
		require.NoErrorf(t, err, "the shared org must have its default %s workflow", appliesTo)
	}
	types, err := queries.ListItemTypesByOrg(ctx, ids[0])
	require.NoError(t, err)
	require.NotEmpty(t, types, "the shared org must have its default item types")
}
