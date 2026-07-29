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

// Revision history end to end against real PostgreSQL: who wrote each version,
// what changed between two of them, and what happens when somebody asks for an
// earlier one back.
//
// The single most important test in this file is
// TestWikiRevisions_RestoreRefusesToDropPreservedContentSilently. A restore is
// the one operation that can remove content nobody typed a keystroke to
// remove — an older version usually lacks the preserved unknown blocks the
// current one has, which is what makes it older — so if restore were exempt
// from the ADR-0012 refusal it would become the only way to lose preserved
// content without being asked. The refusal firing here is the proof that it is
// not exempt.
//
// The fixture, the personas and the page helpers are docFixture's, from
// wiki_document_integration_test.go; only what revision history needs is added
// here.

// ── fixture helpers ────────────────────────────────────────────────────────

// revisionRow is one row of the revisions list.
//
// AuthorName is a *string rather than a string because the query LEFT JOINs
// users: an unresolvable author must still leave its revision in the history,
// and a null name is how the surface is told to render "Unknown" rather than a
// person called "". Decoding it as a plain string would erase the difference
// between "the join found nobody" and "the join was never made" — which is
// exactly the regression TestWikiRevisions_ListNamesTheAuthorOfEachVersion is
// there to catch.
type revisionRow struct {
	ID         string  `json:"id"`
	PageID     string  `json:"page_id"`
	Version    int32   `json:"version"`
	Title      string  `json:"title"`
	AuthorID   string  `json:"author_id"`
	AuthorName *string `json:"author_name"`
}

// pageState is the page read route's response, narrowed to what history
// assertions need.
type pageState struct {
	Title   string          `json:"title"`
	Content string          `json:"content"`
	Version int32           `json:"version"`
	Doc     json.RawMessage `json:"doc"`
}

// diffSegmentBody is one run of text and what happened to it. Op is decoded as
// an int, not as the domain's DiffOp, so the assertions are about the numbers
// that actually go over the wire.
type diffSegmentBody struct {
	Op   int    `json:"op"`
	Text string `json:"text"`
}

type revisionDiffBody struct {
	FromVersion     int32             `json:"from_version"`
	ToVersion       int32             `json:"to_version"`
	TitleSegments   []diffSegmentBody `json:"title_segments"`
	ContentSegments []diffSegmentBody `json:"content_segments"`
}

// restoreOutcome is the restore route's response. Page is a pointer because a
// no-op restore publishes nothing and therefore reports no page at all — the
// absence is part of the contract, not an omission.
type restoreOutcome struct {
	Restored bool   `json:"restored"`
	Message  string `json:"message"`
	Page     *struct {
		Version int32           `json:"version"`
		Title   string          `json:"title"`
		Content string          `json:"content"`
		Doc     json.RawMessage `json:"doc"`
	} `json:"page"`
}

// renameUser gives a seeded user a distinctive display name. Every
// testutil-created user is called "Test User", so without this an assertion on
// author_name would pass against a query that resolved every revision to the
// same person — or to the caller.
func (f *docFixture) renameUser(t *testing.T, userID uuid.UUID, name string) {
	t.Helper()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE users SET display_name = $2 WHERE id = $1`, userID, name)
	require.NoError(t, err)
}

func (f *docFixture) revisions(t *testing.T, token, pageID string) []revisionRow {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodGet, f.pagePath(pageID, "/revisions"), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "listing revisions: %s", r.Body)
	var out []revisionRow
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

// revisionContent reads one stored revision's markdown back through the API, so
// "history was not rewritten" is asserted against what a caller would see
// rather than against a table read the surface might not agree with.
func (f *docFixture) revisionContent(t *testing.T, token, pageID string, version int32) string {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodGet,
		f.pagePath(pageID, fmt.Sprintf("/revisions/%d", version)), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "getting revision %d: %s", version, r.Body)
	var out struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out.Content
}

// revisionDocument reads a revision's stored document as text, returning nil
// when the column is NULL — which is what a markdown-era revision looks like.
func (f *docFixture) revisionDocument(t *testing.T, pageID string, version int32) *string {
	t.Helper()
	var stored *string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT doc::text FROM page_revisions WHERE page_id = $1 AND version = $2`,
		uuid.MustParse(pageID), version).Scan(&stored))
	return stored
}

