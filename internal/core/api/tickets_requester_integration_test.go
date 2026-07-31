package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The AGENT side of a portal-raised ticket, against a real database and the
// fully wired router.
//
// portal_integration_test.go asserts what must NOT cross to the customer. This
// file asserts the one thing that must cross the other way: a ticket raised by
// an external requester has no `users` row behind its reporter, so until the
// read path resolved the requester the agent surface had nothing to render and
// showed "Unknown". That is the defect the last test here is named for.
//
// Each property below names the edit that makes it fail, and every one of those
// edits was APPLIED AND RUN.
//
//	resolveRequesters stops populating v.Requester
//	    → PortalRaisedTicketCarriesResolvedIdentity FAILS
//	ticketView.Requester gains omitempty
//	    → AgentRaisedTicketHasNullRequester FAILS
//	resolveRequesters calls the lookup once per ticket
//	    → ListResolvesWithoutNPlusOne FAILS
//	respondKanban slices views[0:n] instead of views[at:at+n]
//	    → KanbanResolvesAcrossColumns FAILS
//	requesterIdentity gains an is_active field
//	    → RevocationStateNeverReachesTheAgentWire FAILS
//	portal's requestView gains a requester field
//	    → PortalWire_NeverGainsTheAgentRequesterObject FAILS

// requesterFixture is a portal fixture read through a persona that is NOT an
// org admin.
//
// The distinction is the whole reason this type exists. testutil.CreateTestUser
// makes an org OWNER, and org admin is a middleware BYPASS (CLAUDE.md §1), so
// every read-path assertion written against ts.UserID passes without consulting
// the space at all. The space is pinned to `hidden` for the same reason: the
// default is `discoverable`, which every org member can read, and a grant that
// is never needed proves nothing about grants.
type requesterFixture struct {
	*portalFixture
	// agent is a plain org member holding an agent grant on this space. Every
	// READ assertion below is made through this token.
	agent string
}

func newRequesterFixture(t *testing.T) *requesterFixture {
	t.Helper()
	f := newPortalFixture(t)
	testutil.SetSpaceVisibility(t, f.ts.DB.Pool, f.spaceID, "hidden")
	return &requesterFixture{portalFixture: f, agent: personaOn(t, f.ts, f.spaceID, "agent")}
}

func (f *requesterFixture) ticketsPath() string {
	return "/api/v1/orgs/" + f.ts.OrgID.String() + "/spaces/" + f.spaceID.String() + "/tickets"
}

func (f *requesterFixture) ticketPath(id string) string { return f.ticketsPath() + "/" + id }

// raiseAgentTicket creates an ordinary in-product ticket, the control case for
// every assertion here. The write is made by the fixture owner — setup writes
// are not what the persona rule is about — but it is never READ through that
// token.
func (f *requesterFixture) raiseAgentTicket(t *testing.T, title string) string {
	t.Helper()
	res := f.ts.post(t, f.ticketsPath(), map[string]any{"title": title, "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(res.Body))
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &created))
	return created.ID
}

// ticketRequester is the agent-facing shape under test, decoded.
type ticketRequester struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type resolvedTicket struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Status      string           `json:"status"`
	ReporterID  *string          `json:"reporter_id"`
	RequesterID *string          `json:"requester_id"`
	Requester   *ticketRequester `json:"requester"`
}

// ── Property 1: a portal ticket arrives with its requester resolved ──────

// TestTicketRequester_PortalRaisedTicketCarriesResolvedIdentity is the whole
// feature in one read: the agent asks for a ticket the product never saw a user
// raise, and gets a name and an address back.
//
// FAILS-BEFORE: stop populating v.Requester in resolveRequesters — leave it nil
// — and this fails on the nil check, which is exactly the state main is in.
func TestTicketRequester_PortalRaisedTicketCarriesResolvedIdentity(t *testing.T) {
	f := newRequesterFixture(t)
	token := f.signInAs(t, "priya@customer.example", "Priya Raman")
	ref := f.submit(t, token, "The exports are empty")

	// Persona validation first. A plain member with NO grant on this hidden
	// space must be refused — otherwise the "agent" token below is not what is
	// carrying the read and the rest of this file measures nothing.
	stranger := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	res := f.ts.getAs(t, f.ts.tokenFor(t, stranger.ID, stranger.Email), f.ticketPath(ref))
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"an ungranted member must not read a hidden space, or the agent grant below is decorative")

	res = f.ts.getAs(t, f.agent, f.ticketPath(ref))
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
	requireSnakeCaseKeys(t, res.Body)

	var got resolvedTicket
	require.NoError(t, json.Unmarshal(res.Body, &got))

	require.Nil(t, got.ReporterID,
		"a portal-raised ticket has no users row behind it: reporter_id must be null")
	require.NotNil(t, got.RequesterID, "and requester_id must be the identity that raised it")

	require.NotNil(t, got.Requester, "the read path must resolve the external requester")
	require.Equal(t, "priya@customer.example", got.Requester.Email)
	require.Equal(t, "Priya Raman", got.Requester.DisplayName)
	require.Equal(t, *got.RequesterID, got.Requester.ID,
		"the resolved identity must be the one the ticket names, not any identity")
}

