package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The customer portal's security properties, against a real database and the
// fully wired router.
//
// These are the tests the phase is actually for. The portal is the only route
// family an unauthenticated stranger on the public internet can reach, and the
// only one whose readers are outside the organisation entirely, so the
// interesting assertions are all about what does NOT come back.
//
// Each property below names the edit that makes it fail, and every one of
// those edits was APPLIED AND RUN. The nine confirmed mutations:
//
//	ListPortalTicketComments loses `AND c.visibility = 'public'`
//	    → InternalCommentIsInvisible FAILS
//	GetPortalRequest loses its requester_id predicate
//	    → RequesterCannotSeeAnotherRequestersRequests FAILS
//	ConsumeMagicLink loses `AND consumed_at IS NULL`
//	    → MagicLinkIsSingleUse FAILS
//	ConsumeMagicLink loses `AND expires_at > now()`
//	    → ExpiredLinkIsRefused FAILS
//	CreateMagicLink stops calling InvalidateOutstandingLinks
//	    → RequestingANewLinkSupersedesTheOld FAILS
//	requestView gains a space_id field
//	    → WireCarriesNoContainerContext FAILS
//	resolveVisibility defaults to public
//	    → AgentCommentDefaultsToInternal FAILS
//	Service.Authenticate stops comparing session_generation
//	    → DeactivationRevokesLiveSessions FAILS
//	RequirePortalSession stops comparing the URL's portal to the session's
//	    → SessionForOnePortalDoesNotReachAnother FAILS
//
// One near-miss is worth recording, because it is the failure mode of
// mutation testing itself. The first attempt at the single-use mutation used a
// regex that matched `AND consumed_at IS NULL` in InvalidateOutstandingLinks —
// an earlier query in the same file — so it silently mutated the wrong
// statement, ConsumeMagicLink was left intact, and the test PASSED. A passing
// mutation run reads exactly like a test that cannot fail, and the only way to
// tell them apart is to look at the mutated source rather than the exit code.

// portalFixture is a Beacon space with a portal and two unrelated requesters.
type portalFixture struct {
	ts        *testServer
	spaceID   uuid.UUID
	portalKey string
	svc       *portal.Service
}

func newPortalFixture(t *testing.T) *portalFixture {
	t.Helper()
	ts := newTestServer(t)

	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	svc := ts.RouterCfg.PortalService
	require.NotNil(t, svc, "the harness must wire a portal service")

	p, err := svc.CreatePortal(context.Background(), space.ID, "beacon", "Acme Support", "How can we help?", ts.UserID)
	require.NoError(t, err)

	// The public identifier must not be derivable from the space. Migration
	// 044's CHECK enforces the shape; this asserts the intent behind it.
	row, err := generated.New(ts.DB.Pool).GetSpaceByID(context.Background(), space.ID)
	require.NoError(t, err)
	require.NotContains(t, p.Key, space.Slug)
	require.NotContains(t, p.Key, row.Key)
	require.Regexp(t, `^[a-z0-9]{16,32}$`, p.Key)

	return &portalFixture{ts: ts, spaceID: space.ID, portalKey: p.Key, svc: svc}
}

// signIn drives the real sign-in flow: request a link, then redeem it. It uses
// the HTTP surface rather than the service so that the guard, the router and
// the wire format are all exercised.
func (f *portalFixture) signIn(t *testing.T, email string) string {
	t.Helper()

	res := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": email, "name": email}, false)
	require.Equal(t, http.StatusAccepted, res.StatusCode, string(res.Body))

	var issued struct {
		MagicLinkURL string `json:"magic_link_url"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &issued))
	require.NotEmpty(t, issued.MagicLinkURL, "link-delivery mode must return the URL in the harness")

	// The raw token is the last path segment.
	rawToken := issued.MagicLinkURL[len(issued.MagicLinkURL)-43:]

	res = f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": rawToken}, false)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))

	var sess struct {
		SessionToken string `json:"session_token"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &sess))
	require.NotEmpty(t, sess.SessionToken)
	return sess.SessionToken
}