func (f *docFixture) readPage(t *testing.T, token, pageID string) pageState {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodGet, f.pagePath(pageID, ""), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "reading page: %s", r.Body)
	var out pageState
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

func (f *docFixture) restore(t *testing.T, token, pageID string, version int32, body map[string]any) httpResult {
	t.Helper()
	return f.ts.requestAs(t, token, http.MethodPost,
		f.pagePath(pageID, fmt.Sprintf("/revisions/%d/restore", version)), body)
}

// restored decodes a restore that was expected to succeed.
func (f *docFixture) restored(t *testing.T, r httpResult) restoreOutcome {
	t.Helper()
	require.Equal(t, http.StatusOK, r.StatusCode, "restore: %s", r.Body)
	var out restoreOutcome
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

func (f *docFixture) diff(t *testing.T, token, pageID string, from, to int32) revisionDiffBody {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodGet,
		f.pagePath(pageID, fmt.Sprintf("/diff?from=%d&to=%d", from, to)), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "diffing %d..%d: %s", from, to, r.Body)
	var out revisionDiffBody
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

// paragraphDoc is a one-paragraph document: the smallest publishable thing that
// can be told apart from another by its text. doc.ToMarkdown projects it back to
// exactly that text, which is what makes the content assertions below exact
// rather than approximate.
func paragraphDoc(text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}`, text))
}

// joinSegments concatenates the text of every segment carrying one of the given
// ops, in order.
func joinSegments(segments []diffSegmentBody, ops ...int) string {
	wanted := make(map[int]bool, len(ops))
	for _, op := range ops {
		wanted[op] = true
	}
	var out string
	for _, segment := range segments {
		if wanted[segment.Op] {
			out += segment.Text
		}
	}
	return out
}

func versionsOf(rows []revisionRow) []int32 {
	out := make([]int32, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Version)
	}
	return out
}

// ── The author on the list ─────────────────────────────────────────────────

// TestWikiRevisions_ListNamesTheAuthorOfEachVersion covers the LEFT JOIN
// ListPageRevisions gained this phase.
//
// page_revisions.author_id has been stored since migration 005; it was only ever
// missing from the read, so a history view could show "who" as a UUID and
// nothing else. The two personas are given different display names on purpose:
// with one name in the fixture, a query that resolved every row to the same user
// — the caller, say, or the page's creator — would pass. Delete the JOIN and
// author_name is absent from the JSON, so both pointers decode as nil and the
// NotNil assertions fail; join it wrongly and the two names land on the wrong
// versions.
func TestWikiRevisions_ListNamesTheAuthorOfEachVersion(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	f.renameUser(t, f.contrib.ID, "Ines Okafor")
	f.renameUser(t, f.editor.ID, "Marcus Vale")

	pageID := f.createPage(t, f.contribTok, "Runbook", "First wording.")
	require.Equal(t, int32(2),
		f.publish(t, f.editorTok, pageID, "Runbook", paragraphDoc("Second wording."), 1, nil))

	rows := f.revisions(t, f.contribTok, pageID)
	require.Len(t, rows, 2, "one row per version, newest first")

	require.Equal(t, int32(2), rows[0].Version)
	require.Equal(t, f.editor.ID.String(), rows[0].AuthorID)
	require.NotNil(t, rows[0].AuthorName, "author_name must be resolved from the users table")
	require.Equal(t, "Marcus Vale", *rows[0].AuthorName)

	require.Equal(t, int32(1), rows[1].Version)
	require.Equal(t, f.contrib.ID.String(), rows[1].AuthorID)
	require.NotNil(t, rows[1].AuthorName)
	require.Equal(t, "Ines Okafor", *rows[1].AuthorName,
		"the creating author must survive on version 1 after somebody else published version 2")

	// The rest of the row is what a history view lists beside the name.
	require.Equal(t, pageID, rows[0].PageID)
	require.Equal(t, "Runbook", rows[0].Title)
	require.NotEqual(t, rows[0].ID, rows[1].ID, "each version is its own history row")
}

// ── History is never rewritten ─────────────────────────────────────────────

// TestWikiRevisions_RestoreAppendsAVersionAndRewritesNoHistory is the core
// contract of RestoreRevision, stated in its doc comment: restoring version 1
// onto a page at version 3 produces version 4 holding version 1's content, and
// versions 1, 2 and 3 stay exactly as they were. That is what makes a restore
// itself undoable — by restoring version 3.
//
// The row ids are compared before and after, not just the versions: an
// implementation that "restored" by deleting the versions above and re-writing
// them would keep the version numbers and fail here, which is the failure mode
// a count-only assertion would wave through.
func TestWikiRevisions_RestoreAppendsAVersionAndRewritesNoHistory(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Runbook", "Version one wording.")
	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Version two wording."), 1, nil))
	require.Equal(t, int32(3),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Version three wording."), 2, nil))

	before := f.revisions(t, f.contribTok, pageID)
	require.Equal(t, []int32{3, 2, 1}, versionsOf(before))

	out := f.restored(t, f.restore(t, f.contribTok, pageID, 1, map[string]any{"base_version": 3}))
	require.True(t, out.Restored)
	require.NotNil(t, out.Page)
	require.Equal(t, int32(4), out.Page.Version,
		"a restore appends a version; it must not move the page back onto the one it restored")
	require.Equal(t, "Version one wording.", out.Page.Content)

	// The page really is at version 4 carrying version 1's words.
	page := f.readPage(t, f.contribTok, pageID)
	require.Equal(t, int32(4), page.Version)
	require.Equal(t, "Version one wording.", page.Content)

	after := f.revisions(t, f.contribTok, pageID)
	require.Equal(t, []int32{4, 3, 2, 1}, versionsOf(after),
		"restoring version 1 must not remove the versions that came after it")

	// Every pre-existing row is the same row, not a rewritten one.
	for i, row := range before {
		matching := after[i+1]
		require.Equal(t, row.ID, matching.ID, "history row for version %d was replaced", row.Version)
		require.Equal(t, row.Version, matching.Version)
		require.Equal(t, row.Title, matching.Title)
		require.Equal(t, row.AuthorID, matching.AuthorID)
	}

	// And their content is untouched, including the versions the restore skipped
	// over — the append-only guarantee is about bytes, not just row counts.
	require.Equal(t, "Version one wording.", f.revisionContent(t, f.contribTok, pageID, 1))
	require.Equal(t, "Version two wording.", f.revisionContent(t, f.contribTok, pageID, 2))
	require.Equal(t, "Version three wording.", f.revisionContent(t, f.contribTok, pageID, 3))
	require.Equal(t, "Version one wording.", f.revisionContent(t, f.contribTok, pageID, 4),
		"the appended version must carry the restored version's content")
}

// ── The no-op ──────────────────────────────────────────────────────────────

// TestWikiRevisions_RestoringTheVersionThePageAlreadyHoldsIsANoOp.
//
// 200 rather than an error: nothing went wrong and the caller asked for a state
// the page is already in. What must NOT happen is a new version in which nothing
// changed — a history full of those is a history nobody can read. The version and
// the row count are both asserted because either alone would miss half of it: a
// version bump with no revision row, or a revision row with no version bump, are
// each their own defect.
func TestWikiRevisions_RestoringTheVersionThePageAlreadyHoldsIsANoOp(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Runbook", "Original wording.")
	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Current wording."), 1, nil))

	r := f.restore(t, f.contribTok, pageID, 2, map[string]any{"base_version": 2})
	require.Equal(t, http.StatusOK, r.StatusCode, "a no-op is not an error: %s", r.Body)

	var out restoreOutcome
	require.NoError(t, json.Unmarshal(r.Body, &out))
	require.False(t, out.Restored, "restoring the current version restores nothing")
	require.Contains(t, out.Message, "already this page's content",
		"the body must say why nothing happened; the UI shows this sentence verbatim")
	require.Nil(t, out.Page, "a no-op published no page, so it must not report one")

	require.Equal(t, int32(2), f.readPage(t, f.contribTok, pageID).Version,
		"a no-op restore must not bump the version")
	require.Equal(t, []int32{2, 1}, versionsOf(f.revisions(t, f.contribTok, pageID)),
		"a no-op restore must not add a history entry in which nothing changed")
}

// ── The version guard ──────────────────────────────────────────────────────

// TestWikiRevisions_RestoreIsNotExemptFromTheVersionGuard.
//
// A restore is an ordinary publish and gets no bypass. The stale base_version
// here is what a history view loaded before two other publishes landed would
// send, and answering it with a silent overwrite would lose both of them. The
// second half is the other arm of the same dialogue: the identical request,
// with the overwrite said out loud, is allowed.
func TestWikiRevisions_RestoreIsNotExemptFromTheVersionGuard(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Runbook", "Version one wording.")
	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Version two wording."), 1, nil))
	require.Equal(t, int32(3),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Version three wording."), 2, nil))

	r := f.restore(t, f.contribTok, pageID, 1, map[string]any{"base_version": 1})
	require.Equal(t, http.StatusConflict, r.StatusCode,
		"a restore carrying a stale base version must conflict like any other publish: %s", r.Body)

	var conflict struct {
		PageID          string `json:"page_id"`
		ExpectedVersion int32  `json:"expected_version"`
		CurrentPage     struct {
			Version int32 `json:"version"`
		} `json:"current_page"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &conflict))
	require.Equal(t, pageID, conflict.PageID)
	require.Equal(t, int32(1), conflict.ExpectedVersion)
	require.Equal(t, int32(3), conflict.CurrentPage.Version,
		"the body must carry the current state, or the UI cannot offer the two arms")
	require.NotEmpty(t, conflict.Message)

	// The refusal wrote nothing at all.
	unchanged := f.readPage(t, f.contribTok, pageID)
	require.Equal(t, int32(3), unchanged.Version)
	require.Equal(t, "Version three wording.", unchanged.Content)
	require.Equal(t, []int32{3, 2, 1}, versionsOf(f.revisions(t, f.contribTok, pageID)),
		"a refused restore must not record a revision")

	// The same request with overwrite lands on whatever is current, as version 4.
	out := f.restored(t, f.restore(t, f.contribTok, pageID, 1,
		map[string]any{"base_version": 1, "overwrite": true}))
	require.True(t, out.Restored)
	require.NotNil(t, out.Page)
	require.Equal(t, int32(4), out.Page.Version)
	require.Equal(t, "Version one wording.", out.Page.Content)
	require.Equal(t, []int32{4, 3, 2, 1}, versionsOf(f.revisions(t, f.contribTok, pageID)),
		"an overwriting restore is still an append")
}

