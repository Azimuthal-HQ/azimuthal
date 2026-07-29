package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// sharedTestKey is the RSA-2048 key the token tests in this package sign
// with. One per test binary, not one per call (known-issues #19): keygen
// costs ~40ms on a developer machine and several times that under -race, and
// testTokenConfig is called on nearly every test in this file and in
// middleware_test.go.
//
// Read the doc comment on freshTestKey before using this for anything that
// cares about *which* key.
var sharedTestKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generating the shared test signing key: " + err.Error())
	}
	return key
})

// freshTestKey mints a key nothing else holds. Exactly one test needs it —
// TestJWTService_WrongKey, whose entire subject is that a service rejects a
// token signed by a key it does not have. Calling testTokenConfig twice would
// hand that test the same key twice and turn its rejection assertion into a
// tautology that passes while asserting nothing, which is why the distinct
// key is requested explicitly and loudly rather than inherited from a helper.
func freshTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	return key
}

// testTokenConfig returns a TokenConfig over the shared test key. The struct
// is returned by value, so a test may adjust TTLs or the issuer on its own
// copy without disturbing anyone else; the key itself is shared and must be
// treated as read-only.
func testTokenConfig(t *testing.T) TokenConfig {
	t.Helper()
	return tokenConfigFor(sharedTestKey())
}

// tokenConfigFor builds the standard test TokenConfig around a given key.
func tokenConfigFor(key *rsa.PrivateKey) TokenConfig {
	return TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
		Issuer:     "azimuthal-test",
	}
}

func TestJWTService_IssueAndValidate(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	userID := uuid.New()
	email := "jwt@example.com"

	pair, err := svc.IssueTokenPair(userID, email, uuid.New().String(), "member", 3)
	if err != nil {
		t.Fatalf("issuing token pair: %v", err)
	}

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validating access token: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, claims.UserID)
	}
	if claims.Email != email {
		t.Errorf("expected email %s, got %s", email, claims.Email)
	}
	if claims.TokenType != "access" {
		t.Errorf("expected type access, got %s", claims.TokenType)
	}
	if claims.TokenGeneration != 3 {
		t.Errorf("expected token generation 3 in claims, got %d", claims.TokenGeneration)
	}
}

