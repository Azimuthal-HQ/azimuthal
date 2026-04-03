package auth

import (
	"context"
	"net/http"
	"strings"
)

// contextKey is the unexported type used for storing values in request contexts.
type contextKey int

const (
	// contextKeyClaims is the context key for JWT claims.
	contextKeyClaims contextKey = iota
	// contextKeySession is the context key for a session record.
	contextKeySession
)

// Authenticator provides HTTP middleware for the chi router.
// It supports both Bearer-token (JWT) and session-cookie auth.
type Authenticator struct {
	jwt     *JWTService
	session *SessionService
}

// NewAuthenticator creates an Authenticator using the provided services.
func NewAuthenticator(jwt *JWTService, session *SessionService) *Authenticator {
	return &Authenticator{jwt: jwt, session: session}
}

// RequireAuth is chi middleware that rejects unauthenticated requests with
// 401 Unauthorized. It accepts either a Bearer JWT or a session cookie.
// On success, it stores the JWT claims in the request context.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.extractClaims(r)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
// Returns ErrInvalidToken if neither credential is present or valid.
func (a *Authenticator) extractClaims(r *http.Request) (*Claims, error) {
	// 1. Try Bearer token.
	if bearer := bearerToken(r); bearer != "" {
		return a.jwt.ValidateAccessToken(bearer)
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
	return claims, nil
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
