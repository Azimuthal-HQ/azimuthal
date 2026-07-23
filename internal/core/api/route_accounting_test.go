package api_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// The read-path sweep (v0.3 spec §9 P2 DoD): every route is enumerated from
// the router itself — never from memory — and accounted for. Each route
// carries exactly one guard class:
//
//	public          unauthenticated by design (reason required in the table)
//	user-scoped     RequireAuth; data is filtered by the caller's identity
//	org-member      RequireAuth + ResolveAccess (404 for non-members)
//	org-admin       org-member + RequireOrgAdmin on the mutation
//	org-admin-404   org-member + RequireOrgAdmin404 — the P2.5 administration
//	                surface: non-admins get 404, never 403, because the
//	                surface's existence is itself privileged
//	space-read      org-member + RequireSpaceInOrg + RequireSpaceReadable (404)
//	space-write     space-read + the create_items write floor (403), with
//	                handler-level refinement above the floor
//	space-cap       space-read + handler-enforced capability (manage_space,
//	                manage_grants)
//	share-manage    org-member; the handler resolves the shared entity to its
//	                space and enforces manage_shares there, with a read check
//	                first so an unreadable space 404s (no existence leak) and
//	                a readable-but-uncapable space 403s. Org-scoped because a
//	                share names an entity, not a {spaceID} in the URL.
//	share-read      org-member + ResolveShares; authorised by an active,
//	                unexpired, unrevoked share whose audience includes the
//	                caller — NOT by space access. This is the one route family
//	                that reaches content without space-readability, by design
//	                (ADR-0008). 404 for both "no such entity" and "not shared
//	                with you", so it leaks neither existence nor shared-ness.
//
// A route added without a row here fails this test — that is the point.
var routeAccounting = map[string]string{
	// Public surface. /health and /ready expose liveness only; the docs
	// routes serve the committed OpenAPI spec; the auth endpoints are the
	// front door.
	"GET /health":                "public: liveness probe, no org data",
	"GET /ready":                 "public: readiness probe, no org data",
	"GET /api/docs":              "public: API documentation UI",
	"GET /api/docs/openapi.yaml": "public: committed OpenAPI spec",
	"POST /api/v1/auth/login":    "public: credential exchange",
	"POST /api/v1/auth/register": "public: account creation — 404 unless allow_registration (default off since P2.5)",
	"POST /api/v1/auth/refresh":  "public: token refresh (validates refresh token + live account state)",
	"POST /api/v1/auth/logout":   "public: session teardown (validates session)",

	// Invite acceptance: possession of the raw crypto/rand token is the
	// credential, exactly like a password-reset link.
	"GET /api/v1/invites/{token}": "public: invite inspection, token-authenticated",
	"POST /api/v1/invites/accept": "public: invite acceptance, token-authenticated",

	// User-scoped: authenticated, filtered by caller identity, no org data
	// beyond the caller's own memberships.
	"GET /api/v1/auth/me":                              "user-scoped",
	"PATCH /api/v1/auth/me":                            "user-scoped",
	"PUT /api/v1/auth/me/avatar":                       "user-scoped: self avatar upload",
	"GET /api/v1/notifications/":                       "user-scoped",
	"POST /api/v1/notifications/read-all":              "user-scoped",
	"POST /api/v1/notifications/{notificationID}/read": "user-scoped",

	// Org-scoped reads (membership required; 404 for non-members).
	"GET /api/v1/orgs/{orgID}/":                    "org-member",
	"PATCH /api/v1/orgs/{orgID}/":                  "org-admin",
	"GET /api/v1/orgs/{orgID}/labels/":             "org-member",
	"POST /api/v1/orgs/{orgID}/labels/":            "org-member: org-wide metadata, any member (status quo)",
	"DELETE /api/v1/orgs/{orgID}/labels/{labelID}": "org-member: org-wide metadata, any member (status quo)",

	// Teams: members read (the picker groups by team); admin mutates.
	"GET /api/v1/orgs/{orgID}/teams/":                             "org-member",
	"POST /api/v1/orgs/{orgID}/teams/":                            "org-admin",
	"GET /api/v1/orgs/{orgID}/teams/{teamID}":                     "org-member",
	"PATCH /api/v1/orgs/{orgID}/teams/{teamID}":                   "org-admin",
	"DELETE /api/v1/orgs/{orgID}/teams/{teamID}":                  "org-admin",
	"GET /api/v1/orgs/{orgID}/teams/{teamID}/members":             "org-member",
	"PUT /api/v1/orgs/{orgID}/teams/{teamID}/members/{userID}":    "org-admin",
	"DELETE /api/v1/orgs/{orgID}/teams/{teamID}/members/{userID}": "org-admin",

	// The P2.5 administration surface (people lifecycle, invites, access
	// matrix, bulk grants, audit viewer): org-admin-404 throughout — the
	// surface does not exist as far as non-admins can tell. The picker
	// search is the one member-visible route (space admins operate the
	// grants panel without being org admins).
	"GET /api/v1/orgs/{orgID}/users/":                       "org-admin-404: People directory",
	"PATCH /api/v1/orgs/{orgID}/users/{userID}":             "org-admin-404: org role / primary team; last-admin protected in the store",
	"DELETE /api/v1/orgs/{orgID}/users/{userID}":            "org-admin-404: remove from org; last-admin protected in the store",
	"POST /api/v1/orgs/{orgID}/users/{userID}/deactivate":   "org-admin-404: always terminates sessions; last-admin protected in the store",
	"POST /api/v1/orgs/{orgID}/users/{userID}/reactivate":   "org-admin-404",
	"POST /api/v1/orgs/{orgID}/users/{userID}/force-logout": "org-admin-404: bumps token_generation only; user stays active",
	"PUT /api/v1/orgs/{orgID}/users/{userID}/avatar":        "org-admin-404: admin sets another member's avatar",
	"GET /api/v1/orgs/{orgID}/users/{userID}/avatar":        "org-member: avatars are shown org-wide; object key derived server-side",
	"GET /api/v1/orgs/{orgID}/invites/":                     "org-admin-404",
	"POST /api/v1/orgs/{orgID}/invites/":                    "org-admin-404: raw token returned once, hashed at rest",
	"DELETE /api/v1/orgs/{orgID}/invites/{inviteID}":        "org-admin-404: revoke",
	"POST /api/v1/orgs/{orgID}/invites/{inviteID}/resend":   "org-admin-404: rotates the token",
	"GET /api/v1/orgs/{orgID}/access-matrix":                "org-admin-404: teams × spaces grant matrix",
	"POST /api/v1/orgs/{orgID}/grants/bulk-preview":         "org-admin-404: diff only, writes nothing",
	"POST /api/v1/orgs/{orgID}/grants/bulk-apply":           "org-admin-404: one transaction, one batch_id, one audit batch",
	"GET /api/v1/orgs/{orgID}/audit-log/":                   "org-admin-404: append-only viewer, batches collapsed",
	"GET /api/v1/orgs/{orgID}/audit-log/batches/{batchID}":  "org-admin-404: batch expansion",
	"GET /api/v1/orgs/{orgID}/members/search":               "org-member: person picker over active members",

	// Workflows: members read, admins mutate.
	"GET /api/v1/orgs/{orgID}/workflows/":                                           "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/":                                          "org-admin",
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}":                               "org-member",
	"PUT /api/v1/orgs/{orgID}/workflows/{workflowID}":                               "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}":                            "org-admin",
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}/states":                        "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/{workflowID}/states":                       "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}/states/{stateID}":           "org-admin",
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions":                   "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions":                  "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}": "org-admin",

	// Space directory and governance. The directory filters against the
	// resolved readable set in the handler (readable + discoverable-locked
	// rows); creation checks org-admin-or-lead; the rest is capability
	// checked (manage_space).
	"GET /api/v1/orgs/{orgID}/spaces/":                              "org-member: directory, filtered against the readable set in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/":                             "org-member: authority checked in-handler (org admin or lead of owning team); accepts initial visibility WITHOUT set_visibility — pre-existing carve-out, flagged for the maintainer",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/":                    "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/":                    "space-cap: manage_space; visibility changes additionally set_visibility (org admin only)",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/":                 "space-cap: manage_space",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/summary":             "space-cap: manage_space — delete-confirmation contents counts",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/members":             "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/members":            "space-cap: manage_space",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/members/{userID}": "space-cap: manage_space",

	// Grants and effective-access (manage_grants in-handler; self-inspection
	// allowed on effective-access).
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/grants/":             "space-cap: manage_grants",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/grants/":            "space-cap: manage_grants",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/grants/{grantID}":  "space-cap: manage_grants",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/grants/{grantID}": "space-cap: manage_grants",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/effective-access":    "space-read: self always; other users need manage_grants (in-handler)",

	// Tickets.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/":                           "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/":                          "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/search":                     "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/kanban":                     "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}":                 "space-read",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}":               "space-write: edit_own/edit_any in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}":              "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/status":         "space-write: transition_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/assign":         "space-write: edit_any_item in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/assign":       "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/workflow-state": "space-write: transition_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/comments":        "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/comments":       "space-write: comment capability",

	// Wiki.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/":                             "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/":                            "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/tree":                         "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/search":                       "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/shares":                       "space-read: active page shares in the space, for ShareBadge annotation",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}":                     "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}":                     "space-write: edit_own/edit_any in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}":                  "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/move":               "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/revisions":           "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/revisions/{version}": "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/diff":                "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/render":              "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/lock":                "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/lock":               "space-write",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/lock":             "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments":            "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments":           "space-write: comment capability",

	// Projects.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items":                          "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items":                         "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/search":                   "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/resolve":                  "space-read",
	"GET /api/v1/orgs/{orgID}/item-types/":                                              "org-read: members read for pickers/filters",
	"POST /api/v1/orgs/{orgID}/item-types/":                                             "org-admin: orgAdminGuard",
	"PATCH /api/v1/orgs/{orgID}/item-types/{typeID}":                                    "org-admin: orgAdminGuard",
	"DELETE /api/v1/orgs/{orgID}/item-types/{typeID}":                                   "org-admin: orgAdminGuard",
	"GET /api/v1/orgs/{orgID}/custom-fields/":                                           "org-read: members read definitions for item forms",
	"POST /api/v1/orgs/{orgID}/custom-fields/":                                          "org-admin: orgAdminGuard",
	"PATCH /api/v1/orgs/{orgID}/custom-fields/{fieldID}":                                "org-admin: orgAdminGuard",
	"DELETE /api/v1/orgs/{orgID}/custom-fields/{fieldID}":                               "org-admin: orgAdminGuard",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields":          "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields/{slug}":   "space-write: edit_own/edit_any in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":                 "space-read",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":               "space-write: edit_own/edit_any in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":              "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/status":         "space-write: transition_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/sprint":         "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/rank":           "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/workflow-state": "space-write: transition_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations":       "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations":      "space-write",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/relations/{relationID}":      "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments":        "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments":       "space-write: comment capability",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints":                        "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints":                       "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/active":                 "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}":             "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}":             "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/start":      "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/complete":   "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/items":       "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog":                        "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-sprint":        "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-backlog":       "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap":                        "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap/overdue":                "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap/sprints":                "space-read",

	// Space workflow reads.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/":       "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/states": "space-read",

	// Entity shares — management (P3, ADR-0008). Org-scoped; the handler
	// resolves the shared entity's space and enforces manage_shares there.
	"GET /api/v1/orgs/{orgID}/shares/":             "share-manage: list an entity's shares + cascade page count; manage_shares on the entity's space in-handler",
	"POST /api/v1/orgs/{orgID}/shares/":            "share-manage: create a share; manage_shares on the entity's space in-handler",
	"DELETE /api/v1/orgs/{orgID}/shares/{shareID}": "share-manage: revoke a share; manage_shares on the entity's space in-handler",

	// Entity shares — the standalone read family (P3, ADR-0008). Authorised
	// by share coverage alone, never by space access — the single most
	// dangerous route in the application, guarded by ResolveShares +
	// CoversForCaller.
	"GET /api/v1/orgs/{orgID}/shared/{entityType}/{entityID}":                            "share-read: the container-stripped shared-entity read route",
	"GET /api/v1/orgs/{orgID}/shared/{entityType}/{entityID}/attachments":                "share-read: list a shared entity's attachments",
	"GET /api/v1/orgs/{orgID}/shared/{entityType}/{entityID}/attachments/{attachmentID}": "share-read: stream a shared entity's attachment (object key from the row, entity-bound)",

	// Attachments — space-scoped (P3). Uploads/deletes gated by the write
	// floor; reads by space-readability. Every route re-checks the
	// attachment's entity lives in the URL space.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/attachments/":                  "space-read: list an entity's attachments",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/attachments/":                 "space-write: upload an attachment to an entity in the space",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/attachments/{attachmentID}":    "space-read: stream an attachment (entity re-verified to be in the space)",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/attachments/{attachmentID}": "space-write: soft-delete an attachment",

	// The move-confirmation warning count (ADR-0008 rule 9), served by the
	// API so the UI never counts client-side.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/share-impact": "space-read: active-share count a cross-space move would revoke",
}

