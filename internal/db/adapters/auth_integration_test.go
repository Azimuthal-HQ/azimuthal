package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func TestUserAdapter_Update(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)
	ctx := context.Background()

	u, err := adapter.GetByID(ctx, user.ID)
	require.NoError(t, err)

	u.DisplayName = "Updated Name"
	u.IsActive = true
	err = adapter.Update(ctx, u)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated Name", fetched.DisplayName)
}

func TestUserAdapter_UpdateProfile(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)
	ctx := context.Background()

	updated, err := adapter.UpdateProfile(ctx, user.ID, "Profile Name", user.Email)
	require.NoError(t, err)
	require.Equal(t, "Profile Name", updated.DisplayName)
}

func TestUserAdapter_Delete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)
	ctx := context.Background()

	err := adapter.Delete(ctx, user.ID)
	require.NoError(t, err)

	// Soft delete: user should not be fetchable.
	_, err = adapter.GetByID(ctx, user.ID)
	require.Error(t, err)
}

func TestUserAdapter_GetByEmailAcrossOrgs(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)
	ctx := context.Background()

	fetched, err := adapter.GetByEmailAcrossOrgs(ctx, user.Email)
	require.NoError(t, err)
	require.Equal(t, user.ID, fetched.ID)
}

func TestSessionAdapter_Delete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)
	sessionAdapter := adapters.NewSessionAdapter(queries)
	ctx := context.Background()

	sess := &auth.Session{
		ID:        uuid.New(),
		UserID:    user.ID,
		Token:     uuid.New().String(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, sessionAdapter.Create(ctx, sess))

	// Verify session exists.
	_, err := sessionAdapter.GetByToken(ctx, sess.Token)
	require.NoError(t, err)

	require.NoError(t, sessionAdapter.Delete(ctx, sess.ID))

	// After delete, GetByToken should fail.
	_, err = sessionAdapter.GetByToken(ctx, sess.Token)
	require.Error(t, err)
}

func TestSessionAdapter_DeleteExpired(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)
	sessionAdapter := adapters.NewSessionAdapter(queries)
	ctx := context.Background()

	// Insert a session directly with past expiry.
	pastExpiry := time.Now().Add(-time.Hour)
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		uuid.New(), user.ID, "expired-hash", pastExpiry,
	)
	require.NoError(t, err)

	// DeleteExpired should succeed.
	err = sessionAdapter.DeleteExpired(ctx)
	require.NoError(t, err)
}
