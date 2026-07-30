package adapters_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/search"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Cross-module search: the permission tests.
//
// These run against real PostgreSQL because the access predicate IS SQL. A Go
// double cannot be wrong in the way a WHERE clause is wrong, so a fake store
// proves nothing here — see the service's own tests for what that layer owns.
//
// Every test below asserts a POSITIVE half alongside its negative one. That is
// not padding. An empty tsquery matches nothing, an empty access array matches
// nothing, and a zero row limit returns nothing — so "the row the viewer must
// not see is absent" passes vacuously under three separate accidents, each of
// which looks like a legitimate test input. The positive half is what makes the
// negative half mean something.

// searchFixture is one org with two spaces the fixtures can place things in.
type searchFixture struct {
	pool    *pgxpool.Pool
	orgID   uuid.UUID
	userID  uuid.UUID
	openSp  uuid.UUID // the viewer can read this one
	otherSp uuid.UUID // the viewer cannot
	adapter *adapters.SearchAdapter
}

func newSearchFixture(t *testing.T) *searchFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex")
	other := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex")
	return &searchFixture{
		pool: db.Pool, orgID: org.ID, userID: user.ID,
		openSp: open.ID, otherSp: other.ID,
		adapter: adapters.NewSearchAdapter(db.Pool),
	}
}

// page inserts a page with an explicit path so subtree fixtures can be exact.
func (f *searchFixture) page(t *testing.T, spaceID uuid.UUID, title, body, path string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if path == "" {
		path = id.String()
	}
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO pages (id, space_id, title, content, author_id, path)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		id, spaceID, title, body, f.userID, path)
	require.NoError(t, err)
	return id
}

func (f *searchFixture) ticket(t *testing.T, spaceID uuid.UUID, num int, title, body string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO tickets (id, space_id, number, title, description, reporter_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		id, spaceID, num, title, body, f.userID)
	require.NoError(t, err)
	return id
}

