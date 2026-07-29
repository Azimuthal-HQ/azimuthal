package portal

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
)

// RequirePortalSession authenticates an external requester and puts the
// principal on the request context.
//
// THIS IS NOT auth.RequireAuth AND MUST NEVER BECOME IT. It reads a different
// header value into a different claims type, validates a different audience,
// checks a different revocation column, and stores the result under a context
// key that no internal handler can read. A requester holds no membership, no
// grant and no team, so access.Can would return false for every capability —
// which means calling it here would look like a check while asserting
// nothing. The guard IS the authorisation for this subtree.
//
// It also binds the session to the portal in the URL. A magic link is issued
// for one service desk; without this comparison a session minted on portal A
// would authenticate against portal B in the same deployment, and the request
// scoping downstream (which trusts the session's SpaceID) would happily serve
// A's requester from B's space.
func RequirePortalSession(svc *portal.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail closed on a wiring mistake rather than waving traffic
			// through. A nil collaborator that authenticated everything is
			// exactly the dark-harness shape CLAUDE.md §2 describes.
			if svc == nil {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
				return
			}

			token := bearerToken(r)
			if token == "" {
				respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
				return
			}

			sess, err := svc.Authenticate(r.Context(), token)
			if err != nil {
				respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
				return
			}

			// The URL's portal must be the session's portal.
			key := chi.URLParam(r, "portalKey")
			p, err := svc.LookupPortal(r.Context(), key)
			if err != nil || p.ID != sess.PortalID {
				// 404, not 403: a session for another portal must not learn
				// that this portal exists.
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "portal not found")
				return
			}

			next.ServeHTTP(w, r.WithContext(portal.WithSession(r.Context(), &sess)))
		})
	}
}

// bearerToken extracts a bearer credential from the Authorization header.
//
// HEADER ONLY — the portal issues no cookie. Nothing in this codebase sets one
// (defect S8 records what assuming otherwise cost), and introducing the first
// cookie in the product on the surface that faces the public internet would
// mean reasoning about CSRF on a route family that currently cannot be
// attacked that way at all.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