// ── Property 2: an agent ticket says `requester: null`, out loud ─────────

// TestTicketRequester_AgentRaisedTicketHasNullRequester pins the ABSENCE as a
// present, null key rather than a missing one.
//
// The distinction is the point. A client that receives no `requester` key at
// all cannot tell "this ticket has no requester" from "this endpoint has not
// been taught about requesters" — and the second is a bug it must degrade
// around, which is how a surface ends up rendering "Unknown" defensively.
//
// FAILS-BEFORE: make ticketView.Requester `json:"requester,omitempty"` and the
// key disappears for exactly this case.
func TestTicketRequester_AgentRaisedTicketHasNullRequester(t *testing.T) {
	f := newRequesterFixture(t)
	id := f.raiseAgentTicket(t, "Rebuild the search index")

	res := f.ts.getAs(t, f.agent, f.ticketPath(id))
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(res.Body, &m), "body is not a JSON object: %s", res.Body)

	raw, present := m["requester"]
	require.True(t, present,
		"`requester` must be present and null on an agent-raised ticket, not absent: %s", res.Body)
	require.Equal(t, "null", string(raw))

	require.NotEqual(t, "null", string(m["reporter_id"]),
		"an agent-raised ticket keeps its reporter — the addition must not have displaced it")
}

// ── Property 3: a queue resolves in ONE round trip ───────────────────────

// countingRequesterLookup delegates to the real adapter and records every batch
// it was asked for.
//
// It is a fake over the API-layer SEAM, not over the database: the call it
// forwards runs the real SQL against real postgres, so nothing here weakens the
// no-mocks rule. What it observes is the shape of the fan-out, which is not
// visible from the response body at all.
type countingRequesterLookup struct {
	inner ticketsapi.RequesterLookup

	mu      sync.Mutex
	batches [][]uuid.UUID
}

func (c *countingRequesterLookup) RequestersByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]portal.RequesterIdentity, error) {
	c.mu.Lock()
	c.batches = append(c.batches, append([]uuid.UUID(nil), ids...))
	c.mu.Unlock()
	return c.inner.RequestersByIDs(ctx, ids)
}

func (c *countingRequesterLookup) observed() [][]uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.batches
}

