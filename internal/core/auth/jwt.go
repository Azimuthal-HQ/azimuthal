package auth

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenConfig holds RSA key material and token lifetime settings.
// Keys are loaded from config at startup — never hardcoded.
type TokenConfig struct {
	// PrivateKey is used to sign new tokens (RS256).
	PrivateKey *rsa.PrivateKey
	// PublicKey is used to verify incoming tokens.
	PublicKey *rsa.PublicKey
	// AccessTTL is how long an access token remains valid.
	AccessTTL time.Duration
	// RefreshTTL is how long a refresh token remains valid.
	RefreshTTL time.Duration
	// Issuer is the "iss" claim value (e.g. "azimuthal").
	Issuer string
}

// TokenPair holds an access token and a refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Claims are the JWT payload fields for Azimuthal tokens.
type Claims struct {
	UserID uuid.UUID `json:"uid"`
	Email  string    `json:"email"`
	OrgID  string    `json:"org_id"`
	Role   string    `json:"role"`
	// TokenType differentiates "access" from "refresh" tokens.
	TokenType string `json:"typ"`
	// TokenGeneration mirrors users.token_generation at issue time. The auth
	// middleware compares it against the live column on every request and
	// rejects any mismatch — incrementing the column instantly invalidates
	// every outstanding token (deactivation, force logout, password change).
	// Tokens minted before this claim existed decode as 0, matching the
	// column default, so the upgrade disrupts no session.
	TokenGeneration int `json:"tgen"`
	jwt.RegisteredClaims
}

// JWTService issues and validates RS256 JSON Web Tokens.
type JWTService struct {
	cfg TokenConfig
}

// NewJWTService creates a JWTService from the provided configuration.
func NewJWTService(cfg TokenConfig) *JWTService {
	return &JWTService{cfg: cfg}
}

// IssueTokenPair generates a new access/refresh token pair for the given
// user. generation must be the user's CURRENT token_generation — a token
// minted with a stale generation is rejected by the auth middleware on its
// first use.
func (s *JWTService) IssueTokenPair(userID uuid.UUID, email, orgID, role string, generation int) (*TokenPair, error) {
	access, err := s.signToken(userID, email, orgID, role, "access", generation, s.cfg.AccessTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := s.signToken(userID, email, orgID, role, "refresh", generation, s.cfg.RefreshTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing refresh token: %w", err)
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

// ValidateAccessToken parses and verifies an access token, returning its claims.
// Returns ErrInvalidToken if the token is malformed, expired, or not an access token.
func (s *JWTService) ValidateAccessToken(tokenString string) (*Claims, error) {
	claims, err := s.parseToken(tokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "access" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ValidateRefreshToken parses and verifies a refresh token, returning its
// claims. Returns ErrInvalidToken if the token is malformed, expired, or not
// a refresh token. The caller (the refresh handler) must check the claims'
// TokenGeneration and the account's active status against the store before
// issuing a new pair — a refresh token survives neither deactivation nor a
// generation bump.
func (s *JWTService) ValidateRefreshToken(refreshTokenString string) (*Claims, error) {
	claims, err := s.parseToken(refreshTokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// signToken creates a signed JWT with the given parameters.
func (s *JWTService) signToken(userID uuid.UUID, email, orgID, role, tokenType string, generation int, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := &Claims{
		UserID:          userID,
		Email:           email,
		OrgID:           orgID,
		Role:            role,
		TokenType:       tokenType,
		TokenGeneration: generation,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.New().String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(s.cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}

// parseToken parses a token string and returns its claims.
func (s *JWTService) parseToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.cfg.PublicKey, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
