package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The entity-generic half of the tag surface (migration 055): tickets and
// project items carry the same org-scoped tags pages do, the browse unions the
// three kinds, and the association writes reconcile their target in the same
// statement. The page-flavoured battery lives in
// wiki_tags_integration_test.go on the same fixture.

func (f *tagFixture) ticketTagsPath(spaceID, ticketID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s/tags", f.ts.OrgID, spaceID, ticketID)
}

func (f *tagFixture) itemTagsPath(spaceID, itemID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items/%s/tags", f.ts.OrgID, spaceID, itemID)
}

// createTicketRow seeds a ticket directly; the ticket-create endpoint is not
// what these tests exercise. The reporter is whoever must pass the
// edit_own_items half of the tag write's capability check.
func (f *tagFixture) createTicketRow(t *testing.T, spaceID string, number int32, title string, reporter uuid.UUID) string {
	t.Helper()
	id := uuid.New()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO tickets (id, space_id, number, title, description, reporter_id)
		 VALUES ($1,$2,$3,$4,'body',$5)`, id, uuid.MustParse(spaceID), number, title, reporter)
	require.NoError(t, err)
	return id.String()
}

// createItemRow seeds a project item directly, with its item_key composed the
// way migration 031 composes it.
func (f *tagFixture) createItemRow(t *testing.T, spaceID, spaceKey string, number int32, title string, reporter uuid.UUID) string {
	t.Helper()
	id := uuid.New()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO project_items (id, space_id, org_id, kind, number, item_key, title, description, reporter_id, rank)
		 SELECT $1, $2, s.org_id, 'task', $3, $4, $5, 'body', $6, 'a'
		 FROM spaces s WHERE s.id = $2`,
		id, uuid.MustParse(spaceID), number, fmt.Sprintf("%s-%d", spaceKey, number), title, reporter)
	require.NoError(t, err)
	return id.String()
}

func (f *tagFixture) entityTags(t *testing.T, token, path string) []tagDTO {
	t.Helper()
	r := f.ts.getAs(t, token, path)
	require.Equal(t, http.StatusOK, r.StatusCode, "reading an entity's tags: %s", r.Body)
	return decodeTags(t, r)
}

// TestEntityTags_TicketAndItemRoundTrip is the sibling of
// TestWikiTags_SetThenGetRoundTripsAndCreatesTheTagRows for the two kinds
// migration 055 added: a tag set on a ticket and on a project item round-trips
// through its own route, and all three kinds share ONE org vocabulary — the
// convergence this track exists for, stated as an assertion.
func TestEntityTags_TicketAndItemRoundTrip(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	beacon := createScopedSpace(t, f.ts, "Tag Desk", "tag-desk", "beacon")
	vector := createScopedSpace(t, f.ts, "Tag Board", "tag-board", "vector")
	grantSpaceRole(t, f.ts, uuid.MustParse(beacon), f.author.ID, "contributor")
	grantSpaceRole(t, f.ts, uuid.MustParse(vector), f.author.ID, "contributor")

	ticket := f.createTicketRow(t, beacon, 1, "Printer on fire", f.author.ID)
	item := f.createItemRow(t, vector, "TAGB", 1, "Extinguish printer", f.author.ID)

	// A page tag first, so the shared-vocabulary assertion below has a row
	// that predates the ticket write.
	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"Ops Runbooks"})

	// Ticket: set, read back, and the SAME tag row — not a second "ops
	// runbooks" minted per module.
	r := f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beacon, ticket),
		map[string]any{"tags": []string{"ops runbooks", "printers"}})
	require.Equal(t, http.StatusOK, r.StatusCode, "setting a ticket's tags: %s", r.Body)
	require.Equal(t, []string{"ops_runbooks", "printers"}, tagSlugs(decodeTags(t, r)))

	got := f.entityTags(t, f.authorTok, f.ticketTagsPath(beacon, ticket))
	require.Equal(t, []string{"Ops Runbooks", "printers"}, []string{got[0].Name, got[1].Name},
		"the ticket carries the page's existing tag row — first spelling wins across modules")

	// Item: the same round trip through the Vector route.
	r = f.ts.putAs(t, f.authorTok, f.itemTagsPath(vector, item), map[string]any{"tags": []string{"printers"}})
	require.Equal(t, http.StatusOK, r.StatusCode, "setting an item's tags: %s", r.Body)
	require.Equal(t, []string{"printers"}, tagSlugs(f.entityTags(t, f.authorTok, f.itemTagsPath(vector, item))))

	// One vocabulary: two tags exist in the org, not four.
	require.Equal(t, []string{"ops_runbooks", "printers"}, tagSlugs(f.orgTags(t, f.authorTok)),
		"pages, tickets and items must share one org vocabulary")

	// And replacement removes, on a ticket, exactly as it does on a page.
	r = f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beacon, ticket), map[string]any{"tags": []string{"printers"}})
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, []string{"printers"}, tagSlugs(f.entityTags(t, f.authorTok, f.ticketTagsPath(beacon, ticket))))
}

