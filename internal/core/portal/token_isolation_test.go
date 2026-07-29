package portal_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
)

// The token boundary between the internal product and the customer portal.
//
// Both families are signed with the SAME RSA key — `auth_signing_keys` holds
// one row by construction (migration 018, `CHECK (id = 1)`) — so the audience
// claim is not a label on these tokens, it is the entire boundary. These
// tests exist to prove it holds in both directions and to fail if either
// half of it is removed.
//
// FAILS-BEFORE EVIDENCE. Each isolating test names the exact edit that makes
// it fail, and each was run against that edit before the guard was written:
//
//	TestPortalAudience_IsRefusedByInternalValidation
//	    → delete the `checkAudience` call in auth.parseToken: FAILS.
//	TestInternalAudience_IsRefusedByPortalValidation
//	    → remove `jwt.WithAudience(AudiencePortal)` from
//	      portal.ValidateSession: FAILS.
//	TestPortalToken_WithNoAudience_IsRefused
//	    → same edit: FAILS.
//	TestLegacyInternalToken_WithNoAudience_StillValidates
//	    → make auth.checkAudience strict (reject absent): FAILS.
//
// The last one is the counterweight to the other three. It is what stops the
// audience change from signing out every existing user on deploy, which is
// precisely the defect ADR-0004 was written about.

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func internalService(key *rsa.PrivateKey) *auth.JWTService {
	return auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 2 * time.Hour,
		Issuer:     "azimuthal",
		Audience:   auth.AudienceInternal,
	})
}

func portalService(key *rsa.PrivateKey) *portal.TokenService {
	return portal.NewTokenService(portal.TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		SessionTTL: time.Hour,
		Issuer:     "azimuthal",
	})
}

// signRaw mints a token with arbitrary claims under the shared key. It is how
// these tests construct the adversarial shapes that the two minters will not
// produce on their own — a portal-audience token carrying the internal `typ`,
// and vice versa. Without it the tests would only ever exercise `typ`, and
// the audience check could be deleted with every test still green.
func signRaw(t *testing.T, key *rsa.PrivateKey, claims jwt.Claims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	require.NoError(t, err)
	return s
}

// ── Direction 1: a portal credential must never authenticate internally ──

func TestPortalSessionToken_IsRefusedByInternalValidation(t *testing.T) {
	key := testKey(t)
	token, err := portalService(key).IssueSession(uuid.New(), uuid.New(), uuid.New().String(), 0)
	require.NoError(t, err)

	_, err = internalService(key).ValidateAccessToken(token)
	require.ErrorIs(t, err, auth.ErrInvalidToken,
		"a real portal session token must not validate as an internal access token")

	_, err = internalService(key).ValidateRefreshToken(token)
	require.ErrorIs(t, err, auth.ErrInvalidToken)
}

// TestPortalAudience_IsRefusedByInternalValidation isolates the AUDIENCE
// check from the `typ` check.
//
// The previous test passes even with no audience verification at all, because
// a portal token's `typ` is "portal_session" and ValidateAccessToken refuses
// anything but "access". That makes it a fine end-to-end assertion and a
// useless regression guard. This one hands the internal validator a token
// carrying the internal `typ` and the PORTAL audience, so the only thing that
// can refuse it is checkAudience.
func TestPortalAudience_IsRefusedByInternalValidation(t *testing.T) {
	key := testKey(t)
	now := time.Now().UTC()

	// Everything an internal access token has, except the audience.
	token := signRaw(t, key, &auth.Claims{
		UserID:    uuid.New(),
		Email:     "agent@example.com",
		OrgID:     uuid.New().String(),
		Role:      "member",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azimuthal",
			Audience:  jwt.ClaimStrings{portal.AudiencePortal},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	})

	_, err := internalService(key).ValidateAccessToken(token)
	require.ErrorIs(t, err, auth.ErrInvalidToken,
		"the portal audience must be refused even when every other claim says 'internal access token'")
}

// TestPortalToken_IsRefusedByRequireAuth walks the real middleware rather
// than the validator, because the validator is not what guards the product.
// Three routes sit behind RequireAuth with no org-membership check after it
// (/notifications, /auth/me, PUT /auth/me/avatar), so RequireAuth is the only
// thing standing between a foreign token and them.
func TestPortalToken_IsRefusedByRequireAuth(t *testing.T) {
	key := testKey(t)
	token, err := portalService(key).IssueSession(uuid.New(), uuid.New(), uuid.New().String(), 0)
	require.NoError(t, err)

	// A state store that would ACCEPT anything, so the test cannot pass by
	// accident on the users-table miss. That miss is a real second barrier in
	// production, but it is not the one under test here, and leaving it in
	// place would let the audience check be deleted with this test still
	// green.
	authenticator := auth.NewAuthenticator(internalService(key), nil, permissiveStates{})

	var reached bool
	h := authenticator.RequireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.False(t, reached, "a portal token must never reach a handler behind RequireAuth")
}

type permissiveStates struct{}

func (permissiveStates) AuthState(context.Context, uuid.UUID) (auth.State, error) {
	return auth.State{IsActive: true, TokenGeneration: 0}, nil
}

