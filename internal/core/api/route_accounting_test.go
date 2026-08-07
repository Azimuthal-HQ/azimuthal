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
	"GET /api/docs/init.js":      "public: the documentation UI's own initialiser (moved out of the page so the CSP can keep script-src bare)",
	"GET /api/docs/openapi.yaml": "public: committed OpenAPI spec",
	"GET /api/docs/assets/":      "public: vendored Swagger UI assets (static, embedded)",
	"POST /api/v1/auth/login":    "public: credential exchange",
	"POST /api/v1/auth/register": "public: account creation — 404 unless allow_registration (default off since P2.5)",
	"POST /api/v1/auth/refresh":  "public: token refresh (validates refresh token + live account state)",

	// Invite acceptance: possession of the raw crypto/rand token is the
	// credential, exactly like a password-reset link.
	"GET /api/v1/invites/{token}": "public: invite inspection, token-authenticated",
	"POST /api/v1/invites/accept": "public: invite acceptance, token-authenticated",

	// Customer-portal configuration, AGENT side. Ordinary space-scoped routes
	// with the capability enforced in the handler — the opposite of the
	// requester routes below in every way that matters.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/portal/":   "space-cap: manage_space in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/portal/":  "space-cap: manage_space in-handler",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/portal/": "space-cap: manage_space in-handler",

	// Customer portal, unauthenticated half. An external requester holds no
	// account by design (migration 044), so these three cannot require one.
	// Possession of the emailed magic-link token is the credential, exactly
	// as it is for invite acceptance above.
	"GET /api/v1/portal/{portalKey}":                    "public: portal name and blurb only — no space, org or request data",
	"POST /api/v1/portal/{portalKey}/auth/request-link": "public: issues a sign-in link; answers 202 identically for known and unknown addresses so it is not a membership oracle",
	"POST /api/v1/portal/auth/redeem":                   "public: magic-link redemption, token-authenticated",

	// Customer portal, requester-authenticated half. NOT org-member: the
	// caller is outside the capability model entirely, so access.Can is never
	// consulted. Authorisation is RequirePortalSession (audience-verified
	// token + live session_generation) plus queries scoped to the requester's
	// own rows.
	"POST /api/v1/portal/{portalKey}/my/requests":                     "portal-session: raise a request as the signed-in requester",
	"GET /api/v1/portal/{portalKey}/my/requests":                      "portal-session: the caller's OWN requests, scoped by requester_id in the query",
	"GET /api/v1/portal/{portalKey}/my/requests/{requestID}":          "portal-session: one of the caller's own requests; another requester's answers 404, never 403",
	"POST /api/v1/portal/{portalKey}/my/requests/{requestID}/replies": "portal-session: appends a PUBLIC comment; ownership re-checked before the write",
	"POST /api/v1/portal/{portalKey}/my/auth/sign-out":                "portal-session: bumps session_generation, revoking every session this requester holds",

	// User-scoped: authenticated, filtered by caller identity, no org data
	// beyond the caller's own memberships.
	//
	// /logout was in the public block above, as "public: session teardown
	// (validates session)". Both halves of that row were wrong. It was mounted
	// by AuthHandler.Routes(), outside the RequireAuth group, and OptionalAuth
	// is mounted nowhere — so nothing put claims on the context at that path,
	// it validated nothing, and the handler's nil-claims branch answered 401 to
	// every caller including a valid one. It now sits inside RequireAuth beside
	// /me. TestAuthLogoutIsAuthenticated asserts both directions through the
	// wired router, and the sweep below now checks this row against the real
	// middleware chain — it did not when the defect shipped, which is why the
	// row could say `public` about an authenticated route with every gate green.
	"POST /api/v1/auth/logout":                         "user-scoped: revokes the caller's own token generation and sessions",
	"GET /api/v1/auth/me":                              "user-scoped",
	"PATCH /api/v1/auth/me":                            "user-scoped",
	"PUT /api/v1/auth/me/avatar":                       "user-scoped: self avatar upload",
	"GET /api/v1/notifications/":                       "user-scoped",
	"POST /api/v1/notifications/read-all":              "user-scoped",
	"POST /api/v1/notifications/{notificationID}/read": "user-scoped",

	// Org-scoped reads (membership required; 404 for non-members).
	"GET /api/v1/orgs/{orgID}/":                    "org-member",
	"PATCH /api/v1/orgs/{orgID}/":                  "org-admin",
	"GET /api/v1/orgs/{orgID}/config":              "org-member: boot-time deployment flags on an explicit code allowlist, never secrets or connection strings; the orgID authorises the read, it does not scope the values (they are process-wide)",
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
	"GET /api/v1/orgs/{orgID}/pages/suggest":                "org-member: page-picker typeahead (A4), filtered to the caller's resolved readable spaces in-handler",

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

	// ADR-0011 tier configuration. Reads are org-member because knowing WHY a
	// transition is restricted helps the person it restricts; every mutation is
	// org-admin, because a workflow is an org object shared by every space bound
	// to it, so editing one edits other spaces' rules. Each of these also
	// re-scopes {workflowID} to {orgID} in the handler, which the pre-existing
	// workflow routes do not — see the finding in the PR body.
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards":                             "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards":                            "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards/{guardID}":                "org-admin",
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions":                     "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions":                    "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions/{postFunctionID}": "org-admin",
	"GET /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers":                          "org-member",
	"POST /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers":                         "org-admin",
	"DELETE /api/v1/orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers/{approverID}":          "org-admin",

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
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/fields":          "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/fields/{slug}":   "space-write: edit_own/edit_any in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/relations":       "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/relations":      "space-write",

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
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments":            "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments":           "space-write: comment capability",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/relations":           "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/relations":          "space-write",

	// The Codex document surface (issue #15, ADR-0012). Reading a page's document
	// is a read of the page, so space-read is the whole guard — the write floor
	// lets GET through untouched. Everything that writes carries the same
	// in-handler edit_own/edit_any check as the markdown save path: drafting and
	// publishing are the same permission as editing, and this phase adds no
	// capability.
	//
	// The author scoping on the two reads is a separate property from the guard
	// class. A draft is visible only to the person who wrote it because the query
	// is keyed on the caller, not because a wider result is filtered afterwards;
	// the integration tests assert that directly.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/drafts":            "space-read: the caller's own drafts in this space, author-scoped in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/document": "space-read: includes the caller's own draft, author-scoped in-handler",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/draft":    "space-write: edit_own/edit_any in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/draft": "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/publish": "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/images":  "space-write: edit_own/edit_any in-handler; the entity comes from the URL, never a form field",

	// Revision restore, and page-level tags (this phase, migration 040).
	//
	// Restore is space-write for the plainest reason there is: it republishes
	// through the same publish path, so it is the same permission as any other
	// edit, and it inherits the version guard and the lost-content refusal with it.
	//
	// Tags divide the way everything else on a page does — reading a page's tags
	// is reading the page, and setting them is editing it. Note that the org-level
	// tag routes are a different guard class (org-read) because a tag is org-scoped
	// and its NAME is not a space's secret; the pages carrying it are filtered
	// separately against the caller's readable set.
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/revisions/{version}/restore": "space-write: edit_own/edit_any in-handler; republishes through the ordinary publish path and gets no exemption from its refusals",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/tags":                         "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/tags":                         "space-write: edit_own/edit_any in-handler",

	// Projects.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items":         "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items":        "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/search":  "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/resolve": "space-read",
	"GET /api/v1/orgs/{orgID}/item-types/":                             "org-read: members read for pickers/filters",
	"POST /api/v1/orgs/{orgID}/item-types/":                            "org-admin: orgAdminGuard",
	"PATCH /api/v1/orgs/{orgID}/item-types/{typeID}":                   "org-admin: orgAdminGuard",
	"DELETE /api/v1/orgs/{orgID}/item-types/{typeID}":                  "org-admin: orgAdminGuard",
	// Codex tags, org level (migration 040). Read-only: there is no tag
	// administration surface in this phase — tags are created by use, on the
	// space-scoped page routes where the page's own edit permission applies.
	// The pages route is cross-space, so it filters against the caller's
	// resolved readable set in-handler (ADR-0010).
	"GET /api/v1/orgs/{orgID}/tags/":                      "org-read: members read the tag list for the autocomplete; a tag name is not a space's secret",
	"GET /api/v1/orgs/{orgID}/tags/{slug}/pages":          "org-read: cross-space, filtered to the caller's readable spaces in-handler (ADR-0010)",
	"GET /api/v1/orgs/{orgID}/custom-fields/":             "org-read: members read definitions for item forms",
	"POST /api/v1/orgs/{orgID}/custom-fields/":            "org-admin: orgAdminGuard",
	"PATCH /api/v1/orgs/{orgID}/custom-fields/{fieldID}":  "org-admin: orgAdminGuard",
	"DELETE /api/v1/orgs/{orgID}/custom-fields/{fieldID}": "org-admin: orgAdminGuard",
	// Scopes are org-admin in BOTH directions, the read included: a scope row
	// names a space id, so listing them to any member would disclose which
	// private spaces a field is attached to. Forms never read raw scopes —
	// they read the composed per-entity render on the space-scoped routes.
	"GET /api/v1/orgs/{orgID}/custom-fields/{fieldID}/scopes":                           "org-admin: orgAdminGuard",
	"PUT /api/v1/orgs/{orgID}/custom-fields/{fieldID}/scopes/{spaceID}/{entityType}":    "org-admin: orgAdminGuard",
	"DELETE /api/v1/orgs/{orgID}/custom-fields/{fieldID}/scopes/{spaceID}/{entityType}": "org-admin: orgAdminGuard",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields":          "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields/{slug}":   "space-write: edit_own/edit_any in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":                 "space-read",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":               "space-write: edit_own/edit_any in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}":              "space-write: edit_own/edit_any in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/status":         "space-write: transition_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/sprint":         "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/rank":           "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/workflow-state": "space-write: transition_any_item in-handler",
	// Relations are one entity-generic satellite mounted per subtree (A4);
	// the ticket and page rows are below with their own subtrees. One delete
	// serves all three mounts — a relation is addressed by its own id.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations":     "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations":    "space-write",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/relations/{relationID}":    "space-write",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments":      "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments":     "space-write: comment capability",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints":                      "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints":                     "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/active":               "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}":           "space-read",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}":           "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/start":    "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/complete": "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/items":     "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog":                      "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-sprint":      "space-write: edit_any_item in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-backlog":     "space-write: edit_any_item in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap":                      "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap/overdue":              "space-read",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/projects/roadmap/sprints":              "space-read",

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

	// ADR-0011 approvals. The pending list is space-read: an item blocked on an
	// approval must be visible to the person it blocks, not only to approvers.
	// The decision is space-write, and authority above the write floor is NOT a
	// capability — the handler requires the caller to be a configured approver,
	// directly or through an ADR-0007 effective team (workflow.CanDecide).
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/approvals":                      "space-read",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/approvals/{approvalID}/decide": "space-write: configured approver in-handler",
	// One item's approval history, pending and decided alike (P-W PR-B).
	// space-read, the same class as the pending list beside it and for the same
	// reason. It exists separately because a DECIDED approval leaves the pending
	// set: the moment an approver declines, the request and its reason vanish
	// from /approvals, and a detail surface built only on that list would show
	// the requester a blocked item and then nothing at all.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/approvals": "space-read",

	// The transitions this entity may be offered, with ADR-0011 conditions
	// applied. space-read even though the subtree carries the write floor:
	// RequireWriteFloor returns early for GET before it parses {spaceID}.
	//
	// It is the route that makes a condition mean anything — the filter existed
	// with no HTTP caller, so a configured condition hid a transition from
	// nobody — and it is deliberately NOT built on the gate, which WRITES the
	// pending approval row and notifies its approvers. A picker built on that
	// would file an approval request every time a page loaded.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/transitions": "space-read",

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

	// Saved views (P4, ADR-0009/ADR-0010). Org-scoped: a view spans
	// containers, so there is no {spaceID} to scope it to.
	//
	// Every route is org-member rather than capability-gated, deliberately.
	// Creating a private view reads nothing the caller could not already
	// read, so gating it would mean a capability every role holds; who may
	// SEE or CHANGE a view is decided by its own ownership and visibility,
	// in-handler. An edit attempt on a view the caller cannot even see
	// answers 404, not 403, so the surface does not confirm that somebody
	// else's private view exists.
	//
	// The two result routes additionally carry ResolveShares — a saved view
	// is the sanctioned ADR-0008 exception and unions the caller's shared
	// entities into its results. They are still org-member: the share
	// coverage widens what the view returns, it does not authorise the route.
	"GET /api/v1/orgs/{orgID}/views/":                 "org-member: list own + shared-with-me views; audience matching in-handler",
	"POST /api/v1/orgs/{orgID}/views/":                "org-member: create a saved view owned by the caller",
	"POST /api/v1/orgs/{orgID}/views/preview":         "org-member: resolve an unsaved query for the filter builder; +ResolveShares, results filtered per viewer",
	"GET /api/v1/orgs/{orgID}/views/{viewID}":         "org-member: read one view; 404 unless owned or shared to the caller",
	"PATCH /api/v1/orgs/{orgID}/views/{viewID}":       "org-member: update a view; owner-only in-handler (org admin bypasses), 404 when not visible",
	"DELETE /api/v1/orgs/{orgID}/views/{viewID}":      "org-member: delete a view; owner-only in-handler (org admin bypasses), 404 when not visible",
	"GET /api/v1/orgs/{orgID}/views/{viewID}/results": "org-member: run a view; +ResolveShares, results resolve against the CALLER's readable spaces unioned with their shares",
	"POST /api/v1/orgs/{orgID}/views/aggregate":       "org-member: count or group an unsaved query in SQL; +ResolveShares, counted over the CALLER's own readable set so two people legitimately see different totals",

	// Cross-module search (P6, spec §5/§7; ADR-0009/ADR-0010). Org-scoped for
	// the same reason saved views are: a search spans containers by
	// definition, so there is no {spaceID} to scope it to and
	// RequireSpaceReadable has nothing to check.
	//
	// Org-member rather than capability-gated: searching reads nothing the
	// caller could not already read one entity at a time, so a gate here would
	// be a capability every role holds. What bounds the answer is the
	// per-viewer access set applied INSIDE each of the three fan-out queries —
	// ADR-0010's rule for every cross-space read path.
	//
	// It carries ResolveShares, unlike the dashboard family: search reads
	// pages, tickets and project items directly, so a share is the only way an
	// entity outside the caller's readable spaces can legitimately appear. For
	// pages that includes cascade SUBTREES, whose (space, pattern) pairing is
	// the D46 accessor. As with the view result routes, share coverage widens
	// what is returned; it does not authorise the route.
	//
	// A hit reached ONLY through a share carries no container identity — no
	// space id, key or name — because matrix case 16 forbids a share-only read
	// from disclosing the space it lives in, and a search hit is still a read.
	"GET /api/v1/orgs/{orgID}/search/": "org-member: cross-module search; +ResolveShares, results filtered per viewer (readable spaces + direct shares + cascade subtrees) and share-only hits are container-stripped",

	// Dashboards (P5, ADR-0009). Org-scoped for the same reason saved views
	// are: a dashboard arranges gadgets that cross containers, so it has no
	// {spaceID} to hang off (ADR-0010). Who may see or change one is decided
	// by its own ownership and visibility — the same views.Audience rule the
	// saved-view family applies — rather than by a space capability.
	//
	// NO ResolveShares anywhere in this family, deliberately. Not one route
	// here reads a ticket or an item: the response hands the client the query
	// each gadget should run, and the client resolves it through
	// /views/preview and /views/aggregate, which carry the share resolver
	// themselves. Mounting it here would make every dashboard read pay for a
	// query no route in the family uses.
	//
	// NO new capability either. access.CapReadAggregates is still uncalled: an
	// aggregate resolves the identical readable set a results page does, so a
	// gate there would refuse somebody who could get the same number by paging.
	// See the package comment on internal/core/api/dashboards.
	"GET /api/v1/orgs/{orgID}/dashboards/":                      "org-member: list own + shared-with-me dashboards; audience matching in-handler",
	"POST /api/v1/orgs/{orgID}/dashboards/":                     "org-member: create a dashboard owned by the caller",
	"GET /api/v1/orgs/{orgID}/dashboards/home":                  "org-member: the caller's own Home dashboard, seeding a starter layout on first visit; idempotent by the dashboards_one_default index",
	"GET /api/v1/orgs/{orgID}/dashboards/{dashboardID}":         "org-member: read one dashboard with its gadgets; 404 unless owned or shared to the caller; every gadget resolved per viewer",
	"PATCH /api/v1/orgs/{orgID}/dashboards/{dashboardID}":       "org-member: update a dashboard; owner-only in-handler (org admin bypasses), 404 when not visible",
	"DELETE /api/v1/orgs/{orgID}/dashboards/{dashboardID}":      "org-member: delete a dashboard; owner-only in-handler (org admin bypasses), 404 when not visible",
	"PUT /api/v1/orgs/{orgID}/dashboards/{dashboardID}/gadgets": "org-member: replace the whole gadget collection in one transaction; owner-only in-handler, unknown gadget keys refused",

	// Beacon queues (P4 PR-B, ADR-0009). A queue is a saved view bound to a
	// space, so reading one needs only space-readability — which IS a queue's
	// audience (visibility 'space'). Every mutation is refined above the write
	// floor by an in-handler CapManageQueue check, which is where that
	// capability finally lands; ADR-0007 puts it at the agent role, so the
	// persona a gate test must refuse is a CONTRIBUTOR, not a viewer.
	//
	// The results route additionally carries ResolveShares, for the same
	// reason the saved-view results route does.
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/":                  "space-read: a space's queues in display order",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/":                 "space-write: create a queue; manage_queue in-handler",
	"POST /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/defaults":         "space-write: create the missing default queues, idempotent; manage_queue in-handler",
	"PUT /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/order":             "space-write: reorder in one transaction; manage_queue in-handler",
	"PATCH /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/{queueID}":       "space-write: edit a queue; manage_queue in-handler",
	"DELETE /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/{queueID}":      "space-write: delete a queue; manage_queue in-handler",
	"GET /api/v1/orgs/{orgID}/spaces/{spaceID}/queues/{queueID}/results": "space-read: run a queue; +ResolveShares, resolved per viewer so me-token queues mean each agent",

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
	"share-read": true, "portal-session": true,
}