// ── The lost-content refusal ───────────────────────────────────────────────

// restoreLegacyHTML is a block of raw HTML of the kind the old markdown editor
// wrote. The document model cannot represent it, so it is preserved verbatim
// rather than converted — which is what makes it the content a restore can lose.
const restoreLegacyHTML = `<div class="callout" data-id="7">An imported callout</div>`

// TestWikiRevisions_RestoreRefusesToDropPreservedContentSilently is the most
// important test in this file.
//
// An older version will usually NOT contain the preserved unknown content the
// current version has — that is what makes it older. So restoring it removes
// that content, and ADR-0012 says a removal is acknowledged, never inferred.
// Giving restore a bypass would make it the one way to lose preserved content
// without being asked: every other route to the same deletion goes through the
// publish refusal, and a "restore version 3" button that quietly discarded three
// imported macros would look, to the person who pressed it, exactly like a
// button that had worked.
//
// The page is built the way a real one arrives: markdown carrying raw HTML,
// converted on first edit — the same on-ramp
// TestDocumentAPI_LegacyMarkdownPageOpensAndPublishesWithoutLoss walks — so the
// preserved block is genuine rather than planted straight into the column.
func TestWikiRevisions_RestoreRefusesToDropPreservedContentSilently(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// Version 1: ordinary markdown, nothing the editor cannot show.
	pageID := f.createPage(t, f.contribTok, "Runbook", "Plain prose only.")

	// Version 2: still markdown, now carrying the raw HTML.
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(pageID, ""),
		map[string]any{
			"title":            "Runbook",
			"content":          "Plain prose only.\n\n" + restoreLegacyHTML + "\n",
			"expected_version": 1,
		})
	require.Equal(t, http.StatusOK, r.StatusCode, "markdown save: %s", r.Body)

	// Version 3: the same content published through the editor, which is what
	// puts the preserved block into pages.doc.
	opened := f.openDocument(t, f.contribTok, pageID)
	require.Equal(t, int32(2), opened.BaseVersion)
	require.Equal(t, []string{"u1"}, opened.PreservedIDs,
		"the raw HTML must have been preserved, or the rest of this test proves nothing")
	require.Equal(t, int32(3),
		f.publish(t, f.contribTok, pageID, "Runbook", opened.Doc, 2, nil))
	require.Contains(t, f.storedDocument(t, pageID), "legacyHtmlBlock")

	// Restoring version 1 — which predates the HTML — would remove it.
	r = f.restore(t, f.contribTok, pageID, 1, map[string]any{"base_version": 3})
	require.Equal(t, http.StatusConflict, r.StatusCode,
		"a restore that would drop preserved content must be refused, not performed: %s", r.Body)

	var lost struct {
		LostIDs []string `json:"lost_ids"`
		Lost    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"lost"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &lost))
	require.Equal(t, []string{"u1"}, lost.LostIDs)
	require.Len(t, lost.Lost, 1)
	require.Equal(t, "u1", lost.Lost[0].ID)
	require.Equal(t, "legacyHtmlBlock", lost.Lost[0].Name,
		"the author must be told WHAT would go, not just that something would")
	require.NotEmpty(t, lost.Message)

	// The refusal is not a partial write.
	require.Equal(t, int32(3), f.readPage(t, f.contribTok, pageID).Version)
	require.Contains(t, f.storedDocument(t, pageID), "legacyHtmlBlock",
		"a refused restore must leave the preserved content in the page")
	require.Equal(t, []int32{3, 2, 1}, versionsOf(f.revisions(t, f.contribTok, pageID)))

	// Acknowledged, the same restore goes through — losing an inert block on
	// purpose is a legitimate edit, it just has to be said.
	out := f.restored(t, f.restore(t, f.contribTok, pageID, 1,
		map[string]any{"base_version": 3, "acknowledged_lost_ids": []string{"u1"}}))
	require.True(t, out.Restored)
	require.NotNil(t, out.Page)
	require.Equal(t, int32(4), out.Page.Version)
	require.Equal(t, "Plain prose only.", out.Page.Content)
	require.NotContains(t, f.storedDocument(t, pageID), "legacyHtmlBlock",
		"an acknowledged restore must actually remove what it said it would")

	// And the version that held it is still in the history, so the removal is
	// undoable by restoring version 3.
	require.Contains(t, f.revisionContent(t, f.contribTok, pageID, 3), "An imported callout")
}