func TestJWTService_RefreshToken_NotAccepted_AsAccess(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	pair, err := svc.IssueTokenPair(uuid.New(), "a@b.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Refresh token must not be accepted as an access token.
	if _, err := svc.ValidateAccessToken(pair.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestJWTService_AccessToken_NotAccepted_AsRefresh(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	pair, err := svc.IssueTokenPair(uuid.New(), "a@b.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Access token must not be accepted in the refresh flow.
	if _, err := svc.ValidateRefreshToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestJWTService_ValidateRefresh_ReturnsClaimsForReissue(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	userID := uuid.New()
	pair, err := svc.IssueTokenPair(userID, "refresh@example.com", uuid.New().String(), "member", 2)
	if err != nil {
		t.Fatal(err)
	}

	// The refresh flow is now two explicit steps: validate the refresh
	// token's claims (here), check them against the live account state (the
	// handler's job — a stale generation or inactive account must refuse),
	// then mint a new pair from the claims.
	claims, err := svc.ValidateRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validating refresh token: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("expected userID %s in refresh claims, got %s", userID, claims.UserID)
	}
	if claims.TokenGeneration != 2 {
		t.Errorf("expected generation 2 in refresh claims, got %d", claims.TokenGeneration)
	}

	newPair, err := svc.IssueTokenPair(claims.UserID, claims.Email, claims.OrgID, claims.Role, claims.TokenGeneration)
	if err != nil {
		t.Fatalf("reissuing pair: %v", err)
	}
	if newPair.AccessToken == pair.AccessToken {
		t.Error("refreshed access token must be different from original")
	}
	reclaims, err := svc.ValidateAccessToken(newPair.AccessToken)
	if err != nil {
		t.Fatalf("validating refreshed access token: %v", err)
	}
	if reclaims.UserID != userID {
		t.Errorf("expected userID %s after refresh, got %s", userID, reclaims.UserID)
	}
}

func TestJWTService_LegacyTokenWithoutGenerationClaim_DecodesAsZero(t *testing.T) {
	// Tokens minted before P2.5 carry no tgen claim at all. They must
	// decode as generation 0 — matching the column default — so the upgrade
	// itself signs nobody out. Hand-craft a token with the pre-P2.5 claim
	// shape, signed by the same key.
	cfg := testTokenConfig(t)
	svc := NewJWTService(cfg)
	userID := uuid.New()

	now := time.Now().UTC()
	legacy := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"uid":    userID.String(),
		"email":  "legacy@example.com",
		"org_id": uuid.New().String(),
		"role":   "member",
		"typ":    "access",
		// no "tgen" key — the pre-P2.5 shape
		"iss": cfg.Issuer,
		"sub": userID.String(),
		"iat": now.Unix(),
		"exp": now.Add(15 * time.Minute).Unix(),
		"jti": uuid.New().String(),
	})
	signed, err := legacy.SignedString(cfg.PrivateKey)
	if err != nil {
		t.Fatalf("signing legacy-shaped token: %v", err)
	}

	claims, err := svc.ValidateAccessToken(signed)
	if err != nil {
		t.Fatalf("validating legacy-shaped token: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, claims.UserID)
	}
	if claims.TokenGeneration != 0 {
		t.Errorf("legacy token must decode as generation 0, got %d", claims.TokenGeneration)
	}
}

func TestJWTService_ExpiredToken(t *testing.T) {
	cfg := testTokenConfig(t)
	cfg.AccessTTL = -time.Second // expired immediately
	svc := NewJWTService(cfg)

	pair, err := svc.IssueTokenPair(uuid.New(), "exp@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestJWTService_TamperedToken(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	pair, err := svc.IssueTokenPair(uuid.New(), "tamper@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	tampered := pair.AccessToken + "x"
	if _, err := svc.ValidateAccessToken(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for tampered token, got %v", err)
	}
}

func TestJWTService_OrgIDInClaims(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	userID := uuid.New()
	orgID := uuid.New().String()

	pair, err := svc.IssueTokenPair(userID, "org@example.com", orgID, "owner", 0)
	if err != nil {
		t.Fatalf("issuing token pair: %v", err)
	}

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validating access token: %v", err)
	}
	if claims.OrgID != orgID {
		t.Errorf("expected orgID %s, got %s", orgID, claims.OrgID)
	}
	if claims.Role != "owner" {
		t.Errorf("expected role owner, got %s", claims.Role)
	}
}

func TestJWTService_RefreshClaimsPreserveOrgID(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	orgID := uuid.New().String()

	pair, err := svc.IssueTokenPair(uuid.New(), "refresh-org@example.com", orgID, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}

	claims, err := svc.ValidateRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validating refresh token: %v", err)
	}
	if claims.OrgID != orgID {
		t.Errorf("expected orgID %s in refresh claims, got %s", orgID, claims.OrgID)
	}
	if claims.Role != "admin" {
		t.Errorf("expected role admin in refresh claims, got %s", claims.Role)
	}
}

func TestJWTService_WrongKey(t *testing.T) {
	// The two keys must genuinely differ, so both are requested explicitly:
	// testTokenConfig hands out one shared key per test binary, and calling
	// it twice here would make the rejection below unprovable.
	svc1 := NewJWTService(tokenConfigFor(freshTestKey(t)))
	svc2 := NewJWTService(tokenConfigFor(freshTestKey(t))) // different key pair

	pair, err := svc1.IssueTokenPair(uuid.New(), "key@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	// svc2 has a different public key — must reject svc1's token.
	if _, err := svc2.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for wrong-key token, got %v", err)
	}
}
