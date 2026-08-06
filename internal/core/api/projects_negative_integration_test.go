package api_test

// Negative-path HTTP coverage for the project surface
// (internal/core/api/projects/handler.go and board_handler.go).
//
// Everything here is a refusal, and every refusal is asserted positively: the
// exact status code, the documented error code, and — where the message is what
// distinguishes one branch from its neighbours — the message itself. A test
// that only asserted "not 500" would assert nothing, because a handler that
// stopped parsing its path parameters altogether would still pass it.
//
// Two families of refusal are deliberately ABSENT, because they are unreachable
// through the wired router rather than untested:
//
//   - A malformed {spaceID} on any space-scoped project route. RequireSpaceInOrg
//     (router.go, buildSpaceGuards) parses spaceID and answers 400 before the
//     handler runs, so spaceIDFromURL's error branch cannot be reached from a
//     request. The client-visible behaviour is covered — by the middleware's own
//     tests — but no test here can attribute it to the handler.
//   - A malformed {orgID} on any project route. ResolveAccess parses orgID at
//     the /orgs/{orgID} group and answers 400 first, for the same reason.
//
// The ids the handlers DO parse themselves — itemID, sprintID, relationID,
// labelID, typeID, fieldID, columnID — have no middleware in front of them and
// are covered below.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// projNegFixture is one vector space carrying an item and a sprint owned by the
// org admin, plus a contributor persona. Contributor is the persona that
// matters for the capability rows: it clears the subtree's
// RequireWriteFloor(CapCreateItems), so a 403 it receives can only come from the
// handler's own access.Can check. A viewer would be refused by the floor and
// would prove nothing about the handler.
type projNegFixture struct {
	ts      *testServer
	spaceID string
	// base is the space's project subtree; orgBase the org-scoped families
	// (labels, item types, custom fields) the same handler serves.
	base    string
	orgBase string

	contrib    testutil.User
	contribTok string
	// agent holds CapEditAnyItem, the capability the contributor lacks. It is
	// the control persona: it makes the identical requests and must succeed,
	// which is what proves a contributor's 403 is the capability and not a
	// malformed request or a broken route.
	agent    testutil.User
	agentTok string

	// ownerItem and ownerSprint are reported/created by the org admin, so the
	// contributor is never their author.
	ownerItem   string
	ownerSprint string
}

func newProjNegFixture(t *testing.T) *projNegFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &projNegFixture{ts: ts}
	f.spaceID = createScopedSpace(t, ts, "Proj Neg Space", "proj-neg-space", "vector")
	f.base = fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects", ts.OrgID, f.spaceID)
	f.orgBase = fmt.Sprintf("/api/v1/orgs/%s", ts.OrgID)

	// Both personas are org "member": an owner or admin would clear every
	// capability check through the ADR-0007 org-admin bypass, and the denials
	// below would prove nothing about the grants.
	grantee := func(role access.Role) (testutil.User, string) {
		u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		_, err := ts.GrantService.Create(context.Background(), ts.OrgID, uuid.MustParse(f.spaceID),
			access.SubjectUser, u.ID, role, ts.UserID)
		require.NoError(t, err)
		return u, ts.tokenFor(t, u.ID, u.Email)
	}
	f.contrib, f.contribTok = grantee(access.RoleContributor)
	f.agent, f.agentTok = grantee(access.RoleAgent)

	f.ownerItem = f.createItem(t, ts.Token, "Owner Item")
	f.ownerSprint = f.createSprint(t, "Owner Sprint")
	return f
}

// as issues a request as the holder of token, JSON-encoding body when present.
func (f *projNegFixture) as(t *testing.T, token, method, path string, body any) httpResult {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path), reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return f.ts.do(t, req)
}

// raw sends body verbatim, which is how a body that will not decode gets onto
// the wire — json.Marshal cannot produce one.
func (f *projNegFixture) raw(t *testing.T, token, method, path, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path),
		bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return f.ts.do(t, req)
}

func (f *projNegFixture) createItem(t *testing.T, token, title string) string {
	t.Helper()
	r := f.as(t, token, http.MethodPost, f.base+"/items",
		map[string]any{"title": title, "kind": "task", "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	var item struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &item))
	require.NotEmpty(t, item.ID)
	return item.ID
}

func (f *projNegFixture) createSprint(t *testing.T, name string) string {
	t.Helper()
	r := f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints", map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create sprint: %s", r.Body)
	var sprint struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &sprint))
	require.NotEmpty(t, sprint.ID)
	return sprint.ID
}