func (f *portalFixture) submit(t *testing.T, token, summary string) string {
	t.Helper()
	res := f.ts.requestAs(t, token, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/requests",
		map[string]string{"summary": summary, "description": "please help"})
	require.Equal(t, http.StatusCreated, res.StatusCode, string(res.Body))
	var v struct {
		Reference string `json:"reference"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &v))
	return v.Reference
}

// ── Property 1: internal comments are invisible at the QUERY level ───────

// TestPortal_InternalCommentIsInvisible fixtures an internal agent note on a
// portal request and asserts it is absent from the portal's wire response.
//
// FAILS-BEFORE: relax ListPortalTicketComments by deleting its
// `AND c.visibility = 'public'` predicate and this fails, because the internal
// note appears in the thread.
//
// The point of the assertion is WHERE the exclusion happens. The portal has
// its own SQL statement carrying a literal visibility predicate, rather than
// sharing ListCommentsByEntity with a parameter, so a serialiser bug cannot
// leak an internal note — the row was never fetched.
func TestPortal_InternalCommentIsInvisible(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	ref := f.submit(t, token, "Printer is on fire")

	q := generated.New(f.ts.DB.Pool)
	ticketID := uuid.MustParse(ref)

	// An agent writes one internal note and one public reply.
	_, err := q.CreateComment(context.Background(), generated.CreateCommentParams{
		ID: uuid.New(), EntityType: "ticket", EntityID: ticketID,
		AuthorID:   pgtype.UUID{Bytes: f.ts.UserID, Valid: true},
		Body:       "INTERNAL: customer is on the no-refund list",
		Visibility: "internal",
	})
	require.NoError(t, err)
	_, err = q.CreateComment(context.Background(), generated.CreateCommentParams{
		ID: uuid.New(), EntityType: "ticket", EntityID: ticketID,
		AuthorID:   pgtype.UUID{Bytes: f.ts.UserID, Valid: true},
		Body:       "We are looking into it.",
		Visibility: "public",
	})
	require.NoError(t, err)

	res := f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Assert against the RAW BYTES, not a decoded struct. A decode would only
	// prove the field we thought to look at is absent; the raw body proves the
	// string never left the server by any route at all.
	require.NotContains(t, string(res.Body), "no-refund list",
		"an internal note reached the customer portal")
	require.NotContains(t, string(res.Body), "INTERNAL")
	require.Contains(t, string(res.Body), "We are looking into it.",
		"the public reply must still be visible, or the test proves nothing")
}

// TestPortal_AgentCommentDefaultsToInternal pins the default at the HTTP
// boundary, where the product decision actually lives.
//
// FAILS-BEFORE: change resolveVisibility's nil branch to return
// visibilityPublic and this fails.
func TestPortal_AgentCommentDefaultsToInternal(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	ref := f.submit(t, token, "Cannot log in")

	// An agent comments through the ordinary ticket comment route, sending no
	// visibility at all — the shape an old client necessarily takes.
	res := f.ts.post(t,
		"/api/v1/orgs/"+f.ts.OrgID.String()+"/spaces/"+f.spaceID.String()+"/tickets/"+ref+"/comments",
		map[string]string{"content": "Checking their account now"}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(res.Body))

	var created struct {
		Visibility string `json:"visibility"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &created))
	require.Equal(t, "internal", created.Visibility,
		"an agent comment with no stated visibility must be internal")

	// And it must not reach the customer.
	res = f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
	require.NotContains(t, string(res.Body), "Checking their account now")
}

// ── Property 2: one requester cannot see another's requests ──────────────

