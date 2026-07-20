package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// contextKey is the unexported type used for storing values in request contexts.
type contextKey int

const (
	// contextKeyClaims is the context key for JWT claims.
	contextKeyClaims contextKey = iota
	// contextKeySession is the context key for a session record.
	contextKeySession
)

// State is the per-request account check: the live token_generation
// column and active flag, read in a single primary-key query.
type State struct {
	TokenGeneration int
	IsActive        bool
}

// StateStore loads a user's live auth state. Implemented by
// internal/db/adapters against the users table; the read must stay a single
// constant-cost indexed lookup because it runs on every authenticated
// request (TestMatrixAPI23 counts it).
type StateStore interface {
	// State returns the user's current auth state. Returns ErrNotFound
	// for unknown or soft-deleted users.
	AuthState(ctx context.Context, userID uuid.UUID) (State, error)
}

// Authenticator provides HTTP middleware for the chi router.
// It supports both Bearer-token (JWT) and session-cookie auth.
type Authenticator struct {
	jwt     *JWTService
	session *SessionService
	// states backs the per-request generation and active check. nil disables
	// the check — permitted only for routing-only unit tests without a
	// database, mirroring the RouterConfig.AccessResolver convention; every
	// real construction site wires one.
	states StateStore
}

// NewAuthenticator creates an Authenticator using the provided services.
// states may be nil ONLY in routing-only unit tests; production and the
// integration harness always wire the DB-backed store.
func NewAuthenticator(jwt *JWTService, session *SessionService, states StateStore) *Authenticator {
	return &Authenticator{jwt: jwt, session: session, states: states}
}

// RequireAuth is chi middleware that rejects unauthenticated requests with
// 401 Unauthorized. It accepts either a Bearer JWT or a session cookie.
// On success, it stores the JWT claims in the request context.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaims(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			body := map[string]any{
				"error": map[string]any{
					"code":    "UNAUTHORIZED",
					"message": "missing or invalid authentication",
				},
			}
			if encErr := json.NewEncoder(w).Encode(body); encErr != nil {
				slog.Error("writing auth error response", "error", encErr)
			}
			return
		}
		ctx := context.WithValue(r.Context(), contextKeyClaims, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuth is chi middleware that attempts authentication but allows the
// request to proceed even if no credentials are present. Handlers can check
// ClaimsFromContext to determine whether the user is authenticated.
func (a *Authenticator) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaims(r)
		if err == nil {
			r = r.WithContext(context.WithValue(r.Context(), contextKeyClaims, claims))
		}
		next.ServeHTTP(w, r)
	})
}

// ClaimsFromContext retrieves JWT claims stored by RequireAuth or OptionalAuth.
// Returns nil if the context carries no auth claims.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(contextKeyClaims).(*Claims)
	return c
}

// extractClaims attempts to authenticate the request via:
//  1. Authorization: Bearer <token> header (JWT)
//  2. "session" cookie (opaque session token)
//
// A signature-valid credential is then checked against the live account
// state (checkAuthState) — RS256 tokens are stateless, so this single
// indexed read is what makes deactivation and force logout take effect on
// the very next request instead of at token expiry.
//
// Returns ErrInvalidToken if neither credential is present or valid.
func (a *Authenticator) extractClaims(r *http.Request) (*Claims, error) {
	// 1. Try Bearer token.
	if bearer := bearerToken(r); bearer != "" {
		claims, err := a.jwt.ValidateAccessToken(bearer)
		if err != nil {
			return nil, err
		}
		if err := a.checkAuthState(r.Context(), claims); err != nil {
			return nil, err
		}
		return claims, nil
	}

	// 2. Try session cookie.
	cookie, err := r.Cookie("session")
	if err != nil || cookie.Value == "" {
		return nil, ErrInvalidToken
	}

	sess, err := a.session.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// Synthesise Claims from the session so handlers have a uniform interface.
	claims := &Claims{
		UserID:    sess.UserID,
		TokenType: "session",
	}
	if err := a.checkAuthState(r.Context(), claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// checkAuthState rejects credentials whose account is deactivated or whose
// token_generation claim is stale. One primary-key read per request —
// constant cost, asserted by TestMatrixAPI23 so it cannot be silently
// optimised away later.
//
// DB sessions (the cookie path) are revocable server-side and carry no
// generation claim, so they are checked for active status only.
func (a *Authenticator) checkAuthState(ctx context.Context, claims *Claims) error {
	if a.states == nil {
		// Routing-only unit tests without a database. Every real
		// construction site wires a store; the integration suite and
		// TestMatrixAPI23 run against the wired path.
		return nil
	}
	state, err := a.states.AuthState(ctx, claims.UserID)
	if err != nil {
		return ErrInvalidToken
	}
	if !state.IsActive {
		return ErrInvalidToken
	}
	if claims.TokenType != "session" && claims.TokenGeneration != state.TokenGeneration {
		return ErrInvalidToken
	}
	return nil
}

// bearerToken extracts the token value from an "Authorization: Bearer <token>"
// header. Returns an empty string if the header is absent or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// RateLimiter is a stub interface for request-rate limiting.
// The concrete implementation will be added in Phase 2.
type RateLimiter interface {
	// Allow reports whether the request from the given key (IP or user ID)
	// should be allowed to proceed.
	Allow(key string) bool
}
