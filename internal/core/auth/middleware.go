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
// column, the active flag, and whether the token's session is still live —
// all read in a single indexed query (users PK + a session PK join).
type State struct {
	TokenGeneration int
	IsActive        bool
	// SessionValid reports whether the sessions row named by the token's sid
	// claim is present, unrevoked, and unexpired. False for a revoked or
	// expired session, and false for a token that names no session at all
	// (the zero-UUID sid). The middleware only consults it for bearer user
	// tokens; the cookie path carries no sid.
	SessionValid bool
}

// StateStore loads a user's live auth state. Implemented by
// internal/db/adapters against the users table; the read must stay a single
// constant-cost indexed lookup because it runs on every authenticated
// request (TestMatrixAPI23 counts it).
type StateStore interface {
	// AuthState returns the user's current auth state, joined with the named
	// session's liveness. Returns ErrNotFound for unknown or soft-deleted
	// users. sessionID may be uuid.Nil (the cookie path, which carries no
	// session claim); SessionValid is then simply false and the caller
	// ignores it.
	AuthState(ctx context.Context, userID, sessionID uuid.UUID) (State, error)
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

// checkAuthState rejects credentials whose account is deactivated, whose
// token_generation claim is stale, or whose session has been revoked. One
// indexed read per request — the users PK plus a sessions PK join, constant
// cost, asserted by TestMatrixAPI23 so it cannot be silently split or dropped
// later.
//
// The store read fails closed: any error refuses the request, matching the
// posture the generation check has always had.
//
// Two credential kinds, checked differently:
//
//   - The COOKIE path (TokenType "session") is a server-side DB session the
//     caller reached via GetSession, which already validated revocation and
//     expiry. It carries no generation and no sid claim, so it is checked for
//     active status only — SessionValid is meaningless for it.
//
//   - A BEARER USER TOKEN (any other TokenType — "access" in practice) must
//     clear BOTH the generation check and a live session. The generation gate
//     is the org-wide hammer (deactivation, force logout, logout-all,
//     password change); the session gate is per-device (single-device
//     logout). A token that names no session — the zero-UUID sid, which is
//     every legacy token and any token minted without a session row — has
//     SessionValid false and is refused. That refusal is the point of the
//     whole track: there is deliberately no "sessionless tokens still work"
//     branch, because it would be a revocation bypass, and no user predates
//     the sid claim so there is nothing to keep working.
func (a *Authenticator) checkAuthState(ctx context.Context, claims *Claims) error {
	if a.states == nil {
		// Routing-only unit tests without a database. Every real
		// construction site wires a store; the integration suite and
		// TestMatrixAPI23 run against the wired path.
		return nil
	}
	state, err := a.states.AuthState(ctx, claims.UserID, claims.SessionID)
	if err != nil {
		return ErrInvalidToken
	}
	if !state.IsActive {
		return ErrInvalidToken
	}
	if claims.TokenType == "session" {
		return nil
	}
	if claims.TokenGeneration != state.TokenGeneration {
		return ErrInvalidToken
	}
	if !state.SessionValid {
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