// TestPortal_RequesterCannotSeeAnotherRequestersRequests is the per-viewer
// property in its strictest form: two requesters on the SAME portal.
//
// FAILS-BEFORE: drop `AND requester_id = $3` from GetPortalRequest and the
// detail read succeeds for the wrong requester.
//
// The refusal must be 404 rather than 403. A 403 would confirm the request
// exists, which is §2.6's rule applied to an external reader — and here the
// reader is a stranger, so the leak is worse than it would be internally.
func TestPortal_RequesterCannotSeeAnotherRequestersRequests(t *testing.T) {
	f := newPortalFixture(t)

	alice := f.signIn(t, "alice@example.com")
	bob := f.signIn(t, "bob@example.com")

	aliceRef := f.submit(t, alice, "Alice's confidential problem")
	bobRef := f.submit(t, bob, "Bob's unrelated problem")

	// Bob's list contains only Bob's request.
	res := f.ts.requestAs(t, bob, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotContains(t, string(res.Body), "Alice's confidential problem")
	require.NotContains(t, string(res.Body), aliceRef)
	require.Contains(t, string(res.Body), bobRef)

	// Bob cannot read Alice's request by reference, and is told it does not
	// exist rather than that he may not have it.
	res = f.ts.requestAs(t, bob, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+aliceRef, nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	requireErrorCode(t, res, http.StatusNotFound, "NOT_FOUND")

	// Nor reply to it. The reply path re-resolves ownership rather than
	// trusting that the read already happened.
	res = f.ts.requestAs(t, bob, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+aliceRef+"/replies",
		map[string]string{"body": "let me add something to your ticket"})
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	// And Alice's thread is unchanged by the attempt.
	res = f.ts.requestAs(t, alice, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+aliceRef, nil)
	require.NotContains(t, string(res.Body), "let me add something")
}

// ── Property 3: the wire carries zero container context ──────────────────

// TestPortal_WireCarriesNoContainerContext asserts the WHOLE SHAPE of every
// portal response, not a handful of fields.
//
// Spot-checking "does it contain space_id" is the weaker test and would pass
// while a future author added `space_name` beside it. Comparing the exact key
// set means any new field fails until somebody decides it belongs on a surface
// an external customer reads.
//
// FAILS-BEFORE: add `SpaceID uuid.UUID \`json:"space_id"\“ to requestView and
// this fails on the exact-key comparison.
func TestPortal_WireCarriesNoContainerContext(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	ref := f.submit(t, token, "A problem")

	// Give the ticket every internal attribute the portal must not surface, so
	// the assertion is made against a ticket that HAS something to leak.
	q := generated.New(f.ts.DB.Pool)
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE tickets SET assignee_id = $2, labels = ARRAY['vip','escalated'], rank = '0|zzz:'
		 WHERE id = $1`, uuid.MustParse(ref), f.ts.UserID)
	require.NoError(t, err)

	cases := []struct {
		name string
		path string
		keys []string
	}{
		{"describe", "/api/v1/portal/" + f.portalKey, []string{"intro", "name"}},
		{"detail", "/api/v1/portal/" + f.portalKey + "/my/requests/" + ref, []string{"messages", "request"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var res httpResult
			if c.name == "describe" {
				res = f.ts.get(t, c.path, false)
			} else {
				res = f.ts.requestAs(t, token, http.MethodGet, c.path, nil)
			}
			require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
			require.Equal(t, c.keys, topLevelKeys(t, res.Body))
			requireSnakeCaseKeys(t, res.Body)
		})
	}

	// The request object itself, in full.
	res := f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
	var detail struct {
		Request json.RawMessage `json:"request"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &detail))
	require.Equal(t,
		[]string{"created_at", "description", "reference", "status", "summary", "updated_at"},
		topLevelKeys(t, detail.Request),
		"the portal's request shape changed — every field here is read by someone outside the organisation")

	// And the list shape, which omits the description.
	res = f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	var list []json.RawMessage
	require.NoError(t, json.Unmarshal(res.Body, &list))
	require.Len(t, list, 1)
	require.Equal(t,
		[]string{"created_at", "reference", "status", "summary", "updated_at"},
		topLevelKeys(t, list[0]))

	// Belt and braces against the raw bytes: none of the container's names
	// appear anywhere in any portal response.
	space, err := q.GetSpaceByID(context.Background(), f.spaceID)
	require.NoError(t, err)
	for _, body := range [][]byte{res.Body, detail.Request} {
		require.NotContains(t, string(body), space.Key)
		require.NotContains(t, string(body), space.Slug)
		require.NotContains(t, string(body), space.Name)
		require.NotContains(t, string(body), f.spaceID.String())
		require.NotContains(t, string(body), f.ts.OrgID.String())
	}
}

// ── Property 4: a link fails closed once used, superseded or expired ─────

