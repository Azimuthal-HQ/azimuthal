package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

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

	orgID, orgSlug, err := ensureOrgForUser(ctx, queries, displayName)
	require.NoError(t, err, "ensureOrgForUser must succeed")
	require.NotEqual(t, uuid.Nil, orgID, "org must have an ID")
	require.Equal(t, "admin-cli-user", orgSlug, "slug must come from display name")

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, orgID))
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
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	first, slug1, err := ensureOrgForUser(ctx, queries, "Shared Org")
	require.NoError(t, err)

	second, slug2, err := ensureOrgForUser(ctx, queries, "Shared Org")
	require.NoError(t, err)

	require.Equal(t, first, second, "second call must return the same org ID")
	require.Equal(t, slug1, slug2)
}
