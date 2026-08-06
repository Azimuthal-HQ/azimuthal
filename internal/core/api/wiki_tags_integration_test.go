package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The tag surface, end to end against real PostgreSQL (migrations 040, 055).
// The page-flavoured half lives here; the ticket and project-item siblings and
// the cross-entity browse live in entity_tags_integration_test.go on the same
// fixture.
//
// Two of these carry more weight than the rest.
//
// TestWikiTags_PublishAggregationAddsAndNeverRemoves pins the asymmetry the
// whole model rests on: the explicit page-level list is authoritative and can
// remove, while the aggregation of inline `#tag` tokens at publish only ever
// adds. Getting that backwards would mean an author who tags a page and then
// rewords a sentence silently loses the tag.
//
// TestWikiTags_BrowseIsFilteredToTheCallersReadableSpaces is the ADR-0010 one:
// a tag is org-scoped, so browsing it is a cross-space read and has to be cut
// to the caller's own resolved readable set — and a page the caller cannot
// read must be absent rather than refused, so the answer never reports that
// such a page exists.

// tagDTO is one row of any of the tag responses. Every tag endpoint returns
// the same shape.
type tagDTO struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
}

// taggedEntityDTO is one row of the tag browse: any of the three kinds, with
// its space context and its kind's own composed ref.
type taggedEntityDTO struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	SpaceID    string `json:"space_id"`
	SpaceKey   string `json:"space_key"`
	Title      string `json:"title"`
	Ref        string `json:"ref"`
}

// tagBrowse is the {"tag": …, "entities": […]} envelope of the browse
// endpoint.
type tagBrowse struct {
	Tag       tagDTO            `json:"tag"`
	Entities  []taggedEntityDTO `json:"entities"`
	Truncated bool              `json:"truncated"`
}

// tagFixture is a Codex space with one page, plus the two personas the
// capability assertions need.
type tagFixture struct {
	ts      *testServer
	spaceID string
	pageID  string

	// author holds contributor on the space and created pageID. Contributor is
	// the only persona that can prove the in-handler gate on the write route:
	// a viewer never gets past RequireWriteFloor(CapCreateItems), so a
	// viewer-based refusal passes with access.CanEditEntity deleted and
	// asserts the middleware rather than the gate (CLAUDE.md section 2).
	author    testutil.User
	authorTok string
	// peer is a second contributor on the same space. It holds edit_own_items
	// and not edit_any_item, so on pageID — which author created — only the
	// in-handler ownership check can refuse it.
	peer    testutil.User
	peerTok string
}

func newTagFixture(t *testing.T) *tagFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &tagFixture{ts: ts}
	f.spaceID = createScopedSpace(t, ts, "Codex Tags", "codex-tags", "codex")
	f.author, f.authorTok = f.contributorOn(t, f.spaceID)
	f.peer, f.peerTok = f.contributorOn(t, f.spaceID)
	f.pageID = f.createPage(t, f.authorTok, f.spaceID, "Runbook")
	return f
}

// contributorOn creates a plain org member and grants them contributor on the
// space. The harness admin is deliberately not used for these: an org admin
// holds every capability through the middleware bypass, so nothing it does
// tells you anything about a gate.
func (f *tagFixture) contributorOn(t *testing.T, spaceID string) (testutil.User, string) {
	t.Helper()
	u := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	grantSpaceRole(t, f.ts, uuid.MustParse(spaceID), u.ID, "contributor")
	return u, f.ts.tokenFor(t, u.ID, u.Email)
}

func (f *tagFixture) orgTagsPath() string {
	return fmt.Sprintf("/api/v1/orgs/%s/tags", f.ts.OrgID)
}

// browsePath escapes the segment, so a test may browse by the label a person
// typed as well as by the slug — which is a behaviour of the handler, not an
// accident of the path.
func (f *tagFixture) browsePath(segment string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/tags/%s/entities", f.ts.OrgID, url.PathEscape(segment))
}

func (f *tagFixture) pageTagsPath(spaceID, pageID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/tags", f.ts.OrgID, spaceID, pageID)
}

func (f *tagFixture) publishPath(spaceID, pageID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/publish", f.ts.OrgID, spaceID, pageID)
}

