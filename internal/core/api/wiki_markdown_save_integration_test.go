package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The markdown save path (PUT .../wiki/{pageID}) through the real HTTP surface
// and a real database. Items S3 and S13 of the integrity pass, at the altitude a
// client sees them.

// S3 — a page that holds a document refuses a markdown save, with 409.
//
// Before the fix the request returned 200. The markdown UPDATE writes `content`
// and never `doc`, and `doc` is authoritative once it exists (ADR-0012), so the
// author saw a successful save, the version advanced, a history row appeared —
// and every reader that opens the page through the document surface kept seeing
// the old text. Nothing reported the loss, at any layer.
//
// 409 rather than 400 or 403: the request is well formed and the caller is
// entitled to make it. The page has moved to a representation this endpoint
// cannot write, and reloading is what resolves it — the same shape as a version
// conflict.
func TestMarkdownSave_RefusesPageThatHoldsADocument(t *testing.T) {
	f := newDocFixture(t)

	// Publish through the document editor: the page now holds a document.
	opened := f.openDocument(t, f.contribTok, f.pageID)
	version := f.publish(t, f.contribTok, f.pageID, "Runbook", opened.Doc, opened.BaseVersion, nil)

	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, ""),
		map[string]any{"title": "Markdown overwrite", "content": "markdown overwrite",
			"expected_version": version})
	require.Equal(t, http.StatusConflict, r.StatusCode,
		"a markdown save against a document-backed page must be refused, not silently discarded: %s", r.Body)

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.Contains(t, body.Error.Message, "document",
		"the refusal must say why, so the client can send the author to the editor")

	// The page is untouched: the refusal is not a partial write.
	var (
		gotVersion int32
		gotTitle   string
		gotContent string
	)
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT version, title, content FROM pages WHERE id = $1`, uuid.MustParse(f.pageID)).
		Scan(&gotVersion, &gotTitle, &gotContent))
	require.Equal(t, version, gotVersion, "the refused save must not bump the version")
	require.Equal(t, "Runbook", gotTitle)
	require.NotEqual(t, "markdown overwrite", gotContent)
}

// The refusal is strictly `doc IS NOT NULL`. A page that has never been
// published as a document keeps taking markdown saves — including one that is
// open in the Codex editor right now. web/e2e/codex-editor.spec.ts asserts
// exactly this and would break under any broader rule.
func TestMarkdownSave_AllowedWhileTheDocumentIsStillNull(t *testing.T) {
	f := newDocFixture(t)

	// Opening the editor converts markdown on the way out and writes nothing.
	opened := f.openDocument(t, f.contribTok, f.pageID)
	require.Equal(t, "markdown", opened.SourceFormat)
	require.Empty(t, f.storedDocument(t, f.pageID), "opening the editor must not store a document")

	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, ""),
		map[string]any{"title": "Runbook v2", "content": "still markdown", "expected_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "markdown save on a doc-less page: %s", r.Body)

	var page struct {
		Version int32  `json:"version"`
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	require.Equal(t, int32(2), page.Version)
	require.Equal(t, "still markdown", page.Content)
}

// S13 — the save's two writes are one. A successful markdown save leaves a
// history row for the version it created; there is no state in which the page
// advanced and its history did not.
//
// The rollback half of this — a failure between the two writes — is proven
// against the schema's own foreign key in
// internal/db/adapters/update_page_content_tx_integration_test.go, which can
// inject the failure. What this asserts is the invariant that failure would
// break: version and history do not diverge.
func TestMarkdownSave_RecordsHistoryForEveryVersionItCreates(t *testing.T) {
	f := newDocFixture(t)

	for i, want := range []struct {
		title, content string
		version        int32
	}{
		{"Runbook v2", "second", 2},
		{"Runbook v3", "third", 3},
	} {
		r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, ""),
			map[string]any{"title": want.title, "content": want.content,
				"expected_version": want.version - 1})
		require.Equal(t, http.StatusOK, r.StatusCode, "save %d: %s", i, r.Body)
	}

	rows, err := f.ts.DB.Pool.Query(context.Background(),
		`SELECT version, title, content FROM page_revisions WHERE page_id = $1 ORDER BY version`,
		uuid.MustParse(f.pageID))
	require.NoError(t, err)
	defer rows.Close()

	var versions []int32
	for rows.Next() {
		var v int32
		var title, content string
		require.NoError(t, rows.Scan(&v, &title, &content))
		versions = append(versions, v)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []int32{1, 2, 3}, versions,
		"every version the page reached must have a history row — a gap is unrecoverable")
}

// A stale expected_version returns the 409 conflict payload the merge UI needs:
// the page id, the version the caller believed in, and the page as it actually
// stands.
//
// Fails before the fix, for a reason that had nothing to do with the
// transaction. The handler tested `err != nil` before `conflict != nil`, and
// UpdatePageOrConflict returns the detail TOGETHER with ErrVersionConflict — so
// the conflict branch was unreachable and every version conflict answered with
// the bare {error:{…}} envelope. The detail type, its construction and its unit
// tests all existed; nothing delivered it. Found while writing this test.
func TestMarkdownSave_StaleVersionStillReturnsConflictDetail(t *testing.T) {
	f := newDocFixture(t)

	r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, ""),
		map[string]any{"title": "First", "content": "first", "expected_version": 1})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	r = f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, ""),
		map[string]any{"title": "Stale", "content": "stale", "expected_version": 1})
	require.Equal(t, http.StatusConflict, r.StatusCode, "%s", r.Body)

	var conflict struct {
		PageID          string `json:"page_id"`
		ExpectedVersion int32  `json:"expected_version"`
		CurrentPage     struct {
			Version int32 `json:"version"`
		} `json:"current_page"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &conflict))
	require.Equal(t, f.pageID, conflict.PageID)
	require.Equal(t, int32(1), conflict.ExpectedVersion)
	require.Equal(t, int32(2), conflict.CurrentPage.Version)
	require.NotEmpty(t, conflict.Message)
}
