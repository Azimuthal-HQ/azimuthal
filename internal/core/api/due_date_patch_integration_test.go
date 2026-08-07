package api_test

// A6: the due-date lockout.
//
// A workflow transition guard can require `due_at` (workflow.FieldDueAt, and
// "Due date" in the admin editor's GUARD_FIELD_KEYS), and until this change no
// surface in the product could set one on a ticket. That is the data-integrity
// class "the server enforces a requirement the UI cannot satisfy": an
// administrator configures the guard from a shipped admin screen, and every
// transition through it is then refused forever.
//
// The Beacon half was not a missing frontend control. It was a missing write
// path: `updateTicketRequest` carried no due_at at all, and respond.DecodeJSON
// calls DisallowUnknownFields, so a PATCH mentioning due_at was rejected 400 as
// an unknown field. The whole chain below it already worked — CreateTicketParams
// had a DueAt field, TicketService.Create copied it onto the model, and both the
// INSERT and the UPDATE in internal/db/queries/tickets.sql already listed the
// column. Only the HTTP layer was absent.
//
// Fixing that exposed a second defect in the same handler. updateTicketRequest
// declared Title, Description and Priority as plain values and Update assigned
// all of them onto the stored ticket unconditionally, so a body carrying only
// due_at would decode a title of "" and be rejected by the service's
// title-required rule. A due-date-only PATCH was therefore unexpressible even
// once the field existed. Vector fixed exactly this shape on the item side (see
// item_patch_integration_test.go); Beacon never did, because nothing had ever
// called its PATCH.
//
// Which of these fail against the old handler, and how:
//   - the ticket set/persist cases: 400, unknown field "due_at"
//   - TestUpdateTicket_DueOnlyPatchLeavesEverythingElse: 400, title required
//   - TestUpdateTicket_OmittedDueAtIsNotCleared: cannot run at all — there is
//     no way to give the ticket a due date to preserve
//
// Every ticket case above was mutation-checked: deleting the `req.DueAt.Set`
// guard from applyTicketPatch fails TestUpdateTicket_OmittedDueAtIsNotCleared
// and nothing else in the internal suite, so that test is the only thing
// standing between this column and the defect it already suffered once.
//
// The single item case at the bottom is scoped narrowly and says why.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Two distinguishable dates. Midnight UTC is what <input type="date"> plus
// toRFC3339Date produces, so these are the exact values the rail sends.
const (
	dueFirst  = "2026-09-01T00:00:00Z"
	dueSecond = "2026-10-15T00:00:00Z"
)

// requireDueAt asserts that a wire due_at names the same instant as want.
//
// This cannot be a string comparison, and finding out why is worth the helper.
// The server does not echo the timestamp it was given: a due date sent as
// 2026-09-01T00:00:00Z comes back as 2026-08-31T20:00:00-04:00 — the same
// instant, serialized in the database session's zone rather than in UTC. Both
// modules do it and they do it identically, so it is a property of the pgx
// timestamptz scan, not of either handler.
//
// That is exactly the serialization formatUTCDate exists to survive on the
// frontend. Slicing the first ten characters of that string to seed an
// <input type="date"> would render 2026-08-31 for a due date of 2026-09-01 —
// the off-by-one its doc comment warns about — which is why the rail parses the
// timestamp instead of slicing it, and why a string-equality assertion here
// would have quietly encoded the wrong expectation.
func requireDueAt(t *testing.T, want string, got any, msg string) {
	t.Helper()
	s, ok := got.(string)
	require.True(t, ok, "%s: due_at must be a string, got %#v", msg, got)

	wantT, err := time.Parse(time.RFC3339, want)
	require.NoError(t, err, "the test's own fixture must be RFC3339")
	gotT, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err, "%s: due_at must be RFC3339 on the wire, got %q", msg, s)

	require.True(t, wantT.Equal(gotT), "%s: want the instant %s, got %s", msg, want, s)
	// The property the UI actually depends on: whatever the offset, the UTC
	// calendar date is the one the user picked.
	require.Equal(t, wantT.UTC().Format(time.DateOnly), gotT.UTC().Format(time.DateOnly),
		"%s: the UTC calendar date must round-trip", msg)
}

