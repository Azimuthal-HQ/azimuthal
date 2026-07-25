package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The Codex document surface, end to end against real PostgreSQL and the
// harness's object store (issue #15, ADR-0012).
//
// The single most important test in this file is
// TestDocumentAPI_UnknownContentSurvivesAnEditAndPublish. The unit-level
// guarantee is proven in internal/core/wiki/doc; this proves the guarantee holds
// through the HTTP surface, the transaction and the column — the whole path a
// real save takes.

// docFixture is a Codex space with one page, plus the personas the capability
// tests need.
type docFixture struct {
	ts      *testServer
	spaceID string
	pageID  string

	// contrib holds contributor (create_items + edit_own_items, NOT
	// edit_any_item). It is the only persona that can prove the in-handler gate:
	// a viewer never gets past the write floor, so a "viewer is refused" test
	// would pass with the gate deleted (CLAUDE.md section 2).
	contrib    testutil.User
	contribTok string
	// second is another contributor. It holds edit_own_items only, so on
	// f.pageID — which contrib created — it is refused. That is what makes it the
	// right persona for the capability test and the wrong one for anything that
	// needs a second person to actually edit the same page.
	second    testutil.User
	secondTok string
	// editor holds agent, which is the lowest role carrying edit_any_item
	// (ADR-0007). Tests that need somebody else to publish the same page use it.
	editor    testutil.User
	editorTok string
}

func newDocFixture(t *testing.T) *docFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &docFixture{ts: ts}
	f.spaceID = createScopedSpace(t, ts, "Codex Docs", "codex-docs", "codex")
	spaceUUID := uuid.MustParse(f.spaceID)

	mk := func(role access.Role) (testutil.User, string) {
		u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		_, err := ts.GrantService.Create(context.Background(), ts.OrgID, spaceUUID,
			access.SubjectUser, u.ID, role, ts.UserID)
		require.NoError(t, err)
		return u, ts.tokenFor(t, u.ID, u.Email)
	}
	f.contrib, f.contribTok = mk(access.RoleContributor)
	f.second, f.secondTok = mk(access.RoleContributor)
	f.editor, f.editorTok = mk(access.RoleAgent)

	f.pageID = f.createPage(t, f.contribTok, "Runbook", "")
	return f
}

func (f *docFixture) wikiPath(suffix string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki%s", f.ts.OrgID, f.spaceID, suffix)
}

func (f *docFixture) pagePath(pageID, suffix string) string {
	return f.wikiPath("/" + pageID + suffix)
}

// createPage creates a page as the given persona and returns its id.
func (f *docFixture) createPage(t *testing.T, token, title, content string) string {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodPost, f.wikiPath("/"),
		map[string]any{"title": title, "content": content})
	require.Equal(t, http.StatusCreated, r.StatusCode, "creating page: %s", r.Body)
	var page struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	return page.ID
}

// editableDocument is the /document response.
type editableDocument struct {
	PageID       string          `json:"page_id"`
	Title        string          `json:"title"`
	Doc          json.RawMessage `json:"doc"`
	BaseVersion  int32           `json:"base_version"`
	SourceFormat string          `json:"source_format"`
	PreservedIDs []string        `json:"preserved_ids"`
	Draft        *struct {
		Title       string          `json:"title"`
		Doc         json.RawMessage `json:"doc"`
		BaseVersion int32           `json:"base_version"`
		Stale       bool            `json:"stale"`
	} `json:"draft"`
}

func (f *docFixture) openDocument(t *testing.T, token, pageID string) editableDocument {
	t.Helper()
	r := f.ts.requestAs(t, token, http.MethodGet, f.pagePath(pageID, "/document"), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "opening document: %s", r.Body)
	var out editableDocument
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

// publish publishes a document and returns the new version.
func (f *docFixture) publish(t *testing.T, token, pageID, title string, document json.RawMessage, baseVersion int32, acknowledged []string) int32 {
	t.Helper()
	body := map[string]any{"title": title, "doc": document, "base_version": baseVersion}
	if len(acknowledged) > 0 {
		body["acknowledged_lost_ids"] = acknowledged
	}
	r := f.ts.requestAs(t, token, http.MethodPost, f.pagePath(pageID, "/publish"), body)
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)
	var page struct {
		Version int32 `json:"version"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	return page.Version
}

// setStoredDocument writes a document straight into pages.doc, which is how a
// page that an importer produced — full of content this editor has never heard
// of — is put in front of the surface under test.
//
// Note what it does NOT do: write a matching page_revisions row. That asymmetry
// is deliberate and is itself under test — see
// TestDocumentAPI_OverwriteRefusesWhenTheBaseVersionHasNoDocument.
func (f *docFixture) setStoredDocument(t *testing.T, pageID, document string) {
	t.Helper()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE pages SET doc = $2 WHERE id = $1`, uuid.MustParse(pageID), document)
	require.NoError(t, err)
}