// TestTicketRequester_ListResolvesWithoutNPlusOne is the queue read, which is
// where the "Unknown reporter" wound is actually felt — an agent meets the
// ticket list long before they open one.
//
// It asserts two different things, and both are load-bearing. That every row
// carries the right identity is the feature; that the whole page cost ONE
// lookup is the constraint, and no assertion on the response body can see it.
// A per-ticket resolve would serialise identically and pass every other test in
// this file while putting an N+1 on the busiest read path in Beacon.
//
// The lookup is swapped on the LIVE handler rather than a second server:
// RouterConfig holds *tickets.Handler and the router binds method values to
// that same pointer, so WithRequesterLookup after construction reaches the
// routes already mounted. Wiring the fake at construction time would have meant
// a parallel copy of newTestServerOn, which is the dark-harness trap (CLAUDE.md
// §2) in a new disguise — the copy drifts, and the drift is silent.
//
// FAILS-BEFORE: make resolveRequesters call h.requesters.RequestersByIDs once
// per ticket and the batch count is 3, not 1.
func TestTicketRequester_ListResolvesWithoutNPlusOne(t *testing.T) {
	f := newRequesterFixture(t)

	// Three portal requests from TWO requesters — the repeated id is what makes
	// the de-duplication assertion below able to fail.
	priya := f.signInAs(t, "priya@customer.example", "Priya Raman")
	tomas := f.signInAs(t, "tomas@other.example", "Tomás Whitfield")
	f.submit(t, priya, "The exports are empty")
	f.submit(t, priya, "And now the imports too")
	f.submit(t, tomas, "Billing address will not save")
	f.raiseAgentTicket(t, "Rebuild the search index")

	counter := &countingRequesterLookup{inner: adapters.NewPortalAdapter(f.ts.DB.Pool)}
	f.ts.RouterCfg.TicketHandler.WithRequesterLookup(counter)

	res := f.ts.getAs(t, f.agent, f.ticketsPath())
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
	requireSnakeCaseKeys(t, res.Body)

	var page []resolvedTicket
	require.NoError(t, json.Unmarshal(res.Body, &page))
	require.Len(t, page, 4, "the queue must return every ticket in the space")

	byTitle := make(map[string]resolvedTicket, len(page))
	for _, v := range page {
		byTitle[v.Title] = v
	}

	for title, wantEmail := range map[string]string{
		"The exports are empty":         "priya@customer.example",
		"And now the imports too":       "priya@customer.example",
		"Billing address will not save": "tomas@other.example",
	} {
		got, ok := byTitle[title]
		require.True(t, ok, "missing %q from the queue", title)
		require.NotNil(t, got.Requester, "%q lost its requester in the list path", title)
		require.Equal(t, wantEmail, got.Requester.Email, "wrong identity on %q", title)
		require.Nil(t, got.ReporterID, "%q is portal-raised and has no reporter", title)
	}

	agentTicket, ok := byTitle["Rebuild the search index"]
	require.True(t, ok)
	require.Nil(t, agentTicket.Requester, "an agent-raised ticket in a mixed page stays null")
	require.NotNil(t, agentTicket.ReporterID)

	// The batching, which the body cannot show.
	batches := counter.observed()
	require.Len(t, batches, 1,
		"a page of 4 tickets must cost exactly one requester lookup, got %d", len(batches))
	require.Len(t, batches[0], 2,
		"three portal tickets from two requesters must ask for two ids, not three: %v", batches[0])
}

// ── Property 4: the board's index arithmetic ─────────────────────────────

