package testutil_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// spaceKeyPattern mirrors the API's own validator
// (internal/core/api/spaces.validKey). A fixture that produced a key the API
// would reject builds state the product could not.
var spaceKeyPattern = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

// TestCreateTestSpace_KeysAreUniqueWithinAnOrg is the regression test for a
// fixture defect, not for a product one.
//
// CreateTestSpace used to build its key from two hex digits of a UUID: 256
// possible keys for a whole run. spaces has a unique index on (org_id, key)
// where deleted_at is null, so a test creating enough spaces of one type in
// one org eventually hit it — 64 spaces draws a collision better than 99% of
// the time by the birthday bound. It surfaced as an intermittent
// "duplicate key value violates unique constraint idx_spaces_org_key" from
// whichever test drew the short straw, which reads as a product flake.
//
// Both directions: against the two-hex-digit version this fails almost every
// run at n=64; against the counter it cannot fail at all.
func TestCreateTestSpace_KeysAreUniqueWithinAnOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	const n = 64
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

		var key string
		require.NoError(t, db.Pool.QueryRow(context.Background(),
			`SELECT key FROM spaces WHERE id = $1`, space.ID).Scan(&key))

		require.Regexp(t, spaceKeyPattern, key,
			"a fixture key the API's own validator would reject builds state the product could not")
		require.Falsef(t, seen[key], "space key %q was handed out twice in one org", key)
		seen[key] = true
	}
	require.Len(t, seen, n)
}