// TestPortal_MagicLinkIsSingleUse redeems a link twice.
//
// FAILS-BEFORE: remove `AND consumed_at IS NULL` from ConsumeMagicLink and the
// second redemption succeeds.
func TestPortal_MagicLinkIsSingleUse(t *testing.T) {
	f := newPortalFixture(t)

	res := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": "once@example.com"}, false)
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	var issued struct {
		MagicLinkURL string `json:"magic_link_url"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &issued))
	raw := issued.MagicLinkURL[len(issued.MagicLinkURL)-43:]

	first := f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": raw}, false)
	require.Equal(t, http.StatusOK, first.StatusCode)

	second := f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": raw}, false)
	require.Equal(t, http.StatusUnauthorized, second.StatusCode,
		"a magic link must be redeemable exactly once")
}

// TestPortal_RequestingANewLinkSupersedesTheOld covers rotation.
//
// FAILS-BEFORE: delete the InvalidateOutstandingLinks call from
// PortalAdapter.CreateMagicLink and the first link keeps working, leaving two
// live credentials in one inbox.
func TestPortal_RequestingANewLinkSupersedesTheOld(t *testing.T) {
	f := newPortalFixture(t)

	get := func() string {
		res := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
			map[string]string{"email": "rotate@example.com"}, false)
		require.Equal(t, http.StatusAccepted, res.StatusCode)
		var issued struct {
			MagicLinkURL string `json:"magic_link_url"`
		}
		require.NoError(t, json.Unmarshal(res.Body, &issued))
		return issued.MagicLinkURL[len(issued.MagicLinkURL)-43:]
	}

	oldToken := get()
	newToken := get()
	require.NotEqual(t, oldToken, newToken)

	res := f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": oldToken}, false)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"requesting a new link must invalidate the previous one")

	res = f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": newToken}, false)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// TestPortal_ExpiredLinkIsRefused ages a link past its window directly in the
// database, because waiting an hour is not a test.
//
// FAILS-BEFORE: remove `AND expires_at > now()` from ConsumeMagicLink.
func TestPortal_ExpiredLinkIsRefused(t *testing.T) {
	f := newPortalFixture(t)

	res := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": "stale@example.com"}, false)
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	var issued struct {
		MagicLinkURL string `json:"magic_link_url"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &issued))
	raw := issued.MagicLinkURL[len(issued.MagicLinkURL)-43:]

	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE requester_magic_links SET expires_at = now() - interval '1 minute'
		 WHERE consumed_at IS NULL AND invalidated_at IS NULL`)
	require.NoError(t, err)

	res = f.ts.post(t, "/api/v1/portal/auth/redeem", map[string]string{"token": raw}, false)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

// TestPortal_DeactivationRevokesLiveSessions is the requester-side counterpart
// of users.token_generation (shared-surfaces §8).
//
// FAILS-BEFORE: delete the SessionGeneration comparison in
// portal.Service.Authenticate and a deactivated requester's token keeps
// working until it expires — the exact hole the column exists to close.
func TestPortal_DeactivationRevokesLiveSessions(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "revoke@example.com")

	res := f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "persona must work before the revocation")

	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE requesters SET session_generation = session_generation + 1 WHERE email = $1`,
		"revoke@example.com")
	require.NoError(t, err)

	res = f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"a session must die the moment its generation moves")
}

// TestPortal_RequestLinkDoesNotRevealWhetherTheAddressIsKnown is the
// non-enumeration property.
//
// An unauthenticated caller must not be able to use this endpoint to discover
// whether an address has ever contacted this service desk. Login takes the
// same posture, mapping a wrong password and a deactivated account to one
// identical 401.
func TestPortal_RequestLinkDoesNotRevealWhetherTheAddressIsKnown(t *testing.T) {
	f := newPortalFixture(t)
	f.signIn(t, "known@example.com") // creates the requester

	known := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": "known@example.com"}, false)
	unknown := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": "never-seen@example.com"}, false)

	require.Equal(t, known.StatusCode, unknown.StatusCode)
	require.Equal(t, http.StatusAccepted, known.StatusCode)

	// The bodies differ only in the magic-link URL, which is the harness
	// affordance; the status and the shape must be identical.
	require.Equal(t, topLevelKeys(t, known.Body), topLevelKeys(t, unknown.Body))
}

// TestPortal_SessionForOnePortalDoesNotReachAnother covers the PortalID claim.
//
// FAILS-BEFORE: delete the `p.ID != sess.PortalID` comparison in
// RequirePortalSession and a session minted on one service desk authenticates
// against every portal in the deployment.
func TestPortal_SessionForOnePortalDoesNotReachAnother(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")

	other := testutil.CreateTestSpace(t, f.ts.DB.Pool, f.ts.OrgID, f.ts.UserID, "beacon")
	otherPortal, err := f.svc.CreatePortal(context.Background(), other.ID, "beacon", "Other Desk", "", f.ts.UserID)
	require.NoError(t, err)

	res := f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+otherPortal.Key+"/my/requests", nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"a portal session must not authenticate against a different portal")
}