// portalGuardedPrefixes is the customer-portal analogue of
// adminGuardedPrefixes, and it exists for the reason the sweep's own doc
// comment gives: adding a token to guardClasses only stops the "not a
// documented class" error. Without a prefix rule AND a carries() check, a
// portal route added outside the guarded closure would carry no
// authentication at all, its accounting row would happily claim
// "portal-session", and every test in the repository would pass.
//
// The portal is the surface where that matters most: it is the only route
// family reachable from the public internet by someone with no account, so an
// unguarded route here is not a privilege escalation between colleagues, it is
// an open door.
var portalGuardedPrefixes = []string{
	"/api/v1/portal/{portalKey}/my/",
}

// deliberatePublicPortalRoutes are portal routes that are unauthenticated on
// purpose. Each needs a stated reason, so that "this one is public" is an
// explicit act rather than an omission — the same discipline
// deliberateNonAdminRoutes enforces inside the admin subtree.
//
// All three are outside the /my/ prefix above, so this map is currently
// empty and should stay that way: a new public portal route belongs outside
// /my/, not inside it with an exemption.
var deliberatePublicPortalRoutes = map[string]string{}

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

// carriesMethodValue is carries() for a middleware supplied as a METHOD VALUE
// rather than a package-level function.
//
// `r.Use(cfg.Authenticator.RequireAuth)` compiles to a frame named
// "….auth.(*Authenticator).RequireAuth-fm" — no trailing dot — so carries()
// cannot see it and reports every authenticated route in the product as
// missing its guard. That is not a hypothetical: it is what happened the first
// time the check below was written, and it looks exactly like a catastrophic
// finding rather than a broken matcher.
//
// carries() is deliberately NOT loosened to cover this. Its trailing dot is
// what keeps "RequireOrgAdmin" from matching every "RequireOrgAdmin404" frame,
// and those two classes must stay distinguishable.
func carriesMethodValue(chain []string, guard string) bool {
	for _, name := range chain {
		if strings.Contains(name, "."+guard+".") ||
			strings.Contains(name, "."+guard+"-fm") ||
			strings.HasSuffix(name, "."+guard) {
			return true
		}
	}
	return false
}