// setStoredDocumentAtVersion establishes a page version holding document in both
// pages.doc and its page_revisions row — what a correct importer writes.
func (f *docFixture) setStoredDocumentAtVersion(t *testing.T, pageID string, version int32, document string) {
	t.Helper()
	id := uuid.MustParse(pageID)
	ctx := context.Background()

	_, err := f.ts.DB.Pool.Exec(ctx,
		`UPDATE pages SET doc = $2, version = $3 WHERE id = $1`, id, document, version)
	require.NoError(t, err)

	_, err = f.ts.DB.Pool.Exec(ctx,
		`INSERT INTO page_revisions (id, page_id, version, title, content, doc, author_id)
		 SELECT $1, id, $2, title, content, $3, author_id FROM pages WHERE id = $4`,
		uuid.New(), version, document, id)
	require.NoError(t, err)
}

// storedDocument reads pages.doc back as text, so assertions can be made on the
// bytes rather than on a re-decoding of them.
func (f *docFixture) storedDocument(t *testing.T, pageID string) string {
	t.Helper()
	var stored *string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT doc::text FROM pages WHERE id = $1`, uuid.MustParse(pageID)).Scan(&stored))
	if stored == nil {
		return ""
	}
	return *stored
}

// ── The binding test ───────────────────────────────────────────────────────

// storedImportedDocument is a page as an importer would leave it: paragraphs the
// editor understands around a macro it does not, with attributes out of
// alphabetical order, an exponent literal, and angle brackets in the body.
const storedImportedDocument = `{"type":"doc","content":[` +
	`{"type":"paragraph","content":[{"type":"text","text":"Before the diagram."}]},` +
	`{"type":"gliffyDiagram","attrs":{"zzz":"last","macroId":"a1b2","scale":1e2},` +
	`"content":[{"type":"gliffyLayer","attrs":{"raw":"<shape a=\"1\">x &amp; y</shape>"}}]},` +
	`{"type":"paragraph","content":[{"type":"text","text":"After the diagram."}]}` +
	`]}`

const importedMacroSubtree = `{"type":"gliffyDiagram","attrs":{"zzz":"last","macroId":"a1b2","scale":1e2},` +
	`"content":[{"type":"gliffyLayer","attrs":{"raw":"<shape a=\"1\">x &amp; y</shape>"}}]}`

// TestDocumentAPI_UnknownContentSurvivesAnEditAndPublish is ADR-0012 point 3
// asserted through the whole stack: HTTP in, HTTP out, the publish transaction,
// and the `json` column.
//
// The unit test in internal/core/wiki/doc proves Shield and Restore preserve
// bytes. This proves nothing between them loses those bytes — which is the part
// that would actually bite, because every layer here is a chance to re-encode.
func TestDocumentAPI_UnknownContentSurvivesAnEditAndPublish(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	f.setStoredDocument(t, f.pageID, storedImportedDocument)

	// Open: the editor is never handed the unknown type.
	opened := f.openDocument(t, f.contribTok, f.pageID)
	require.Equal(t, "document", opened.SourceFormat)
	require.Equal(t, []string{"u1"}, opened.PreservedIDs)
	require.Contains(t, string(opened.Doc), `"unknownContent"`)
	require.NotContains(t, string(opened.Doc), `"type":"gliffyDiagram"`,
		"the raw unknown node reached the editor; ProseMirror would drop it on load")

	// Edit around it, re-serialising the whole document the way a browser does.
	edited := reserialise(t, opened.Doc, "Before the diagram.", "Before the diagram, edited.")

	// Publish.
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": opened.Title, "doc": edited, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	// The stored bytes still contain the macro, character for character.
	stored := f.storedDocument(t, f.pageID)
	require.Contains(t, stored, importedMacroSubtree,
		"the preserved macro is not byte-identical in storage after an edit-and-publish cycle")
	require.Contains(t, stored, "Before the diagram, edited.", "the edit did not land")

	// And the markdown projection carries the macro's text, so the page is still
	// findable — ADR-0012 allows indexing an unknown body as plain text.
	var content string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT content FROM pages WHERE id = $1`, uuid.MustParse(f.pageID)).Scan(&content))
	require.Contains(t, content, "Before the diagram, edited.")
	require.Contains(t, content, "shape a=", "preserved content must still reach the search projection")
}

