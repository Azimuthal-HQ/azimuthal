package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Negative-path coverage for the share surface (internal/core/api/shares)
// and the ticket surface (internal/core/api/tickets) — the refusals spec §2
// asks for by name: a malformed uuid (400), a body that will not decode
// (400), a resource that does not exist (404), a caller past the write floor
// but short of the capability (403), and validation refusals (400/409).
//
// Every case asserts the EXACT status AND the documented error envelope
// code, because the codes are what distinguish one refusal from another: a
// handler that answers 404 where it should answer 400 has turned a client
// bug into "the thing you named does not exist", and a handler that answers
// VALIDATION_ERROR where it should answer BAD_REQUEST has claimed to have
// understood a body it never decoded. "Not a 500" would assert neither.
//
// Two paths in this cluster are deliberately absent, because they cannot be
// reached through the router and a test for them would read as coverage of
// something that never runs:
//
//   - shares.orgIDFromURL's 400 branch. Every /orgs/{orgID}/... route sits
//     under ResolveAccess, which parses {orgID} first and answers the same
//     400 — the handler's own parse can never see a bad value.
//   - tickets.spaceIDFromURL's 400 branches, for the same reason:
//     RequireSpaceInOrg parses {spaceID} ahead of every ticket handler.

// --- fixture -----------------------------------------------------------

// shtNegFixture is one org with a space per module and the three personas
// these refusals need:
//
//	agent    — a grant on every fixture space. Past read, write, edit_any and
//	           transition; short of manage_shares. The persona a manage_shares
//	           gate must refuse, and the one that proves the gate is the
//	           handler's own rather than an upstream middleware's.
//	contrib  — past the create_items write floor, short of edit_any_item.
//	outsider — an org member with no grant at all: the spaces do not exist as
//	           far as they can tell, so their refusals must be 404, never 403.
type shtNegFixture struct {
	ts *testServer

	codexSpace  string
	beaconSpace string
	vectorSpace string

	agent    testutil.User
	agentTok string

	contrib    testutil.User
	contribTok string

	outsider    testutil.User
	outsiderTok string
}

func shtNegNewFixture(t *testing.T) *shtNegFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &shtNegFixture{ts: ts}
	f.codexSpace = createScopedSpace(t, ts, "Neg Codex", "neg-codex", "codex")
	f.beaconSpace = createScopedSpace(t, ts, "Neg Beacon", "neg-beacon", "beacon")
	f.vectorSpace = createScopedSpace(t, ts, "Neg Vector", "neg-vector", "vector")

	granted := func(role access.Role) (testutil.User, string) {
		u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		for _, space := range []string{f.codexSpace, f.beaconSpace, f.vectorSpace} {
			_, err := ts.GrantService.Create(context.Background(), ts.OrgID, uuid.MustParse(space),
				access.SubjectUser, u.ID, role, ts.UserID)
			require.NoError(t, err)
		}
		return u, ts.tokenFor(t, u.ID, u.Email)
	}
	f.agent, f.agentTok = granted(access.RoleAgent)
	f.contrib, f.contribTok = granted(access.RoleContributor)

	f.outsider = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	f.outsiderTok = ts.tokenFor(t, f.outsider.ID, f.outsider.Email)
	return f
}

func (f *shtNegFixture) sharesPath() string {
	return fmt.Sprintf("/api/v1/orgs/%s/shares", f.ts.OrgID)
}

func (f *shtNegFixture) sharedPath(entityType, entityID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/shared/%s/%s", f.ts.OrgID, entityType, entityID)
}

func (f *shtNegFixture) ticketsPath(suffix string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets%s", f.ts.OrgID, f.beaconSpace, suffix)
}

// shtNegRaw issues a request whose body is sent verbatim — the only way to
// present a body that will not decode, since every other helper marshals a
// Go value into valid JSON first.
func (f *shtNegFixture) shtNegRaw(t *testing.T, token, method, path, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path), strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return f.ts.do(t, req)
}

// shtNegCreatedID posts as the harness admin and returns the created id.
func (f *shtNegFixture) shtNegCreatedID(t *testing.T, path string, body map[string]interface{}) string {
	t.Helper()
	r := f.ts.post(t, path, body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create %s: %s", path, r.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))
	require.NotEmpty(t, created.ID)
	return created.ID
}

func (f *shtNegFixture) createPage(t *testing.T, title string) string {
	t.Helper()
	return f.shtNegCreatedID(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, f.codexSpace),
		map[string]interface{}{"title": title, "content": "body"})
}

func (f *shtNegFixture) createTicket(t *testing.T, title string) string {
	t.Helper()
	return f.shtNegCreatedID(t, f.ticketsPath(""),
		map[string]interface{}{"title": title, "priority": "medium"})
}

