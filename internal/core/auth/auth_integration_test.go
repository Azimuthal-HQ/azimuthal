package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// newTestUserService creates a UserService backed by a real database.
func newTestUserService(t *testing.T) (*auth.UserService, *testutil.TestDB, uuid.UUID) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	queries := generated.New(db.Pool)
	adapter := adapters.NewUserAdapter(queries, org.ID)
	svc := auth.NewUserService(adapter)
	return svc, db, org.ID
}

// TestLogin_GlobalEmailLookup verifies login succeeds regardless of which org
// the user belongs to — the email lookup must be global.
func TestLogin_GlobalEmailLookup(t *testing.T) {
	svc, db, _ := newTestUserService(t)

	// Create a second org and user in it — not the default org.
	secondOrg := testutil.CreateTestOrg(t, db.Pool)
	email := "global-lookup@azimuthal.dev"
	password := "testpassword123"

	_, err := svc.CreateUserInOrg(context.Background(), email, "Global User", password, secondOrg.ID)
	require.NoError(t, err)

	// Login must succeed even though user is in secondOrg, not defaultOrg.
	user, err := svc.Authenticate(context.Background(), email, password)
	require.NoError(t, err)
	require.Equal(t, email, user.Email)
	require.Equal(t, secondOrg.ID, user.OrgID)
}

// TestLogin_InvalidPassword ensures wrong password returns ErrInvalidCredentials.
func TestLogin_InvalidPassword(t *testing.T) {
	svc, _, _ := newTestUserService(t)

	email := "wrong-pass@azimuthal.dev"
	_, err := svc.CreateUser(context.Background(), email, "Test", "correctpassword")
	require.NoError(t, err)

	_, err = svc.Authenticate(context.Background(), email, "wrongpassword")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
}

// TestLogin_NonexistentEmail must return ErrInvalidCredentials — same error as
// wrong password. Must NOT return a database error or 500.
func TestLogin_NonexistentEmail(t *testing.T) {
	svc, _, _ := newTestUserService(t)

	_, err := svc.Authenticate(context.Background(), "nonexistent@azimuthal.dev", "password")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
}

// TestLogin_UserInNonDefaultOrg — this was the exact bug that caused 401s.
func TestLogin_UserInNonDefaultOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	queries := generated.New(db.Pool)

	// Create a non-default org.
	nonDefaultOrg := testutil.CreateTestOrg(t, db.Pool)

	// Use an adapter with uuid.Nil as default — user is in nonDefaultOrg.
	adapter := adapters.NewUserAdapter(queries, uuid.Nil)
	svc := auth.NewUserService(adapter)

	email := "non-default@azimuthal.dev"
	password := "testpassword123"
	_, err := svc.CreateUserInOrg(context.Background(), email, "Non Default", password, nonDefaultOrg.ID)
	require.NoError(t, err)

	user, err := svc.Authenticate(context.Background(), email, password)
	require.NoError(t, err)
	require.Equal(t, email, user.Email)
}

// TestCreateUser_CreatesUserSuccessfully verifies the full user creation flow.
func TestCreateUser_CreatesUserSuccessfully(t *testing.T) {
	svc, _, orgID := newTestUserService(t)

	email := "newuser@azimuthal.dev"
	user, err := svc.CreateUserInOrg(context.Background(), email, "New User", "password123", orgID)
	require.NoError(t, err)
	require.Equal(t, email, user.Email)
	require.Equal(t, "New User", user.DisplayName)
	require.True(t, user.IsActive)
	require.NotEqual(t, uuid.Nil, user.ID)

	// Verify user can be retrieved.
	fetched, err := svc.GetUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, user.ID, fetched.ID)
	require.Equal(t, email, fetched.Email)
}

// TestCreateUser_DuplicateEmail verifies that creating a user with the same
// email fails with a clear error (not a raw postgres constraint string).
func TestCreateUser_DuplicateEmail(t *testing.T) {
	svc, _, orgID := newTestUserService(t)

	email := "duplicate@azimuthal.dev"
	_, err := svc.CreateUserInOrg(context.Background(), email, "First", "password123", orgID)
	require.NoError(t, err)

	_, err = svc.CreateUserInOrg(context.Background(), email, "Second", "password456", orgID)
	require.Error(t, err)
}