// TestReadPathSweep_EveryRouteAccounted walks the fully wired router and
// fails on any route missing from the accounting table above, and on any
// table row whose route no longer exists.
func TestReadPathSweep_EveryRouteAccounted(t *testing.T) {
	ts := newTestServer(t)

	found := map[string]bool{}
	walker := func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi mounts leave "/*" artifacts on subtree roots; normalise.
		route = strings.ReplaceAll(route, "/*/", "/")
		route = strings.TrimSuffix(route, "*")
		found[method+" "+route] = true
		return nil
	}

	mux, ok := ts.Handler.(chi.Routes)
	require.True(t, ok, "router must expose chi.Routes for enumeration")
	require.NoError(t, chi.Walk(mux, walker))

	var unaccounted, stale []string
	for route := range found {
		if _, ok := routeAccounting[route]; !ok {
			unaccounted = append(unaccounted, route)
		}
	}
	for route := range routeAccounting {
		if !found[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(unaccounted)
	sort.Strings(stale)

	if len(unaccounted) > 0 {
		t.Errorf("routes present in the router but missing from the accounting table "+
			"(classify each before shipping):\n%s", strings.Join(unaccounted, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("accounting rows with no matching route (stale table):\n%s", strings.Join(stale, "\n"))
	}
	require.GreaterOrEqual(t, len(found), 90, "route walk looks implausibly small — enumeration broken?")
}