// TestTicketRequester_KanbanResolvesAcrossColumns guards the one piece of this
// change that is arithmetic rather than lookup.
//
// respondKanban flattens every column into one slice, resolves that slice in a
// single round trip, and then cuts it back into columns by running offset. An
// off-by-one there does not fail loudly: the board still has the right shape
// and the right ticket count, and every ticket still has A requester — just
// somebody else's. So the fixture puts DISTINCT requesters in different
// columns, which is the only arrangement in which a mis-cut is visible.
//
// FAILS-BEFORE: slice `views[0:n]` instead of `views[at : at+n]` and the second
// and third columns carry the first column's identities.
func TestTicketRequester_KanbanResolvesAcrossColumns(t *testing.T) {
	f := newRequesterFixture(t)

	priya := f.signInAs(t, "priya@customer.example", "Priya Raman")
	tomas := f.signInAs(t, "tomas@other.example", "Tomás Whitfield")
	openRef := f.submit(t, priya, "The exports are empty")
	movedRef := f.submit(t, tomas, "Billing address will not save")
	resolvedID := f.raiseAgentTicket(t, "Rebuild the search index")

	// Portal requests are raised `open`; move two of them so the board has three
	// occupied columns. Written directly, because the point here is the
	// serialiser, not the transition rules.
	for id, status := range map[string]string{movedRef: "in_progress", resolvedID: "resolved"} {
		_, err := f.ts.DB.Pool.Exec(context.Background(),
			`UPDATE tickets SET status = $2 WHERE id = $1`, uuid.MustParse(id), status)
		require.NoError(t, err)
	}

	res := f.ts.getAs(t, f.agent, f.ticketsPath()+"/kanban")
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))
	requireSnakeCaseKeys(t, res.Body)

	var board []struct {
		Status  string           `json:"status"`
		Tickets []resolvedTicket `json:"tickets"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &board))

	// The grouping itself, unchanged by the addition: four columns, in order,
	// with the counts the fixture put in them.
	require.Len(t, board, 4)
	gotShape := make(map[string]int, len(board))
	order := make([]string, 0, len(board))
	for _, col := range board {
		gotShape[col.Status] = len(col.Tickets)
		order = append(order, col.Status)
	}
	require.Equal(t, []string{"open", "in_progress", "resolved", "closed"}, order)
	require.Equal(t, map[string]int{"open": 1, "in_progress": 1, "resolved": 1, "closed": 0}, gotShape)

	// And each column's ticket carries its OWN requester.
	//
	// The identity is asserted BEFORE the ticket id in each pair, deliberately.
	// A mis-cut swaps the whole view, so both would fail — but the id failing
	// first would report the mis-cut as a grouping bug and say nothing about the
	// identity, which is the thing this test was added to hold.
	require.NotNil(t, board[0].Tickets[0].Requester)
	require.Equal(t, "priya@customer.example", board[0].Tickets[0].Requester.Email)
	require.Equal(t, openRef, board[0].Tickets[0].ID)

	require.NotNil(t, board[1].Tickets[0].Requester,
		"the second column's ticket lost its identity — the views were cut at the wrong offset")
	require.Equal(t, "tomas@other.example", board[1].Tickets[0].Requester.Email,
		"the second column is carrying the first column's requester")
	require.Equal(t, movedRef, board[1].Tickets[0].ID)

	require.Nil(t, board[2].Tickets[0].Requester,
		"an agent-raised ticket must stay null wherever it sits on the board")
	require.Equal(t, resolvedID, board[2].Tickets[0].ID)
}

// ── Property 5: only three fields cross to the agent ─────────────────────

// TestTicketRequester_RevocationStateNeverReachesTheAgentWire pins the WHOLE
// SHAPE of the resolved requester, not a list of fields it must not have.
//
// The forbidden-list form of this test — "assert it has no is_active" — is the
// weaker one and would pass while a future author added `session_generation`
// beside it. Comparing the exact key set means any new field fails until
// somebody decides it belongs on a ticket read. requesters carries the portal
// guard's revocation state (is_active, session_generation); that is the guard's
// business and no part of what an agent is looking at a queue to learn.
//
// FAILS-BEFORE: add `IsActive bool \`json:"is_active"\“ to requesterIdentity
// and populate it, and this fails on the exact-key comparison.
func TestTicketRequester_RevocationStateNeverReachesTheAgentWire(t *testing.T) {
	f := newRequesterFixture(t)
	token := f.signInAs(t, "priya@customer.example", "Priya Raman")
	ref := f.submit(t, token, "The exports are empty")

	// Deactivate the requester first, so the assertion is made against an
	// identity that HAS revocation state worth leaking rather than a default.
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE requesters SET is_active = false, session_generation = 7 WHERE email = $1`,
		"priya@customer.example")
	require.NoError(t, err)

	res := f.ts.getAs(t, f.agent, f.ticketPath(ref))
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(res.Body, &m))
	require.Equal(t, []string{"display_name", "email", "id"}, topLevelKeys(t, m["requester"]),
		"the agent-facing requester shape changed — every field here is copied out of the portal's own tables")

	// Belt and braces against the raw bytes: the generation cannot have reached
	// the wire under any other key either.
	require.NotContains(t, string(res.Body), "session_generation")
	require.NotContains(t, string(res.Body), "is_active")

	// The ticket is still readable. A deactivated requester's tickets do not
	// vanish from the queue — the work outlives the account.
	var got resolvedTicket
	require.NoError(t, json.Unmarshal(res.Body, &got))
	require.NotNil(t, got.Requester)
	require.Equal(t, "Priya Raman", got.Requester.DisplayName)
}

// ── Property 6: the addition did not cross back to the portal ────────────

