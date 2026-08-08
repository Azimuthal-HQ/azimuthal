package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// testSigningKey is the one RS256 key every server built in this package's
// test binary signs with (known-issues #19).
//
// It used to be minted per server. newTestServerOn alone backs 203 of the 208
// NewTestDB calls in this package, and rsa.GenerateKey(rand.Reader, 2048)
// costs ~40ms on a developer machine and several times that under -race, so
// the package spent tens of seconds of every CI run generating keys no test
// asserts anything about. sync.OnceValue reduces that to one keygen per test
// binary — and a Go test binary is per package, so the sharing never crosses
// a package boundary and never leaks into production code.
//
// # What this is allowed to be used for
//
// Signing keys for a test org. Nothing more. No test in this package depends
// on two servers holding *different* keys, and this helper cannot provide
// that property — it hands out the same key every time, by design.
//
// One test in the repository does depend on key distinctness:
// TestJWTService_WrongKey in internal/core/auth. It mints its second key
// explicitly and says why. A test here that needs the same thing must do the
// same rather than call this twice, which would silently give it one key and
// turn a rejection assertion into a vacuous one.
var testSigningKey = sync.OnceValue(func() *rsa.PrivateKey {
	// sync.OnceValue takes no error return, and a test binary that cannot
	// generate a key has nothing useful left to do.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generating the shared test signing key: " + err.Error())
	}
	return key
})

// TestHarness_ServersShareOneSigningKey locks the sharing above in place.
//
// Without it the optimisation is invisible: reverting newTestServerOn to a
// per-server rsa.GenerateKey would leave every existing test passing and cost
// the CI job tens of seconds again, silently. Here it fails immediately — two
// independently constructed servers must issue tokens the other accepts,
// which is true only while they hold the same key.
//
// It is deliberately an assertion about the *harness*, not about production:
// production signing keys live in the database (ADR-0004, migration 018) and
// are covered by the signing-key restart-safety suite in internal/core/auth.
func TestHarness_ServersShareOneSigningKey(t *testing.T) {
	a := newTestServer(t)
	b := newTestServer(t)

	// This asserts only that b accepts a's signature — the token never reaches
	// the middleware, so any sid serves; no session row is needed.
	pair, err := a.JWT.IssueTokenPair(a.UserID, "shared-key@azimuthal.dev", a.OrgID.String(), "member", 0, uuid.New())
	require.NoError(t, err)

	claims, err := b.JWT.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err,
		"the second server rejected the first server's token: the harness has gone back to minting a key per server (known-issues #19)")
	require.Equal(t, a.UserID, claims.UserID)
}