// --- Beacon: the lockout being closed ---

// ticketsPathForDue returns the ticket-collection URL for a fresh beacon space.
func ticketsPathForDue(t *testing.T, ts *testServer) string {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "beacon")
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, space.ID)
}

// createTicketForDue posts a ticket and returns its collection path and id.
// due is sent only when non-empty, so callers can exercise both a ticket born
// with a due date and one born without.
func createTicketForDue(t *testing.T, ts *testServer, due string) (string, string) {
	t.Helper()
	base := ticketsPathForDue(t, ts)
	body := map[string]any{
		"title":       "Original Title",
		"description": "Original description",
		"priority":    "high",
	}
	if due != "" {
		body["due_at"] = due
	}
	res := ts.post(t, base, body, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", res.Body)

	created := decodeJSONMap(t, res.Body)
	id, ok := created["id"].(string)
	require.True(t, ok, "created ticket must carry an id: %s", res.Body)
	return base, id
}

func TestCreateTicket_DueAtIsPersisted(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, dueFirst)

	// Read it back rather than trusting the create response: CreateTicketParams
	// carried DueAt long before anything populated it, so the field being on the
	// response proves only that the handler echoed what it built.
	res := ts.get(t, base+"/"+id, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	requireDueAt(t, dueFirst, got["due_at"], "a ticket must be able to be born with a due date")
}

func TestCreateTicket_WithoutDueAtIsNull(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, "")

	res := ts.get(t, base+"/"+id, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Contains(t, got, "due_at", "the key is always present: the Go field has no omitempty")
	require.Nil(t, got["due_at"], "a ticket created without a due date must have none")
}

// TestUpdateTicket_DueOnlyPatchSetsIt is the assertion that the lockout is
// closed. It sends exactly what the due-date control on ticket detail sends,
// and nothing else.
func TestUpdateTicket_DueOnlyPatchSetsIt(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, "")

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": dueFirst}, true)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a patch carrying only due_at must succeed; body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	requireDueAt(t, dueFirst, got["due_at"], "the due date must actually have been set")

	// And it survives the round trip to postgres, not merely the response.
	after := ts.get(t, base+"/"+id, true)
	require.Equal(t, http.StatusOK, after.StatusCode, "body: %s", after.Body)
	requireDueAt(t, dueFirst, decodeJSONMap(t, after.Body)["due_at"], "the due date must persist")
}

// TestUpdateTicket_DueOnlyPatchLeavesEverythingElse is the other half of the
// same request: what a due-date-only body must NOT do. Against the old handler
// this was a 400 (title required); the failure mode it guards against now is
// silent, so the assertions are per-field.
func TestUpdateTicket_DueOnlyPatchLeavesEverythingElse(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, "")

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": dueFirst}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Equal(t, "Original Title", got["title"], "an omitted title must be left alone, not blanked")
	require.Equal(t, "Original description", got["description"], "an omitted description must be left alone")
	require.Equal(t, "high", got["priority"], "an omitted priority must be left alone")
}

// TestUpdateTicket_OmittedDueAtIsNotCleared is the anti-clear regression, and
// the reason the field is respond.OptionalField rather than a plain pointer.
//
// A plain *time.Time cannot tell "the client did not mention due_at" from "the
// client sent null", so the handler would resolve both as "clear it" — and
// every edit that did not happen to resend the due date would destroy it. That
// is not hypothetical: it is what shipped on the item side and silently wiped
// every item's due date until optionalField was introduced.
func TestUpdateTicket_OmittedDueAtIsNotCleared(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, dueFirst)

	// A perfectly ordinary edit that says nothing about the due date.
	res := ts.patch(t, base+"/"+id, map[string]any{"title": "Renamed"}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Equal(t, "Renamed", got["title"], "the rename must have happened")
	requireDueAt(t, dueFirst, got["due_at"],
		"a PATCH that never mentions due_at must leave the stored due date alone")

	after := ts.get(t, base+"/"+id, true)
	requireDueAt(t, dueFirst, decodeJSONMap(t, after.Body)["due_at"],
		"and the surviving due date must be the stored one, not an echo")
}