func (f *tagFixture) createPage(t *testing.T, token, spaceID, title string) string {
	t.Helper()
	r := f.ts.postAs(t, token, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/", f.ts.OrgID, spaceID),
		map[string]any{"title": title, "content": ""})
	require.Equal(t, http.StatusCreated, r.StatusCode, "creating page: %s", r.Body)
	var page struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	return page.ID
}

// setTags PUTs the page's whole tag set. A nil labels argument is sent as `[]`
// rather than as `null`, because "clear this page's tags" is the case the
// adapter's ANY(NULL) note exists for and it has to be sent as a real empty
// array to be worth asserting.
func (f *tagFixture) setTags(t *testing.T, token, spaceID, pageID string, labels []string) httpResult {
	t.Helper()
	if labels == nil {
		labels = []string{}
	}
	return f.ts.putAs(t, token, f.pageTagsPath(spaceID, pageID), map[string]any{"tags": labels})
}

// mustSetTags PUTs and requires success, for the arrange half of a test.
func (f *tagFixture) mustSetTags(t *testing.T, token, spaceID, pageID string, labels []string) []tagDTO {
	t.Helper()
	r := f.setTags(t, token, spaceID, pageID, labels)
	require.Equal(t, http.StatusOK, r.StatusCode, "setting page tags: %s", r.Body)
	return decodeTags(t, r)
}

func (f *tagFixture) pageTags(t *testing.T, token, spaceID, pageID string) []tagDTO {
	t.Helper()
	r := f.ts.getAs(t, token, f.pageTagsPath(spaceID, pageID))
	require.Equal(t, http.StatusOK, r.StatusCode, "reading a page's tags: %s", r.Body)
	return decodeTags(t, r)
}

func (f *tagFixture) orgTags(t *testing.T, token string) []tagDTO {
	t.Helper()
	r := f.ts.getAs(t, token, f.orgTagsPath())
	require.Equal(t, http.StatusOK, r.StatusCode, "reading the org tag list: %s", r.Body)
	return decodeTags(t, r)
}

// publish publishes a document at the given base version. The page fixture is
// created with empty markdown, so its first publish is at version 1.
func (f *tagFixture) publish(t *testing.T, token, spaceID, pageID, title string, document string, baseVersion int32) {
	t.Helper()
	r := f.ts.postAs(t, token, f.publishPath(spaceID, pageID), map[string]any{
		"title": title, "doc": json.RawMessage(document), "base_version": baseVersion,
	})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)
}

func decodeTags(t *testing.T, r httpResult) []tagDTO {
	t.Helper()
	var out []tagDTO
	require.NoError(t, json.Unmarshal(r.Body, &out), "decoding a tag list: %s", r.Body)
	return out
}

// tagSlugs reduces a tag list to its slugs, so assertions can compare exact
// sequences rather than membership. An exact sequence is what makes the
// replacement and aggregation tests able to fail: "contains keeper" would pass
// on a page carrying keeper and six tags it should not.
func tagSlugs(list []tagDTO) []string {
	out := make([]string, 0, len(list))
	for _, tag := range list {
		out = append(out, tag.Slug)
	}
	return out
}

func entityIDsOf(entities []taggedEntityDTO) []string {
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.EntityID)
	}
	return out
}

// requireAPIErrorCode asserts a failure carries the documented error envelope
// with the given code — so a test cannot be satisfied by any 400 the router
// happens to produce for an unrelated reason.
func requireAPIErrorCode(t *testing.T, r httpResult, status int, code string) {
	t.Helper()
	require.Equal(t, status, r.StatusCode, "unexpected status, body: %s", r.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "a failure must carry the API error envelope")
	require.Equal(t, code, body.Error.Code)
}

// ── The explicit page-level list ───────────────────────────────────────────