// TestDocumentAPI_PublishRefusesToDropPreservedContentSilently is the guard that
// turns the ADR-0012 catastrophe into a loud refusal.
//
// A client that lost the preserved block — because its schema dropped it, which
// is exactly the failure mode — sends a document without the placeholder. The
// server refuses, names what would be lost, and leaves the page alone.
func TestDocumentAPI_PublishRefusesToDropPreservedContentSilently(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	f.setStoredDocument(t, f.pageID, storedImportedDocument)

	opened := f.openDocument(t, f.contribTok, f.pageID)

	// Everything but the two paragraphs — what a save would carry if ProseMirror
	// had silently dropped the block on load.
	stripped := json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Before the diagram."}]},` +
		`{"type":"paragraph","content":[{"type":"text","text":"After the diagram."}]}` +
		`]}`)

	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": opened.Title, "doc": stripped, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusConflict, r.StatusCode, "a silent loss must be refused: %s", r.Body)

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
	require.Equal(t, "gliffyDiagram", lost.Lost[0].Name,
		"the author must be told WHAT would be lost, not just that something would")
	require.NotEmpty(t, lost.Message)

	// The page is untouched: the refusal is not a partial write.
	require.Contains(t, f.storedDocument(t, f.pageID), importedMacroSubtree)
	require.Equal(t, int32(1), f.openDocument(t, f.contribTok, f.pageID).BaseVersion,
		"a refused publish must not bump the version")

	// Acknowledging the removal lets it through — deleting an inert block is a
	// legitimate edit, it just has to be said.
	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{
			"title": opened.Title, "doc": stripped, "base_version": opened.BaseVersion,
			"acknowledged_lost_ids": []string{"u1"},
		})
	require.Equal(t, http.StatusOK, r.StatusCode, "an acknowledged removal must be allowed: %s", r.Body)
	require.NotContains(t, f.storedDocument(t, f.pageID), "gliffyDiagram")
}