// projNegRequireError is the positive form of a refusal: exact status, JSON
// envelope, exact error code, a non-empty message, and — when msgContains is
// given — the phrase that identifies which branch answered. The message check
// is what stops one handler's 400 from standing in for another's.
func projNegRequireError(t *testing.T, r httpResult, status int, code, msgContains string) {
	t.Helper()
	require.Equal(t, status, r.StatusCode, "body: %s", r.Body)
	require.Contains(t, r.ContentType, "application/json", "a refusal must carry the JSON envelope")
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "error envelope expected, got: %s", r.Body)
	require.Equal(t, code, body.Error.Code)
	require.NotEmpty(t, body.Error.Message, "an error envelope must carry a message")
	if msgContains != "" {
		require.Contains(t, body.Error.Message, msgContains,
			"the message must name what was rejected")
	}
}

// projNegRoute is one row of the tables below.
type projNegRoute struct {
	name   string
	method string
	path   string
	body   any
}

// --- Malformed path parameters the handlers parse themselves ---

// TestProjectsNeg_MalformedPathIDsAre400 covers every id the project handlers
// parse with uuid.Parse and answer for themselves.
//
// Defect it catches: an id that never reaches a parse — because the parse was
// dropped, or moved below a query that would run with uuid.Nil — stops being a
// 400. The concrete failure is uuid.Nil reaching the database as a real
// argument: "not-a-uuid" becomes a lookup for the zero uuid, which finds
// nothing and answers 404 or 500 instead of telling the client its URL is
// malformed. The message assertions pin WHICH id was rejected, so a handler
// that parsed the wrong parameter would fail here too.
//
// The requests are made as the org admin, so nothing upstream refuses them:
// every one of these must reach the handler and be rejected on its path
// parameter alone.
func TestProjectsNeg_MalformedPathIDsAre400(t *testing.T) {
	f := newProjNegFixture(t)
	const bad = "not-a-uuid"

	// Control: the same routes answer normally when the id parses. Without it,
	// a subtree that 400'd on everything would satisfy every row below.
	r := f.as(t, f.ts.Token, http.MethodGet, f.base+"/items/"+f.ownerItem, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "a well-formed item id must be served: %s", r.Body)
	r = f.as(t, f.ts.Token, http.MethodGet, f.base+"/sprints/"+f.ownerSprint, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "a well-formed sprint id must be served: %s", r.Body)

	t.Run("item id", func(t *testing.T) {
		for _, tc := range []projNegRoute{
			{"get", http.MethodGet, f.base + "/items/" + bad, nil},
			{"patch", http.MethodPatch, f.base + "/items/" + bad, map[string]any{"title": "x"}},
			{"delete", http.MethodDelete, f.base + "/items/" + bad, nil},
			{"status", http.MethodPost, f.base + "/items/" + bad + "/status", map[string]any{"status": "done"}},
			{"assign sprint", http.MethodPost, f.base + "/items/" + bad + "/sprint", map[string]any{"sprint_id": nil}},
			{"rank", http.MethodPost, f.base + "/items/" + bad + "/rank", map[string]any{}},
			{"get fields", http.MethodGet, f.base + "/items/" + bad + "/fields", nil},
			{"set field", http.MethodPut, f.base + "/items/" + bad + "/fields/squad", map[string]any{"value": "x"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
					http.StatusBadRequest, "BAD_REQUEST", "invalid item ID")
			})
		}

		// The relation routes moved onto the entity-generic satellite (A4),
		// whose shared core answers the same "invalid entity ID" the comments
		// core does for all three subtrees. The assertion still pins the exact
		// message — only the expected literal moved with the handler.
		for _, tc := range []projNegRoute{
			{"list relations", http.MethodGet, f.base + "/items/" + bad + "/relations", nil},
			{"create relation", http.MethodPost, f.base + "/items/" + bad + "/relations",
				map[string]any{"to_id": uuid.NewString(), "kind": "relates_to"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
					http.StatusBadRequest, "BAD_REQUEST", "invalid entity ID")
			})
		}
	})

	t.Run("sprint id", func(t *testing.T) {
		for _, tc := range []projNegRoute{
			{"get", http.MethodGet, f.base + "/sprints/" + bad, nil},
			{"update", http.MethodPut, f.base + "/sprints/" + bad, map[string]any{"name": "x"}},
			{"start", http.MethodPost, f.base + "/sprints/" + bad + "/start", nil},
			{"complete", http.MethodPost, f.base + "/sprints/" + bad + "/complete", nil},
			{"items", http.MethodGet, f.base + "/sprints/" + bad + "/items", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
					http.StatusBadRequest, "BAD_REQUEST", "invalid sprint ID")
			})
		}
	})

	t.Run("other ids", func(t *testing.T) {
		for _, tc := range []struct {
			projNegRoute
			wantMessage string
		}{
			{projNegRoute{"relation", http.MethodDelete, f.base + "/relations/" + bad, nil},
				"invalid relation ID"},
			{projNegRoute{"label", http.MethodDelete, f.orgBase + "/labels/" + bad, nil},
				"invalid label ID"},
			{projNegRoute{"item type patch", http.MethodPatch, f.orgBase + "/item-types/" + bad,
				map[string]any{"name": "x"}}, "invalid item type ID"},
			{projNegRoute{"item type delete", http.MethodDelete, f.orgBase + "/item-types/" + bad, nil},
				"invalid item type ID"},
			{projNegRoute{"custom field patch", http.MethodPatch, f.orgBase + "/custom-fields/" + bad,
				map[string]any{"name": "x"}}, "invalid custom field ID"},
			{projNegRoute{"custom field delete", http.MethodDelete, f.orgBase + "/custom-fields/" + bad, nil},
				"invalid custom field ID"},
			{projNegRoute{"board column", http.MethodDelete, f.base + "/board/config/columns/" + bad,
				map[string]any{"remap_to": uuid.NewString()}}, "invalid column ID"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
					http.StatusBadRequest, "BAD_REQUEST", tc.wantMessage)
			})
		}
	})
}