func (f *shtNegFixture) createItem(t *testing.T, title string) string {
	t.Helper()
	return f.shtNegCreatedID(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", f.ts.OrgID, f.vectorSpace),
		map[string]interface{}{"title": title, "kind": "task", "priority": "medium"})
}

func (f *shtNegFixture) createShare(t *testing.T, entityType, entityID string) string {
	t.Helper()
	return f.shtNegCreatedID(t, f.sharesPath(),
		map[string]interface{}{"entity_type": entityType, "entity_id": entityID, "audience": "org"})
}

// shtNegActiveShares counts the unrevoked shares on an entity — used to
// prove a refusal was total, not a status code painted over a mutation that
// had already happened.
func (f *shtNegFixture) shtNegActiveShares(t *testing.T, entityID string) int {
	t.Helper()
	var n int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM entity_shares WHERE entity_id = $1 AND revoked_at IS NULL`,
		uuid.MustParse(entityID)).Scan(&n))
	return n
}

// --- shares: List --------------------------------------------------------

// TestShareTicketNegSharesListRejectsMalformedEntityID: the list route's
// entity_id must parse, and a value that does not is a 400 VALIDATION_ERROR.
//
// Defect this catches: dropping List's uuid.Parse guard. The zero UUID would
// then flow into LookupEntity, which finds nothing and answers 404 — turning
// "you sent me a broken id" into "that entity does not exist", which is the
// one answer a client cannot act on. The assertion is on the code as much as
// the status, because both refusals are client errors and only the code
// distinguishes them.
func TestShareTicketNegSharesListRejectsMalformedEntityID(t *testing.T) {
	f := shtNegNewFixture(t)
	pageID := f.createPage(t, "List Guard Page")

	// Premise: the same route answers 200 for a well-formed id, so the 400s
	// below are the parse guard and not a broken route.
	ok := f.ts.get(t, f.sharesPath()+"?entity_type=page&entity_id="+pageID, true)
	require.Equal(t, http.StatusOK, ok.StatusCode, "premise: list works for a real page: %s", ok.Body)

	for _, raw := range []string{"not-a-uuid", "", "12345", pageID + "x"} {
		t.Run("entity_id="+raw, func(t *testing.T) {
			r := f.ts.get(t, f.sharesPath()+"?entity_type=page&entity_id="+raw, true)
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	// entity_id omitted entirely is the same refusal, not a listing of
	// everything.
	requireErrorCode(t, f.ts.get(t, f.sharesPath()+"?entity_type=page", true),
		http.StatusBadRequest, "VALIDATION_ERROR")
}

// TestShareTicketNegSharesListRejectsUnknownEntityType: only page, ticket
// and project_item are shareable, and anything else is a 400 before any
// lookup happens.
//
// Defect this catches: removing resolveManageable's ValidShareEntityType
// check. LookupEntity validates too, but it reports
// ErrInvalidShareEntityType, which the handler maps to 404 — so the removal
// would silently downgrade "that is not a shareable kind of thing" to "not
// found", and this test fails on the code and the status both.
func TestShareTicketNegSharesListRejectsUnknownEntityType(t *testing.T) {
	f := shtNegNewFixture(t)
	pageID := f.createPage(t, "Type Guard Page")

	for _, typ := range []string{"sasquatch", "Page", "PAGE", "space", "comment", ""} {
		t.Run("entity_type="+typ, func(t *testing.T) {
			r := f.ts.get(t, f.sharesPath()+"?entity_type="+typ+"&entity_id="+pageID, true)
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

// TestShareTicketNegSharesListUnknownEntityIsNotFound: a well-formed id
// naming nothing is a 404 with the envelope, for each shareable type.
//
// Defect this catches: dropping resolveManageable's
// errors.Is(ErrSharedEntityNotFound) arm. The error would fall through to
// the generic branch and answer 500 — an ordinary miss reported as a server
// fault, which is both wrong and noisy in production.
func TestShareTicketNegSharesListUnknownEntityIsNotFound(t *testing.T) {
	f := shtNegNewFixture(t)

	for _, typ := range []string{"page", "ticket", "project_item"} {
		t.Run(typ, func(t *testing.T) {
			r := f.ts.get(t, fmt.Sprintf("%s?entity_type=%s&entity_id=%s", f.sharesPath(), typ, uuid.NewString()), true)
			requireErrorCode(t, r, http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// TestShareTicketNegSharesManagementNeedsManageShares pins the read-then-
// manage split on BOTH management verbs, with the persona spec §2 requires:
// an agent, who holds every space capability below manage_shares. A viewer
// would prove nothing here — but neither would it be refused by anything
// else, since /shares carries no write floor, so the agent is what makes the
// 403 attributable to the in-handler access.Can check alone.
//
// Defect this catches: deleting the CapManageShares check in
// resolveManageable or authorizeShareManagement — an agent would then list,
// create and revoke shares on any space they can read, which is exactly the
// widening ADR-0008 forbids. The 404 half catches the opposite mistake:
// answering 403 to the outsider, which would confirm the entity exists to
// somebody who cannot see its space.
func TestShareTicketNegSharesManagementNeedsManageShares(t *testing.T) {
	f := shtNegNewFixture(t)
	pageID := f.createPage(t, "Managed Page")
	shareID := f.createShare(t, "page", pageID)
	listURL := f.sharesPath() + "?entity_type=page&entity_id=" + pageID
	createBody := map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	}

	// Premise: the agent CAN read the page's space through its own route, so
	// every refusal below is the capability and not an invisible space.
	readable := f.ts.getAs(t, f.agentTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.codexSpace, pageID))
	require.Equal(t, http.StatusOK, readable.StatusCode,
		"premise: the agent reads the page in-space: %s", readable.Body)

	// Readable space, no manage_shares → 403 on all three verbs.
	requireErrorCode(t, f.ts.getAs(t, f.agentTok, listURL), http.StatusForbidden, "FORBIDDEN")
	requireErrorCode(t, f.ts.postAs(t, f.agentTok, f.sharesPath(), createBody), http.StatusForbidden, "FORBIDDEN")
	requireErrorCode(t, f.ts.deleteAs(t, f.agentTok, f.sharesPath()+"/"+shareID), http.StatusForbidden, "FORBIDDEN")

	// Unreadable space → 404 on all three, never 403: existence never leaks.
	requireErrorCode(t, f.ts.getAs(t, f.outsiderTok, listURL), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, f.ts.postAs(t, f.outsiderTok, f.sharesPath(), createBody), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, f.ts.deleteAs(t, f.outsiderTok, f.sharesPath()+"/"+shareID), http.StatusNotFound, "NOT_FOUND")

	// Nothing was created and nothing was revoked: the refusals were total.
	require.Equal(t, 1, f.shtNegActiveShares(t, pageID),
		"exactly the one share the admin created — no refused create leaked through, no refused revoke landed")
}

// TestShareTicketNegSharesRevokeRejectsMalformedShareID: {shareID} must
// parse, and a value that does not is a 400 BAD_REQUEST.
//
// Defect this catches: dropping Revoke's uuid.Parse guard, which would send
// the zero UUID to ShareService.Get and answer 404 — a client bug reported
// as a missing share.
func TestShareTicketNegSharesRevokeRejectsMalformedShareID(t *testing.T) {
	f := shtNegNewFixture(t)

	for _, raw := range []string{"not-a-uuid", "0", "%20"} {
		t.Run("shareID="+raw, func(t *testing.T) {
			requireErrorCode(t, f.ts.delete(t, f.sharesPath()+"/"+raw, true),
				http.StatusBadRequest, "BAD_REQUEST")
		})
	}

	// A well-formed id naming no share is the other refusal — 404, not 400.
	requireErrorCode(t, f.ts.delete(t, f.sharesPath()+"/"+uuid.NewString(), true),
		http.StatusNotFound, "NOT_FOUND")
}

// TestShareTicketNegSharesListOnTicketOmitsCascadeCount: cascade is
// pages-only (ADR-0008), so the list of a ticket's shares must not carry a
// cascade_page_count at all — the dialog decides whether to offer cascade
// from the key's presence.
//
// Defect this catches: dropping List's `entityType == page` condition. The
// cascade preview would then run for every type, counting a page subtree
// under a ticket id, and the response would advertise a cascade option that
// ADR-0008 forbids and the database CHECK would refuse. The page half of the
// assertion is what stops this passing by counting nothing at all.
func TestShareTicketNegSharesListOnTicketOmitsCascadeCount(t *testing.T) {
	f := shtNegNewFixture(t)
	ticketID := f.createTicket(t, "Cascade Key Ticket")
	itemID := f.createItem(t, "Cascade Key Item")
	pageID := f.createPage(t, "Cascade Key Page")

	for _, tc := range []struct{ typ, id string }{{"ticket", ticketID}, {"project_item", itemID}} {
		t.Run(tc.typ, func(t *testing.T) {
			r := f.ts.get(t, fmt.Sprintf("%s?entity_type=%s&entity_id=%s", f.sharesPath(), tc.typ, tc.id), true)
			require.Equal(t, http.StatusOK, r.StatusCode, "list %s shares: %s", tc.typ, r.Body)
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(r.Body, &raw))
			require.Contains(t, raw, "shares")
			require.NotContains(t, raw, "cascade_page_count",
				"cascade is pages-only: a %s listing must not carry the key", tc.typ)
		})
	}

	// The page listing DOES carry it — otherwise the assertions above would
	// be satisfied by a handler that never emits the key for anything.
	r := f.ts.get(t, f.sharesPath()+"?entity_type=page&entity_id="+pageID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list page shares: %s", r.Body)
	var pageBody map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(r.Body, &pageBody))
	require.Contains(t, pageBody, "cascade_page_count", "a page listing must carry the cascade preview count")
}

// --- shares: Create expiry ----------------------------------------------

// TestShareTicketNegSharesExpiryMustBeInFuture: a share whose expiry has
// already passed is dead on arrival, and creating one is refused with 400
// VALIDATION_ERROR — no row is written.
//
// Defect this catches: dropping ShareService.Create's expiry check. The
// share would be created, occupy the (entity, audience) uniqueness cell so
// the real share could not be made, and grant nothing — a share that exists,
// blocks, and does not work. The paired future-expiry case is what proves
// the refusal is about the past rather than about expiry existing at all,
// and it is also the only path that exercises toShareResponse's expires_at
// branch: a non-nil expiry must ship as a UTC RFC3339 string alongside
// expired:false.
func TestShareTicketNegSharesExpiryMustBeInFuture(t *testing.T) {
	f := shtNegNewFixture(t)
	pageID := f.createPage(t, "Expiring Page")

	for name, at := range map[string]time.Time{
		"an_hour_ago": time.Now().Add(-time.Hour),
		"long_ago":    time.Now().Add(-72 * time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			r := f.ts.post(t, f.sharesPath(), map[string]interface{}{
				"entity_type": "page", "entity_id": pageID, "audience": "org",
				"expires_at": at.UTC().Format(time.RFC3339),
			}, true)
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
	require.Zero(t, f.shtNegActiveShares(t, pageID),
		"a refused expiry must leave no share row behind")

	// A future expiry is accepted and round-trips on the wire.
	want := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	r := f.ts.post(t, f.sharesPath(), map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
		"expires_at": want.Format(time.RFC3339),
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "future expiry must be accepted: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)

	var created struct {
		ID        string  `json:"id"`
		ExpiresAt *string `json:"expires_at"`
		Expired   bool    `json:"expired"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))
	require.NotNil(t, created.ExpiresAt, "a share with an expiry must ship expires_at")
	require.Equal(t, want.Format(time.RFC3339), *created.ExpiresAt,
		"expires_at is the requested instant, formatted UTC RFC3339")
	require.False(t, created.Expired, "a share expiring in two hours is not expired")

	// And the listing carries the same projection.
	lr := f.ts.get(t, f.sharesPath()+"?entity_type=page&entity_id="+pageID, true)
	require.Equal(t, http.StatusOK, lr.StatusCode, "list: %s", lr.Body)
	var listed struct {
		Shares []struct {
			ID        string  `json:"id"`
			ExpiresAt *string `json:"expires_at"`
			Expired   bool    `json:"expired"`
		} `json:"shares"`
	}
	require.NoError(t, json.Unmarshal(lr.Body, &listed))
	require.Len(t, listed.Shares, 1)
	require.Equal(t, created.ID, listed.Shares[0].ID)
	require.NotNil(t, listed.Shares[0].ExpiresAt, "the listing must carry the expiry too")
	require.Equal(t, want.Format(time.RFC3339), *listed.Shares[0].ExpiresAt)
	require.False(t, listed.Shares[0].Expired)
}

