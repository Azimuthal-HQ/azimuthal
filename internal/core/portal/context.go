package portal

import "context"

// contextKey is this package's PRIVATE context key type.
//
// It is deliberately not auth's. auth.contextKeyClaims carries an
// *auth.Claims whose UserID is a users.id, and forty-eight non-test sites
// across internal/core/api destructure it as one without asking. A requester
// stored under that key would be read as a user by every one of them.
//
// Two separate key types in two separate packages means the mistake is not
// available: nothing outside this file can write a Session onto the context,
// and nothing outside this package can read one off it except through
// SessionFromContext, which returns the portal type.
type contextKey int

const contextKeySession contextKey = iota

// WithSession returns a context carrying the authenticated portal session.
// Called only by the portal guard.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, contextKeySession, s)
}

// SessionFromContext returns the authenticated portal session, or nil.
//
// Every portal handler nil-checks this and refuses on nil. That is the same
// fail-closed convention access.FromContext uses, and it matters for the same
// reason: a handler mounted without its guard must deny, not proceed with a
// zero-valued principal.
func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(contextKeySession).(*Session)
	return s
}
