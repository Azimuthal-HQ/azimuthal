package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The wire-level half of the search permission story. The adapter tests prove
// the SQL filters correctly; these prove the ROUTE is reachable, is
// share-resolved, and does not put container identity on the wire for a hit the
// viewer reached only through a share.
//
// The route being reachable at all is worth asserting on its own: a handler
// missing from newTestServerOn does not fail, it answers a tidy 404 on every
// request, and the endpoint then reads as covered while never having been hit.

// searchWire is the response shape, decoded loosely on purpose — a field that
// must be ABSENT cannot be asserted through a struct with a zero value for it.
type searchWire struct {
	Results []map[string]any `json:"results"`
	Modules []string         `json:"modules"`
	State   string           `json:"state"`
}

func decodeSearch(t *testing.T, body []byte) searchWire {
	t.Helper()
	var out searchWire
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func seedSearchPage(t *testing.T, ts *testServer, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO pages (id, space_id, title, content, author_id, path)
		 VALUES ($1,$2,$3,'body',$4,$5)`,
		id, spaceID, title, ts.UserID, id.String())
	require.NoError(t, err)
	return id
}

func sharePageWithOrg(t *testing.T, ts *testServer, pageID uuid.UUID) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(context.Background(), `
		INSERT INTO entity_shares (id, org_id, space_id, entity_type, entity_id,
		                           audience, cascade, created_by)
		SELECT $1, $2, p.space_id, 'page', p.id, 'org', false, $3
		FROM pages p WHERE p.id = $4`,
		uuid.New(), ts.OrgID, ts.UserID, pageID)
	require.NoError(t, err)
}

// TestSearchEndpoint_ShareOnlyHitCarriesNoContainerOnTheWire is D82's wire-level
// guard: matrix case 16 beats spec §7's "tagged with module and owning team".
//
// The persona is a plain org member holding a read grant on ONE space. The other
// space is unreadable to them, and a page in it is shared org-wide. Both hits
// come back; only one of them may say where it lives.
//
// Fails-before: relax the serializer (or search.redactSharedContainers) to emit
// the container fields for a share-only hit, and the absence assertions fail.
// The presence assertions on the readable hit are what stop the test passing
// against a serializer that strips every row, or against a search that returned
// nothing at all.
func TestSearchEndpoint_ShareOnlyHitCarriesNoContainerOnTheWire(t *testing.T) {
	ts := newTestServer(t)

	readable := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	hidden := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	// The fixture default is 'discoverable', which the readable-set resolution
	// treats as visible to every org member. A "hidden space" test built on the
	// default persona is therefore not testing a hidden space at all — the
	// unreadable row is readable and the leak assertion passes for the wrong
	// reason. Both spaces are pinned explicitly so the fixture default cannot
	// quietly decide what this test proves.
	testutil.SetSpaceVisibility(t, ts.DB.Pool, readable.ID, "hidden")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, hidden.ID, "hidden")

	minePage := seedSearchPage(t, ts, readable.ID, "Kestrel runbook")
	sharedPage := seedSearchPage(t, ts, hidden.ID, "Kestrel embargo")
	seedSearchPage(t, ts, hidden.ID, "Kestrel unshared")
	sharePageWithOrg(t, ts, sharedPage)

	// A PLAIN MEMBER. testutil.CreateTestUser makes an org OWNER, which is an
	// org admin under ADR-0007's middleware bypass — such a persona reads every
	// space in the org, so this test would return all three pages and the leak
	// assertion would fail for the right reason only by accident. The persona
	// has to be one the access filter actually binds.
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, readable.ID, member.ID, "viewer")
	token := ts.tokenFor(t, member.ID, member.Email)

	req, err := http.NewRequest(http.MethodGet,
		ts.url("/api/v1/orgs/"+ts.OrgID.String()+"/search?q=kestrel"), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	res := ts.do(t, req)

	require.Equal(t, http.StatusOK, res.StatusCode,
		"the search route must be mounted and share-resolved — a 404 here means the handler "+
			"is missing from newTestServerOn and every search test is vacuous")

	got := decodeSearch(t, res.Body)
	require.Equal(t, "ok", got.State)
	require.Len(t, got.Results, 2, "the readable hit and the shared hit; the unshared one stays hidden")

	byID := map[string]map[string]any{}
	for _, r := range got.Results {
		byID[r["id"].(string)] = r
	}

	own := byID[minePage.String()]
	require.NotNil(t, own, "the hit in the readable space must be returned")
	require.Equal(t, "space", own["origin"])
	require.Equal(t, readable.Name, own["space_name"], "a readable hit names its space")
	require.NotEmpty(t, own["space_key"])
	require.NotEmpty(t, own["space_id"])

	via := byID[sharedPage.String()]
	require.NotNil(t, via, "the shared hit must be returned — a share widens what search sees")
	require.Equal(t, "share", via["origin"])
	require.NotContains(t, via, "space_id", "a share-only hit must not disclose its space id")
	require.NotContains(t, via, "space_key", "a share-only hit must not disclose its space key")
	require.NotContains(t, via, "space_name", "a share-only hit must not name the space")

	// And the unreadable space's KEY appears nowhere in the raw body — including
	// in a field this test did not think to check by name.
	//
	// The key, not the name: testutil.CreateTestSpace names every space of a
	// type "Test codex", so both spaces here share a name and asserting on it
	// would fail against the READABLE hit, which is entitled to carry it. The
	// key is unique per space, so it identifies the container the viewer must
	// not learn about.
	var hiddenKey string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT key FROM spaces WHERE id = $1`, hidden.ID).Scan(&hiddenKey))
	require.NotContains(t, string(res.Body), hiddenKey,
		"the unreadable space's key must not appear anywhere in the response")
}