func (f *searchFixture) item(t *testing.T, spaceID uuid.UUID, num int, key, title, body string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO project_items (id, space_id, number, kind, title, description, reporter_id, org_id, item_key)
		 VALUES ($1,$2,$3,'task',$4,$5,$6,$7,$8)`,
		id, spaceID, num, title, body, f.userID, f.orgID, key)
	require.NoError(t, err)
	return id
}

func ids(rows []search.Result) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// TestSearch_HiddenSpaceNeverLeaks is the A1/P4 pattern for all three entity
// types: a row that matches the query perfectly, in a space the viewer cannot
// read, must never come back.
//
// Fails-before: drop the `space_id = ANY($readable)` arm from any one of the
// three queries and that module's subtest fails. The positive half — the
// matching row in the READABLE space — is what stops the whole test passing
// against a query that returns nothing at all.
func TestSearch_HiddenSpaceNeverLeaks(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	minePage := f.page(t, f.openSp, "Quarterly kestrel runbook", "body", "")
	theirsPage := f.page(t, f.otherSp, "Quarterly kestrel secrets", "body", "")
	mineTicket := f.ticket(t, f.openSp, 1, "Kestrel outage", "body")
	theirsTicket := f.ticket(t, f.otherSp, 2, "Kestrel breach", "body")
	mineItem := f.item(t, f.openSp, 1, "OPEN-1", "Kestrel rollout", "body")
	theirsItem := f.item(t, f.otherSp, 2, "HID-2", "Kestrel embargo", "body")

	readable := []uuid.UUID{f.openSp}
	p := search.FanoutParams{OrgID: f.orgID, Query: "kestrel", ReadableSpaceIDs: readable, Limit: 50}

	pages, err := f.adapter.SearchPages(ctx, p)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{minePage}, ids(pages), "pages: readable only")
	require.NotContains(t, ids(pages), theirsPage)

	tickets, err := f.adapter.SearchTickets(ctx, p)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{mineTicket}, ids(tickets), "tickets: readable only")
	require.NotContains(t, ids(tickets), theirsTicket)

	items, err := f.adapter.SearchProjectItems(ctx, p)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{mineItem}, ids(items), "items: readable only")
	require.NotContains(t, ids(items), theirsItem)
}

// TestSearch_SharedSubtreeDescendantIsFindable is D46's positive half: a share
// of a page SUBTREE must make descendants searchable for the recipient, even
// though the subtree lives in a space the viewer cannot read.
//
// Fails-before, both directions: without the subtree arm the descendant is
// absent (the share only names the root); with the arm but no space pin, the
// cartesian case below leaks.
func TestSearch_SharedSubtreeDescendantIsFindable(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	// A subtree in a space the viewer CANNOT read.
	rootPath := uuid.New().String()
	root := f.page(t, f.otherSp, "Kestrel root", "body", rootPath)
	child := f.page(t, f.otherSp, "Kestrel child", "body", rootPath+"."+uuid.New().String())
	// A sibling that shares a textual prefix but is NOT in the subtree. `a.bc`
	// must never be matched by a cascade on `a.b`.
	sibling := f.page(t, f.otherSp, "Kestrel sibling", "body", rootPath+"X")
	// And an unrelated page in the same unreadable space.
	unrelated := f.page(t, f.otherSp, "Kestrel unrelated", "body", uuid.New().String())

	se := access.NewSharedEntities([]access.ShareRow{
		{EntityType: access.ShareEntityPage, EntityID: root, Cascade: true,
			RootPath: &rootPath, RootSpaceID: &f.otherSp},
	})
	subSpaces, subPatterns := se.CascadeSubtreeArrays()

	rows, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel",
		ReadableSpaceIDs: []uuid.UUID{f.openSp}, // deliberately NOT otherSp
		SharedPageIDs:    se.DirectIDs(access.ShareEntityPage),
		SubtreeSpaceIDs:  subSpaces,
		SubtreePatterns:  subPatterns,
		Limit:            50,
	})
	require.NoError(t, err)

	got := ids(rows)
	require.Contains(t, got, root, "the shared root itself is covered by the direct arm")
	require.Contains(t, got, child, "a DESCENDANT of a shared subtree must be findable — this is D46")
	require.NotContains(t, got, sibling, "a prefix sibling is not in the subtree")
	require.NotContains(t, got, unrelated, "an unrelated page in the same space stays hidden")
}

// TestSearch_CascadeSubtreeDoesNotWidenAcrossSpaces is D46's negative half, and
// the reason the accessor returns paired arrays.
//
// The collision CANNOT arise from a natural fixture — paths are dot-separated
// UUIDs — so the path here is forged with direct SQL, exactly as
// entity_shares_integration_test.go already does for the sibling case. Written
// with ordinary page creation this test passes against BOTH the correct shape
// and the broken one, and reads as proof of something it never exercised.
//
// Fails-before: replace the paired unnest in GlobalSearchPages with
// `space_id = ANY($spaces) AND path LIKE ANY($patterns)` and the forged page
// appears.
func TestSearch_CascadeSubtreeDoesNotWidenAcrossSpaces(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	rootA := "aaaaaaaa-0000-4000-8000-00000000000a"
	rootB := "bbbbbbbb-0000-4000-8000-00000000000b"

	// Two cascade roots, one per space.
	pageA := f.page(t, f.openSp, "Kestrel root A", "body", rootA)
	pageB := f.page(t, f.otherSp, "Kestrel root B", "body", rootB)
	// A real descendant of each.
	childA := f.page(t, f.openSp, "Kestrel child A", "body", rootA+".1111")
	childB := f.page(t, f.otherSp, "Kestrel child B", "body", rootB+".2222")
	// THE FORGED ROW: it lives in space A but its path sits under root B's
	// subtree. Only a query that pins each pattern to its own root's space
	// excludes it.
	forged := f.page(t, f.openSp, "Kestrel forged", "body", rootB+".9999")

	se := access.NewSharedEntities([]access.ShareRow{
		{EntityType: access.ShareEntityPage, EntityID: pageA, Cascade: true,
			RootPath: &rootA, RootSpaceID: &f.openSp},
		{EntityType: access.ShareEntityPage, EntityID: pageB, Cascade: true,
			RootPath: &rootB, RootSpaceID: &f.otherSp},
	})
	subSpaces, subPatterns := se.CascadeSubtreeArrays()

	rows, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel",
		ReadableSpaceIDs: nil, // shares are the ONLY access, so the arm is isolated
		SharedPageIDs:    se.DirectIDs(access.ShareEntityPage),
		SubtreeSpaceIDs:  subSpaces,
		SubtreePatterns:  subPatterns,
		Limit:            50,
	})
	require.NoError(t, err)

	got := ids(rows)
	require.Contains(t, got, childA, "each root's own descendant is covered")
	require.Contains(t, got, childB, "each root's own descendant is covered")
	require.NotContains(t, got, forged,
		"a page in root A's space matched by root B's pattern is the D46 cross-space leak")
	require.ElementsMatch(t, []uuid.UUID{pageA, pageB, childA, childB}, got)
}

// TestSearch_NonMatchingQueryReturnsNothingForASharedViewer is the test the
// repository has nowhere else, and it is the one that catches an access
// predicate whose arms have escaped their parentheses.
//
// If the outer parens around (readable OR shared OR subtree) are lost, the share
// and subtree arms are promoted above `deleted_at IS NULL` and above the `@@`
// match. The query then returns every descendant of every cascade root the
// viewer holds — soft-deleted ones included — for EVERY query, whether or not it
// matches. The readable-space arm keeps behaving, so a hidden-space leak test,
// an exact-id-set test and a two-viewer divergence test all stay green.
//
// A term that matches nothing, run by a viewer who holds both a readable space
// and a share, is what makes it visible.
func TestSearch_NonMatchingQueryReturnsNothingForASharedViewer(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	rootPath := uuid.New().String()
	root := f.page(t, f.otherSp, "Kestrel root", "body", rootPath)
	f.page(t, f.otherSp, "Kestrel child", "body", rootPath+"."+uuid.New().String())
	f.page(t, f.openSp, "Kestrel readable", "body", "")

	// A soft-deleted descendant: the escaped-paren shape returns these too.
	deleted := f.page(t, f.otherSp, "Kestrel deleted", "body", rootPath+"."+uuid.New().String())
	_, err := f.pool.Exec(ctx, `UPDATE pages SET deleted_at = now() WHERE id = $1`, deleted)
	require.NoError(t, err)

	se := access.NewSharedEntities([]access.ShareRow{
		{EntityType: access.ShareEntityPage, EntityID: root, Cascade: true,
			RootPath: &rootPath, RootSpaceID: &f.otherSp},
	})
	subSpaces, subPatterns := se.CascadeSubtreeArrays()

	base := search.FanoutParams{
		OrgID:            f.orgID,
		ReadableSpaceIDs: []uuid.UUID{f.openSp},
		SharedPageIDs:    se.DirectIDs(access.ShareEntityPage),
		SubtreeSpaceIDs:  subSpaces,
		SubtreePatterns:  subPatterns,
		Limit:            50,
	}

	// The premise: this viewer really can see things. Without it the assertion
	// below would hold for a viewer with no access at all.
	matching := base
	matching.Query = "kestrel"
	hits, err := f.adapter.SearchPages(ctx, matching)
	require.NoError(t, err)
	require.NotEmpty(t, hits, "premise: this viewer can see matching pages")
	require.NotContains(t, ids(hits), deleted, "a soft-deleted page is never a hit")

	// The assertion: a term nothing matches returns nothing, for the same viewer.
	none := base
	none.Query = "xyzzyplughquux"
	empty, err := f.adapter.SearchPages(ctx, none)
	require.NoError(t, err)
	require.Empty(t, empty,
		"a non-matching query must return nothing even for a viewer holding shares — "+
			"rows here mean the access arms are no longer conjoined to the match")
}

// TestSearch_TwoViewersDivergeOnOneQuery proves the filter is per-viewer rather
// than global. Both personas hold a share, so both arrays are non-empty for
// both: a "nobody else" persona holding NO share would make the share term
// deletable with the test still green.
func TestSearch_TwoViewersDivergeOnOneQuery(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	aPage := f.page(t, f.openSp, "Kestrel alpha", "body", "")
	bPage := f.page(t, f.otherSp, "Kestrel bravo", "body", "")

	// Each viewer holds a share on the OTHER one's page only.
	seA := access.NewSharedEntities([]access.ShareRow{
		{EntityType: access.ShareEntityPage, EntityID: bPage},
	})
	seB := access.NewSharedEntities([]access.ShareRow{
		{EntityType: access.ShareEntityPage, EntityID: aPage},
	})

	rowsA, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel",
		ReadableSpaceIDs: []uuid.UUID{f.openSp},
		SharedPageIDs:    seA.DirectIDs(access.ShareEntityPage),
		Limit:            50,
	})
	require.NoError(t, err)

	rowsB, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel",
		ReadableSpaceIDs: []uuid.UUID{f.otherSp},
		SharedPageIDs:    seB.DirectIDs(access.ShareEntityPage),
		Limit:            50,
	})
	require.NoError(t, err)

	// Viewer A: their readable space plus the page shared to them.
	require.ElementsMatch(t, []uuid.UUID{aPage, bPage}, ids(rowsA))
	require.ElementsMatch(t, []uuid.UUID{bPage, aPage}, ids(rowsB))

	// The divergence that matters: strip the shares and the two viewers see
	// disjoint sets. Same query, same corpus, different answers.
	onlyA, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel", ReadableSpaceIDs: []uuid.UUID{f.openSp}, Limit: 50,
	})
	require.NoError(t, err)
	onlyB, err := f.adapter.SearchPages(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "kestrel", ReadableSpaceIDs: []uuid.UUID{f.otherSp}, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{aPage}, ids(onlyA))
	require.Equal(t, []uuid.UUID{bPage}, ids(onlyB))
}

// TestSearch_IndexIsFreshOnWriteAndUpdate proves the generated columns behave.
// Structural, but asserted anyway — and the negative half (the OLD term stops
// matching) has no precedent anywhere in the repo.
func TestSearch_IndexIsFreshOnWriteAndUpdate(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)
	readable := []uuid.UUID{f.openSp}

	tkt := f.ticket(t, f.openSp, 1, "Helmfile rollout", "body")
	p := search.FanoutParams{OrgID: f.orgID, Query: "helmfile", ReadableSpaceIDs: readable, Limit: 50}

	rows, err := f.adapter.SearchTickets(ctx, p)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tkt}, ids(rows), "a just-written row is immediately findable")

	_, err = f.pool.Exec(ctx, `UPDATE tickets SET title = 'Kustomize rollout' WHERE id = $1`, tkt)
	require.NoError(t, err)

	rows, err = f.adapter.SearchTickets(ctx, p)
	require.NoError(t, err)
	require.Empty(t, rows, "the OLD term must stop matching after an edit")

	p.Query = "kustomize"
	rows, err = f.adapter.SearchTickets(ctx, p)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tkt}, ids(rows), "the NEW term matches")
}

// TestSearch_TitleOutranksBodyAcrossModules is migration 049's whole point, and
// it can only be seen across modules: each module ranks self-consistently, so a
// per-module test cannot detect the 10x scale mismatch the weights removed.
//
// Fails-before: revert 049 and the ticket's body match ranks equal to its title
// match, and both rank an order of magnitude below the page.
func TestSearch_TitleOutranksBodyAcrossModules(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)
	readable := []uuid.UUID{f.openSp}

	titlePage := f.page(t, f.openSp, "Kestrel", "unrelated prose", "")
	titleTicket := f.ticket(t, f.openSp, 1, "Kestrel", "unrelated prose")
	bodyTicket := f.ticket(t, f.openSp, 2, "Unrelated heading", "kestrel appears only here")

	p := search.FanoutParams{OrgID: f.orgID, Query: "kestrel", ReadableSpaceIDs: readable, Limit: 50}
	pages, err := f.adapter.SearchPages(ctx, p)
	require.NoError(t, err)
	tickets, err := f.adapter.SearchTickets(ctx, p)
	require.NoError(t, err)

	keyOf := func(rows []search.Result, id uuid.UUID) string {
		for _, r := range rows {
			if r.ID == id {
				return r.SortKey
			}
		}
		t.Fatalf("row %s not returned", id)
		return ""
	}

	pageKey := keyOf(pages, titlePage)
	ticketTitleKey := keyOf(tickets, titleTicket)
	ticketBodyKey := keyOf(tickets, bodyTicket)

	require.Greater(t, ticketTitleKey, ticketBodyKey,
		"a title hit must outrank a body hit — this is what setweight bought")
	require.Equal(t, pageKey, ticketTitleKey,
		fmt.Sprintf("a title hit must rank the SAME across modules; got page=%s ticket=%s",
			pageKey, ticketTitleKey))
}

// TestSearch_ItemKeyIsSearchable covers the one asymmetry migration 049
// introduced: project_items carry item_key in their vector, tickets cannot.
func TestSearch_ItemKeyIsSearchable(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	target := f.item(t, f.openSp, 1, "VEC-14", "Rollout plan", "body")
	f.item(t, f.openSp, 2, "VEC-15", "Rollback plan", "body")

	rows, err := f.adapter.SearchProjectItems(ctx, search.FanoutParams{
		OrgID: f.orgID, Query: "VEC-14", ReadableSpaceIDs: []uuid.UUID{f.openSp}, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{target}, ids(rows), "an item is findable by its key")
}

// TestSearch_ParsedQueryDistinguishesEmptyFromNoMatch pins the guard the service
// relies on. These inputs do not error in PostgreSQL — they yield an empty
// tsquery and at most a NOTICE — so without asking, "nothing to search for" is
// indistinguishable from "nothing matched", and every absence assertion above
// would pass with the access filter deleted.
func TestSearch_ParsedQueryDistinguishesEmptyFromNoMatch(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	for _, empty := range []string{"the of a", "!!! &&& ||| (((", "", "   "} {
		parsed, err := f.adapter.ParsedQuery(ctx, empty)
		require.NoError(t, err, "input %q must not error — that is the point", empty)
		require.Empty(t, parsed, "input %q parses to an empty tsquery", empty)
	}

	parsed, err := f.adapter.ParsedQuery(ctx, "kestrel runbook")
	require.NoError(t, err)
	require.NotEmpty(t, parsed, "a real query parses to something")
}

// TestSearch_SnippetsHighlightWithoutMarkup covers ts_headline end to end,
// including the decision that matters for the surface: the delimiters are
// control characters, never HTML.
//
// ts_headline escapes NOTHING — it returns the source text with the delimiters
// inserted. With `StartSel=<mark>` a page body containing a script tag produces
// a snippet carrying that script verbatim, and any client rendering the snippet
// as HTML executes it. STX/ETX cannot occur in ordinary prose, so the client
// splits on them and wraps the pieces in real elements instead.
func TestSearch_SnippetsHighlightWithoutMarkup(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	const stx, etx = "\x02", "\x03"

	// A body carrying markup, to prove the snippet does not become markup.
	hostile := f.page(t, f.openSp, "Runbook",
		"before the kestrel appears <script>alert(1)</script> and after", "")

	got, err := f.adapter.Snippets(ctx, search.ModuleCodex, "kestrel", []uuid.UUID{hostile})
	require.NoError(t, err)
	snippet := got[hostile]
	require.NotEmpty(t, snippet, "a matching body must produce a snippet")

	require.Contains(t, snippet, stx+"kestrel"+etx,
		"the matched term is wrapped in the control-character delimiters")
	require.NotContains(t, snippet, "<mark>", "the delimiters must not be markup")
	require.NotContains(t, snippet, "<b>", "the delimiters must not be markup")

	// MEASURED, and not what the first version of this test assumed: the text
	// search parser recognises HTML tags as their own token type and DROPS them,
	// so `<script>alert(1)</script>` reaches the snippet as the bare text
	// "alert(1)". Tag stripping is a property of the parser, not a guarantee this
	// code arranged, so it is pinned here — if a future configuration change
	// stopped dropping tags, an HTML-rendering client would become injectable and
	// nothing else would notice.
	require.NotContains(t, snippet, "<script>",
		"the text search parser drops HTML tags; if this ever changes, the delimiters "+
			"being control characters is the only thing standing between stored content and the DOM")
	require.Contains(t, snippet, "alert(1)",
		"the tag's CONTENT still survives as text, which is why a snippet is text and never markup")

	// Empty input is not a query.
	none, err := f.adapter.Snippets(ctx, search.ModuleCodex, "kestrel", nil)
	require.NoError(t, err)
	require.Empty(t, none)

	// Tickets and items headline their description.
	tkt := f.ticket(t, f.openSp, 1, "Outage", "the kestrel service failed")
	gotT, err := f.adapter.Snippets(ctx, search.ModuleBeacon, "kestrel", []uuid.UUID{tkt})
	require.NoError(t, err)
	require.Contains(t, gotT[tkt], stx+"kestrel"+etx)

	item := f.item(t, f.openSp, 1, "VEC-1", "Rollout", "the kestrel migration")
	gotI, err := f.adapter.Snippets(ctx, search.ModuleVector, "kestrel", []uuid.UUID{item})
	require.NoError(t, err)
	require.Contains(t, gotI[item], stx+"kestrel"+etx)
}