// --- Bodies that will not decode ---

// TestProjectsNeg_MalformedJSONBodyIs400 sends a truncated object to every
// project route that reads a body.
//
// Defect it catches: a handler that ignores the decode error and proceeds with a
// zero-valued request struct. That is silent corruption, not a crash — a PATCH
// whose body failed to parse would apply the zero value of every field, and a
// board save whose body failed to parse would store a board with no columns.
// Asserting the exact "invalid request body" message is what distinguishes this
// from a 400 raised by some later validation on the zero value.
func TestProjectsNeg_MalformedJSONBodyIs400(t *testing.T) {
	f := newProjNegFixture(t)
	// Well-formed JSON up to the point it stops: enough to get past a router
	// that only sniffs the first byte, and still undecodable.
	const truncated = `{"title": "unterminated`
	someUUID := uuid.NewString()

	for _, tc := range []projNegRoute{
		{"create item", http.MethodPost, f.base + "/items", nil},
		{"update item", http.MethodPatch, f.base + "/items/" + f.ownerItem, nil},
		{"item status", http.MethodPost, f.base + "/items/" + f.ownerItem + "/status", nil},
		{"assign sprint", http.MethodPost, f.base + "/items/" + f.ownerItem + "/sprint", nil},
		{"rank item", http.MethodPost, f.base + "/items/" + f.ownerItem + "/rank", nil},
		{"create relation", http.MethodPost, f.base + "/items/" + f.ownerItem + "/relations", nil},
		{"set item field", http.MethodPut, f.base + "/items/" + f.ownerItem + "/fields/squad", nil},
		{"create sprint", http.MethodPost, f.base + "/sprints", nil},
		{"update sprint", http.MethodPut, f.base + "/sprints/" + f.ownerSprint, nil},
		{"complete sprint", http.MethodPost, f.base + "/sprints/" + f.ownerSprint + "/complete", nil},
		{"move to sprint", http.MethodPost, f.base + "/backlog/move-to-sprint", nil},
		{"move to backlog", http.MethodPost, f.base + "/backlog/move-to-backlog", nil},
		{"save board config", http.MethodPut, f.base + "/board/config", nil},
		{"delete board column", http.MethodDelete, f.base + "/board/config/columns/" + someUUID, nil},
		{"create label", http.MethodPost, f.orgBase + "/labels", nil},
		{"create item type", http.MethodPost, f.orgBase + "/item-types", nil},
		{"update item type", http.MethodPatch, f.orgBase + "/item-types/" + someUUID, nil},
		{"create custom field", http.MethodPost, f.orgBase + "/custom-fields", nil},
		{"update custom field", http.MethodPatch, f.orgBase + "/custom-fields/" + someUUID, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projNegRequireError(t, f.raw(t, f.ts.Token, tc.method, tc.path, truncated),
				http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		})
	}
}