// ── The markdown era ───────────────────────────────────────────────────────

// TestWikiRevisions_MarkdownEraRevisionRestoresAsADocument.
//
// A revision written before the document editor carries doc IS NULL and only
// markdown. Restoring it converts through the same deterministic
// doc.FromMarkdown the editor's own on-ramp uses and publishes the result, so
// the page stays document-backed with content re-derived from it. The assertion
// that matters is that BOTH columns are written: a restore that wrote only
// `content` would leave a document-backed page whose document still held the
// version nobody asked for, and every subsequent open would show the wrong text.
func TestWikiRevisions_MarkdownEraRevisionRestoresAsADocument(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	const legacy = "# Heading\n\nMarkdown-era wording with **bold**."
	pageID := f.createPage(t, f.contribTok, "Runbook", legacy)
	require.Nil(t, f.revisionDocument(t, pageID, 1),
		"version 1 must be markdown-era, or this test is not exercising the conversion path")

	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Rewritten in the editor."), 1, nil))
	require.NotNil(t, f.revisionDocument(t, pageID, 2))

	out := f.restored(t, f.restore(t, f.contribTok, pageID, 1, map[string]any{"base_version": 2}))
	require.True(t, out.Restored)
	require.NotNil(t, out.Page)
	require.Equal(t, int32(3), out.Page.Version)

	// The page is document-backed: the markdown-era content came back as a
	// document, not as a doc-less page.
	stored := f.storedDocument(t, pageID)
	require.NotEmpty(t, stored,
		"restoring a markdown-era revision must leave the page document-backed")
	require.Contains(t, stored, `"type":"heading"`,
		"the conversion must produce real document structure, not one paragraph of raw markdown")
	require.Contains(t, stored, "Markdown-era wording with ")

	// …and content is re-derived from it, so search and any legacy reader keep
	// working.
	page := f.readPage(t, f.contribTok, pageID)
	require.Equal(t, legacy, page.Content)
	require.NotEqual(t, "null", string(page.Doc), "the page read must carry the document")

	// The appended revision holds both too, which is what lets a later overwrite
	// resolve preserved content against this version.
	require.Equal(t, legacy, f.revisionContent(t, f.contribTok, pageID, 3))
	require.NotNil(t, f.revisionDocument(t, pageID, 3),
		"the revision recorded by a restore must carry its document")
}