// TestEntityTags_BrowseUnionsAllThreeKindsWithTheirOwnRefs: the browse is one
// union over the three kinds, each row carrying its kind's own composed
// reference — a ticket's "KEY-number", an item's item_key, a page's path — and
// the union is filtered per arm to the caller's readable set.
func TestEntityTags_BrowseUnionsAllThreeKindsWithTheirOwnRefs(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	beacon := createScopedSpace(t, f.ts, "Union Desk", "union-desk", "beacon")
	vector := createScopedSpace(t, f.ts, "Union Board", "union-board", "vector")
	grantSpaceRole(t, f.ts, uuid.MustParse(beacon), f.author.ID, "contributor")
	grantSpaceRole(t, f.ts, uuid.MustParse(vector), f.author.ID, "contributor")

	ticket := f.createTicketRow(t, beacon, 7, "Union ticket", f.author.ID)
	item := f.createItemRow(t, vector, "UNIB", 3, "Union item", f.author.ID)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"unioned"})
	require.Equal(t, http.StatusOK,
		f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beacon, ticket), map[string]any{"tags": []string{"unioned"}}).StatusCode)
	require.Equal(t, http.StatusOK,
		f.ts.putAs(t, f.authorTok, f.itemTagsPath(vector, item), map[string]any{"tags": []string{"unioned"}}).StatusCode)

	// A fourth entity in a space the author cannot read, carrying the same
	// tag: the union must filter EVERY arm, not only the page one.
	hidden := createScopedSpace(t, f.ts, "Hidden Desk", "hidden-desk", "beacon")
	hiddenTicket := f.createTicketRow(t, hidden, 1, "Hidden ticket", f.ts.UserID)
	require.Equal(t, http.StatusOK,
		f.ts.putAs(t, f.ts.Token, f.ticketTagsPath(hidden, hiddenTicket), map[string]any{"tags": []string{"unioned"}}).StatusCode)

	r := f.ts.getAs(t, f.authorTok, f.browsePath("unioned"))
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var browse tagBrowse
	require.NoError(t, json.Unmarshal(r.Body, &browse))
	require.False(t, browse.Truncated)

	byType := map[string]taggedEntityDTO{}
	for _, e := range browse.Entities {
		byType[e.EntityType] = e
	}
	require.Len(t, browse.Entities, 3, "one row per readable kind, and the unreadable ticket absent: %s", r.Body)
	require.ElementsMatch(t, []string{f.pageID, ticket, item},
		entityIDsOf(browse.Entities), "the hidden space's ticket must be absent from the union")

	// Each kind's ref is its own spelling, composed server-side. The ticket's
	// is space key + number through tickets.ComposeRef; the item's is its
	// stored item_key; the page's is its path.
	require.Equal(t, "UNION-7", byType["ticket"].Ref, "ticket refs are KEY-number, composed by tickets.ComposeRef")
	require.Equal(t, "UNIB-3", byType["project_item"].Ref, "item refs are the stored item_key")
	require.NotEmpty(t, byType["page"].Ref, "a page's ref is its materialised path")
	require.Equal(t, "Union ticket", byType["ticket"].Title)
	require.Equal(t, beacon, byType["ticket"].SpaceID, "each row carries its own space")
}

