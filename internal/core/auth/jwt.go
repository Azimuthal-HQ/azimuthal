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
	// Audience is the "aud" claim value stamped on every token this service
	// mints, and the only audience it will accept back. Leave it empty and
	// the audience machinery is inert — which is what the routing-only unit
	// harness does, and what every deployment did before the customer portal
	// existed. See AudienceInternal.
	Audience string
}

// AudienceInternal is the "aud" claim carried by tokens that authenticate a
// user of the internal product — the only tokens RequireAuth accepts.
//
// It exists because the customer portal introduced a second token family
// signed by the SAME RSA key. `auth_signing_keys` holds one row and says so
// (`CHECK (id = 1)`, migration 018), so a key per family is not available
// without changing that decision; the audience claim is therefore not a
// label but the entire boundary between an agent's credential and a
// requester's.
//
// Both halves are required and neither is sufficient. Minting an `aud` and
// not verifying it changes nothing, because golang-jwt skips audience
// verification unless the parser is given jwt.WithAudience — which is why
// `iss` has been minted since v0.1.11 and never once checked. Verifying
// without minting rejects everything. parseToken below does the verifying.
const AudienceInternal = "azimuthal-internal"

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
	// SessionID names the sessions row (migration 002) this token belongs to,
	// and it is what makes revocation per-session rather than all-or-nothing.
	// Login mints one row per sign-in and stamps its id here; the auth
	// middleware's per-request read joins that row and refuses a token whose
	// session is revoked, expired, or absent (B1 per-session revocation). A
	// single-device logout revokes one row and leaves the others alone, where
	// a token_generation bump would have signed the user out everywhere.
	//
	// A dedicated `sid` claim rather than the registered `jti`: `jti` is minted
	// fresh (uuid.New) on every access AND refresh token and must stay
	// per-token-unique, while `sid` is deliberately STABLE — the refresh
	// handler carries it forward so the session survives token rotation. A
	// token carrying no sid decodes to the zero UUID, which the middleware
	// treats as "no live session" and refuses; there is no sessionless-token
	// window to honour (no users predate this claim), and a branch that
	// admitted one would be the revocation bypass this track exists to close.
	SessionID uuid.UUID `json:"sid,omitempty"`
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
// first use. sessionID is the sessions row the pair belongs to, stamped into
// both tokens as the `sid` claim: login mints a fresh row, and the refresh
// handler carries the same id forward so a rotated pair stays inside the same
// session. It is a REQUIRED parameter rather than an optional one on purpose —
// a user token with no session is one the middleware refuses, so there is no
// caller that legitimately wants a sessionless pair.
func (s *JWTService) IssueTokenPair(userID uuid.UUID, email, orgID, role string, generation int, sessionID uuid.UUID) (*TokenPair, error) {
	access, err := s.signToken(userID, email, orgID, role, "access", generation, sessionID, s.cfg.AccessTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing access token: %w", err)
	}
	refresh, err := s.signToken(userID, email, orgID, role, "refresh", generation, sessionID, s.cfg.RefreshTTL)
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
func (s *JWTService) signToken(userID uuid.UUID, email, orgID, role, tokenType string, generation int, sessionID uuid.UUID, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := &Claims{
		UserID:          userID,
		Email:           email,
		OrgID:           orgID,
		Role:            role,
		TokenType:       tokenType,
		TokenGeneration: generation,
		SessionID:       sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.New().String(),
		},
	}
	if s.cfg.Audience != "" {
		claims.Audience = jwt.ClaimStrings{s.cfg.Audience}
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
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if err := s.checkAudience(claims.Audience); err != nil {
		return nil, err
	}
	return claims, nil
}

// checkAudience rejects a token minted for a different token family.
//
// The rule is asymmetric on purpose, and the asymmetry is the whole design:
//
//   - A token carrying SOME OTHER audience is rejected. That is the portal
//     boundary. A requester's token names the portal audience, so it fails
//     here and can never reach RequireAuth, /notifications, /auth/me or any
//     other route that reads auth.Claims.
//
//   - A token carrying NO audience is accepted. Those are the internal
//     tokens minted before this claim existed, and rejecting them would sign
//     out every user at the moment of deploy. That is precisely the failure
//     ADR-0004 was written about — eleven releases of "everyone is logged out
//     again" — so the upgrade is made non-disruptive the same way
//     token_generation was in migration 024: the old shape decodes to the
//     value that means "fine", and no session breaks.
//
// The leniency costs nothing, because an absent audience is not something an
// attacker can choose. Minting any token at all requires the RSA private key
// from `auth_signing_keys`; if that is available the audience claim is not
// what is standing between the attacker and the product. The only tokens in
// existence with no audience are ones this deployment issued itself, to its
// own users, before the upgrade.
//
// The portal parser is strict in the direction this one is lenient — it
// REQUIRES its audience — so the two families are mutually exclusive in both
// directions even while legacy internal tokens are still in flight.
func (s *JWTService) checkAudience(aud jwt.ClaimStrings) error {
	if s.cfg.Audience == "" || len(aud) == 0 {
		return nil
	}
	for _, a := range aud {
		if a == s.cfg.Audience {
			return nil
		}
	}
	return ErrInvalidToken
}