// ── Direction 2: an internal credential must never authenticate a requester ──

func TestInternalAccessToken_IsRefusedByPortalValidation(t *testing.T) {
	key := testKey(t)
	pair, err := internalService(key).IssueTokenPair(uuid.New(), "agent@example.com", uuid.New().String(), "member", 0)
	require.NoError(t, err)

	_, err = portalService(key).ValidateSession(pair.AccessToken)
	require.ErrorIs(t, err, portal.ErrInvalidSession,
		"an agent's access token must not authenticate a portal session")

	_, err = portalService(key).ValidateSession(pair.RefreshToken)
	require.ErrorIs(t, err, portal.ErrInvalidSession)
}

// TestInternalAudience_IsRefusedByPortalValidation isolates the audience
// check on the portal side, for the same reason its mirror does on the
// internal side: the previous test would pass on the `typ` check alone.
func TestInternalAudience_IsRefusedByPortalValidation(t *testing.T) {
	key := testKey(t)
	now := time.Now().UTC()

	token := signRaw(t, key, &portal.Claims{
		RequesterID:       uuid.New(),
		OrgID:             uuid.New().String(),
		PortalID:          uuid.New(),
		TokenType:         "portal_session",
		SessionGeneration: 0,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azimuthal",
			Audience:  jwt.ClaimStrings{auth.AudienceInternal},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	})

	_, err := portalService(key).ValidateSession(token)
	require.ErrorIs(t, err, portal.ErrInvalidSession,
		"the internal audience must be refused even when every other claim says 'portal session'")
}

// TestPortalToken_WithNoAudience_IsRefused pins the ASYMMETRY.
//
// The internal validator tolerates a missing audience so that tokens minted
// before the claim existed keep working. The portal validator must not:
// nothing predates AudiencePortal, so an audience-less token reaching it is
// either a forgery attempt or a bug, never a legacy session.
func TestPortalToken_WithNoAudience_IsRefused(t *testing.T) {
	key := testKey(t)
	now := time.Now().UTC()

	token := signRaw(t, key, &portal.Claims{
		RequesterID: uuid.New(),
		PortalID:    uuid.New(),
		TokenType:   "portal_session",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azimuthal",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	})

	_, err := portalService(key).ValidateSession(token)
	require.ErrorIs(t, err, portal.ErrInvalidSession)
}

// ── The non-disruption guarantee ──

// TestLegacyInternalToken_WithNoAudience_StillValidates is the counterweight.
//
// Every token this deployment issued before the audience claim existed
// carries no `aud`. Refusing those would sign out every user at the moment of
// deploy — the exact failure ADR-0004 exists to document, and the one
// migration 024 went to some trouble to avoid when it introduced `tgen`.
// The leniency costs nothing: minting any token requires the private key, so
// an absent audience is not a shape an attacker can choose.
func TestLegacyInternalToken_WithNoAudience_StillValidates(t *testing.T) {
	key := testKey(t)
	now := time.Now().UTC()
	userID := uuid.New()

	token := signRaw(t, key, &auth.Claims{
		UserID:    userID,
		Email:     "agent@example.com",
		OrgID:     uuid.New().String(),
		Role:      "member",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azimuthal",
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	})

	claims, err := internalService(key).ValidateAccessToken(token)
	require.NoError(t, err, "a pre-audience token must keep working; the upgrade signs nobody out")
	require.Equal(t, userID, claims.UserID)
}

// TestInternalTokensCarryTheAudience proves the minting half. Verification
// without minting refuses everything; minting without verification changes
// nothing. `iss` has been minted since v0.1.11 and never checked, which is
// exactly the failure mode this guards against.
func TestInternalTokensCarryTheAudience(t *testing.T) {
	key := testKey(t)
	pair, err := internalService(key).IssueTokenPair(uuid.New(), "a@example.com", uuid.New().String(), "member", 0)
	require.NoError(t, err)

	claims, err := internalService(key).ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	require.Contains(t, []string(claims.Audience), auth.AudienceInternal)
}

func TestPortalTokensCarryTheAudience(t *testing.T) {
	key := testKey(t)
	token, err := portalService(key).IssueSession(uuid.New(), uuid.New(), uuid.New().String(), 3)
	require.NoError(t, err)

	claims, err := portalService(key).ValidateSession(token)
	require.NoError(t, err)
	require.Contains(t, []string(claims.Audience), portal.AudiencePortal)
	require.Equal(t, 3, claims.SessionGeneration)
}

// TestPortalToken_IsRefusedByAForeignKey guards the obvious floor: the
// audience boundary is layered on top of signature verification, not instead
// of it.
func TestPortalToken_IsRefusedByAForeignKey(t *testing.T) {
	token, err := portalService(testKey(t)).IssueSession(uuid.New(), uuid.New(), uuid.New().String(), 0)
	require.NoError(t, err)

	_, err = portalService(testKey(t)).ValidateSession(token)
	require.ErrorIs(t, err, portal.ErrInvalidSession)
}
