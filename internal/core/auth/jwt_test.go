package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testTokenConfig generates a fresh RSA-2048 key pair for testing.
func testTokenConfig(t *testing.T) TokenConfig {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
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

	pair, err := svc.IssueTokenPair(userID, email, uuid.New().String(), "member")
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
}

func TestJWTService_RefreshToken_NotAccepted_AsAccess(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	pair, err := svc.IssueTokenPair(uuid.New(), "a@b.com", uuid.New().String(), "member")
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
	pair, err := svc.IssueTokenPair(uuid.New(), "a@b.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}
	// Access token must not be accepted in the refresh flow.
	if _, err := svc.RefreshTokens(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestJWTService_Refresh_IssuesNewPair(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	userID := uuid.New()
	pair, err := svc.IssueTokenPair(userID, "refresh@example.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}

	newPair, err := svc.RefreshTokens(pair.RefreshToken)
	if err != nil {
		t.Fatalf("refreshing tokens: %v", err)
	}
	if newPair.AccessToken == pair.AccessToken {
		t.Error("refreshed access token must be different from original")
	}

	// New access token must be valid.
	claims, err := svc.ValidateAccessToken(newPair.AccessToken)
	if err != nil {
		t.Fatalf("validating refreshed access token: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("expected userID %s after refresh, got %s", userID, claims.UserID)
	}
}

func TestJWTService_ExpiredToken(t *testing.T) {
	cfg := testTokenConfig(t)
	cfg.AccessTTL = -time.Second // expired immediately
	svc := NewJWTService(cfg)

	pair, err := svc.IssueTokenPair(uuid.New(), "exp@example.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestJWTService_TamperedToken(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	pair, err := svc.IssueTokenPair(uuid.New(), "tamper@example.com", uuid.New().String(), "member")
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

	pair, err := svc.IssueTokenPair(userID, "org@example.com", orgID, "owner")
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

func TestJWTService_RefreshPreservesOrgID(t *testing.T) {
	svc := NewJWTService(testTokenConfig(t))
	orgID := uuid.New().String()

	pair, err := svc.IssueTokenPair(uuid.New(), "refresh-org@example.com", orgID, "admin")
	if err != nil {
		t.Fatal(err)
	}

	newPair, err := svc.RefreshTokens(pair.RefreshToken)
	if err != nil {
		t.Fatalf("refreshing tokens: %v", err)
	}

	claims, err := svc.ValidateAccessToken(newPair.AccessToken)
	if err != nil {
		t.Fatalf("validating refreshed token: %v", err)
	}
	if claims.OrgID != orgID {
		t.Errorf("expected orgID %s after refresh, got %s", orgID, claims.OrgID)
	}
	if claims.Role != "admin" {
		t.Errorf("expected role admin after refresh, got %s", claims.Role)
	}
}

func TestJWTService_WrongKey(t *testing.T) {
	svc1 := NewJWTService(testTokenConfig(t))
	svc2 := NewJWTService(testTokenConfig(t)) // different key pair

	pair, err := svc1.IssueTokenPair(uuid.New(), "key@example.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}
	// svc2 has a different public key — must reject svc1's token.
	if _, err := svc2.ValidateAccessToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for wrong-key token, got %v", err)
	}
}