// ── Absence ────────────────────────────────────────────────────────────────

// TestWikiRevisions_RestoringAVersionThatDoesNotExistIsNotFound. A client
// asking for a version that was never published gets 404, not a 500 and not a
// silently invented empty page.
func TestWikiRevisions_RestoringAVersionThatDoesNotExistIsNotFound(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Runbook", "Only ever one version.")

	r := f.restore(t, f.contribTok, pageID, 99, map[string]any{"base_version": 1})
	require.Equal(t, http.StatusNotFound, r.StatusCode, "body: %s", r.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "the refusal must carry the API error envelope")
	require.Equal(t, "NOT_FOUND", body.Error.Code)

	// And it published nothing on its way to saying so.
	require.Equal(t, int32(1), f.readPage(t, f.contribTok, pageID).Version)
	require.Equal(t, []int32{1}, versionsOf(f.revisions(t, f.contribTok, pageID)))
}

// ── Capability ─────────────────────────────────────────────────────────────

// TestWikiRevisions_ContributorCannotRestoreSomebodyElsesPage.
//
// The persona is a CONTRIBUTOR and that choice is the whole test. A viewer would
// prove nothing: RequireWriteFloor(create_items) refuses viewers before the
// handler runs, so a viewer-based test passes with access.CanEditEntity deleted
// from editablePage — it would assert the middleware, not the gate. f.second
// clears the write floor and holds edit_own_items but not edit_any_item, so on a
// page f.contrib created only the in-handler check can refuse it.
//
// The persona's power is demonstrated rather than asserted about: f.second
// restores a page of its OWN through the same route, so the 403 below cannot be
// read as "this user cannot restore anything". That pairing is what makes the
// refusal evidence about the gate — remove access.CanEditEntity from
// editablePage and the 403 becomes a 200 while the own-page arm keeps passing.
func TestWikiRevisions_ContributorCannotRestoreSomebodyElsesPage(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// pages.author_id is the creator and is never updated by an edit, so this is
	// the ownership key the gate uses.
	pageID := f.createPage(t, f.contribTok, "Runbook", "Version one wording.")
	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("Version two wording."), 1, nil))

	requireAPIForbidden(t, f.restore(t, f.secondTok, pageID, 1, map[string]any{"base_version": 2}))

	// The refusal wrote nothing.
	unchanged := f.readPage(t, f.contribTok, pageID)
	require.Equal(t, int32(2), unchanged.Version)
	require.Equal(t, "Version two wording.", unchanged.Content)
	require.Equal(t, []int32{2, 1}, versionsOf(f.revisions(t, f.contribTok, pageID)))

	// Reading the history is not restricted — restore adds a write gate, not a
	// read one, so the same persona can still see what it may not restore.
	require.Equal(t, http.StatusOK, f.ts.requestAs(t, f.secondTok, http.MethodGet,
		f.pagePath(pageID, "/revisions"), nil).StatusCode)
	require.Equal(t, http.StatusOK, f.ts.requestAs(t, f.secondTok, http.MethodGet,
		f.pagePath(pageID, "/diff?from=1&to=2"), nil).StatusCode)

	// f.second is refused on somebody else's page and allowed on its own, which
	// is what rules out the alternative reading of the 403 above — that the
	// persona is simply powerless and the write floor did the refusing.
	theirs := f.createPage(t, f.secondTok, "Theirs", "Their version one.")
	require.Equal(t, int32(2),
		f.publish(t, f.secondTok, theirs, "Theirs", paragraphDoc("Their version two."), 1, nil))
	mine := f.restored(t, f.restore(t, f.secondTok, theirs, 1, map[string]any{"base_version": 2}))
	require.True(t, mine.Restored, "a contributor must be able to restore a page it created")
	require.NotNil(t, mine.Page)
	require.Equal(t, "Their version one.", mine.Page.Content)

	// And the page's own author is allowed on the page f.second was refused,
	// so the refusal is about ownership rather than about the page.
	out := f.restored(t, f.restore(t, f.contribTok, pageID, 1, map[string]any{"base_version": 2}))
	require.True(t, out.Restored)
	require.NotNil(t, out.Page)
	require.Equal(t, int32(3), out.Page.Version)
}