// TestDocumentAPI_PublishRefusesAnUnresolvablePlaceholder: a placeholder id with
// nothing behind it means the document was prepared against a different version,
// and writing the placeholder as though it were content is not an option.
func TestDocumentAPI_PublishRefusesAnUnresolvablePlaceholder(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	f.setStoredDocument(t, f.pageID, storedImportedDocument)
	opened := f.openDocument(t, f.contribTok, f.pageID)

	invented := json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"unknownContent","attrs":{"az_id":"u99","az_name":"invented","az_source":"document","az_raw":"{}","az_text":""}}` +
		`]}`)

	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": opened.Title, "doc": invented, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode, "body: %s", r.Body)
	require.Contains(t, f.storedDocument(t, f.pageID), importedMacroSubtree)
}

// ── The legacy on-ramp ─────────────────────────────────────────────────────

// TestDocumentAPI_LegacyMarkdownPageOpensAndPublishesWithoutLoss walks the
// conversion path a real Codex page takes the first time somebody opens it in
// the new editor. Nothing in the page may be lost, including the inline HTML the
// old markdown editor wrote for coloured text.
func TestDocumentAPI_LegacyMarkdownPageOpensAndPublishesWithoutLoss(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	const legacy = "# Runbook\n\n" +
		"Steps with **bold** and a [link](https://example.com/docs).\n\n" +
		"- [x] Done\n- [ ] Pending\n\n" +
		"> A quoted warning.\n\n" +
		"```bash\necho hello\n```\n\n" +
		"| Env | Host |\n| --- | --- |\n| prod | prod.example |\n\n" +
		"Coloured <span style=\"color:#e53e3e\">text</span> from the old editor.\n\n" +
		"<div class=\"callout\" data-id=\"7\">Legacy HTML block</div>\n"

	pageID := f.createPage(t, f.contribTok, "Legacy", legacy)

	opened := f.openDocument(t, f.contribTok, pageID)
	require.Equal(t, "markdown", opened.SourceFormat,
		"a page that has only ever held markdown must be reported as converted")
	// Three, not two: goldmark reports `<span …>` and `</span>` as separate raw
	// inline runs, so each tag is preserved on its own around the text between
	// them — which stays ordinary editable text. Preserving the tags rather than
	// swallowing the run is the honest outcome: the colour cannot be represented,
	// and the words must not disappear with it.
	require.Len(t, opened.PreservedIDs, 3,
		"expected the two span tags and the HTML block to be preserved")

	// Opening a page is a read: nothing is written until publish.
	require.Empty(t, f.storedDocument(t, pageID),
		"opening a legacy page must not write a document — conversion happens on publish, not on read")

	edited := reserialise(t, opened.Doc, "A quoted warning.", "A quoted warning, revised.")
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(pageID, "/publish"),
		map[string]any{"title": "Legacy", "doc": edited, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	stored := f.storedDocument(t, pageID)
	// Every construct survived, and the two unconvertible ones survived verbatim.
	for _, fragment := range []string{
		"Runbook", "bold", "https://example.com/docs",
		"Done", "Pending", "A quoted warning, revised.",
		"echo hello", "prod.example",
		`style=\"color:#e53e3e\"`,
		`class=\"callout\" data-id=\"7\"`, "Legacy HTML block",
	} {
		require.Contains(t, stored, fragment, "converting a legacy page lost %q", fragment)
	}

	// The markdown column is still populated, so search and any legacy reader
	// keep working.
	var content string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT content FROM pages WHERE id = $1`, uuid.MustParse(pageID)).Scan(&content))
	require.Contains(t, content, "Runbook")
	require.Contains(t, content, "echo hello")
}

// TestDocumentAPI_MarkdownPageIsSearchableAfterPublish is the search_vector's
// half of the same story. pages.search_vector is GENERATED over title and
// content (migration 009); if publish stopped writing the markdown projection,
// every document-backed page would silently vanish from search.
func TestDocumentAPI_MarkdownPageIsSearchableAfterPublish(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Deployment", "placeholder")
	opened := f.openDocument(t, f.contribTok, pageID)

	document := json.RawMessage(`{"type":"doc","content":[
		{"type":"paragraph","content":[{"type":"text","text":"The kubernetes rollout uses helmfile."}]},
		{"type":"someImportedMacro","attrs":{"body":"grafana dashboards live here"}}
	]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(pageID, "/publish"),
		map[string]any{"title": "Deployment", "doc": document, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	// Text in the document is findable…
	found := f.ts.requestAs(t, f.contribTok, http.MethodGet, f.wikiPath("/search?q=helmfile"), nil)
	require.Equal(t, http.StatusOK, found.StatusCode)
	require.Contains(t, string(found.Body), pageID, "a published document must be searchable")

	// …and so is the preserved content's plain text.
	found = f.ts.requestAs(t, f.contribTok, http.MethodGet, f.wikiPath("/search?q=grafana"), nil)
	require.Equal(t, http.StatusOK, found.StatusCode)
	require.Contains(t, string(found.Body), pageID,
		"preserved content must reach the search index as plain text (ADR-0012)")
}

// TestDocumentAPI_LegacyRendererStillWorksForPagesNeverOpenedInTheEditor: the
// old markdown read path must not change for a page nobody has published as a
// document. Both storage formats have to render.
func TestDocumentAPI_LegacyRendererStillWorksForPagesNeverOpenedInTheEditor(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	pageID := f.createPage(t, f.contribTok, "Untouched", "# Still markdown\n\nWith *emphasis*.")

	// The page read returns the markdown unchanged and no document.
	r := f.ts.requestAs(t, f.contribTok, http.MethodGet, f.pagePath(pageID, ""), nil)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var page struct {
		Content string          `json:"content"`
		Doc     json.RawMessage `json:"doc"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	require.Equal(t, "# Still markdown\n\nWith *emphasis*.", page.Content)
	require.True(t, len(page.Doc) == 0 || string(page.Doc) == "null",
		"a page never published as a document must carry no document")

	// And the server-side markdown renderer is untouched.
	rendered := f.ts.requestAs(t, f.contribTok, http.MethodGet, f.pagePath(pageID, "/render"), nil)
	require.Equal(t, http.StatusOK, rendered.StatusCode)
	require.Contains(t, string(rendered.Body), "<h1")
	require.Contains(t, string(rendered.Body), "<em>emphasis</em>")
}

// ── Drafts ─────────────────────────────────────────────────────────────────

// TestDocumentAPI_DraftIsVisibleOnlyToItsAuthor is the whole point of drafts:
// readers see the published version until the author publishes.
func TestDocumentAPI_DraftIsVisibleOnlyToItsAuthor(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	draftDoc := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"My unpublished thinking"}]}]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook WIP", "doc": draftDoc, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "autosave: %s", r.Body)

	// The author sees their draft…
	mine := f.openDocument(t, f.contribTok, f.pageID)
	require.NotNil(t, mine.Draft, "the author must get their own draft back")
	require.Equal(t, "Runbook WIP", mine.Draft.Title)
	require.Contains(t, string(mine.Draft.Doc), "My unpublished thinking")
	require.False(t, mine.Draft.Stale)

	// …and nobody else does, not even another contributor on the same page.
	theirs := f.openDocument(t, f.secondTok, f.pageID)
	require.Nil(t, theirs.Draft, "another user must not see somebody else's draft")
	require.NotContains(t, string(theirs.Doc), "My unpublished thinking",
		"an unpublished draft must not leak into the published document")

	// The published page is unchanged for everyone.
	published := f.ts.requestAs(t, f.secondTok, http.MethodGet, f.pagePath(f.pageID, ""), nil)
	require.Equal(t, http.StatusOK, published.StatusCode)
	require.NotContains(t, string(published.Body), "My unpublished thinking")

	// Each author gets their own draft on the same page.
	r = f.ts.requestAs(t, f.editorTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Their WIP", "doc": draftDoc, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "Runbook WIP", f.openDocument(t, f.contribTok, f.pageID).Draft.Title,
		"a second author's draft must not overwrite the first's")
}

// TestDocumentAPI_DraftSurvivesNavigatingAwayAndBack — a draft is server-side
// state, not editor state, so leaving the page and coming back restores it.
// Asserted by re-reading through a fresh request, which is all "navigating away"
// means to the API.
func TestDocumentAPI_DraftSurvivesNavigatingAwayAndBack(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	draftDoc := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Half-written"}]}]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook", "doc": draftDoc, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode)

	// Look at a different page, then come back.
	other := f.createPage(t, f.contribTok, "Elsewhere", "")
	require.Nil(t, f.openDocument(t, f.contribTok, other).Draft)
	require.Contains(t, string(f.openDocument(t, f.contribTok, f.pageID).Draft.Doc), "Half-written")

	// A new session — a different token for the same user — sees it too: the
	// draft belongs to the person, not to the browser tab.
	fresh := f.ts.tokenFor(t, f.contrib.ID, f.contrib.Email)
	require.Contains(t, string(f.openDocument(t, fresh, f.pageID).Draft.Doc), "Half-written")

	// The drafts list names the page.
	list := f.ts.requestAs(t, f.contribTok, http.MethodGet, f.wikiPath("/drafts"), nil)
	require.Equal(t, http.StatusOK, list.StatusCode, "body: %s", list.Body)
	var drafts []struct {
		PageID    string `json:"page_id"`
		PageTitle string `json:"page_title"`
		Stale     bool   `json:"stale"`
	}
	require.NoError(t, json.Unmarshal(list.Body, &drafts))
	require.Len(t, drafts, 1)
	require.Equal(t, f.pageID, drafts[0].PageID)
	require.False(t, drafts[0].Stale)

	// And it is the caller's list, not the space's.
	otherList := f.ts.requestAs(t, f.secondTok, http.MethodGet, f.wikiPath("/drafts"), nil)
	require.Equal(t, http.StatusOK, otherList.StatusCode)
	var theirs []json.RawMessage
	require.NoError(t, json.Unmarshal(otherList.Body, &theirs))
	require.Empty(t, theirs, "the drafts list must be author-scoped")
}

// TestDocumentAPI_DiscardDraftLeavesThePublishedPageAlone.
func TestDocumentAPI_DiscardDraftLeavesThePublishedPageAlone(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	draftDoc := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Regrettable"}]}]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook", "doc": draftDoc, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode)

	r = f.ts.requestAs(t, f.contribTok, http.MethodDelete, f.pagePath(f.pageID, "/draft"), nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	require.Nil(t, f.openDocument(t, f.contribTok, f.pageID).Draft)
	require.Equal(t, int32(1), f.openDocument(t, f.contribTok, f.pageID).BaseVersion,
		"discarding a draft must not touch the published page")

	// Discarding again reports 404 rather than success for something it did not
	// do — a confirmed destructive action must not lie.
	r = f.ts.requestAs(t, f.contribTok, http.MethodDelete, f.pagePath(f.pageID, "/draft"), nil)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}

// TestDocumentAPI_PublishClearsTheDraft — and does it in the publish
// transaction, so a draft cannot reappear as unpublished work already published.
func TestDocumentAPI_PublishClearsTheDraft(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	document := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Final wording"}]}]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook", "doc": document, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode)

	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": document, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	after := f.openDocument(t, f.contribTok, f.pageID)
	require.Nil(t, after.Draft, "publishing must clear the draft it published")
	require.Equal(t, int32(2), after.BaseVersion)
	require.Contains(t, string(after.Doc), "Final wording")

	// The revision was recorded with its document, which is what makes an
	// overwrite-after-conflict able to resolve preserved content later.
	var revisionDoc *string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT doc::text FROM page_revisions WHERE page_id = $1 AND version = 2`,
		uuid.MustParse(f.pageID)).Scan(&revisionDoc))
	require.NotNil(t, revisionDoc)
	require.Contains(t, *revisionDoc, "Final wording")
}