// unauthenticatedClasses are the two guard classes that legitimately reach a
// handler with no internal session. Everything else must sit behind
// RequireAuth — see the RequireAuth check in
// TestReadPathSweep_GuardClassMatchesMiddleware.
//
// portal-session is authenticated, just not by RequireAuth: an external
// requester holds no internal credential, so the portal subtree carries
// RequirePortalSession instead and is checked against that above.
var unauthenticatedClasses = map[string]bool{
	"public":         true,
	"portal-session": true,
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

		// RequireAuth, both directions. This is the check whose absence let
		// POST /api/v1/auth/logout sit outside the RequireAuth group with a
		// row calling it `public` and every gate green — the v0.4.1 trust
		// patch. The sweep already refused to take the admin and portal
		// classifications on trust; it took the authenticated/public
		// distinction on trust, which is the coarsest one there is.
		//
		// The failing direction that matters is a route classified as
		// anything but public that turns out not to be authenticated. The
		// other direction is worth having too: a row that says `public` on a
		// route sitting behind RequireAuth is a row nobody has reread since
		// the route moved.
		hasAuth := carriesMethodValue(chain, "RequireAuth")
		if !unauthenticatedClasses[class] && !hasAuth {
			mismatched = append(mismatched, route+
				": classified "+class+" but its middleware chain does not include RequireAuth")
		}
		if unauthenticatedClasses[class] && hasAuth && class != "portal-session" {
			mismatched = append(mismatched, route+
				": classified "+class+" but sits behind RequireAuth")
		}

		// The customer portal, both directions. This is the same shape as the
		// admin check below and exists for a sharper reason: the portal is
		// the only route family an unauthenticated stranger on the public
		// internet can reach, so a route that lost its guard is not a
		// privilege escalation between colleagues — it is an open door.
		hasPortalGuard := carries(chain, "RequirePortalSession")
		if class == "portal-session" && !hasPortalGuard {
			mismatched = append(mismatched, route+
				": claims portal-session but its middleware chain does not include RequirePortalSession")
		}
		if hasPortalGuard && class != "portal-session" {
			mismatched = append(mismatched, route+
				": carries RequirePortalSession but is classified "+class)
		}
		for _, prefix := range portalGuardedPrefixes {
			if !strings.HasPrefix(strings.SplitN(route, " ", 2)[1], prefix) {
				continue
			}
			if hasPortalGuard {
				break
			}
			if _, deliberate := deliberatePublicPortalRoutes[route]; deliberate {
				break
			}
			unguarded = append(unguarded, route+" (portal subtree, no RequirePortalSession)")
			break
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
		t.Errorf("routes inside a guarded subtree that do not carry that subtree's guard.\n"+
			"ADMINISTRATION route: since #64 these guards are applied per-route, so a new route\n"+
			"inherits nothing — add .With(admin404) in mountAdminSurface, or list it in\n"+
			"deliberateNonAdminRoutes with the reason it is deliberately member-visible.\n"+
			"CUSTOMER-PORTAL route: it must sit inside the r.Use(RequirePortalSession) closure in\n"+
			"NewRouter, or move outside /my/ and be listed in deliberatePublicPortalRoutes with its\n"+
			"reason. An unguarded portal route is reachable by anyone on the internet:\n%s",
			strings.Join(unguarded, "\n"))
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