// TestPortalWire_NeverGainsTheAgentRequesterObject is the other direction of
// the same boundary.
//
// The agent's `requester` object is an INTERNAL affordance: it exists so a
// queue can say who raised a ticket. The portal's own wire is read by somebody
// outside the organisation, and it is pinned key-for-key in
// TestPortal_WireCarriesNoContainerContext for that reason. Adding a resolved
// identity to the agent view must not have widened it. (Showing a requester
// their OWN name inside the portal is a separate, fine thing — this asserts the
// agent object did not leak across, not that the portal may never name anyone.)
//
// FAILS-BEFORE: add a `requester` field to portal's requestView and this fails
// on the request object's exact-key comparison.
func TestPortalWire_NeverGainsTheAgentRequesterObject(t *testing.T) {
	f := newRequesterFixture(t)
	token := f.signInAs(t, "priya@customer.example", "Priya Raman")
	ref := f.submit(t, token, "The exports are empty")

	// Read it once from the agent side first, so the portal assertions below are
	// made on a ticket whose requester HAS been resolved somewhere.
	agentRead := f.ts.getAs(t, f.agent, f.ticketPath(ref))
	require.Equal(t, http.StatusOK, agentRead.StatusCode)
	require.Contains(t, string(agentRead.Body), `"requester":`)

	detail := f.ts.requestAs(t, token, http.MethodGet,
		"/api/v1/portal/"+f.portalKey+"/my/requests/"+ref, nil)
	require.Equal(t, http.StatusOK, detail.StatusCode, string(detail.Body))
	require.Equal(t, []string{"messages", "request"}, topLevelKeys(t, detail.Body))

	var wrapper struct {
		Request json.RawMessage `json:"request"`
	}
	require.NoError(t, json.Unmarshal(detail.Body, &wrapper))
	require.Equal(t,
		[]string{"created_at", "description", "reference", "status", "summary", "updated_at"},
		topLevelKeys(t, wrapper.Request),
		"the portal's request shape gained a field — an external customer reads every field here")

	list := f.ts.requestAs(t, token, http.MethodGet, "/api/v1/portal/"+f.portalKey+"/my/requests", nil)
	require.Equal(t, http.StatusOK, list.StatusCode)
	var rows []json.RawMessage
	require.NoError(t, json.Unmarshal(list.Body, &rows))
	require.Len(t, rows, 1)
	require.Equal(t,
		[]string{"created_at", "reference", "status", "summary", "updated_at"},
		topLevelKeys(t, rows[0]))

	// The agent object's own field names must not appear anywhere on the portal
	// wire, under `requester` or beside it.
	for _, body := range [][]byte{detail.Body, list.Body} {
		require.NotContains(t, string(body), "display_name")
		require.NotContains(t, string(body), "requester")
	}
}

// ── The regression ───────────────────────────────────────────────────────

// TestTicketRequester_PortalTicketNoLongerRendersAsUnknownReporter is named for
// the defect, not the function.
//
// On main today a portal-raised ticket reads as "Unknown" in the agent surface.
// TicketDetailPage.tsx looks reporter_id up in the org's member list; a
// requester has no users row, so the lookup misses and the fallback renders.
// The Go half of that defect is upstream of the frontend and is what this
// asserts: the read gave the client NOTHING to render — no name, no address,
// nothing but a null reporter — so no frontend change alone could have fixed
// it. The frontend half (rendering the resolved identity, and the badge that
// marks the ticket as portal-raised) is covered by a vitest test alongside
// TicketDetailPage.
//
// FAILS-BEFORE: this is the same edit as property 1 — stop populating
// v.Requester — and it fails on the non-empty display-name assertion, which is
// literally the string the surface had nothing to put in.
func TestTicketRequester_PortalTicketNoLongerRendersAsUnknownReporter(t *testing.T) {
	f := newRequesterFixture(t)
	token := f.signInAs(t, "priya@customer.example", "Priya Raman")
	ref := f.submit(t, token, "The exports are empty")

	res := f.ts.getAs(t, f.agent, f.ticketPath(ref))
	require.Equal(t, http.StatusOK, res.StatusCode, string(res.Body))

	var got resolvedTicket
	require.NoError(t, json.Unmarshal(res.Body, &got))

	// The defect, stated as the client sees it: reporter_id is null, so the
	// member lookup that produced "Unknown" still misses — and must, because
	// there is no user. What is new is that there is something else to render.
	require.Nil(t, got.ReporterID)
	require.NotNil(t, got.Requester,
		"a portal-raised ticket gave the agent surface nothing to name the reporter with")
	require.NotEmpty(t, got.Requester.DisplayName,
		"the resolved display name is the string that used to read Unknown")
	require.Equal(t, "Priya Raman", got.Requester.DisplayName)

	// And the same on the queue, which is where an agent meets it first.
	res = f.ts.getAs(t, f.agent, f.ticketsPath())
	require.Equal(t, http.StatusOK, res.StatusCode)
	var page []resolvedTicket
	require.NoError(t, json.Unmarshal(res.Body, &page))
	require.Len(t, page, 1)
	require.NotNil(t, page[0].Requester)
	require.Equal(t, "Priya Raman", page[0].Requester.DisplayName)
}