// TestPortal_UnauthenticatedRequestRoutesAreRefused is the floor: the guarded
// subtree refuses a caller with no credential at all.
func TestPortal_UnauthenticatedRequestRoutesAreRefused(t *testing.T) {
	f := newPortalFixture(t)
	for _, path := range []string{
		"/api/v1/portal/" + f.portalKey + "/my/requests",
	} {
		res := f.ts.get(t, path, false)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, path)
	}
}

// TestPortal_InternalTokenIsRefusedByThePortal is the boundary at the ROUTER,
// complementing the validator-level tests in internal/core/portal.
//
// An agent's perfectly valid internal access token must not authenticate a
// portal session.
func TestPortal_InternalTokenIsRefusedByThePortal(t *testing.T) {
	f := newPortalFixture(t)

	res := f.ts.requestAs(t, f.ts.Token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"an internal access token must not authenticate a portal session")
}

// TestPortal_PortalTokenIsRefusedByInternalRoutes is the same boundary in the
// other direction, and it targets the three routes that sit behind
// RequireAuth with no org-membership check after it — the concrete escape
// surface for any token that clears authentication.
func TestPortal_PortalTokenIsRefusedByInternalRoutes(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")

	for _, path := range []string{
		"/api/v1/auth/me",
		"/api/v1/notifications",
		"/api/v1/orgs/" + f.ts.OrgID.String() + "/spaces/" + f.spaceID.String() + "/tickets",
	} {
		res := f.ts.requestAs(t, token, http.MethodGet, path, nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"a portal session token reached %s", path)
	}
}

// topLevelKeys returns a JSON object's keys, sorted, so an assertion can pin
// the whole shape rather than the fields somebody remembered to check.
func topLevelKeys(t *testing.T, body []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m), "body is not a JSON object: %s", string(body))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── The agent-side capability gate ───────────────────────────────────────

