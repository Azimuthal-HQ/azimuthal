package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// saveCreateUserFlags snapshots the cobra-bound globals so a test that sets them
// cannot leak into a sibling test in this package (they are process-global).
func saveCreateUserFlags(t *testing.T) {
	t.Helper()
	email, name, pw, role, link := createUserEmail, createUserName, createUserPassword, createUserRole, createUserLink
	t.Cleanup(func() {
		createUserEmail, createUserName, createUserPassword, createUserRole, createUserLink = email, name, pw, role, link
	})
}

// TestValidateCreateUserFlags pins the exactly-one-credential-mode rule and the
// role allow-list. Each case would pass with the guard deleted only if the
// opposite outcome were asserted, so the negatives are explicit.
func TestValidateCreateUserFlags(t *testing.T) {
	saveCreateUserFlags(t)

	cases := []struct {
		name       string
		link       bool
		password   string
		role       string
		wantErr    bool
		wantErrSub string
	}{
		{name: "link only", link: true, password: "", role: "member", wantErr: false},
		{name: "password only", link: false, password: "hunter2xy", role: "owner", wantErr: false},
		{name: "both set is refused", link: true, password: "hunter2xy", role: "member", wantErr: true, wantErrSub: "mutually exclusive"},
		{name: "neither set is refused", link: false, password: "", role: "member", wantErr: true, wantErrSub: "required"},
		{name: "unknown role is refused", link: true, password: "", role: "superadmin", wantErr: true, wantErrSub: "role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			createUserLink, createUserPassword, createUserRole = tc.link, tc.password, tc.role
			err := validateCreateUserFlags()
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErrSub)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestAdminCreateUserWithLink_ProvisionsPasswordlessUserAndSignInLink drives the
// CLI's --link path end to end. It must land a user with NO password, an org
// membership, default-team enrolment, and exactly one outstanding sign-in
// credential link — and the account must be unusable until that link is redeemed.
//
// FAILS-BEFORE: have runCreateUserWithLink set a password, skip the link, or use
// a purpose other than "signin" and one of the assertions below fails.
func TestAdminCreateUserWithLink_ProvisionsPasswordlessUserAndSignInLink(t *testing.T) {
	saveCreateUserFlags(t)
	tdb := testutil.NewTestDB(t)
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	const (
		email       = "link-cli@azimuthal.dev"
		displayName = "Link CLI User"
	)
	createUserEmail, createUserName, createUserRole, createUserLink = email, displayName, "member", true

	orgID, orgSlug, err := ensureOrgForUser(ctx, tdb.Pool, displayName)
	require.NoError(t, err)

	cfg := &config.Config{CredentialLinkTTL: time.Hour, AppBaseURL: "http://localhost:8080"}
	require.NoError(t, runCreateUserWithLink(ctx, cfg, tdb.Pool, orgID, orgSlug))

	// The user exists in the org, with NO password set.
	user, err := queries.GetUserByEmailAndOrg(ctx, generated.GetUserByEmailAndOrgParams{OrgID: orgID, Email: email})
	require.NoError(t, err, "the user row must be readable")
	require.Nil(t, user.PasswordHash, "an account created behind a sign-in link has no password")
	require.True(t, user.IsActive)

	// Membership at the requested role.
	membership, err := queries.GetMembership(ctx, generated.GetMembershipParams{OrgID: orgID, UserID: user.ID})
	require.NoError(t, err, "membership must exist")
	require.Equal(t, "member", membership.Role)

	// Default-team enrolment (ADR-0006: never teamless).
	team, err := queries.GetDefaultTeam(ctx, orgID)
	require.NoError(t, err)
	_, err = queries.GetTeamMember(ctx, generated.GetTeamMemberParams{TeamID: team.ID, UserID: user.ID})
	require.NoError(t, err, "the user must be enrolled in the org's default team")

	// Exactly one outstanding sign-in link, unconsumed, carrying no email payload.
	var (
		count      int
		purpose    string
		consumedAt *time.Time
		newEmail   *string
	)
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT count(*) FROM credential_links WHERE user_id = $1`, user.ID).Scan(&count))
	require.Equal(t, 1, count, "exactly one credential link is minted")
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT purpose, consumed_at, new_email FROM credential_links WHERE user_id = $1`, user.ID).
		Scan(&purpose, &consumedAt, &newEmail))
	require.Equal(t, "signin", purpose)
	require.Nil(t, consumedAt, "the freshly minted link is not consumed")
	require.Nil(t, newEmail, "a sign-in link carries no pending address")

	// The account cannot be authenticated until the link is redeemed (nil hash
	// makes bcrypt verification fail for any password).
	userSvc := auth.NewUserService(adapters.NewUserAdapter(tdb.Pool, orgID))
	_, authErr := userSvc.Authenticate(ctx, email, "anything-at-all")
	require.Error(t, authErr, "a passwordless account must not authenticate")
}