// TestUpdateTicket_ExplicitNullDueAtClearsIt is the negative half. Making
// absent mean "leave alone" must not make clearing impossible — that is the
// whole point of three states rather than two, and it is what the rail's
// emptied date input relies on.
func TestUpdateTicket_ExplicitNullDueAtClearsIt(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, dueFirst)

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": nil}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Nil(t, got["due_at"], "an explicit null due_at must clear it")
	require.Equal(t, "Original Title", got["title"], "clearing a due date must not disturb the title")

	after := ts.get(t, base+"/"+id, true)
	require.Nil(t, decodeJSONMap(t, after.Body)["due_at"], "and the clear must persist")
}

func TestUpdateTicket_DueAtCanBeChanged(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, dueFirst)

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": dueSecond}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)
	requireDueAt(t, dueSecond, decodeJSONMap(t, res.Body)["due_at"],
		"replacing one due date with another must write the new one")
}

// TestUpdateTicket_BareCalendarDateIsRejected pins the encoding the frontend
// helper exists to satisfy. toRFC3339Date turns the YYYY-MM-DD an
// <input type="date"> produces into a timestamp precisely because this is a
// 400 — if that ever became accepted, the helper would look like ceremony and
// the next caller would drop it.
func TestUpdateTicket_BareCalendarDateIsRejected(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, "")

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": "2026-09-01"}, true)
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"a bare calendar date is not RFC3339 and must be refused; body: %s", res.Body)
}

// TestUpdateTicket_ExplicitEmptyTitleIsStillRejected guards the partial-PATCH
// change from over-reaching: making the fields optional must not make the
// title-required rule optional. This is what separates "absent" from "empty".
func TestUpdateTicket_ExplicitEmptyTitleIsStillRejected(t *testing.T) {
	ts := newTestServer(t)
	base, id := createTicketForDue(t, ts, "")

	res := ts.patch(t, base+"/"+id, map[string]any{"title": ""}, true)
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"an explicitly empty title must still be rejected; body: %s", res.Body)
}

// --- Vector: the item side, which needed no server change ---
//
// Only one case, and the scope is deliberate.
//
// A first draft mirrored the whole Beacon set here — absent-is-not-cleared,
// explicit-null-clears — on the belief that the item due_at tri-state was
// uncovered. It is not. TestItemPatch_AbsentAssigneeAndDueDateAreNotCleared in
// item_kind_patch_integration_test.go already pins all three readings, and a
// suite-wide mutation run proved it: deleting the `req.DueAt.Set` guard from
// applyItemPatch fails that test. Those drafts asserted nothing that was not
// already asserted, and coverage that cannot fail on its own reads as
// protection while providing none.
//
// What survives is the one thing that test does not do. It checks due_at with
// require.NotNil, so it would pass against a handler that stored the wrong
// date; and it always sends due_at alongside assignee_id, never alone. This
// case sends the exact body the new rail control sends — due_at and nothing
// else — and asserts the instant itself round-trips.

func createItemForDue(t *testing.T, ts *testServer, due string) (string, string) {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, space.ID)

	body := map[string]any{
		"title":       "Original Title",
		"description": "Original description",
		"kind":        "task",
		"priority":    "high",
	}
	if due != "" {
		body["due_at"] = due
	}
	res := ts.post(t, base, body, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", res.Body)

	id, ok := decodeJSONMap(t, res.Body)["id"].(string)
	require.True(t, ok, "created item must carry an id: %s", res.Body)
	return base, id
}

func TestUpdateItem_DueOnlyPatchSetsIt(t *testing.T) {
	ts := newTestServer(t)
	base, id := createItemForDue(t, ts, "")

	res := ts.patch(t, base+"/"+id, map[string]any{"due_at": dueFirst}, true)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a patch carrying only due_at must succeed; body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	requireDueAt(t, dueFirst, got["due_at"], "the due date must actually have been set")
	require.Equal(t, "Original Title", got["title"], "an omitted title must be left alone")

	after := ts.get(t, base+"/"+id, true)
	requireDueAt(t, dueFirst, decodeJSONMap(t, after.Body)["due_at"], "the due date must persist")
}