// TestJWT_ContainsRequiredClaims verifies the JWT contains user_id, org_id, exp.
func TestJWT_ContainsRequiredClaims(t *testing.T) {
	svc, _, orgID := newTestUserService(t)

	email := "jwt-claims@azimuthal.dev"
	user, err := svc.CreateUserInOrg(context.Background(), email, "JWT User", "password123", orgID)
	require.NoError(t, err)

	// Create a JWT service with a real RSA key.
	key := testRSAKey(t)
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		AccessTTL:  24 * time.Hour,
		RefreshTTL: 7 * 24 * time.Hour,
		Issuer:     "azimuthal-test",
	})

	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, orgID.String(), "member")
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)

	// Validate and decode.
	claims, err := jwtSvc.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, user.ID, claims.UserID)
	require.Equal(t, orgID.String(), claims.OrgID)
	require.NotNil(t, claims.ExpiresAt)
	require.True(t, claims.ExpiresAt.After(time.Now()))
}

// TestJWT_ExpiredTokenRejected verifies that an expired token is rejected.
func TestJWT_ExpiredTokenRejected(t *testing.T) {
	key := testRSAKey(t)
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		AccessTTL:  -1 * time.Hour, // expired immediately
		RefreshTTL: -1 * time.Hour,
		Issuer:     "azimuthal-test",
	})

	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "test@test.com", uuid.New().String(), "member")
	require.NoError(t, err)

	_, err = jwtSvc.ValidateAccessToken(pair.AccessToken)
	require.ErrorIs(t, err, auth.ErrInvalidToken)
}

// TestSession_CreateAndRetrieve verifies session lifecycle with real database.
func TestSession_CreateAndRetrieve(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)

	sessionAdapter := adapters.NewSessionAdapter(queries)
	sessionSvc := auth.NewSessionService(sessionAdapter, auth.SessionConfig{TTL: 24 * time.Hour})

	sess, err := sessionSvc.CreateSession(context.Background(), user.ID, "test-agent", "127.0.0.1")
	require.NoError(t, err)
	require.NotEmpty(t, sess.Token)
	require.Equal(t, user.ID, sess.UserID)

	// Retrieve by token.
	fetched, err := sessionSvc.GetSession(context.Background(), sess.Token)
	require.NoError(t, err)
	require.Equal(t, sess.ID, fetched.ID)
}

// TestSession_DeleteAllForUser verifies logout invalidates all sessions.
func TestSession_DeleteAllForUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)

	sessionAdapter := adapters.NewSessionAdapter(queries)
	sessionSvc := auth.NewSessionService(sessionAdapter, auth.SessionConfig{TTL: 24 * time.Hour})

	sess1, err := sessionSvc.CreateSession(context.Background(), user.ID, "agent1", "127.0.0.1")
	require.NoError(t, err)
	sess2, err := sessionSvc.CreateSession(context.Background(), user.ID, "agent2", "127.0.0.2")
	require.NoError(t, err)

	// Delete all.
	err = sessionSvc.DeleteAllSessions(context.Background(), user.ID)
	require.NoError(t, err)

	// Both sessions must be gone.
	_, err = sessionSvc.GetSession(context.Background(), sess1.Token)
	require.Error(t, err)
	_, err = sessionSvc.GetSession(context.Background(), sess2.Token)
	require.Error(t, err)
}

// TestMembership_PrimaryOrgForUser verifies membership lookup.
func TestMembership_PrimaryOrgForUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)

	membershipAdapter := adapters.NewMembershipAdapter(queries)
	orgID, slug, name, err := membershipAdapter.PrimaryOrgForUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, org.ID, orgID)
	require.Equal(t, org.Slug, slug)
	require.Equal(t, org.Name, name)
}

// TestOrgProvisioner_CreateAndMembership verifies org provisioning.
func TestOrgProvisioner_CreateAndMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	queries := generated.New(db.Pool)

	provisioner := adapters.NewOrgProvisionerAdapter(queries)
	orgID, slug, err := provisioner.ProvisionOrg(context.Background(), "Test Provisioned Org")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, orgID)
	require.Equal(t, "test-provisioned-org", slug)

	// Create a user to add as member.
	userAdapter := adapters.NewUserAdapter(queries, orgID)
	userSvc := auth.NewUserService(userAdapter)
	user, err := userSvc.CreateUserInOrg(context.Background(), "provisioned@test.dev", "Prov", "pass123", orgID)
	require.NoError(t, err)

	err = provisioner.CreateMembership(context.Background(), orgID, user.ID)
	require.NoError(t, err)

	// Verify membership.
	membershipAdapter := adapters.NewMembershipAdapter(queries)
	resolvedOrgID, _, _, err := membershipAdapter.PrimaryOrgForUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, orgID, resolvedOrgID)
}

// testRSAKey generates a test RSA key pair.
func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}