// TestWikiTags_SetThenGetRoundTripsAndCreatesTheTagRows is the round trip, and
// with it the create-on-use rule: there is no administration surface that
// makes a tag first, so the PUT is the only constructor there is. The org list
// is asserted empty BEFORE — without that, "the list contains runbooks" would
// pass against a seeded fixture and prove nothing about the write.
func TestWikiTags_SetThenGetRoundTripsAndCreatesTheTagRows(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	require.Empty(t, f.orgTags(t, f.authorTok),
		"the org vocabulary must start empty, or the assertions below prove nothing")
	require.Empty(t, f.pageTags(t, f.authorTok, f.spaceID, f.pageID))

	// The PUT answers with what the page now carries, in the order given.
	set := f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"Runbooks", "Oncall"})
	require.Equal(t, []string{"runbooks", "oncall"}, tagSlugs(set))

	// The GET is the authoritative read, ordered by display name.
	got := f.pageTags(t, f.authorTok, f.spaceID, f.pageID)
	require.Equal(t, []string{"oncall", "runbooks"}, tagSlugs(got))
	require.Equal(t, "Oncall", got[0].Name, "the display name is the text the author typed")

	// And the rows now exist in the org, created by that one use.
	inOrg := f.orgTags(t, f.authorTok)
	require.Equal(t, []string{"oncall", "runbooks"}, tagSlugs(inOrg))
	require.Equal(t, f.ts.OrgID.String(), inOrg[0].OrgID)
}

// TestWikiTags_SetIsAWholeSetReplacement is what makes the page-level list
// authoritative: a tag left out is removed. This is the only path that can
// untag a page — publish aggregation deliberately cannot (see
// TestWikiTags_PublishAggregationAddsAndNeverRemoves).
func TestWikiTags_SetIsAWholeSetReplacement(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"alpha", "beta"})
	require.Equal(t, []string{"alpha", "beta"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"alpha"})
	// Exactly alpha. If the write were additive — the bug this test exists for
	// — the page would still carry beta and this equality fails.
	require.Equal(t, []string{"alpha"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))

	// The tag itself survives being taken off a page. Removing a tag from one
	// page is not a request to delete it from the organisation, and everything
	// else carrying it would lose it if the delete cascaded that way.
	require.Equal(t, []string{"alpha", "beta"}, tagSlugs(f.orgTags(t, f.authorTok)))
}