// TestProjectsNeg_MistypedPatchFieldsAre400 covers optionalField's own decoder,
// which is the only thing standing between a mistyped tri-state field and the
// stored value.
//
// assignee_id and due_at are optionalField, so they carry three states —
// absent, explicit null, and a value — and their UnmarshalJSON is the single
// place a value is parsed. Defect it catches: that decoder swallowing its error
// and leaving Value nil while Set stays true. Set-with-nil-Value means "the
// client explicitly sent null", i.e. CLEAR IT — so a typo'd assignee_id would
// not be rejected, it would silently unassign the item, and a typo'd due_at
// would silently wipe the due date and drop the item off the roadmap. That is
// the exact failure shape the tri-state was introduced to end.
func TestProjectsNeg_MistypedPatchFieldsAre400(t *testing.T) {
	f := newProjNegFixture(t)
	itemPath := f.base + "/items/" + f.ownerItem

	for _, tc := range []struct {
		name string
		body string
	}{
		{"assignee_id is not a uuid", `{"assignee_id": "not-a-uuid"}`},
		{"assignee_id is a number", `{"assignee_id": 42}`},
		{"due_at is not a timestamp", `{"due_at": "tomorrow"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projNegRequireError(t, f.raw(t, f.ts.Token, http.MethodPatch, itemPath, tc.body),
				http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		})
	}

	// The refusals wrote nothing: the item still has no assignee and no due
	// date, rather than having been "cleared" by a decode that gave up. A
	// swallowed decode error would produce the same 200-less outcome for the
	// client but a mutated row here.
	r := f.as(t, f.ts.Token, http.MethodGet, itemPath, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "read back the item: %s", r.Body)
	var item map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &item))
	require.Nil(t, item["assignee_id"], "a refused patch must not have assigned or unassigned")
	require.Nil(t, item["due_at"], "a refused patch must not have touched the due date")

	// Control: the same fields in their correct types are accepted, so the
	// refusals above are about the type and not about the fields being
	// rejected outright.
	r = f.as(t, f.ts.Token, http.MethodPatch, itemPath,
		map[string]any{"assignee_id": f.ts.UserID.String(), "due_at": "2026-12-31T00:00:00Z"})
	require.Equal(t, http.StatusOK, r.StatusCode, "well-typed tri-state fields are accepted: %s", r.Body)
}

// TestProjectsNeg_SearchLimitFallsBackRatherThanFailing pins the read side's
// tolerance for a junk limit.
//
// SearchItems parses ?limit and ignores anything unparseable, non-positive, or
// above 200, falling back to 50. Defect it catches: that fallback turning into
// a passthrough. A limit of 0 reaching the query is a search that returns
// nothing at all while answering 200 — the worst kind of failure, because the
// caller reads it as "no matches". The assertion is therefore on the RESULTS,
// not the status: a 200 alone would pass with the fallback deleted.
func TestProjectsNeg_SearchLimitFallsBackRatherThanFailing(t *testing.T) {
	f := newProjNegFixture(t)

	requireFinds := func(t *testing.T, query string) {
		t.Helper()
		r := f.as(t, f.ts.Token, http.MethodGet, f.base+"/items/search?q=Owner"+query, nil)
		require.Equal(t, http.StatusOK, r.StatusCode, "search%s: %s", query, r.Body)
		var items []map[string]any
		require.NoError(t, json.Unmarshal(r.Body, &items))
		require.NotEmpty(t, items, "a junk limit must fall back to the default, not empty the results")
	}

	requireFinds(t, "")
	requireFinds(t, "&limit=not-a-number")
	requireFinds(t, "&limit=0")
	requireFinds(t, "&limit=-5")
	requireFinds(t, "&limit=100000")
	requireFinds(t, "&limit=5")
}

// TestProjectsNeg_CompleteSprintStillAcceptsAnEmptyBody is the control that
// keeps the row above honest.
//
// CompleteSprint decodes by hand precisely so it can tolerate io.EOF: an empty
// body is the documented "return the incomplete items to the backlog" case.
// Without this control, "reject anything that does not decode" could be
// satisfied by rejecting the empty body too, which would break every client
// that completes a sprint without a carry-over target.
func TestProjectsNeg_CompleteSprintStillAcceptsAnEmptyBody(t *testing.T) {
	f := newProjNegFixture(t)

	r := f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/start", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "start sprint: %s", r.Body)

	r = f.raw(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/complete", "")
	require.Equal(t, http.StatusOK, r.StatusCode,
		"an empty completion body means 'back to the backlog' and must not be a 400: %s", r.Body)
}

// --- Well-formed ids that name nothing ---

// TestProjectsNeg_UnknownUUIDIs404 walks the routes that load a row before
// acting on it, with a syntactically perfect id that names nothing.
//
// Defect it catches: a missing row surfacing as a 500 (the handleProjectError
// default branch) or, worse, as a 200 over a zero-valued entity. Both have
// shipped in this repository's neighbours: an adapter that forgets to map
// pgx.ErrNoRows onto projects.ErrNotFound falls through to "project operation
// failed", and this row is what says so. The NOT_FOUND code assertion is
// load-bearing — a 404 carrying INTERNAL_ERROR would still be wrong.
func TestProjectsNeg_UnknownUUIDIs404(t *testing.T) {
	f := newProjNegFixture(t)
	missing := uuid.NewString()

	// Control: an id that DOES name a row is served. The 404s below are
	// therefore about the row being absent, not about the lookup being broken
	// for every input.
	r := f.as(t, f.ts.Token, http.MethodGet, f.base+"/items/"+f.ownerItem, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "an existing item must be found: %s", r.Body)

	for _, tc := range []projNegRoute{
		{"get item", http.MethodGet, f.base + "/items/" + missing, nil},
		{"update item", http.MethodPatch, f.base + "/items/" + missing, map[string]any{"title": "x"}},
		{"delete item", http.MethodDelete, f.base + "/items/" + missing, nil},
		{"rank item", http.MethodPost, f.base + "/items/" + missing + "/rank", map[string]any{}},
		{"get sprint", http.MethodGet, f.base + "/sprints/" + missing, nil},
		{"update sprint", http.MethodPut, f.base + "/sprints/" + missing, map[string]any{"name": "x"}},
		{"start sprint", http.MethodPost, f.base + "/sprints/" + missing + "/start", nil},
		{"complete sprint", http.MethodPost, f.base + "/sprints/" + missing + "/complete", nil},
		{"move to unknown sprint", http.MethodPost, f.base + "/backlog/move-to-sprint",
			map[string]any{"item_id": f.ownerItem, "sprint_id": missing}},
		{"patch item type", http.MethodPatch, f.orgBase + "/item-types/" + missing, map[string]any{"name": "x"}},
		{"delete item type", http.MethodDelete, f.orgBase + "/item-types/" + missing, nil},
		{"patch custom field", http.MethodPatch, f.orgBase + "/custom-fields/" + missing, map[string]any{"name": "x"}},
		{"delete custom field", http.MethodDelete, f.orgBase + "/custom-fields/" + missing, nil},
		{"set an undefined field", http.MethodPut, f.base + "/items/" + f.ownerItem + "/fields/never_defined",
			map[string]any{"value": "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
				http.StatusNotFound, "NOT_FOUND", "")
		})
	}
}

// --- Capability refusals above the write floor ---

// TestProjectsNeg_ContributorIsRefusedAgentWrites is the capability half, and
// the persona is the whole point.
//
// Every route below is guarded in-handler by access.Can(CapEditAnyItem) — an
// agent-tier capability — while the subtree's middleware floor is only
// CapCreateItems. A viewer is refused by the floor and would pass this test with
// every in-handler check deleted; a contributor clears the floor and can only be
// refused by the handler. The first assertion proves the floor really is
// cleared, so a 403 below cannot be the middleware talking.
//
// Defect it catches: deleting any one of these access.Can lines. Without the
// check in AssignToSprint, anyone who can file an item could move other
// people's work between sprints; without it in MoveToBacklog, they could empty
// an active sprint. Both would leave every other test in this package green.
//
// The agent block at the end is what makes each refusal attributable. It
// replays the identical seven requests as a persona differing in exactly one
// respect — the role on its space grant — and requires every one to succeed. A
// row that 403'd because the body was wrong, or the route was broken, would
// fail there instead of passing quietly here.
func TestProjectsNeg_ContributorIsRefusedAgentWrites(t *testing.T) {
	f := newProjNegFixture(t)

	// Premise: the contributor clears RequireWriteFloor(CapCreateItems) on this
	// very subtree. Without this the 403s below prove nothing.
	contribItem := f.createItem(t, f.contribTok, "Contributor Item")

	refused := []projNegRoute{
		{"assign to sprint", http.MethodPost, f.base + "/items/" + contribItem + "/sprint",
			map[string]any{"sprint_id": f.ownerSprint}},
		{"rank item", http.MethodPost, f.base + "/items/" + contribItem + "/rank", map[string]any{}},
		{"update sprint", http.MethodPut, f.base + "/sprints/" + f.ownerSprint,
			map[string]any{"name": "Renamed by a contributor"}},
		{"start sprint", http.MethodPost, f.base + "/sprints/" + f.ownerSprint + "/start", nil},
		{"move to sprint", http.MethodPost, f.base + "/backlog/move-to-sprint",
			map[string]any{"item_id": contribItem, "sprint_id": f.ownerSprint}},
		{"move to backlog", http.MethodPost, f.base + "/backlog/move-to-backlog",
			map[string]any{"item_id": contribItem}},
		{"complete sprint", http.MethodPost, f.base + "/sprints/" + f.ownerSprint + "/complete", nil},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			projNegRequireError(t, f.as(t, f.contribTok, tc.method, tc.path, tc.body),
				http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
		})
	}

	// Every refusal above was the capability and nothing else. The rows run in
	// an order the sprint state machine accepts: start, move items about, then
	// complete.
	for _, tc := range refused {
		t.Run("agent/"+tc.name, func(t *testing.T) {
			r := f.as(t, f.agentTok, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusOK, r.StatusCode,
				"an agent holds edit_any_item and must be allowed: %s", r.Body)
		})
	}
}

// TestProjectsNeg_ContributorCannotTouchAnotherUsersItem covers the
// edit_own/edit_any split on the two item routes that carry it and had no
// coverage: DELETE, and the per-item custom-field write.
//
// Same persona logic as above — the contributor holds edit_own_items, so only
// access.CanEditEntity's authorship comparison can refuse it. The positive
// control on the contributor's OWN item is what proves that: delete the
// CanEditEntity check and the refusals become 204/200 while the control keeps
// passing, so the pair fails in exactly one direction.
//
// Defect it catches: a contributor deleting anyone's item, or rewriting the
// custom-field values on work they do not own.
func TestProjectsNeg_ContributorCannotTouchAnotherUsersItem(t *testing.T) {
	f := newProjNegFixture(t)

	// An active custom field to write through. Defined by the org admin, since
	// definitions are an org-admin surface.
	r := f.as(t, f.ts.Token, http.MethodPost, f.orgBase+"/custom-fields",
		map[string]any{"name": "Squad", "field_type": "text"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "define custom field: %s", r.Body)
	var def struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &def))
	require.Equal(t, "squad", def.Slug)
	attachField(t, f.ts, def.ID, f.spaceID, "project_item", false)

	contribItem := f.createItem(t, f.contribTok, "Contributor Own Item")

	// The contributor may write the field on its own item...
	r = f.as(t, f.contribTok, http.MethodPut, f.base+"/items/"+contribItem+"/fields/"+def.Slug,
		map[string]any{"value": "platform"})
	require.Equal(t, http.StatusOK, r.StatusCode, "contributor writes a field on its own item: %s", r.Body)

	// ...but not on the admin's.
	projNegRequireError(t, f.as(t, f.contribTok, http.MethodPut,
		f.base+"/items/"+f.ownerItem+"/fields/"+def.Slug, map[string]any{"value": "hijack"}),
		http.StatusForbidden, "FORBIDDEN", "insufficient permissions")

	projNegRequireError(t, f.as(t, f.contribTok, http.MethodDelete, f.base+"/items/"+f.ownerItem, nil),
		http.StatusForbidden, "FORBIDDEN", "insufficient permissions")

	// The refused delete did not happen.
	r = f.as(t, f.ts.Token, http.MethodGet, f.base+"/items/"+f.ownerItem, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "a refused delete must leave the item alive: %s", r.Body)

	// ...and deleting its own item is permitted, so the refusal above was
	// authorship and not the route.
	r = f.as(t, f.contribTok, http.MethodDelete, f.base+"/items/"+contribItem, nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "contributor deletes its own item: %s", r.Body)
}

// --- Sprint state-machine refusals ---

// TestProjectsNeg_SprintLifecycleConflicts covers the three conflict branches of
// handleProjectError that the happy-path sprint tests never reach, and the
// carry-over validation.
//
// Defect it catches: a sprint state machine that stopped validating. Completing
// a planned sprint would skip the active state entirely; starting a second
// sprint while one is active would leave a space with two active sprints, which
// every "the active sprint" query in the product assumes cannot happen. The
// distinct error CODES matter as much as the 409: INVALID_TRANSITION and
// CONFLICT are what let a client tell "you cannot do that yet" from "something
// else already holds this".
func TestProjectsNeg_SprintLifecycleConflicts(t *testing.T) {
	f := newProjNegFixture(t)
	second := f.createSprint(t, "Second Sprint")

	// A planned sprint cannot be completed — it was never started.
	projNegRequireError(t,
		f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/complete", nil),
		http.StatusConflict, "INVALID_TRANSITION", "")

	r := f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/start", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "start the first sprint: %s", r.Body)

	// Starting it again is not a no-op, it is an invalid transition.
	projNegRequireError(t,
		f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/start", nil),
		http.StatusConflict, "INVALID_TRANSITION", "")

	// A second active sprint in the same space is refused with a DIFFERENT code:
	// this one is about the space's state, not this sprint's.
	projNegRequireError(t,
		f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+second+"/start", nil),
		http.StatusConflict, "CONFLICT", "active")

	// Carry-over targets: a sprint that does not exist, and the sprint being
	// completed, are both refused as validation failures rather than 500s.
	projNegRequireError(t,
		f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/complete",
			map[string]any{"next_sprint_id": uuid.NewString()}),
		http.StatusBadRequest, "VALIDATION_ERROR", "next sprint")
	projNegRequireError(t,
		f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/complete",
			map[string]any{"next_sprint_id": f.ownerSprint}),
		http.StatusBadRequest, "VALIDATION_ERROR", "next sprint")

	// Control: a legitimate carry-over target completes the sprint. Without
	// this, the two refusals above could be a completion path that is simply
	// broken for every input.
	r = f.as(t, f.ts.Token, http.MethodPost, f.base+"/sprints/"+f.ownerSprint+"/complete",
		map[string]any{"next_sprint_id": second})
	require.Equal(t, http.StatusOK, r.StatusCode, "carry over into an open sprint: %s", r.Body)
}

// --- Validation refusals ---

// TestProjectsNeg_ValidationRefusalsAre400 gathers the request-shape refusals
// across the handler's four families: relations, the query-parameter reads,
// labels, and the org-scoped schema surfaces.
//
// Defect it catches, family by family. A relation whose kind is unrecognised
// would be stored and then rendered as a link nothing knows how to draw, and a
// self-relation would make an item block itself — a cycle every dependency view
// has to survive. A roadmap read with no date range, or an unparseable one,
// would reach the query with the zero time and return every item ever created.
// A PATCH that names no field at all is a client bug that must be told apart
// from a successful no-op, or the caller believes its edit was applied.
//
// Each row asserts the message, because these all share one status and one code:
// without it, any single 400 would satisfy every row.
func TestProjectsNeg_ValidationRefusalsAre400(t *testing.T) {
	f := newProjNegFixture(t)
	someUUID := uuid.NewString()

	for _, tc := range []struct {
		projNegRoute
		wantMessage string
	}{
		{projNegRoute{"unknown relation kind", http.MethodPost, f.base + "/items/" + f.ownerItem + "/relations",
			map[string]any{"to_id": uuid.NewString(), "kind": "entangles"}}, "relation kind"},
		{projNegRoute{"self relation", http.MethodPost, f.base + "/items/" + f.ownerItem + "/relations",
			map[string]any{"to_id": f.ownerItem, "kind": "blocks"}}, "itself"},
		{projNegRoute{"search without a query", http.MethodGet, f.base + "/items/search", nil}, "'q'"},
		{projNegRoute{"roadmap without a range", http.MethodGet, f.base + "/roadmap", nil}, "'from'"},
		{projNegRoute{"roadmap with an unparseable from", http.MethodGet,
			f.base + "/roadmap?from=last-tuesday&to=2026-01-31", nil}, "invalid 'from' date format"},
		{projNegRoute{"roadmap with an unparseable to", http.MethodGet,
			f.base + "/roadmap?from=2026-01-01&to=whenever", nil}, "invalid 'to' date format"},
		{projNegRoute{"label without a name", http.MethodPost, f.orgBase + "/labels",
			map[string]any{"color": "#ffffff"}}, "name is required"},
		{projNegRoute{"item type with a blank name", http.MethodPost, f.orgBase + "/item-types",
			map[string]any{"name": "   "}}, "name is required"},
		{projNegRoute{"item type patch with nothing to change", http.MethodPatch,
			f.orgBase + "/item-types/" + someUUID, map[string]any{}}, "nothing to update"},
		{projNegRoute{"custom field of an unknown type", http.MethodPost, f.orgBase + "/custom-fields",
			map[string]any{"name": "Mood", "field_type": "vibes"}}, "type"},
		{projNegRoute{"custom field patch with nothing to change", http.MethodPatch,
			f.orgBase + "/custom-fields/" + someUUID, map[string]any{}}, "nothing to update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projNegRequireError(t, f.as(t, f.ts.Token, tc.method, tc.path, tc.body),
				http.StatusBadRequest, "VALIDATION_ERROR", tc.wantMessage)
		})
	}
}

// TestProjectsNeg_DuplicateLabelNameIs409 walks the arm of handleProjectError
// that nothing in the tree could reach.
//
// projects.LabelRepository's own doc comment says Create "Returns
// ErrLabelDuplicate if the name exists in the org", and handleProjectError has
// had a 409 arm for that sentinel since it was written — but LabelAdapter.Create
// never mapped the unique violation, so the only producer of the sentinel was a
// test double and a repeated name answered
// `500 "project operation failed: ... duplicate key value violates unique
// constraint"` (known-issues #24), leaking the constraint name with it.
//
// Both halves of that are closed now: the adapter maps the violation, and the
// default arm no longer interpolates the error at all — an unmapped 500 here
// reads `project operation failed` and nothing more, with the cause in the
// server log under the caller's request id. See TestUnmappedProjectError_* in
// internal/core/api/projects.
//
// This is the end-to-end half of TestLabelAdapter_DuplicateName in
// internal/db/adapters: that one proves the mapping, this one proves the arm is
// reachable through the router.
func TestProjectsNeg_DuplicateLabelNameIs409(t *testing.T) {
	f := newProjNegFixture(t)

	first := f.as(t, f.ts.Token, http.MethodPost, f.orgBase+"/labels",
		map[string]any{"name": "escalated", "color": "#ff0000"})
	require.Equal(t, http.StatusCreated, first.StatusCode, "%s", first.Body)

	projNegRequireError(t, f.as(t, f.ts.Token, http.MethodPost, f.orgBase+"/labels",
		map[string]any{"name": "escalated", "color": "#00ff00"}),
		http.StatusConflict, "CONFLICT", "already exists")

	// A different name still succeeds, so the 409 above is the clash and not a
	// handler that refuses every second label.
	second := f.as(t, f.ts.Token, http.MethodPost, f.orgBase+"/labels",
		map[string]any{"name": "deferred", "color": "#00ff00"})
	require.Equal(t, http.StatusCreated, second.StatusCode, "%s", second.Body)
}

// --- Board configuration ---

// TestProjectsNeg_BoardColumnDeleteUnknownColumnIs404 covers the branch of
// respondBoardError that is NOT a validation failure — the one that hands the
// error to the shared project mapping.
//
// The existing board suite covers the validation half (unmapped statuses, bad
// WIP limits, a remap target from another space), all of which are 400s. A
// column id that names nothing is a different kind of failure and has to come
// back as 404 NOT_FOUND, not as the 400 its neighbours produce and not as a 500.
//
// Defect it catches: respondBoardError widening ErrIsBoardValidation, or
// answering everything itself instead of delegating. Either turns a missing
// column into "your layout is invalid", which is a lie about what went wrong —
// and the assertion that the board is untouched afterwards is what proves the
// refusal happened before any write.
func TestProjectsNeg_BoardColumnDeleteUnknownColumnIs404(t *testing.T) {
	f := newProjNegFixture(t)
	cfgPath := f.base + "/board/config"

	// Store a real configuration built from whatever vocabulary this space has,
	// so the delete below fails on the column id and nothing else.
	r := f.as(t, f.ts.Token, http.MethodGet, cfgPath, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "read the derived board: %s", r.Body)
	derived := decodeBoardConfig(t, r)
	require.False(t, derived.Customized, "a fresh space starts on the derived board")
	require.GreaterOrEqual(t, len(derived.Columns), 2,
		"the delete case needs a board with more than one column")

	columns := make([]map[string]any, 0, len(derived.Columns))
	for _, c := range derived.Columns {
		columns = append(columns, map[string]any{"name": c.Name, "statuses": c.Statuses})
	}
	r = f.as(t, f.ts.Token, http.MethodPut, cfgPath, map[string]any{"columns": columns})
	require.Equal(t, http.StatusOK, r.StatusCode, "store the board: %s", r.Body)
	saved := decodeBoardConfig(t, r)
	require.True(t, saved.Customized)

	projNegRequireError(t, f.as(t, f.ts.Token, http.MethodDelete,
		cfgPath+"/columns/"+uuid.NewString(),
		map[string]any{"remap_to": saved.Columns[0].ID}),
		http.StatusNotFound, "NOT_FOUND", "")

	after := decodeBoardConfig(t, f.as(t, f.ts.Token, http.MethodGet, cfgPath, nil))
	require.Equal(t, columnIDs(saved), columnIDs(after), "a refused delete must not have written")
}