// ── Conflict ───────────────────────────────────────────────────────────────

// TestDocumentAPI_StaleBaseVersionBlocksAndTheDraftSurvives is the conflict
// contract: block, keep the draft, and offer both arms honestly.
func TestDocumentAPI_StaleBaseVersionBlocksAndTheDraftSurvives(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	mine := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"My version"}]}]}`)
	theirs := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Their version"}]}]}`)

	// I start a draft from version 1.
	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook", "doc": mine, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode)

	// Somebody else publishes, taking the page to version 2.
	r = f.ts.requestAs(t, f.editorTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": theirs, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	// My publish is blocked, with the current state so the UI can show both.
	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": mine, "base_version": 1})
	require.Equal(t, http.StatusConflict, r.StatusCode, "body: %s", r.Body)

	var conflict struct {
		PageID          string `json:"page_id"`
		ExpectedVersion int32  `json:"expected_version"`
		CurrentPage     struct {
			Version int32 `json:"version"`
		} `json:"current_page"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &conflict))
	require.Equal(t, int32(1), conflict.ExpectedVersion)
	require.Equal(t, int32(2), conflict.CurrentPage.Version)
	require.NotEmpty(t, conflict.Message)
	// friendlyErrorMessage passes CONFLICT messages straight to the user, so the
	// message must read as prose and not as an internal string.
	require.NotContains(t, conflict.Message, "sql")
	require.NotContains(t, conflict.Message, "invalid")

	// Reload discards nothing: my draft is still there, now flagged stale.
	reloaded := f.openDocument(t, f.contribTok, f.pageID)
	require.NotNil(t, reloaded.Draft, "a conflict must not destroy the draft")
	require.Contains(t, string(reloaded.Draft.Doc), "My version")
	require.True(t, reloaded.Draft.Stale, "the draft's base is behind the page")
	require.Equal(t, int32(2), reloaded.BaseVersion)
	require.Contains(t, string(reloaded.Doc), "Their version")

	// Overwrite is explicit and lands on whatever is current.
	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": mine, "base_version": 1, "overwrite": true})
	require.Equal(t, http.StatusOK, r.StatusCode, "overwrite: %s", r.Body)

	final := f.openDocument(t, f.contribTok, f.pageID)
	require.Contains(t, string(final.Doc), "My version")
	require.Equal(t, int32(3), final.BaseVersion)
	require.Nil(t, final.Draft)
}

// TestDocumentAPI_OverwriteResolvesPreservedContentAgainstTheDraftsBaseVersion is
// the subtle one, and the reason page_revisions carries a document.
//
// I start a draft from version 1, which had a macro. Somebody publishes version 2
// without it. If my overwrite resolved preservation ids against the CURRENT page,
// it would find nothing — or worse, the wrong node. It has to resolve against
// version 1, which is where the ids came from.
func TestDocumentAPI_OverwriteResolvesPreservedContentAgainstTheDraftsBaseVersion(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// Establish a version 2 that holds the macro in BOTH pages.doc and its
	// revision — what a correct importer writes, and what an overwrite needs,
	// since it recovers the base document from history. Written as SQL rather
	// than posted through publish for one specific reason: Go's encoding/json
	// HTML-escapes a json.RawMessage it marshals, so a document sent through an
	// HTTP body arrives with its angle brackets as <. Nothing is lost by
	// that — the string decodes the same — but it would mean this test measured
	// the test client's serialiser rather than the server's fidelity.
	f.setStoredDocumentAtVersion(t, f.pageID, 2, storedImportedDocument)

	// Open at version 2 and keep the shielded document as my draft.
	opened := f.openDocument(t, f.contribTok, f.pageID)
	require.Equal(t, int32(2), opened.BaseVersion)
	require.Equal(t, []string{"u1"}, opened.PreservedIDs)
	mine := reserialise(t, opened.Doc, "After the diagram.", "After the diagram, my edit.")

	// Somebody else publishes a document with no macro at all, taking the page to
	// version 3. They have to acknowledge dropping it — which is the refusal in
	// TestDocumentAPI_PublishRefusesToDropPreservedContentSilently doing its job,
	// and is what a real second author would be prompted for.
	plain := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Rewritten from scratch"}]}]}`)
	require.Equal(t, int32(3),
		f.publish(t, f.editorTok, f.pageID, "Runbook", plain, 2, []string{"u1"}))
	require.NotContains(t, f.storedDocument(t, f.pageID), "gliffyDiagram")

	// My overwrite carries base_version 2, and the macro comes back intact —
	// resolved from the revision, not from the current page, which no longer has
	// it.
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": mine, "base_version": 2, "overwrite": true})
	require.Equal(t, http.StatusOK, r.StatusCode, "overwrite: %s", r.Body)

	stored := f.storedDocument(t, f.pageID)
	require.Contains(t, stored, importedMacroSubtree,
		"an overwrite must resolve preserved content against the version the draft started from")
	require.Contains(t, stored, "After the diagram, my edit.")
}