// TestWikiTags_EmptyListClearsThePage is the ANY(NULL) case written down.
//
// The replacement is a DELETE of everything not in the incoming set, and an
// empty set has to delete every association. Expressed in SQL as
// `NOT (tag_id = ANY($1))`, a NULL array makes the predicate NULL rather than
// false, so the DELETE matches nothing and clearing a page's tags silently
// keeps all of them. That failure is invisible from the write's own response,
// which is why this asserts the read and the table.
func TestWikiTags_EmptyListClearsThePage(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"alpha", "beta"})
	require.Len(t, f.pageTags(t, f.authorTok, f.spaceID, f.pageID), 2)

	cleared := f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{})
	require.Empty(t, cleared, "the response must report the page as carrying nothing")
	require.Empty(t, tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"an empty tag list must delete the page's associations, not keep every one of them")

	// Straight at the table, because the endpoint and the association are two
	// different things to get wrong: a handler that filtered its output would
	// satisfy the read above while leaving the rows behind, and the tag browse
	// reads those rows rather than that handler.
	var associations int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM entity_tags WHERE entity_type = 'page' AND entity_id = $1`, uuid.MustParse(f.pageID)).Scan(&associations))
	require.Zero(t, associations)

	// The vocabulary is untouched — clearing a page does not unmake its tags.
	require.Equal(t, []string{"alpha", "beta"}, tagSlugs(f.orgTags(t, f.authorTok)))
}

// TestWikiTags_LabelsThatSlugifyTheSameAreOneTag: the slug is the identity, so
// "Design Docs" and "design_docs" are one tag rather than two that look alike.
// Without this, an org accumulates a tag per spelling and the browse splits the
// pages between them.
func TestWikiTags_LabelsThatSlugifyTheSameAreOneTag(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	set := f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"Design Docs", "design_docs"})
	require.Equal(t, []string{"design_docs"}, tagSlugs(set),
		"two spellings of one slug must collapse to one tag on the page")

	require.Equal(t, []string{"design_docs"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))

	// One row in the org, and it keeps the FIRST spelling: a later use in a
	// different case must not rewrite what everybody else sees.
	inOrg := f.orgTags(t, f.authorTok)
	require.Len(t, inOrg, 1, "one slug is one row, however many ways it was typed")
	require.Equal(t, "Design Docs", inOrg[0].Name)
}

// TestWikiTags_ALabelThatCannotBecomeATagIsRefused: "!!!" slugifies to nothing.
// Dropping it silently would leave an author looking at a tag field that
// quietly discarded what they typed, so it is a 400 — and the page keeps
// exactly the tags it had, because the replacement is all-or-nothing.
func TestWikiTags_ALabelThatCannotBecomeATagIsRefused(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper"})

	requireAPIErrorCode(t, f.setTags(t, f.authorTok, f.spaceID, f.pageID, []string{"!!!"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	require.Equal(t, []string{"keeper"}, tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"a refused write must leave the page's tags exactly as they were")

	// Mixed with a usable label, the whole request still fails and the page is
	// still untouched. (The usable label may well have minted its tag row
	// before the refusal — tags are created by use, and an unused tag is
	// indistinguishable from one somebody stopped using. What must not happen
	// is a half-applied page.)
	requireAPIErrorCode(t, f.setTags(t, f.authorTok, f.spaceID, f.pageID, []string{"fresh", "%%%"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	require.Equal(t, []string{"keeper"}, tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"a partially valid tag list must not partially apply")
}

// TestWikiTags_MoreTagsThanOnePageCanCarryIsRefused: a page is an untrusted
// path into an org-scoped table, so the explicit list is capped. Without the
// cap one request mints an unbounded number of tag rows that nothing can
// administer away — there is no tag-management surface in this phase.
func TestWikiTags_MoreTagsThanOnePageCanCarryIsRefused(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper"})

	// One past tags.MaxTagsPerEntity.
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("bulk%d", i)
	}
	requireAPIErrorCode(t, f.setTags(t, f.authorTok, f.spaceID, f.pageID, tooMany),
		http.StatusBadRequest, "VALIDATION_ERROR")

	require.Equal(t, []string{"keeper"}, tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))
	// The cap is checked before anything is created, so not one of the 51 rows
	// exists. A cap that refused after minting them would be no cap at all.
	require.Equal(t, []string{"keeper"}, tagSlugs(f.orgTags(t, f.authorTok)))
}

// ── Capability ─────────────────────────────────────────────────────────────

// TestWikiTags_ContributorCannotTagSomebodyElsesPage is the gate's real test.
//
// THE PERSONA IS A CONTRIBUTOR AND THAT IS THE POINT. Tagging a page is
// editing it, so SetPageTags goes through the same editablePage check as the
// document writes. A viewer would be refused upstream by
// RequireWriteFloor(CapCreateItems) and never reach it, so a viewer-based
// refusal passes with access.CanEditEntity deleted — it asserts the middleware.
// A contributor clears the floor and holds edit_own_items but not
// edit_any_item, so on a page somebody else created only the in-handler check
// can refuse them.
//
// Mutation-tested while writing: with the access.CanEditEntity call removed
// from editablePage, the refusal below answers 200 and the test fails.
func TestWikiTags_ContributorCannotTagSomebodyElsesPage(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper"})

	// The peer is a contributor on this space who did not create this page.
	requireAPIForbidden(t, f.setTags(t, f.peerTok, f.spaceID, f.pageID, []string{"hijacked"}))
	require.Equal(t, []string{"keeper"}, tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"a refused tag write must not land")
	require.Equal(t, []string{"keeper"}, tagSlugs(f.orgTags(t, f.authorTok)),
		"a refused tag write must not mint the tag it was refused for")

	// Reading the page's tags is fine: the tag surface adds no read
	// restriction beyond the space's own.
	require.Equal(t, []string{"keeper"}, tagSlugs(f.pageTags(t, f.peerTok, f.spaceID, f.pageID)))

	// The author — the same kind of persona, on their own page — is allowed.
	// Without this half, every assertion above would also pass if the route
	// were simply broken for everyone.
	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper", "owned"})
	require.Equal(t, []string{"keeper", "owned"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))

	// And so is the peer, on a page the peer created — so the refusal above is
	// about ownership rather than about the persona being powerless.
	theirPage := f.createPage(t, f.peerTok, f.spaceID, "Theirs")
	f.mustSetTags(t, f.peerTok, f.spaceID, theirPage, []string{"theirs"})
	require.Equal(t, []string{"theirs"},
		tagSlugs(f.pageTags(t, f.peerTok, f.spaceID, theirPage)))
}

// ── Publish aggregation ────────────────────────────────────────────────────

// TestWikiTags_PublishAggregatesInlineTagsFromTheBody: an inline `#tag` token
// in a published body puts the tag on the page, and mints it if it is new.
// This is the shortcut half of the model — typing #runbook in a sentence is
// how most tags will actually come into existence.
func TestWikiTags_PublishAggregatesInlineTagsFromTheBody(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	// Written out by hand rather than built from the editor's own output, so
	// this test states the wire shape the frontend has to produce rather than
	// agreeing with whatever the backend last emitted.
	const body = `{"type":"doc","content":[` +
		`{"type":"paragraph","content":[` +
		`{"type":"text","text":"See the "},` +
		`{"type":"inlineTag","attrs":{"label":"Runbook"}},` +
		`{"type":"text","text":" for the rest."}` +
		`]}]}`

	require.Empty(t, f.orgTags(t, f.authorTok))
	f.publish(t, f.authorTok, f.spaceID, f.pageID, "Runbook", body, 1)

	require.Equal(t, []string{"runbook"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"publishing a body with an inline tag must put that tag on the page")

	// Created by use, with the spelling from the body.
	inOrg := f.orgTags(t, f.authorTok)
	require.Len(t, inOrg, 1)
	require.Equal(t, "Runbook", inOrg[0].Name)
}

// TestWikiTags_PublishAggregationAddsAndNeverRemoves is the important one.
//
// The aggregation is deliberately one-directional. Deleting the last `#other`
// from a page's body does NOT untag the page: the page-level list is the
// authority, and the alternative — inline tokens owning the set — would mean
// an author who tags a page explicitly and then rewords a sentence silently
// loses the tag, with no way to tag a page at all without writing the tag into
// its prose.
//
// So the third publish below, whose body contains no inline tag whatsoever,
// must leave BOTH tags in place. If aggregation were made authoritative, that
// is the assertion that fails.
func TestWikiTags_PublishAggregationAddsAndNeverRemoves(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	const withInlineTag = `{"type":"doc","content":[` +
		`{"type":"paragraph","content":[` +
		`{"type":"text","text":"Filed under "},` +
		`{"type":"inlineTag","attrs":{"label":"other"}}` +
		`]}]}`
	const withoutAnyTag = `{"type":"doc","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Rewritten with no tags in it."}]}]}`

	// An explicit tag first, so the aggregation has something it could destroy.
	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper"})

	f.publish(t, f.authorTok, f.spaceID, f.pageID, "Runbook", withInlineTag, 1)
	require.Equal(t, []string{"keeper", "other"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"the inline tag must be added alongside the explicit one, not instead of it")

	f.publish(t, f.authorTok, f.spaceID, f.pageID, "Runbook", withoutAnyTag, 2)
	require.Equal(t, []string{"keeper", "other"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)),
		"removing the last inline occurrence must NOT untag the page — the page-level list is the authority")

	// And the explicit path still can, which is what makes the pair meaningful
	// rather than "nothing ever removes a tag".
	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"keeper"})
	require.Equal(t, []string{"keeper"},
		tagSlugs(f.pageTags(t, f.authorTok, f.spaceID, f.pageID)))
}

