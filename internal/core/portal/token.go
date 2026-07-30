package portal

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AudiencePortal is the "aud" claim carried by every portal session token, and
// the only audience the portal parser accepts.
//
// Portal tokens are signed with the SAME RSA key as internal tokens, because
// `auth_signing_keys` holds exactly one row and says so in its schema
// (`CHECK (id = 1)`, migration 018). Introducing a second key would mean
// revisiting ADR-0004's single-root-of-trust decision, which is not this
// phase's call to make. So the audience claim is not a label on the token —
// it is the boundary, and it is verified in both directions:
//
//   - Here, strictly: a token with no audience, or any audience other than
//     this one, is refused. An internal access token therefore cannot
//     authenticate a requester, and neither can a legacy token minted before
//     audiences existed.
//
//   - In internal/core/auth, asymmetrically: a token carrying a FOREIGN
//     audience is refused, while one carrying none is accepted so that the
//     upgrade signs nobody out. A portal token always carries this audience,
//     so it is always in the refused set.
//
// The strictness here is affordable for the reason the leniency there is
// necessary: no portal token predates this constant, so there is no legacy
// shape to accommodate.
const AudiencePortal = "azimuthal-portal"

// tokenTypePortalSession is the "typ" claim on a portal session token.
//
// Deliberately not "access", "refresh" or "session". The first two are the
// internal family's values; the third is the literal that makes
// auth.checkAuthState SKIP its generation comparison (middleware.go), and a
// family that adopted it would silently lose revocation. Nothing outside this
// package should ever produce this string.
const tokenTypePortalSession = "portal_session"

// TokenConfig holds the key material and lifetime for portal session tokens.
type TokenConfig struct {
	// PrivateKey signs portal tokens (RS256). Shared with the internal token
	// family — see AudiencePortal for why that is safe and what makes it so.
	PrivateKey *rsa.PrivateKey
	// PublicKey verifies incoming portal tokens.
	PublicKey *rsa.PublicKey
	// SessionTTL is how long a redeemed magic link's session remains valid.
	SessionTTL time.Duration
	// Issuer is the "iss" claim value.
	Issuer string
}

// Claims are the JWT payload fields for a portal session token.
//
// This is a SEPARATE TYPE from auth.Claims, and the separation is structural
// rather than stylistic. Forty-eight non-test call sites across
// internal/core/api read auth.ClaimsFromContext(ctx).UserID and treat it as a
// users.id. A requester principal expressed as an auth.Claims — even with a
// distinguishing TokenType — would be a confused deputy at every one of them
// the moment any code path put it on the context under the same key. Two
// types that cannot be assigned to one another make that class of mistake a
// compile error instead of a security review.
type Claims struct {
	// RequesterID is a requesters.id. It is NOT a users.id and must never be
	// passed anywhere that expects one.
	RequesterID uuid.UUID `json:"rid"`
	// OrgID scopes the requester. Carried as a string to match auth.Claims'
	// wire shape rather than to be used for authorisation — the portal
	// resolves its org from the portal row, not from the token.
	OrgID string `json:"org_id"`
	// PortalID binds the session to ONE service desk. A requester who holds
	// sessions for two portals holds two tokens; neither reaches the other's
	// requests. Without this claim a magic link for one portal would
	// authenticate against every portal in the deployment.
	PortalID uuid.UUID `json:"pid"`
	// TokenType is always tokenTypePortalSession.
	TokenType string `json:"typ"`
	// SessionGeneration mirrors requesters.session_generation at issue time,
	// exactly as auth.Claims.TokenGeneration mirrors users.token_generation.
	// The portal guard compares it against the live column on every request,
	// so bumping the column revokes every session that requester holds.
	SessionGeneration int `json:"sgen"`
	jwt.RegisteredClaims
}

// TokenService issues and validates portal session tokens.
type TokenService struct {
	cfg TokenConfig
}

// NewTokenService creates a TokenService from the provided configuration.
func NewTokenService(cfg TokenConfig) *TokenService {
	return &TokenService{cfg: cfg}
}

// SessionTTL reports the configured session lifetime.
func (s *TokenService) SessionTTL() time.Duration { return s.cfg.SessionTTL }

// IssueSession mints a portal session token for a requester on one portal.
//
// generation must be the requester's CURRENT session_generation; a token
// minted with a stale value is refused by the portal guard on first use.
func (s *TokenService) IssueSession(requesterID, portalID uuid.UUID, orgID string, generation int) (string, error) {
	now := time.Now().UTC()
	claims := &Claims{
		RequesterID:       requesterID,
		OrgID:             orgID,
		PortalID:          portalID,
		TokenType:         tokenTypePortalSession,
		SessionGeneration: generation,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   requesterID.String(),
			Audience:  jwt.ClaimStrings{AudiencePortal},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.SessionTTL)),
			ID:        uuid.New().String(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(s.cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("signing portal session token: %w", err)
	}
	return signed, nil
}

// ValidateSession parses and verifies a portal session token.
//
// Every check here is required for the boundary to hold, so none of them is
// redundant with another:
//
//	WithValidMethods   closes algorithm confusion and `alg: none`.
//	WithAudience       is what refuses an internal access token. Unlike the
//	                   internal parser's tolerant check, this REQUIRES the
//	                   claim: golang-jwt fails a token whose aud is absent
//	                   once an expected audience is configured, which is the
//	                   behaviour wanted here.
//	WithIssuer         pins the deployment.
//	WithExpirationRequired  a token with no exp would otherwise validate
//	                   forever, because golang-jwt only checks exp when it is
//	                   present.
//	TokenType          refuses anything from this package that is not a
//	                   session token, should a second portal token kind ever
//	                   be added.
//
// The caller must still compare SessionGeneration against the live requesters
// row — that is a database read, and it belongs to the guard, not here.
func (s *TokenService) ValidateSession(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.cfg.PublicKey, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithAudience(AudiencePortal),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidSession
	}
	if claims.TokenType != tokenTypePortalSession {
		return nil, ErrInvalidSession
	}
	if claims.RequesterID == uuid.Nil || claims.PortalID == uuid.Nil {
		return nil, ErrInvalidSession
	}
	return claims, nil
}