// TestDocumentAPI_OverwriteRefusesWhenTheBaseVersionHasNoDocument records a real
// constraint on the importer ADR-0012 anticipates, discovered by this suite.
//
// An overwrite recovers the base document from page_revisions. A document that
// reached pages.doc without a matching revision row — which is exactly what a
// naive importer writing straight to the table would produce — cannot be
// recovered from that version, so the preservation ids in a draft started there
// resolve to nothing.
//
// The behaviour is correct: refuse, rather than publish a document with a
// placeholder in it or silently drop the preserved block. But it means an
// importer MUST write a page_revisions row alongside pages.doc, and that is
// easier to get right if it is written down with a failing test behind it than
// discovered later on somebody's imported wiki.
func TestDocumentAPI_OverwriteRefusesWhenTheBaseVersionHasNoDocument(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// A document written straight into the table: version stays 1, and revision 1
	// still holds the empty markdown the page was created with.
	f.setStoredDocument(t, f.pageID, storedImportedDocument)
	opened := f.openDocument(t, f.contribTok, f.pageID)
	require.Equal(t, []string{"u1"}, opened.PreservedIDs)
	mine := reserialise(t, opened.Doc, "After the diagram.", "After the diagram, my edit.")

	// Move the page on, so the overwrite has to go to history for its base.
	plain := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Rewritten"}]}]}`)
	require.Equal(t, int32(2),
		f.publish(t, f.editorTok, f.pageID, "Runbook", plain, 1, []string{"u1"}))

	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": mine, "base_version": 1, "overwrite": true})
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
		"a base version with no recoverable document must be refused, not guessed: %s", r.Body)

	// And the page is untouched by the refusal.
	require.Contains(t, f.storedDocument(t, f.pageID), "Rewritten")
	require.NotContains(t, f.storedDocument(t, f.pageID), "gliffyDiagram")
}