// ── The diff ───────────────────────────────────────────────────────────────

// TestWikiRevisions_DiffReturnsStructuredSegments covers the shape the diff
// endpoint gained this phase.
//
// It used to return DiffPrettyText — a string with ANSI terminal colour codes
// spliced into it, which over a JSON API consumed by a browser is not colour but
// unprintable bytes in the middle of the words. Segments put the decision about
// how to show a change with the surface showing it.
//
// The load-bearing assertion is the reconstruction: segments are a partition of
// BOTH revisions, so deletions plus equalities must rebuild the from-revision
// character for character and insertions plus equalities the to-revision. That
// is the property a rendering cannot fake — a surface that dropped a segment, or
// re-encoded one, or mislabelled an op, fails it.
func TestWikiRevisions_DiffReturnsStructuredSegments(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Runbook", "The rollout uses helmfile.")
	require.Equal(t, int32(2),
		f.publish(t, f.contribTok, pageID, "Runbook", paragraphDoc("The rollout uses terraform."), 1, nil))

	d := f.diff(t, f.contribTok, pageID, 1, 2)
	require.Equal(t, int32(1), d.FromVersion)
	require.Equal(t, int32(2), d.ToVersion)
	require.Empty(t, d.TitleSegments,
		"the title did not change, so the caller must not be handed a title row to render")

	from := f.revisionContent(t, f.contribTok, pageID, 1)
	to := f.revisionContent(t, f.contribTok, pageID, 2)
	require.Equal(t, "The rollout uses helmfile.", from)
	require.Equal(t, "The rollout uses terraform.", to)
	require.Equal(t, from, joinSegments(d.ContentSegments, -1, 0),
		"deletions and equalities must reconstruct the from-revision exactly")
	require.Equal(t, to, joinSegments(d.ContentSegments, 0, 1),
		"insertions and equalities must reconstruct the to-revision exactly")

	// The removed and added words are carried by segments of the right op, so a
	// reader can colour them without re-diffing anything.
	require.Equal(t, "helmfile", joinSegments(d.ContentSegments, -1))
	require.Equal(t, "terraform", joinSegments(d.ContentSegments, 1))
	require.NotEmpty(t, joinSegments(d.ContentSegments, 0),
		"the unchanged surroundings must be carried too, or the change has no context")

	// No segment is empty: diffmatchpatch emits them for an insert-only or
	// delete-only change and they would render as stray markers.
	for i, segment := range d.ContentSegments {
		require.NotEmpty(t, segment.Text, "content segment %d carries no text", i)
		require.Contains(t, []int{-1, 0, 1}, segment.Op, "content segment %d has op %d", i, segment.Op)
	}

	// A title change DOES produce title segments. Without this half, the
	// "empty when unchanged" assertion above would pass against a surface that
	// never emitted a title segment at all.
	require.Equal(t, int32(3),
		f.publish(t, f.contribTok, pageID, "Runbook, revised", paragraphDoc("The rollout uses terraform."), 2, nil))

	titled := f.diff(t, f.contribTok, pageID, 2, 3)
	require.NotEmpty(t, titled.TitleSegments, "a changed title must be reported as segments")
	require.Equal(t, "Runbook", joinSegments(titled.TitleSegments, -1, 0))
	require.Equal(t, "Runbook, revised", joinSegments(titled.TitleSegments, 0, 1))
	for i, segment := range titled.TitleSegments {
		require.NotEmpty(t, segment.Text, "title segment %d carries no text", i)
	}
	for i, segment := range titled.ContentSegments {
		require.Equal(t, 0, segment.Op,
			"the body is identical between versions 2 and 3, so content segment %d must be an equality", i)
	}
}