// ── The browse ─────────────────────────────────────────────────────────────

// TestWikiTags_BrowseIsFilteredToTheCallersReadableSpaces — ADR-0010's rule for
// every cross-space endpoint, applied to a tag.
//
// The two pages carry the same tag in two spaces, and the caller can read one
// of them. The unreadable page must be ABSENT from a 200, not the cause of a
// 403 or a 404: refusing the request would itself report that a page the
// caller cannot see exists and carries this tag.
func TestWikiTags_BrowseIsFilteredToTheCallersReadableSpaces(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	// A second Codex space the fixture's personas hold no grant on. It is
	// created by the harness admin, whose create seeds no creator grant — the
	// admin reads it through the org bypass instead — so the contributor's
	// readable set stays exactly what contributorOn granted.
	otherSpace := createScopedSpace(t, f.ts, "Private Codex", "private-codex", "codex")
	otherPage := f.createPage(t, f.ts.Token, otherSpace, "Their runbook")

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"Design Docs"})
	f.mustSetTags(t, f.ts.Token, otherSpace, otherPage, []string{"Design Docs"})

	// The fixture is only meaningful if the second space really is unreadable
	// to the contributor; readableGuard 404s it.
	require.Equal(t, http.StatusNotFound,
		f.ts.getAs(t, f.authorTok,
			fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, otherSpace, otherPage)).StatusCode,
		"the second space must be outside the contributor's readable set")

	r := f.ts.getAs(t, f.authorTok, f.browsePath("design_docs"))
	require.Equal(t, http.StatusOK, r.StatusCode,
		"an unreadable page must make the answer smaller, never refuse it: %s", r.Body)
	var browse tagBrowse
	require.NoError(t, json.Unmarshal(r.Body, &browse))
	require.Equal(t, "design_docs", browse.Tag.Slug)
	require.Equal(t, []string{f.pageID}, entityIDsOf(browse.Entities),
		"the page in the space the caller cannot read must be absent")
	require.Equal(t, f.spaceID, browse.Entities[0].SpaceID,
		"the row carries its space, so a same-titled page elsewhere is tellable apart")

	// The admin, who reads every space in the org, sees both — so the filter
	// above is cutting the result rather than the query simply finding one row.
	r = f.ts.get(t, f.browsePath("design_docs"), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &browse))
	require.ElementsMatch(t, []string{f.pageID, otherPage}, entityIDsOf(browse.Entities))

	// The path segment is slugified rather than taken verbatim, so a client can
	// link to a tag it only knows the label of without reimplementing the slug
	// convention in TypeScript.
	r = f.ts.getAs(t, f.authorTok, f.browsePath("Design Docs"))
	require.Equal(t, http.StatusOK, r.StatusCode, "browsing by label: %s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &browse))
	require.Equal(t, "design_docs", browse.Tag.Slug)
	require.Equal(t, []string{f.pageID}, entityIDsOf(browse.Entities),
		"browsing by label must be the same filtered answer as browsing by slug")
}