// ── Capability ─────────────────────────────────────────────────────────────

// TestDocumentAPI_ContributorCannotEditSomebodyElsesPage is the capability gate's
// real test.
//
// The persona is a CONTRIBUTOR, not a viewer, and that choice is the whole point
// (CLAUDE.md section 2). A viewer never gets past RequireWriteFloor(create_items),
// so a viewer-based test passes with the in-handler access.CanEditEntity check
// deleted — it asserts the middleware, not the gate. A contributor clears the
// floor and holds edit_own_items but not edit_any_item, so on a page somebody
// else created only the in-handler check can refuse them.
//
// Mutation-tested while writing: with the access.CanEditEntity call removed from
// editablePage, every assertion below fails with 200/204 instead of 403.
func TestDocumentAPI_ContributorCannotEditSomebodyElsesPage(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// A page created by the OTHER contributor. pages.author_id is the creator and
	// is never updated by an edit, so this is the ownership key the gate uses.
	theirPage := f.createPage(t, f.secondTok, "Theirs", "")
	document := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`)

	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodPut,
		f.pagePath(theirPage, "/draft"),
		map[string]any{"title": "Theirs", "doc": document, "base_version": 1}))

	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodPost,
		f.pagePath(theirPage, "/publish"),
		map[string]any{"title": "Theirs", "doc": document, "base_version": 1}))

	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodDelete,
		f.pagePath(theirPage, "/draft"), nil))

	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodPost,
		f.pagePath(theirPage, "/images"), nil))

	// Reading it is fine — the document surface adds no read restriction.
	require.Equal(t, http.StatusOK,
		f.ts.requestAs(t, f.contribTok, http.MethodGet, f.pagePath(theirPage, "/document"), nil).StatusCode)

	// And on their OWN page the same contributor is allowed, so the refusals
	// above are about ownership rather than about the persona being powerless.
	require.Equal(t, http.StatusOK, f.ts.requestAs(t, f.contribTok, http.MethodPut,
		f.pagePath(f.pageID, "/draft"),
		map[string]any{"title": "Runbook", "doc": document, "base_version": 1}).StatusCode)
}

// ── Images ─────────────────────────────────────────────────────────────────

// TestDocumentAPI_ImageUploadSniffsTheBytesAndRejectsNonImages.
func TestDocumentAPI_ImageUploadSniffsTheBytesAndRejectsNonImages(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// A real PNG header is accepted.
	r := f.uploadImage(t, f.contribTok, f.pageID, "diagram.png", pngBytes)
	require.Equal(t, http.StatusCreated, r.StatusCode, "upload: %s", r.Body)
	var image struct {
		AttachmentID string `json:"attachment_id"`
		ContentType  string `json:"content_type"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &image))
	require.Equal(t, "image/png", image.ContentType)
	require.NotEmpty(t, image.AttachmentID)

	// A text file named .png is not, because the type comes from the bytes.
	r = f.uploadImage(t, f.contribTok, f.pageID, "trojan.png", []byte("this is not an image at all"))
	require.Equal(t, http.StatusBadRequest, r.StatusCode,
		"the declared filename must not decide the type: %s", r.Body)

	// Nor is an SVG, however it is labelled: it is a document that can carry
	// script, and attachments stream inline from our own origin.
	r = f.uploadImage(t, f.contribTok, f.pageID, "x.svg",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "SVG must be refused: %s", r.Body)

	// The accepted image renders in the published document.
	opened := f.openDocument(t, f.contribTok, f.pageID)
	document := json.RawMessage(fmt.Sprintf(
		`{"type":"doc","content":[{"type":"paragraph","content":[`+
			`{"type":"image","attrs":{"attachment_id":%q,"alt":"A diagram"}}]}]}`,
		image.AttachmentID))
	published := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": document, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusOK, published.StatusCode, "publish with an image: %s", published.Body)
	require.Contains(t, f.storedDocument(t, f.pageID), image.AttachmentID)

	// And the bytes are served back through the attachment path.
	served := f.ts.requestAs(t, f.contribTok, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s", f.ts.OrgID, f.spaceID, image.AttachmentID), nil)
	require.Equal(t, http.StatusOK, served.StatusCode)
	require.Equal(t, "image/png", served.Header.Get("Content-Type"))
}