// TestSearchEndpoint_StatesAreDistinctOnTheWire pins the three empty answers to
// distinct wire states, so the surface can render them differently rather than
// showing "no results" to someone who typed only stopwords.
func TestSearchEndpoint_StatesAreDistinctOnTheWire(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	// Hidden, so the scopeless persona below really has no scope: the fixture
	// default of 'discoverable' is readable to every org member, which would
	// make no_readable_scope unreachable and the assertion vacuous.
	testutil.SetSpaceVisibility(t, ts.DB.Pool, space.ID, "hidden")
	seedSearchPage(t, ts, space.ID, "Kestrel runbook")

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, space.ID, member.ID, "viewer")
	token := ts.tokenFor(t, member.ID, member.Email)

	scopeless := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	scopelessToken := ts.tokenFor(t, scopeless.ID, scopeless.Email)

	call := func(tok, q string) searchWire {
		req, err := http.NewRequest(http.MethodGet,
			ts.url("/api/v1/orgs/"+ts.OrgID.String()+"/search?q="+q), nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tok)
		res := ts.do(t, req)
		require.Equal(t, http.StatusOK, res.StatusCode)
		return decodeSearch(t, res.Body)
	}

	// The premise: this member can search and does find things. Without it the
	// three assertions below would hold for a route that always returned empty.
	hit := call(token, "kestrel")
	require.Equal(t, "ok", hit.State)
	require.Len(t, hit.Results, 1)

	matched := call(token, "zzzznothingmatchesthis")
	require.Equal(t, "ok", matched.State, "ran and matched nothing is still ok")
	require.Empty(t, matched.Results)

	stopwords := call(token, "the%20of%20a")
	require.Equal(t, "no_searchable_terms", stopwords.State,
		"a stopword-only query had nothing to match, which is not the same as matching nothing")

	noScope := call(scopelessToken, "kestrel")
	require.Equal(t, "no_readable_scope", noScope.State,
		"a member with no grant and no share has nothing to search")
	require.Empty(t, noScope.Results)
}

// TestSearchEndpoint_OperatorsNarrowTheWireResponse covers the two operators
// end to end, including that the response ECHOES the effective module set — so a
// tag filter's implicit narrowing to Codex is visible to the client rather than
// silent.
func TestSearchEndpoint_OperatorsNarrowTheWireResponse(t *testing.T) {
	ts := newTestServer(t)
	codex := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	beacon := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, codex.ID, "hidden")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, beacon.ID, "hidden")

	page := seedSearchPage(t, ts, codex.ID, "Kestrel runbook")
	ticketID := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO tickets (id, space_id, number, title, description, reporter_id)
		 VALUES ($1,$2,1,'Kestrel outage','body',$3)`,
		ticketID, beacon.ID, ts.UserID)
	require.NoError(t, err)

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, codex.ID, member.ID, "viewer")
	grantSpaceRole(t, ts, beacon.ID, member.ID, "viewer")
	token := ts.tokenFor(t, member.ID, member.Email)

	call := func(q string) searchWire {
		req, err := http.NewRequest(http.MethodGet,
			ts.url("/api/v1/orgs/"+ts.OrgID.String()+"/search?q="+q), nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		res := ts.do(t, req)
		require.Equal(t, http.StatusOK, res.StatusCode)
		return decodeSearch(t, res.Body)
	}

	both := call("kestrel")
	require.Len(t, both.Results, 2, "unnarrowed, both modules answer")
	require.ElementsMatch(t, []string{"codex", "beacon", "vector"}, both.Modules)

	narrowed := call("type:beacon%20kestrel")
	require.Len(t, narrowed.Results, 1)
	require.Equal(t, "beacon", narrowed.Results[0]["module"])
	require.Equal(t, []string{"beacon"}, narrowed.Modules,
		"the response echoes the effective fan-out")

	pages := call("type:page%20kestrel")
	require.Len(t, pages.Results, 1)
	require.Equal(t, page.String(), pages.Results[0]["id"])

	// An unknown operator is literal text, never a 400: a search box takes free
	// text and free text contains colons.
	literal := call("status:open%20kestrel")
	require.Equal(t, "ok", literal.State)
}

// TestSearchEndpoint_BadCursorIs400 keeps a malformed cursor from being treated
// as "start from the beginning", which would serve page one forever.
func TestSearchEndpoint_BadCursorIs400(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	seedSearchPage(t, ts, space.ID, "Kestrel runbook")
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, space.ID, member.ID, "viewer")
	token := ts.tokenFor(t, member.ID, member.Email)

	req, err := http.NewRequest(http.MethodGet,
		ts.url("/api/v1/orgs/"+ts.OrgID.String()+"/search?q=kestrel&cursor=!!!garbage!!!"), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	res := ts.do(t, req)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