// --- shares: the standalone read route ----------------------------------

// TestShareTicketNegSharedReadAnswersOnlyTwoOutcomes pins the contract of
// the single most dangerous route in the application: /shared answers 200 or
// 404 and nothing else. Malformed ids, unknown entity types and well-formed
// ids naming nothing all take the identical 404 with the identical envelope.
//
// Defect this catches: any change that gives one of these its "own" status —
// 400 for a malformed id, 405 for an unknown type, 500 for a lookup miss.
// Each of those is an oracle: a caller who cannot see the entity learns from
// the status alone which of "you typed it wrong", "no such kind of thing"
// and "it exists but is not shared with you" they are looking at. The route
// exists precisely to hand out access without space access, so the only safe
// answer to everything it refuses is the same one.
func TestShareTicketNegSharedReadAnswersOnlyTwoOutcomes(t *testing.T) {
	f := shtNegNewFixture(t)
	pageID := f.createPage(t, "Two Outcomes Page")
	f.createShare(t, "page", pageID)

	// Premise: the route does answer 200 for the outsider once a share
	// covers the page — so every 404 below is a refusal, not a dead route.
	ok := f.ts.getAs(t, f.outsiderTok, f.sharedPath("page", pageID))
	require.Equal(t, http.StatusOK, ok.StatusCode, "premise: the org share grants the read: %s", ok.Body)

	cases := map[string]string{
		"malformed_entity_id":       f.sharedPath("page", "not-a-uuid"),
		"empty_looking_entity_id":   f.sharedPath("page", "0"),
		"unknown_entity_type":       f.sharedPath("sasquatch", pageID),
		"wrong_case_entity_type":    f.sharedPath("Page", pageID),
		"space_is_not_shareable":    f.sharedPath("space", f.codexSpace),
		"well_formed_id_naming_nil": f.sharedPath("page", uuid.NewString()),
		// The right id under the wrong type: the share is on a page, so the
		// ticket route must not find it.
		"right_id_wrong_type": f.sharedPath("ticket", pageID),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			requireErrorCode(t, f.ts.getAs(t, f.outsiderTok, path), http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// TestShareTicketNegSharedReadEntityRemovedUnderShare: when the target of an
// active share stops existing, the read route answers 404 with the envelope
// — for all three entity types.
//
// The state is reached by soft-deleting the row directly, NOT through the
// module's DELETE route: that route revokes the entity's shares in the same
// transaction (ADR-0008 rule 10), which is the invariant tested elsewhere.
// Bypassing it is the only way to produce the orphan a partial failure, a
// restore, or a future bulk operation could leave behind — and the handler
// has to survive it.
//
// Defect this catches: dropping ReadShared's
// errors.Is(ErrSharedEntityNotFound) arm, or any of the three matching arms
// in the service reader (readPage/readTicket/readItem). Every one of them
// would fall through to the generic branch and answer 500, so a stale share
// row would turn a page view into a server error rather than a clean "not
// found" — and 500 bodies are where internals leak.
func TestShareTicketNegSharedReadEntityRemovedUnderShare(t *testing.T) {
	f := shtNegNewFixture(t)
	ctx := context.Background()

	cases := []struct {
		typ   string
		table string
		id    string
	}{
		{"page", "pages", f.createPage(t, "Doomed Page")},
		{"ticket", "tickets", f.createTicket(t, "Doomed Ticket")},
		{"project_item", "project_items", f.createItem(t, "Doomed Item")},
	}

	for _, tc := range cases {
		f.createShare(t, tc.typ, tc.id)
		// Premise: the outsider reads it while it is live.
		r := f.ts.getAs(t, f.outsiderTok, f.sharedPath(tc.typ, tc.id))
		require.Equal(t, http.StatusOK, r.StatusCode, "premise: %s readable under its share: %s", tc.typ, r.Body)
	}

	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			// The table name is a constant from the case table above, never
			// caller input — the id is still bound as a parameter.
			tag, err := f.ts.DB.Pool.Exec(ctx,
				`UPDATE `+tc.table+` SET deleted_at = now() WHERE id = $1`, uuid.MustParse(tc.id))
			require.NoError(t, err)
			require.EqualValues(t, 1, tag.RowsAffected())

			// Premise: the share row is STILL active — the 404 below comes
			// from the missing entity, not from a revoked share.
			require.Equal(t, 1, f.shtNegActiveShares(t, tc.id),
				"the share on the removed %s is deliberately still active", tc.typ)

			requireErrorCode(t, f.ts.getAs(t, f.outsiderTok, f.sharedPath(tc.typ, tc.id)),
				http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// --- tickets: malformed ids ---------------------------------------------

// shtNegTicketVerb is one ticket route reached with a {ticketID} the test
// controls, so the same table drives both the malformed-id and the
// unknown-id sweeps.
type shtNegTicketVerb struct {
	name   string
	method string
	suffix func(id string) string
	body   map[string]interface{}
}

// shtNegTicketVerbs builds the table. assignee must be a real org member:
// the ONLY thing wrong with each request below is the {ticketID}, so an
// unresolvable assignee would give the handler a second reason to refuse and
// the test would stop proving which guard answered.
func shtNegTicketVerbs(assignee uuid.UUID) []shtNegTicketVerb {
	patch := map[string]interface{}{"title": "Edited", "description": "d", "priority": "medium"}
	return []shtNegTicketVerb{
		{"get", http.MethodGet, func(id string) string { return "/" + id }, nil},
		{"update", http.MethodPatch, func(id string) string { return "/" + id }, patch},
		{"delete", http.MethodDelete, func(id string) string { return "/" + id }, nil},
		{"transition", http.MethodPost, func(id string) string { return "/" + id + "/status" },
			map[string]interface{}{"status": "in_progress"}},
		{"assign", http.MethodPost, func(id string) string { return "/" + id + "/assign" },
			map[string]interface{}{"assignee_id": assignee.String()}},
		{"unassign", http.MethodDelete, func(id string) string { return "/" + id + "/assign" }, nil},
	}
}

// TestShareTicketNegTicketsMalformedIDIsBadRequest: every ticket route that
// names a {ticketID} refuses an unparseable one with 400 BAD_REQUEST.
//
// Defect this catches: dropping any handler's ticketIDFromURL error branch.
// The zero UUID would reach the service, miss, and answer 404 — reporting a
// malformed request as a missing ticket, which sends the client looking for
// a row instead of at its own url construction. The status is shared with
// nothing else on these routes, and the code separates it from the
// VALIDATION_ERROR that a well-formed but unacceptable value earns.
func TestShareTicketNegTicketsMalformedIDIsBadRequest(t *testing.T) {
	f := shtNegNewFixture(t)
	live := f.createTicket(t, "Malformed ID Probe")

	for _, v := range shtNegTicketVerbs(f.agent.ID) {
		t.Run(v.name, func(t *testing.T) {
			// Premise: the same verb reaches the handler for a real ticket —
			// so the 400 is the id guard and not a missing route. (Only the
			// status matters here; the assign verbs are proven separately.)
			premise := f.ts.requestAs(t, f.ts.Token, v.method, f.ticketsPath(v.suffix(live)), v.body)
			require.NotEqual(t, http.StatusMethodNotAllowed, premise.StatusCode,
				"premise: %s %s is a registered route: %s", v.method, v.suffix(live), premise.Body)

			for _, bad := range []string{"not-a-uuid", "12345", "null"} {
				r := f.ts.requestAs(t, f.ts.Token, v.method, f.ticketsPath(v.suffix(bad)), v.body)
				requireErrorCode(t, r, http.StatusBadRequest, "BAD_REQUEST")
			}
		})
	}
}

// TestShareTicketNegTicketsUnknownIDIsNotFound: a well-formed {ticketID}
// naming no live ticket is a 404 NOT_FOUND on every verb.
//
// Defect this catches: dropping the tickets.ErrNotFound arm of
// handleTicketError. Every miss on every ticket route would become a 500 —
// an ordinary client mistake reported as a server fault, and the generic
// branch formats the underlying error into the body, so the regression also
// starts echoing internals to the caller.
func TestShareTicketNegTicketsUnknownIDIsNotFound(t *testing.T) {
	f := shtNegNewFixture(t)

	for _, v := range shtNegTicketVerbs(f.agent.ID) {
		t.Run(v.name, func(t *testing.T) {
			r := f.ts.requestAs(t, f.ts.Token, v.method, f.ticketsPath(v.suffix(uuid.NewString())), v.body)
			requireErrorCode(t, r, http.StatusNotFound, "NOT_FOUND")
		})
	}
}

// TestShareTicketNegTicketsUndecodableBodyIsBadRequest: a body that will not
// decode is 400 BAD_REQUEST — distinct from the 400 VALIDATION_ERROR a body
// that decoded but said something unacceptable earns.
//
// Defect this catches: dropping a handler's respond.DecodeJSON error check.
// The request would proceed on a zero-valued struct, and the failure would
// resurface downstream as a DIFFERENT refusal — an empty title becomes
// ErrTitleRequired (VALIDATION_ERROR), an empty status becomes
// ErrInvalidStatus (VALIDATION_ERROR), a nil assignee becomes a silent
// UNASSIGN. That last one is the dangerous member of the family: a
// garbled assign body would quietly clear the assignee and answer 200.
// Asserting the exact code, not just "a 400", is what separates these.
func TestShareTicketNegTicketsUndecodableBodyIsBadRequest(t *testing.T) {
	f := shtNegNewFixture(t)
	ticketID := f.createTicket(t, "Body Guard Ticket")

	bodies := map[string]string{
		"truncated_object": `{`,
		"not_json":         `nonsense`,
		"array_not_object": `[1,2,3]`,
		"empty_body":       ``,
		// respond.DecodeJSON sets DisallowUnknownFields, so a field the
		// struct does not carry is undecodable too — that is what stops a
		// typo'd field name being silently ignored.
		"unknown_field": `{"titel":"typo"}`,
	}
	routes := map[string]struct {
		method string
		path   string
	}{
		"update":     {http.MethodPatch, f.ticketsPath("/" + ticketID)},
		"transition": {http.MethodPost, f.ticketsPath("/" + ticketID + "/status")},
		"assign":     {http.MethodPost, f.ticketsPath("/" + ticketID + "/assign")},
	}

	for routeName, route := range routes {
		for bodyName, body := range bodies {
			t.Run(routeName+"/"+bodyName, func(t *testing.T) {
				r := f.shtNegRaw(t, f.ts.Token, route.method, route.path, body)
				requireErrorCode(t, r, http.StatusBadRequest, "BAD_REQUEST")
			})
		}
	}

	// The ticket is untouched by every refused request: still open, still
	// unassigned, still under its original title.
	var title, status string
	var assignee *uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT title, status, assignee_id FROM tickets WHERE id = $1`,
		uuid.MustParse(ticketID)).Scan(&title, &status, &assignee))
	require.Equal(t, "Body Guard Ticket", title)
	require.Equal(t, "open", status)
	require.Nil(t, assignee, "a refused assign body must never have cleared the assignee")
}

// TestShareTicketNegTicketsUnknownStatusIsValidationError: a status outside
// the four the state machine knows is 400 VALIDATION_ERROR — not the 409
// INVALID_TRANSITION that a known status in the wrong place earns.
//
// Defect this catches: dropping ValidateTransition's IsValid guard on the
// target status. CanTransitionTo would then simply not find the unknown
// value among the allowed targets and report ErrInvalidTransition, so the
// API would answer 409 — telling the client "you cannot go there from here"
// about a state that does not exist, which is advice they can only follow by
// trying every other state. The pair of assertions below fails in exactly
// that case and passes only when the two refusals stay distinct.
func TestShareTicketNegTicketsUnknownStatusIsValidationError(t *testing.T) {
	f := shtNegNewFixture(t)
	ticketID := f.createTicket(t, "Status Guard Ticket")
	statusPath := f.ticketsPath("/" + ticketID + "/status")

	for _, bad := range []string{"banana", "", "OPEN", "in-progress", "done"} {
		t.Run("status="+bad, func(t *testing.T) {
			r := f.ts.post(t, statusPath, map[string]interface{}{"status": bad}, true)
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	// A KNOWN status that the machine forbids from `open` is the other
	// refusal — 409, not 400. Without this the test above would also pass
	// against a handler that answered 400 to everything.
	requireErrorCode(t, f.ts.post(t, statusPath, map[string]interface{}{"status": "resolved"}, true),
		http.StatusConflict, "INVALID_TRANSITION")

	// And the ticket never moved.
	var status string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT status FROM tickets WHERE id = $1`, uuid.MustParse(ticketID)).Scan(&status))
	require.Equal(t, "open", status)
}

// TestShareTicketNegTicketsAssignmentNeedsEditAnyItem: assigning and
// unassigning are agent-tier (edit_any_item). A contributor is refused with
// 403 FORBIDDEN — on their OWN ticket, which is the case that matters.
//
// The persona is the point (spec §2): a contributor is past the create_items
// write floor, so the middleware lets them through and the 403 can only have
// come from the handler's own access.Can. A viewer would be refused upstream
// and prove nothing. Owning the ticket closes the other escape: edit_own_items
// is enough to edit and delete it, so the refusal is specifically about
// assignment authority rather than about the ticket being someone else's.
//
// Defect this catches: deleting either handler's CapEditAnyItem check. Any
// contributor could then hand work to anyone — including reassigning an
// agent's queue away from them — and the agent case below is what keeps this
// from passing against a handler that refuses assignment to everybody.
func TestShareTicketNegTicketsAssignmentNeedsEditAnyItem(t *testing.T) {
	f := shtNegNewFixture(t)

	// The contributor creates their own ticket through the real route: they
	// clear the write floor, which is the premise of everything below.
	cr := f.ts.postAs(t, f.contribTok, f.ticketsPath(""),
		map[string]interface{}{"title": "Contributor Own Ticket", "priority": "medium"})
	require.Equal(t, http.StatusCreated, cr.StatusCode, "premise: contributor clears the write floor: %s", cr.Body)
	var own struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(cr.Body, &own))

	assignPath := f.ticketsPath("/" + own.ID + "/assign")
	assignBody := map[string]interface{}{"assignee_id": f.contrib.ID.String()}

	// Premise: edit_own_items really does let them edit their own ticket, so
	// the assign refusal is not "contributors cannot touch this row".
	er := f.ts.patchAs(t, f.contribTok, f.ticketsPath("/"+own.ID),
		map[string]interface{}{"title": "Edited By Owner", "description": "d", "priority": "medium"})
	require.Equal(t, http.StatusOK, er.StatusCode, "premise: contributor edits their own ticket: %s", er.Body)

	requireAPIForbidden(t, f.ts.postAs(t, f.contribTok, assignPath, assignBody))
	requireAPIForbidden(t, f.ts.deleteAs(t, f.contribTok, assignPath))
	// The unassign-by-null-body spelling of the same route is refused too —
	// the capability check runs before the body is even read.
	requireAPIForbidden(t, f.ts.postAs(t, f.contribTok, assignPath,
		map[string]interface{}{"assignee_id": nil}))

	// Nothing was assigned.
	var assignee *uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT assignee_id FROM tickets WHERE id = $1`, uuid.MustParse(own.ID)).Scan(&assignee))
	require.Nil(t, assignee, "every refused assign must leave the ticket unassigned")

	// An agent holds edit_any_item and succeeds on the same route — without
	// this the 403s above would also pass against a broken route.
	ar := f.ts.postAs(t, f.agentTok, assignPath, map[string]interface{}{"assignee_id": f.agent.ID.String()})
	require.Equal(t, http.StatusOK, ar.StatusCode, "agent assigns: %s", ar.Body)
	require.Equal(t, http.StatusOK, f.ts.deleteAs(t, f.agentTok, assignPath).StatusCode, "agent unassigns")
}

// TestShareTicketNegTicketsReassignToSameUserConflicts: assigning a ticket
// to the person who already holds it is 409 CONFLICT.
//
// Defect this catches: dropping the tickets.ErrAlreadyAssigned arm of
// handleTicketError, which sends the case to the generic branch and answers
// 500 — a double-click on the assign button reported as a server fault. The
// following assignment to somebody else proves the conflict is about the
// no-op rather than about assignment being closed once made.
func TestShareTicketNegTicketsReassignToSameUserConflicts(t *testing.T) {
	f := shtNegNewFixture(t)
	ticketID := f.createTicket(t, "Reassign Probe")
	assignPath := f.ticketsPath("/" + ticketID + "/assign")

	first := f.ts.post(t, assignPath, map[string]interface{}{"assignee_id": f.agent.ID.String()}, true)
	require.Equal(t, http.StatusOK, first.StatusCode, "first assign: %s", first.Body)

	requireErrorCode(t, f.ts.post(t, assignPath, map[string]interface{}{"assignee_id": f.agent.ID.String()}, true),
		http.StatusConflict, "CONFLICT")

	// Reassigning to a DIFFERENT member is accepted — the 409 is the no-op,
	// not a lock.
	second := f.ts.post(t, assignPath, map[string]interface{}{"assignee_id": f.contrib.ID.String()}, true)
	require.Equal(t, http.StatusOK, second.StatusCode, "reassign to another member: %s", second.Body)

	var assignee uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT assignee_id FROM tickets WHERE id = $1`, uuid.MustParse(ticketID)).Scan(&assignee))
	require.Equal(t, f.contrib.ID, assignee, "the refused re-assign left the accepted one in place")
}