// TestDocumentAPI_PublishRejectsAnImageThatIsNotAnImageOnThisPage closes the hole
// the generic attachment endpoint leaves open: any space writer can put any file
// on a page through it, so a document could name a spreadsheet as an image, or
// name an attachment belonging to somebody else's page.
func TestDocumentAPI_PublishRejectsAnImageThatIsNotAnImageOnThisPage(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	// A non-image attachment, uploaded through the generic endpoint, which does
	// not sniff.
	textAttachment := f.uploadGenericAttachment(t, f.contribTok, f.pageID, "notes.txt", []byte("plain text"))

	opened := f.openDocument(t, f.contribTok, f.pageID)
	withText := json.RawMessage(fmt.Sprintf(
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"image","attrs":{"attachment_id":%q}}]}]}`,
		textAttachment))
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": withText, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
		"an image node naming a text file must be refused: %s", r.Body)

	// An image that belongs to a different page is equally refused, and with the
	// same answer — telling the two apart would report that the id exists.
	otherPage := f.createPage(t, f.contribTok, "Other", "")
	upload := f.uploadImage(t, f.contribTok, otherPage, "elsewhere.png", pngBytes)
	require.Equal(t, http.StatusCreated, upload.StatusCode)
	var elsewhere struct {
		AttachmentID string `json:"attachment_id"`
	}
	require.NoError(t, json.Unmarshal(upload.Body, &elsewhere))

	borrowed := json.RawMessage(fmt.Sprintf(
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"image","attrs":{"attachment_id":%q}}]}]}`,
		elsewhere.AttachmentID))
	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": borrowed, "base_version": opened.BaseVersion})
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
		"an image node naming another page's attachment must be refused: %s", r.Body)

	// Nothing was published by either attempt.
	require.Equal(t, int32(1), f.openDocument(t, f.contribTok, f.pageID).BaseVersion)
}

// ── Audit ──────────────────────────────────────────────────────────────────

// TestDocumentAPI_PublishWritesThePageUpdatedAuditRow — publishing IS updating
// the page, so it uses the existing event rather than inventing one the audit
// viewer would not recognise. The metadata says how.
func TestDocumentAPI_PublishWritesThePageUpdatedAuditRow(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)

	document := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Audited"}]}]}`)
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": document, "base_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "publish: %s", r.Body)

	var action, payload string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT action, payload::text FROM audit_log
		 WHERE entity_kind = 'page' AND entity_id = $1 ORDER BY created_at DESC LIMIT 1`,
		uuid.MustParse(f.pageID)).Scan(&action, &payload))
	require.Equal(t, "page.updated", action)

	// Decoded, not substring-matched: audit_log.payload is jsonb, so its text
	// form is re-spaced by PostgreSQL and a byte comparison would assert the
	// formatting rather than the content.
	var meta map[string]string
	require.NoError(t, json.Unmarshal([]byte(payload), &meta))
	require.Equal(t, "document_publish", meta["via"])
	require.Equal(t, "2", meta["version"])
}

// ── helpers ────────────────────────────────────────────────────────────────

// reserialise stands in for the browser: decode, change a piece of text, and
// re-encode with encoding/json — which reorders object keys and rewrites number
// literals exactly as JSON.stringify does. Using it rather than a string
// substitution is what proves the guarantee survives a real editor.
func reserialise(t *testing.T, document json.RawMessage, from, to string) json.RawMessage {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(document, &value))
	replaceEveryString(value, from, to)
	out, err := json.Marshal(value)
	require.NoError(t, err)
	return out
}

func replaceEveryString(value any, from, to string) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if s, ok := item.(string); ok && s == from {
				v[key] = to
				continue
			}
			replaceEveryString(item, from, to)
		}
	case []any:
		for _, item := range v {
			replaceEveryString(item, from, to)
		}
	}
}

// uploadImage posts to the page-image route.
func (f *docFixture) uploadImage(t *testing.T, token, pageID, filename string, content []byte) httpResult {
	t.Helper()
	return f.postMultipart(t, token, f.pagePath(pageID, "/images"), filename, content, nil)
}

// uploadGenericAttachment posts to the space attachment route, which does NOT
// sniff — the path a non-image gets onto a page by.
func (f *docFixture) uploadGenericAttachment(t *testing.T, token, pageID, filename string, content []byte) string {
	t.Helper()
	r := f.postMultipart(t, token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID),
		filename, content,
		map[string]string{"entity_type": "page", "entity_id": pageID})
	require.Equal(t, http.StatusCreated, r.StatusCode, "generic upload: %s", r.Body)
	var att struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &att))
	return att.ID
}

func (f *docFixture) postMultipart(t *testing.T, token, path, filename string, content []byte, fields map[string]string) httpResult {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.ts.url(path), &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return f.ts.do(t, req)
}