// TestEntityTags_BrowseTruncatesAcrossTheUnionNotPerArm: the 201-row limit is
// a property of the whole union. Per-arm limits would return up to three
// kinds' worth of rows with a truncation signal that means nothing — so the
// fixture splits the rows so that NO single kind exceeds the cap and only the
// union does, and asserts the signal fires anyway.
func TestEntityTags_BrowseTruncatesAcrossTheUnionNotPerArm(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	beacon := createScopedSpace(t, f.ts, "Bulk Desk", "bulk-desk", "beacon")
	grantSpaceRole(t, f.ts, uuid.MustParse(beacon), f.author.ID, "contributor")

	// One tag row, and 202 entities carrying it: 101 pages and 101 tickets.
	// Both arms are far under the 201 cap alone; the union is one over it.
	var tagID uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`INSERT INTO tags (org_id, slug, name) VALUES ($1, 'bulk', 'bulk') RETURNING id`,
		f.ts.OrgID).Scan(&tagID))
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`WITH new_pages AS (
		     INSERT INTO pages (id, space_id, title, content, author_id, path)
		     SELECT gen_random_uuid(), $1, 'Bulk page '||i, '', $2, '/bulk-'||i
		     FROM generate_series(1, 101) AS i
		     RETURNING id
		 )
		 INSERT INTO entity_tags (entity_type, entity_id, tag_id)
		 SELECT 'page', id, $3 FROM new_pages`,
		uuid.MustParse(f.spaceID), f.ts.UserID, tagID)
	require.NoError(t, err)
	_, err = f.ts.DB.Pool.Exec(context.Background(),
		`WITH new_tickets AS (
		     INSERT INTO tickets (id, space_id, number, title, description, reporter_id)
		     SELECT gen_random_uuid(), $1, i, 'Bulk ticket '||i, '', $2
		     FROM generate_series(1, 101) AS i
		     RETURNING id
		 )
		 INSERT INTO entity_tags (entity_type, entity_id, tag_id)
		 SELECT 'ticket', id, $3 FROM new_tickets`,
		uuid.MustParse(beacon), f.ts.UserID, tagID)
	require.NoError(t, err)

	r := f.ts.getAs(t, f.authorTok, f.browsePath("bulk"))
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var browse tagBrowse
	require.NoError(t, json.Unmarshal(r.Body, &browse))
	require.True(t, browse.Truncated,
		"202 entities across two kinds must trip the union-level truncation signal")
	require.Len(t, browse.Entities, 200, "the page holds exactly the cap")
}

// TestEntityTags_WriteAnswers404AndDisclosesNothing is the pin on the
// generalized writes' authorisation. The page-only predecessors of these
// statements were bare writes whose safety was a calling convention in one
// handler; entity-generic, the reconciliation lives in the statement and the
// surface answers 404 — never 403 — for a target the caller cannot reach.
//
// Three targets, one shape each for ticket and item:
//   - an entity that does not exist at all;
//   - an entity in a space the caller cannot read;
//   - an entity in a space the caller CAN read, addressed through a different
//     readable space's route — the confused-deputy case, where a 200 would
//     mean the route's authorisation was borrowed by another space's entity.
//
// The first two must be byte-identical, or the difference is an existence
// oracle. And none of the three may mint the tag it was refused for.
func TestEntityTags_WriteAnswers404AndDisclosesNothing(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	beaconA := createScopedSpace(t, f.ts, "Desk A", "desk-a", "beacon")
	beaconB := createScopedSpace(t, f.ts, "Desk B", "desk-b", "beacon")
	vectorA := createScopedSpace(t, f.ts, "Board A", "board-a", "vector")
	grantSpaceRole(t, f.ts, uuid.MustParse(beaconA), f.author.ID, "contributor")
	grantSpaceRole(t, f.ts, uuid.MustParse(vectorA), f.author.ID, "contributor")

	// The unreadable target lives in beaconB, which the author holds no grant
	// on; the admin owns it.
	unreadable := f.createTicketRow(t, beaconB, 1, "Unreachable", f.ts.UserID)

	vocabularyBefore := tagSlugs(f.orgTags(t, f.authorTok))

	// (1) No such ticket vs (2) a ticket the caller cannot reach: the same 404
	// with the same body.
	ghost := f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beaconA, uuid.NewString()),
		map[string]any{"tags": []string{"stolen"}})
	crossed := f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beaconA, unreadable),
		map[string]any{"tags": []string{"stolen"}})
	require.Equal(t, http.StatusNotFound, ghost.StatusCode, "%s", ghost.Body)
	require.Equal(t, http.StatusNotFound, crossed.StatusCode,
		"an unreachable target must be 404, never 403: %s", crossed.Body)
	// The envelope minus the per-request id must be identical — code AND
	// message — or the difference is an existence oracle.
	type errEnvelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	var ghostBody, crossedBody errEnvelope
	require.NoError(t, json.Unmarshal(ghost.Body, &ghostBody))
	require.NoError(t, json.Unmarshal(crossed.Body, &crossedBody))
	require.Equal(t, ghostBody, crossedBody,
		"\"never existed\" and \"exists where you cannot see\" must be indistinguishable")

	// (3) The confused deputy: the author is granted contributor on beaconB
	// too, and still may not write to its ticket THROUGH beaconA's route — the
	// route's space is the authorisation, and the statement reconciles against
	// it.
	grantSpaceRole(t, f.ts, uuid.MustParse(beaconB), f.author.ID, "contributor")
	deputized := f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beaconA, unreadable),
		map[string]any{"tags": []string{"stolen"}})
	require.Equal(t, http.StatusNotFound, deputized.StatusCode,
		"a readable entity addressed through the wrong space's route is still not found: %s", deputized.Body)

	// The refused writes minted nothing and associated nothing.
	require.Equal(t, vocabularyBefore, tagSlugs(f.orgTags(t, f.authorTok)),
		"a refused tag write must not create the tag it was refused for")
	require.Empty(t, f.entityTags(t, f.ts.Token, f.ticketTagsPath(beaconB, unreadable)),
		"no association may exist on the target after the refusals")

	// The item route carries the same rule; one arm proves the statement is
	// entity-typed rather than ticket-specific.
	itemGhost := f.ts.putAs(t, f.authorTok, f.itemTagsPath(vectorA, uuid.NewString()),
		map[string]any{"tags": []string{"stolen"}})
	require.Equal(t, http.StatusNotFound, itemGhost.StatusCode, "%s", itemGhost.Body)

	// And the legitimate write through the RIGHT route still works, so the
	// refusals above are about the target rather than a broken surface.
	own := f.createTicketRow(t, beaconA, 2, "Mine", f.author.ID)
	ok := f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beaconA, own), map[string]any{"tags": []string{"kept"}})
	require.Equal(t, http.StatusOK, ok.StatusCode, "%s", ok.Body)
}