// TestPortalConfig_ContributorIsRefusedSpaceAdminIsAllowed is the
// capability-gate pair, with the persona CLAUDE.md §2 requires.
//
// A viewer would prove nothing here. The space subtree's write floor is
// create_items, which already refuses viewers upstream, so a "viewer is
// refused" assertion measures the middleware and would pass with the
// in-handler access.Can(CapManageSpace) deleted. A CONTRIBUTOR clears the
// floor and lacks manage_space, so they isolate this gate and nothing else.
//
// FAILS-BEFORE: delete the access.Can(CapManageSpace) check in
// requireManageSpace and every "refused" case below turns into a success.
//
// Note the portal config routes deliberately do NOT carry the write floor —
// manage_space sits well above create_items, so adding the floor would make
// this gate unreachable for anybody it would refuse, and the test would then
// be measuring the floor again.
func TestPortalConfig_ContributorIsRefusedSpaceAdminIsAllowed(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	path := "/api/v1/orgs/" + ts.OrgID.String() + "/spaces/" + space.ID.String() + "/portal"

	contributor := personaOn(t, ts, space.ID, "contributor")
	spaceAdmin := personaOn(t, ts, space.ID, "space_admin")
	body := map[string]string{"name": "Acme Support", "intro": "How can we help?"}

	// Persona validation first: the contributor must be able to do the
	// baseline thing, or the refusal below is measuring the wrong check.
	t.Run("contributor clears the space floor", func(t *testing.T) {
		res := ts.getAs(t, contributor, "/api/v1/orgs/"+ts.OrgID.String()+"/spaces/"+space.ID.String()+"/tickets")
		require.Equal(t, http.StatusOK, res.StatusCode,
			"the contributor persona must be able to read the space, or this test proves nothing")
	})

	t.Run("contributor cannot enable a portal", func(t *testing.T) {
		res := ts.postAs(t, contributor, path, body)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a contributor is past the write floor and short of manage_space: %s", res.Body)
	})

	t.Run("space admin can enable a portal", func(t *testing.T) {
		res := ts.postAs(t, spaceAdmin, path, body)
		require.Equal(t, http.StatusCreated, res.StatusCode, string(res.Body))
		requireSnakeCaseKeys(t, res.Body)
	})

	t.Run("contributor cannot toggle it", func(t *testing.T) {
		res := ts.patchAs(t, contributor, path, map[string]bool{"enabled": false})
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("contributor cannot read the configuration", func(t *testing.T) {
		// The portal key is the URL an outsider uses. A contributor having it
		// is not a disaster, but manage_space owns the space's configuration
		// and this route is part of it.
		res := ts.getAs(t, contributor, path)
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})
}

// TestPortalConfig_RefusedOnNonBeaconSpaces pins the module restriction.
// Codex and Vector spaces have no requesters and no tickets to raise.
func TestPortalConfig_RefusedOnNonBeaconSpaces(t *testing.T) {
	ts := newTestServer(t)
	for _, module := range []string{"codex", "vector"} {
		t.Run(module, func(t *testing.T) {
			space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, module)
			res := ts.post(t,
				"/api/v1/orgs/"+ts.OrgID.String()+"/spaces/"+space.ID.String()+"/portal",
				map[string]string{"name": "Nope"}, true)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, string(res.Body))
			requireErrorCode(t, res, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

// TestPortalConfig_DisablingEndsLiveSessions is the consequence of
// GetPortalByID requiring `enabled`.
//
// FAILS-BEFORE: drop `AND p.enabled` from GetPortalByID and a requester whose
// portal was switched off keeps working until their token expires.
func TestPortalConfig_DisablingEndsLiveSessions(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")

	res := f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "persona must work before the portal is disabled")

	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE service_desk_portals SET enabled = false WHERE portal_key = $1`, f.portalKey)
	require.NoError(t, err)

	res = f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"disabling a portal must end its outstanding sessions, not wait for them to expire")
}

// ── The reply round trip, which the security tests above never exercise ──

// TestPortal_RequesterReplyIsPublicAndReachesBothSides is the happy path the
// earlier tests deliberately skip: they assert that a reply to somebody ELSE's
// request is refused, which leaves the successful path — the whole point of the
// feature — unexercised. This closes that.
//
// It asserts the reply from both ends, because "public" is a claim about two
// different readers: the customer must see their own words come back, and the
// agent must see them in the internal thread marked as the customer's.
func TestPortal_RequesterReplyIsPublicAndReachesBothSides(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "chatty@example.com")
	ref := f.submit(t, token, "My widget is broken")

	res := f.ts.requestAs(t, token, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref+"/replies",
		map[string]string{"body": "It started after the update."})
	require.Equal(t, http.StatusCreated, res.StatusCode, string(res.Body))
	requireSnakeCaseKeys(t, res.Body)
	require.Equal(t,
		[]string{"author", "body", "created_at", "from_requester", "id"},
		topLevelKeys(t, res.Body),
		"the portal's message shape changed — an external customer reads every field here")

	var created struct {
		FromRequester bool   `json:"from_requester"`
		Body          string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &created))
	require.True(t, created.FromRequester)
	require.Equal(t, "It started after the update.", created.Body)

	// The customer sees it in their own thread.
	res = f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
	require.Contains(t, string(res.Body), "It started after the update.")

	// The AGENT sees it in the internal thread, attributed to the requester and
	// marked public. This is the half the LEFT JOIN in ListCommentsByEntity
	// exists for: the previous INNER JOIN on users would have dropped it
	// silently, leaving the agent looking at a conversation that appeared to
	// have no customer in it.
	res = f.ts.get(t,
		"/api/v1/orgs/"+f.ts.OrgID.String()+"/spaces/"+f.spaceID.String()+"/tickets/"+ref+"/comments", true)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))

	var thread []struct {
		AuthorID      *string `json:"author_id"`
		AuthorName    string  `json:"author_name"`
		FromRequester bool    `json:"from_requester"`
		Visibility    string  `json:"visibility"`
		Body          string  `json:"body"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &thread))
	require.Len(t, thread, 1, "the agent thread must contain the customer's reply")
	require.Equal(t, "It started after the update.", thread[0].Body)
	require.True(t, thread[0].FromRequester)
	require.Equal(t, "public", thread[0].Visibility)
	require.Nil(t, thread[0].AuthorID, "a requester has no users row, so author_id is null")
	require.Equal(t, "chatty@example.com", thread[0].AuthorName,
		"the requester's name must resolve through the LEFT JOIN on requesters")
}

// TestPortal_ReplyNotifiesTheAssignee covers the notification this phase wired.
//
// It matters more than a line of coverage: the comments handler has held a
// NotificationEnqueuer since it was written, main.go wires a live queue into
// it, and it has never been read — so creating a comment notifies nobody. The
// portal deliberately does not inherit that, and this is what proves it.
//
// FAILS-BEFORE: delete the h.notifyAssignee call in Reply and this fails.
func TestPortal_ReplyNotifiesTheAssignee(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	ref := f.submit(t, token, "Needs an agent")

	// Unassigned first: a reply must not invent a recipient.
	res := f.ts.requestAs(t, token, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref+"/replies",
		map[string]string{"body": "anyone there?"})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Empty(t, f.ts.PortalNotifications.All(),
		"an unassigned request has nobody to notify")

	// Now assign it and reply again.
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, uuid.MustParse(ref), f.ts.UserID)
	require.NoError(t, err)

	res = f.ts.requestAs(t, token, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref+"/replies",
		map[string]string{"body": "still broken"})
	require.Equal(t, http.StatusCreated, res.StatusCode)

	sent := f.ts.PortalNotifications.All()
	require.Len(t, sent, 1, "an assigned request must notify its assignee exactly once")
	require.Equal(t, f.ts.UserID.String(), sent[0].UserID)
	require.Equal(t, "portal.reply", sent[0].EventKind)
	require.Equal(t, ref, sent[0].ResourceID)
	require.Equal(t, "ticket", sent[0].EntityKind)
	require.Equal(t, f.spaceID.String(), sent[0].SpaceID)
	// The notification is internal, so it MAY name the space — but it must not
	// carry the customer's words, which the agent reads in the thread itself.
	require.NotContains(t, sent[0].Message, "still broken")
}

// TestPortal_SignOutRevokesEverySession covers the route, and the difference
// between it and the internal /auth/logout.
//
// FAILS-BEFORE: make SignOut a no-op and this fails. Note that mirroring
// /auth/logout — which revokes database sessions production never creates and
// leaves the caller's JWT valid — would ALSO fail it, which is the point: a
// cosmetic sign-out is what this test exists to refuse.
func TestPortal_SignOutRevokesEverySession(t *testing.T) {
	f := newPortalFixture(t)
	first := f.signIn(t, "leaving@example.com")
	second := f.signIn(t, "leaving@example.com") // a second device

	res := f.ts.requestAs(t, first, http.MethodPost,
		"/api/v1/portal/"+f.portalKey+"/my/auth/sign-out", nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	for name, tok := range map[string]string{"the session that signed out": first, "the other device": second} {
		res := f.ts.requestAs(t, tok, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"%s must be revoked: signing out bumps the generation, it does not just drop one token", name)
	}
}

// TestPortal_StatusIsTranslatedNotEchoed walks every status the portal can
// meet, including a WORKFLOW STATE NAME.
//
// tickets.status has no database CHECK and the workflow transition route writes
// targetState.Name straight into it, so the portal cannot assume the four Go
// constants. Anything unrecognised must fall through to a generic value: a
// customer seeing "In progress" is a small loss, a customer seeing "Awaiting
// legal sign-off" is a leak of exactly the internal process this surface exists
// to withhold.
//
// FAILS-BEFORE: return the raw status from requesterStatus and the last case
// fails.
func TestPortal_StatusIsTranslatedNotEchoed(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	ref := f.submit(t, token, "Track my status")

	for _, c := range []struct{ internal, shown string }{
		{"open", "Received"},
		{"in_progress", "In progress"},
		{"resolved", "Resolved"},
		{"closed", "Closed"},
		{"Awaiting legal sign-off", "In progress"},
	} {
		t.Run(c.internal, func(t *testing.T) {
			_, err := f.ts.DB.Pool.Exec(context.Background(),
				`UPDATE tickets SET status = $2 WHERE id = $1`, uuid.MustParse(ref), c.internal)
			require.NoError(t, err)

			res := f.ts.requestAs(t, token, http.MethodGet,
				"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Contains(t, string(res.Body), `"status":"`+c.shown+`"`)
			if c.internal != c.shown {
				require.NotContains(t, string(res.Body), c.internal,
					"the internal status string must not reach the customer")
			}
		})
	}
}

// TestPortalConfig_ToggleKeepsTheKey covers the agent read/toggle pair.
//
// Re-enabling must not mint a new key: every URL already handed to a customer
// would stop working, which is a support incident caused by an administrative
// click.
func TestPortalConfig_ToggleKeepsTheKey(t *testing.T) {
	f := newPortalFixture(t)
	path := "/api/v1/orgs/" + f.ts.OrgID.String() + "/spaces/" + f.spaceID.String() + "/portal"

	res := f.ts.get(t, path, true)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
	requireSnakeCaseKeys(t, res.Body)
	var got struct {
		Key     string `json:"portal_key"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &got))
	require.Equal(t, f.portalKey, got.Key)
	require.Equal(t, "Acme Support", got.Name)
	require.True(t, got.Enabled)

	// Disable, then re-enable.
	res = f.ts.patch(t, path, map[string]bool{"enabled": false}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
	require.Contains(t, string(res.Body), `"enabled":false`)

	// While disabled the public face is gone entirely.
	res = f.ts.get(t, "/api/v1/portal/"+f.portalKey, false)
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	res = f.ts.patch(t, path, map[string]bool{"enabled": true}, true)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, json.Unmarshal(res.Body, &got))
	require.Equal(t, f.portalKey, got.Key,
		"re-enabling must keep the key, or every URL already sent to a customer dies")
	require.True(t, got.Enabled)

	res = f.ts.get(t, "/api/v1/portal/"+f.portalKey, false)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// TestPortalConfig_SecondPortalOnOneSpaceIsRefused pins the UNIQUE on space_id.
// Two portals for one service desk would mean two public identities for the
// same queue, and no way to tell which one a customer used.
func TestPortalConfig_SecondPortalOnOneSpaceIsRefused(t *testing.T) {
	f := newPortalFixture(t)
	res := f.ts.post(t,
		"/api/v1/orgs/"+f.ts.OrgID.String()+"/spaces/"+f.spaceID.String()+"/portal",
		map[string]string{"name": "A second front door"}, true)
	require.Equal(t, http.StatusConflict, res.StatusCode, string(res.Body))
	requireErrorCode(t, res, http.StatusConflict, "CONFLICT")
}

// TestPortalConfig_RequiresAName covers the validation branch.
func TestPortalConfig_RequiresAName(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	res := ts.post(t,
		"/api/v1/orgs/"+ts.OrgID.String()+"/spaces/"+space.ID.String()+"/portal",
		map[string]string{"name": "   "}, true)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(res.Body))
	requireErrorCode(t, res, http.StatusBadRequest, "VALIDATION_ERROR")
}

// TestPortalConfig_GetOnASpaceWithNoPortal404s covers the absent case, which is
// the default for every space in a deployment.
func TestPortalConfig_GetOnASpaceWithNoPortal404s(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	res := ts.get(t, "/api/v1/orgs/"+ts.OrgID.String()+"/spaces/"+space.ID.String()+"/portal", true)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// TestPortal_SubmitValidation covers the two refusals on the write paths.
func TestPortal_SubmitValidation(t *testing.T) {
	f := newPortalFixture(t)
	token := f.signIn(t, "customer@example.com")
	base := "/api/v1/portal/" + f.portalKey + "/my/requests"

	res := f.ts.requestAs(t, token, http.MethodPost, base,
		map[string]string{"summary": "  ", "description": "x"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	requireErrorCode(t, res, http.StatusBadRequest, "VALIDATION_ERROR")

	res = f.ts.requestAs(t, token, http.MethodPost,
		base+"/"+uuid.New().String()+"/replies", map[string]string{"body": "  "})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

// TestPortal_RequestLinkRejectsAMalformedAddress is the one refusal on the
// request-link path that is NOT collapsed into an indistinguishable 202: a
// string that is not an address at all is a client bug, and accepting it would
// silently create junk identities.
func TestPortal_RequestLinkRejectsAMalformedAddress(t *testing.T) {
	f := newPortalFixture(t)
	res := f.ts.post(t, "/api/v1/portal/"+f.portalKey+"/auth/request-link",
		map[string]string{"email": "not-an-address"}, false)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(res.Body))
	requireErrorCode(t, res, http.StatusBadRequest, "VALIDATION_ERROR")
}

// TestPortal_UnknownPortalKeyIs404 on every public route.
func TestPortal_UnknownPortalKeyIs404(t *testing.T) {
	f := newPortalFixture(t)
	bogus := "aaaaaaaaaaaaaaaaaaaa"

	require.Equal(t, http.StatusNotFound, f.ts.get(t, "/api/v1/portal/"+bogus, false).StatusCode)
	require.Equal(t, http.StatusNotFound, f.ts.post(t,
		"/api/v1/portal/"+bogus+"/auth/request-link", map[string]string{"email": "a@b.com"}, false).StatusCode)
	require.Equal(t, http.StatusUnauthorized, f.ts.post(t,
		"/api/v1/portal/auth/redeem", map[string]string{"token": "nonsense"}, false).StatusCode)
}
