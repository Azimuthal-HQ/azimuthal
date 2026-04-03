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
	AccessToken  string
	RefreshToken string
}

// Claims are the JWT payload fields for Azimuthal tokens.
type Claims struct {
	UserID uuid.UUID `json:"uid"`
	Email  string    `json:"email"`
	// TokenType differentiates "access" from "refresh" tokens.
	TokenType string `json:"typ"`
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

// IssueTokenPair generates a new access/refresh token pair for the given user.
func (s *JWTService) IssueTokenPair(userID uuid.UUID, email string) (*TokenPair, error) {
	access, err := s.signToken(userID, email, "access", s.cfg.AccessTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := s.signToken(userID, email, "refresh", s.cfg.RefreshTTL)
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

// RefreshTokens validates a refresh token and, if valid, issues a new token pair.
// Returns ErrInvalidToken if the token is invalid or not a refresh token.
func (s *JWTService) RefreshTokens(refreshTokenString string) (*TokenPair, error) {
	claims, err := s.parseToken(refreshTokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}
	return s.IssueTokenPair(claims.UserID, claims.Email)
}

// signToken creates a signed JWT with the given parameters.
func (s *JWTService) signToken(userID uuid.UUID, email, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := &Claims{
		UserID:    userID,
		Email:     email,
		TokenType: tokenType,
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