// TestEntityTags_TagSearchReturnsAllThreeKinds: the `tag:` operator stops
// narrowing to Codex. Every module's search arm filters on entity_tags, so a
// tag search returns tagged tickets and items beside tagged pages — and does
// not return an entity that merely matches the text without carrying the tag.
func TestEntityTags_TagSearchReturnsAllThreeKinds(t *testing.T) {
	t.Parallel()
	f := newTagFixture(t)

	beacon := createScopedSpace(t, f.ts, "Search Desk", "search-desk", "beacon")
	vector := createScopedSpace(t, f.ts, "Search Board", "search-board", "vector")
	grantSpaceRole(t, f.ts, uuid.MustParse(beacon), f.author.ID, "contributor")
	grantSpaceRole(t, f.ts, uuid.MustParse(vector), f.author.ID, "contributor")

	// Three tagged entities that all match the text, plus one that matches the
	// text and does NOT carry the tag — the row that proves the filter
	// filters.
	taggedTicket := f.createTicketRow(t, beacon, 1, "Kestrel incident", f.author.ID)
	untaggedTicket := f.createTicketRow(t, beacon, 2, "Kestrel bystander", f.author.ID)
	taggedItem := f.createItemRow(t, vector, "SRCB", 1, "Kestrel follow-up", f.author.ID)

	f.mustSetTags(t, f.authorTok, f.spaceID, f.pageID, []string{"kestrel_ops"})
	require.Equal(t, http.StatusOK,
		f.ts.putAs(t, f.authorTok, f.ticketTagsPath(beacon, taggedTicket), map[string]any{"tags": []string{"kestrel_ops"}}).StatusCode)
	require.Equal(t, http.StatusOK,
		f.ts.putAs(t, f.authorTok, f.itemTagsPath(vector, taggedItem), map[string]any{"tags": []string{"kestrel_ops"}}).StatusCode)
	// The page fixture's title is "Runbook"; retitle it so the text term
	// matches all three kinds through one word.
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE pages SET title = 'Kestrel runbook' WHERE id = $1`, uuid.MustParse(f.pageID))
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet,
		f.ts.url("/api/v1/orgs/"+f.ts.OrgID.String()+"/search?q=tag:kestrel_ops%20kestrel"), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+f.authorTok)
	res := f.ts.do(t, req)
	require.Equal(t, http.StatusOK, res.StatusCode)
	wire := decodeSearch(t, res.Body)

	require.ElementsMatch(t, []string{"codex", "beacon", "vector"}, wire.Modules,
		"a tag filter must not narrow the fan-out — the modules echo says so on the wire")
	ids := make([]string, 0, len(wire.Results))
	modules := make([]string, 0, len(wire.Results))
	for _, r := range wire.Results {
		ids = append(ids, fmt.Sprint(r["id"]))
		modules = append(modules, fmt.Sprint(r["module"]))
	}
	require.ElementsMatch(t, []string{f.pageID, taggedTicket, taggedItem}, ids,
		"the tagged page, ticket and item must all be hits; the untagged ticket must not")
	require.ElementsMatch(t, []string{"codex", "beacon", "vector"}, modules)
	require.NotContains(t, ids, untaggedTicket,
		"an entity matching the text but not the tag must be filtered out")
}
