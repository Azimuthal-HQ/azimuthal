package api_test

import (
	"net/http"
	"reflect"
	"runtime"
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
	"GET /api/v1/orgs/{orgID}/tickets/suggest":              "org-member: ticket_ref typeahead, filtered to the caller's resolved readable spaces in-handler",

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
	"POST /api/v1/orgs/{orgID}/spaces/":                             "org-member: authority checked in-handler (org admin or lead of owning team); a non-default initial visibility additionally requires set_visibility, and every create writes space.created",
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

	// Board configuration (W4). Reading the board's shape follows ordinary
	// space read access; every write follows space admin through the existing
	// manage_space capability, checked in-handler. No new capability.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/board/config":                       "space-read: read_items in-handler",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/board/config":                       "space-write: manage_space in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/board/config/reset":                "space-write: manage_space in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/board/config/columns/{columnID}": "space-write: manage_space in-handler",

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

// guardClasses is the closed vocabulary of the classes documented above. A
// row whose class is not one of these fails the sweep, so "TODO", "unknown"
// or a blank classification cannot pass for an answer — the point of the
// table is that somebody decided, and a free-text field lets you skip
// deciding while still looking accounted-for.
var guardClasses = map[string]bool{
	"public": true, "user-scoped": true, "org-member": true, "org-read": true,
	"org-admin": true, "org-admin-404": true, "space-read": true,
	"space-write": true, "space-cap": true, "share-manage": true,
	"share-read": true,
}

// adminGuardedPrefixes are the subtrees of the P2.5 administration surface.
// Every route under them is org-admin-404 unless it appears in
// deliberateNonAdminRoutes with a reason.
//
// This exists because of #64. Before it, /users carried the guard at group
// level (r.Use), so a route added to the group inherited it and could not be
// forgotten. #64 moved the guard per-route (r.With) so the avatar read could
// be org-member — correct, but it means a new route added to that group with
// no .With(admin404) is now silently public to any org member, and the
// accounting table would happily accept a row claiming otherwise. The check
// below reads the actual middleware chain instead of the claim.
var adminGuardedPrefixes = []string{
	"/api/v1/orgs/{orgID}/users/",
	"/api/v1/orgs/{orgID}/invites/",
	"/api/v1/orgs/{orgID}/grants/",
	"/api/v1/orgs/{orgID}/audit-log/",
}

// deliberateNonAdminRoutes are the routes inside an admin-guarded subtree
// that are intentionally reachable by ordinary org members. Each needs a
// stated reason: adding a route here is the explicit act the check exists to
// force.
var deliberateNonAdminRoutes = map[string]string{
	"GET /api/v1/orgs/{orgID}/users/{userID}/avatar": "org-member by design (#64): avatars are shown org-wide and the object key is derived server-side",
}

// middlewareNames renders a route's resolved middleware chain as the fully
// qualified names of the functions that produced each closure — e.g.
// "…/api.NewRouter.func3.2.1.orgAdmin404Guard.RequireOrgAdmin404.2". That is
// enough to tell which guard a route actually carries, as opposed to which
// one its accounting row claims.
func middlewareNames(mws []func(http.Handler) http.Handler) []string {
	names := make([]string, 0, len(mws))
	for _, mw := range mws {
		names = append(names, runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name())
	}
	return names
}

// carries reports whether the chain contains the named guard constructor.
// The dots matter: without the trailing one, "RequireOrgAdmin" would also
// match every "RequireOrgAdmin404" frame and the two classes would be
// indistinguishable.
func carries(chain []string, guard string) bool {
	for _, name := range chain {
		if strings.Contains(name, "."+guard+".") {
			return true
		}
	}
	return false
}

// classOf returns the leading class token of an accounting value, so
// "org-admin-404: People directory" classifies as "org-admin-404".
func classOf(accounting string) string {
	if i := strings.IndexByte(accounting, ':'); i >= 0 {
		return strings.TrimSpace(accounting[:i])
	}
	return strings.TrimSpace(accounting)
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

// TestReadPathSweep_GuardClassMatchesMiddleware is the half of the sweep that
// a written table cannot do on its own.
//
// The table above records what each route's guard is *claimed* to be. This
// walks the router and reads what each route's middleware chain *is*, then
// fails when the two disagree. Three ways to fail:
//
//  1. A classification outside the documented vocabulary — you have to pick a
//     real class, not type a placeholder.
//  2. A route inside an admin-guarded subtree that does not actually carry
//     RequireOrgAdmin404, and is not on the deliberate-exception list. This is
//     the #64 hazard: per-route guards mean a new route inherits nothing.
//  3. A route whose chain carries an admin guard while its row claims a
//     weaker class, or vice versa — the claim and the code drifting apart.
//
// Deliberate-breakage check performed while writing this: adding
// `r.Get("/{userID}/sessions", cfg.AdminHandler.ListPeople)` to the /users
// group in mountAdminSurface without `.With(admin404)`, plus an
// "org-admin-404" row for it, passes the accounting test above and fails here
// with "claims org-admin-404 but its middleware chain does not include
// RequireOrgAdmin404". Both changes reverted.
func TestReadPathSweep_GuardClassMatchesMiddleware(t *testing.T) {
	ts := newTestServer(t)

	chains := map[string][]string{}
	walker := func(method string, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		route = strings.ReplaceAll(route, "/*/", "/")
		route = strings.TrimSuffix(route, "*")
		chains[method+" "+route] = middlewareNames(mws)
		return nil
	}
	mux, ok := ts.Handler.(chi.Routes)
	require.True(t, ok, "router must expose chi.Routes for enumeration")
	require.NoError(t, chi.Walk(mux, walker))

	// 1. Every classification is a real class.
	var badClass []string
	for route, accounting := range routeAccounting {
		if class := classOf(accounting); !guardClasses[class] {
			badClass = append(badClass, route+" -> "+class)
		}
	}
	sort.Strings(badClass)
	if len(badClass) > 0 {
		t.Errorf("accounting rows whose guard class is not one of the documented classes:\n%s",
			strings.Join(badClass, "\n"))
	}

	// 2 and 3. The claim matches the chain.
	var unguarded, mismatched []string
	for route, chain := range chains {
		accounting, accounted := routeAccounting[route]
		if !accounted {
			continue // the sweep above already fails on this
		}
		class := classOf(accounting)
		hasAdmin404 := carries(chain, "RequireOrgAdmin404")

		if class == "org-admin-404" && !hasAdmin404 {
			mismatched = append(mismatched, route+
				": claims org-admin-404 but its middleware chain does not include RequireOrgAdmin404")
		}
		if hasAdmin404 && class != "org-admin-404" {
			mismatched = append(mismatched, route+
				": carries RequireOrgAdmin404 but is classified "+class)
		}
		if class == "org-admin" && !carries(chain, "RequireOrgAdmin") {
			mismatched = append(mismatched, route+
				": claims org-admin but its middleware chain does not include RequireOrgAdmin")
		}

		// A route in an administration subtree is org-admin-404 unless it is
		// a declared exception. Reached through the chain, not the row, so a
		// wrong row cannot satisfy it.
		for _, prefix := range adminGuardedPrefixes {
			if !strings.HasPrefix(strings.SplitN(route, " ", 2)[1], prefix) {
				continue
			}
			if hasAdmin404 {
				break
			}
			if _, deliberate := deliberateNonAdminRoutes[route]; deliberate {
				break
			}
			unguarded = append(unguarded, route)
			break
		}
	}
	sort.Strings(unguarded)
	sort.Strings(mismatched)

	if len(unguarded) > 0 {
		t.Errorf("routes inside an administration subtree that carry no RequireOrgAdmin404.\n"+
			"Since #64 these guards are applied per-route, so a new route inherits nothing — add\n"+
			".With(admin404) in mountAdminSurface, or add the route to deliberateNonAdminRoutes\n"+
			"with the reason it is deliberately member-visible:\n%s", strings.Join(unguarded, "\n"))
	}
	if len(mismatched) > 0 {
		t.Errorf("routes whose accounting row disagrees with their actual middleware chain:\n%s",
			strings.Join(mismatched, "\n"))
	}

	// A stale exception is a rule nobody is being held to any more.
	for route := range deliberateNonAdminRoutes {
		if _, live := chains[route]; !live {
			t.Errorf("deliberateNonAdminRoutes names %q, which no longer exists — drop the exception", route)
		}
	}
}
