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

			// THE PORTAL IS RESOLVED FIRST, BEFORE THE CREDENTIAL.
			//
			// The URL's portal is the primary resource here, so "no such
			// portal" has to answer the same way whoever is asking — and it
			// must answer that way for a DISABLED portal too, which is how
			// switching a portal off ends its outstanding sessions rather than
			// waiting for them to expire (GetPortalByKey requires `enabled`).
			//
			// Authenticating first would answer 401 in that case, which is
			// both less accurate and worse behaviour: it sends the requester
			// to a sign-in page that cannot work either.
			p, err := svc.LookupPortal(r.Context(), chi.URLParam(r, "portalKey"))
			if err != nil {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "portal not found")
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

			// The URL's portal must be the session's portal. Without this a
			// magic link issued for one service desk would authenticate
			// against every portal in the deployment, and the request scoping
			// downstream — which trusts the session's SpaceID — would serve
			// one portal's requester out of another portal's space.
			//
			// 404 rather than 403: a session for another portal must not learn
			// that this one exists.
			if p.ID != sess.PortalID {
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
