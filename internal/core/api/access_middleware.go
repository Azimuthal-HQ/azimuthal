package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// ResolveAccess is the per-request permission resolution middleware (spec §5,
// ADR-0007). Mounted once on the /orgs/{orgID} subtree, it resolves the
// caller's readable space set and per-space roles in a constant number of
// queries, caches the result on the request context, and 404s callers who
// are not members of the org — existence is never leaked.
//
// The cache lives for exactly one request. Nothing is shared across
// requests, which is what makes grant revocation immediate.
func ResolveAccess(resolver *access.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
				return
			}
			claims := auth.ClaimsFromContext(r.Context())
			if claims == nil {
				respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
				return
			}
			res, err := resolver.Resolve(r.Context(), orgID, claims.UserID)
			if errors.Is(err, access.ErrNotOrgMember) {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "organization not found")
				return
			}
			if err != nil {
				respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resolve access")
				return
			}
			next.ServeHTTP(w, r.WithContext(access.WithResolution(r.Context(), res)))
		})
	}
}

// RequireSpaceReadable 404s any {spaceID}-scoped request whose space is not
// in the caller's resolved readable set. Runs after RequireSpaceInOrg, so
// the space is already known to belong to the org — this guard adds the
// access decision. 404, never 403: unreadable spaces do not exist as far as
// the caller can tell (spec §2.6).
func RequireSpaceReadable() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spaceIDRaw := chi.URLParam(r, "spaceID")
			if spaceIDRaw == "" {
				next.ServeHTTP(w, r)
				return
			}
			spaceID, err := uuid.Parse(spaceIDRaw)
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
				return
			}
			res := access.FromContext(r.Context())
			if res == nil || !res.CanReadSpace(spaceID) {
				// Fail closed: a missing resolution denies, never allows.
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCapability rejects requests lacking the capability on the
// {spaceID} in the URL: 403 with readable spaces (right to know it exists,
// wrong capability), and 404 when the space is not readable at all.
func RequireCapability(c access.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
				return
			}
			res := access.FromContext(r.Context())
			if res == nil || !res.CanReadSpace(spaceID) {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
				return
			}
			if !res.Can(c, spaceID) {
				respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireWriteFloor enforces the write floor on a space resource subtree:
// reads pass (the readable guard already ran), every mutating method needs
// at least the given capability. Handlers refine above the floor — the
// edit_own/edit_any split and agent-tier checks live with the entity.
func RequireWriteFloor(c access.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
				return
			}
			if !access.Can(r.Context(), c, spaceID) {
				respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireOrgAdmin rejects non-admin callers with 403. Used for org-level
// administrative surfaces (team management, workflow admin).
func RequireOrgAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res := access.FromContext(r.Context())
			if res == nil {
				respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "organization not found")
				return
			}
			if !res.IsOrgAdmin {
				respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "organization admin required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