// TestShareTicketNegTicketsSearchRequiresQueryAndHonoursLimit covers the two
// refusals and the one bound on the search route.
//
// Defect this catches: dropping the limit parsing. `limit` is the only
// caller-controlled number on this route, so a regression there is either a
// 500 on garbage or an unbounded read — the limit=1 case fails if the
// parsed value stops being used, and the garbage cases fail if a bad value
// stops falling back to the default. The missing-q assertions pin the
// documented 400 VALIDATION_ERROR rather than an empty 200, which is what a
// blank search box would otherwise turn into a full table scan.
func TestShareTicketNegTicketsSearchRequiresQueryAndHonoursLimit(t *testing.T) {
	f := shtNegNewFixture(t)
	f.createTicket(t, "Widget alpha malfunction")
	f.createTicket(t, "Widget bravo malfunction")
	f.createTicket(t, "Unrelated chore")
	searchPath := f.ticketsPath("/search")

	// No q at all, and an explicitly empty q, are both refusals.
	requireErrorCode(t, f.ts.get(t, searchPath, true), http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, f.ts.get(t, searchPath+"?q=", true), http.StatusBadRequest, "VALIDATION_ERROR")

	decode := func(t *testing.T, r httpResult) []map[string]any {
		t.Helper()
		require.Equal(t, http.StatusOK, r.StatusCode, "search: %s", r.Body)
		requireSnakeCaseKeys(t, r.Body)
		var out []map[string]any
		require.NoError(t, json.Unmarshal(r.Body, &out))
		return out
	}

	// A real query returns only the matches — the third ticket is the guard
	// that the filter is a filter.
	require.Len(t, decode(t, f.ts.get(t, searchPath+"?q=malfunction", true)), 2)
	require.Len(t, decode(t, f.ts.get(t, searchPath+"?q=chore", true)), 1)
	require.Empty(t, decode(t, f.ts.get(t, searchPath+"?q=quokka", true)))

	// An in-range limit is honoured...
	require.Len(t, decode(t, f.ts.get(t, searchPath+"?q=malfunction&limit=1", true)), 1)

	// ...and every out-of-range or unparseable limit falls back to the
	// default rather than erroring or truncating to nothing.
	for _, bad := range []string{"abc", "0", "-5", "201", "99999999999999999999", ""} {
		t.Run("limit="+bad, func(t *testing.T) {
			require.Len(t, decode(t, f.ts.get(t, searchPath+"?q=malfunction&limit="+bad, true)), 2,
				"limit %q must fall back to the default, not refuse or truncate", bad)
		})
	}
}