// TestWikiTags_BrowsingATagThatDoesNotExist: 404 for a slug with nothing behind
// it, and 400 for a segment that cannot be a slug at all. The two are different
// answers because they are different mistakes — one is a dead link, the other
// is a malformed request.
func TestWikiTags_BrowsingATagThatDoesNotExist(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	// A real tag exists, so the 404 below is about this slug rather than about
	// an empty table.
	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"real"})

	requireAPIErrorCode(t, f.ts.getAs(t, f.authorTok, f.browsePath("no_such_tag")),
		http.StatusNotFound, "NOT_FOUND")
	requireAPIErrorCode(t, f.ts.getAs(t, f.authorTok, f.browsePath("!!!")),
		http.StatusBadRequest, "BAD_REQUEST")

	// The real one still answers, so the 404 is not the route being broken.
	require.Equal(t, http.StatusOK, f.ts.getAs(t, f.authorTok, f.browsePath("real")).StatusCode)
}

// ── Org scoping ────────────────────────────────────────────────────────────

// TestWikiTags_TagsAreOrgScoped: the vocabulary belongs to one organisation.
// Both directions are asserted, because a query that dropped its org filter
// would return a superset that still "contains" the tag each side expects.
func TestWikiTags_TagsAreOrgScoped(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"ours"})

	// The second org's tag is written straight to the table: reaching it
	// through the API would need a second org's whole space-and-page fixture,
	// and the thing under test is the org filter, not the write path.
	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	otherUser := testutil.CreateTestUser(t, f.ts.DB.Pool, otherOrg.ID)
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO tags (org_id, slug, name) VALUES ($1, 'theirs', 'Theirs')`, otherOrg.ID)
	require.NoError(t, err)

	pair, err := f.ts.JWT.IssueTokenPair(otherUser.ID, otherUser.Email, otherOrg.ID.String(), "member", 0)
	require.NoError(t, err)

	r := f.ts.getAs(t, pair.AccessToken, fmt.Sprintf("/api/v1/orgs/%s/tags", otherOrg.ID))
	require.Equal(t, http.StatusOK, r.StatusCode, "the other org's own tag list: %s", r.Body)
	require.Equal(t, []string{"theirs"}, tagSlugs(decodeTags(t, r)),
		"one org's tag list must be exactly its own tags")

	require.Equal(t, []string{"ours"}, tagSlugs(f.orgTags(t, f.authorTok)),
		"and the other org's tag must not appear in this one")
}
