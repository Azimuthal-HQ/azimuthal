package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestSigningKey_PersistsAcrossRestart is the critical M2 test from the
// rebuild brief: the RS256 signing key must live in the database so that a
// process/container restart neither regenerates the key nor invalidates
// tokens issued before the restart.
func TestSigningKey_PersistsAcrossRestart(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	// First boot: no key in the DB — one is generated and stored.
	store1 := adapters.NewSigningKeyAdapter(generated.New(db.Pool))
	key1, err := auth.EnsureSigningKey(ctx, store1, "")
	require.NoError(t, err)
	require.NotNil(t, key1)

	// Simulated restart: a brand-new store instance over the same database
	// must load the SAME key, not generate a fresh one.
	store2 := adapters.NewSigningKeyAdapter(generated.New(db.Pool))
	key2, err := auth.EnsureSigningKey(ctx, store2, "")
	require.NoError(t, err)
	require.Equal(t, key1.N, key2.N, "restart must reuse the persisted key, not regenerate")
	require.Equal(t, key1.D, key2.D, "private exponent must match — same key material")
}

// TestSigningKey_TokenSurvivesRestart issues a token with the first boot's
// key and asserts it still validates after a simulated restart.
func TestSigningKey_TokenSurvivesRestart(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	key1, err := auth.EnsureSigningKey(ctx, adapters.NewSigningKeyAdapter(generated.New(db.Pool)), "")
	require.NoError(t, err)

	jwt1 := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key1,
		PublicKey:  &key1.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 7 * time.Hour,
		Issuer:     "azimuthal",
	})
	userID := uuid.New()
	pair, err := jwt1.IssueTokenPair(userID, "restart@azimuthal.dev", uuid.New().String(), "member")
	require.NoError(t, err)

	// Simulated restart: fresh key load from the same DB, fresh JWT service.
	key2, err := auth.EnsureSigningKey(ctx, adapters.NewSigningKeyAdapter(generated.New(db.Pool)), "")
	require.NoError(t, err)
	jwt2 := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key2,
		PublicKey:  &key2.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 7 * time.Hour,
		Issuer:     "azimuthal",
	})

	claims, err := jwt2.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err, "token issued before restart must validate after restart")
	require.Equal(t, userID, claims.UserID)
}

// TestSigningKey_ImportsExistingPEMFile: a deployment upgrading from the
// file-based key must keep its existing key — when the DB has no key and a
// PEM file exists at the legacy path, the file's key is imported into the DB.
func TestSigningKey_ImportsExistingPEMFile(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	pemPath := filepath.Join(dir, "jwt-private.pem")
	fileKey, err := auth.LoadOrGenerateRSAKey(pemPath) // writes the PEM file
	require.NoError(t, err)
	_, err = os.Stat(pemPath)
	require.NoError(t, err, "fixture PEM file must exist")

	dbKey, err := auth.EnsureSigningKey(ctx, adapters.NewSigningKeyAdapter(generated.New(db.Pool)), pemPath)
	require.NoError(t, err)
	require.Equal(t, fileKey.N, dbKey.N, "existing file key must be imported, not replaced")

	// And it persists: a later boot without the file still gets the same key.
	again, err := auth.EnsureSigningKey(ctx, adapters.NewSigningKeyAdapter(generated.New(db.Pool)), "")
	require.NoError(t, err)
	require.Equal(t, fileKey.N, again.N)
}
