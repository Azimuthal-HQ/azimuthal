package adapters_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The markdown save path (PUT .../wiki/{pageID}) writes a page row and a
// history row. These tests are against real PostgreSQL because both defects
// they cover are database-shaped: one is an atomicity boundary, the other is
// a NULL check on a jsonb column, and neither is observable through a mock.

type pageTxFixture struct {
	ctx     context.Context
	adapter *adapters.ContentTxAdapter
	q       *generated.Queries
	pageID  uuid.UUID
	spaceID uuid.UUID
	author  uuid.UUID
}

func newPageTxFixture(t *testing.T) *pageTxFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex")

	ctx := context.Background()
	q := generated.New(db.Pool)

	pageID := uuid.New()
	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID: pageID, SpaceID: space.ID, Title: "Original", Content: "original body",
		AuthorID: user.ID, Position: 0, Path: pageID.String(),
	})
	require.NoError(t, err)
	_, err = q.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID: uuid.New(), PageID: page.ID, Version: page.Version,
		Title: page.Title, Content: page.Content, AuthorID: user.ID,
	})
	require.NoError(t, err)

	return &pageTxFixture{
		ctx:     ctx,
		adapter: adapters.NewContentTxAdapter(db.Pool),
		q:       q,
		pageID:  pageID,
		spaceID: space.ID,
		author:  user.ID,
	}
}

func (f *pageTxFixture) page(t *testing.T) generated.Page {
	t.Helper()
	p, err := f.q.GetPageByID(f.ctx, f.pageID)
	require.NoError(t, err)
	return p
}

func (f *pageTxFixture) revisionCount(t *testing.T) int {
	t.Helper()
	revs, err := f.q.ListPageRevisions(f.ctx, generated.ListPageRevisionsParams{PageID: f.pageID, SpaceID: f.spaceID})
	require.NoError(t, err)
	return len(revs)
}

// S13 — the page row and its history row commit together.
//
// Fails before the fix: the service wrote the page through UpdatePageContent
// and the revision through CreatePageRevision as two separate statements, so a
// failure on the second left the page at version 2 with no version-2 history
// row. Nothing retries a half-finished save, so that gap is permanent.
//
// The failure is injected by attributing the save to a user id that does not
// exist: page_revisions.author_id is NOT NULL REFERENCES users (id) (migration
// 005), so the revision insert fails on the foreign key AFTER the page row has
// already been written inside the transaction. That is precisely the shape of
// the real failure, produced by the schema rather than by a fake.
func TestUpdatePageContentTx_RevisionFailureRollsBackThePageRow(t *testing.T) {
	f := newPageTxFixture(t)

	_, err := f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID:          f.pageID,
		ExpectedVersion: 1,
		Title:           "Half-written",
		Content:         "half-written body",
		AuthorID:        uuid.New(), // no such user — the revision insert will fail
	})
	require.Error(t, err, "a revision insert that violates its foreign key must fail the save")

	after := f.page(t)
	require.Equal(t, int32(1), after.Version,
		"the page row must roll back with the revision: a version bump with no history row is unrecoverable")
	require.Equal(t, "Original", after.Title, "the rolled-back title must not have landed")
	require.Equal(t, "original body", after.Content, "the rolled-back content must not have landed")
	require.Equal(t, 1, f.revisionCount(t), "no history row should exist for the failed version")
}

// The ordinary path still works, and still records history.
func TestUpdatePageContentTx_WritesPageAndRevisionTogether(t *testing.T) {
	f := newPageTxFixture(t)

	page, err := f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID:          f.pageID,
		ExpectedVersion: 1,
		Title:           "Second",
		Content:         "second body",
		AuthorID:        f.author,
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), page.Version)
	require.Equal(t, "Second", page.Title)
	require.Equal(t, 2, f.revisionCount(t))

	rev, err := f.q.GetPageRevision(f.ctx, generated.GetPageRevisionParams{PageID: f.pageID, Version: 2})
	require.NoError(t, err)
	require.Equal(t, "Second", rev.Title)
	require.Equal(t, "second body", rev.Content)
	require.Equal(t, f.author, rev.AuthorID)
}

// S3 — the markdown save is refused on a page that holds a document.
//
// Fails before the fix: the markdown UPDATE writes `content` and never touches
// `doc`, and `doc` is the authoritative representation once it exists
// (ADR-0012). So the save reported 200, bumped the version and wrote a history
// row, and then every document reader kept serving the old document — the
// author's edit was gone with no error anywhere.
//
// Delete the `current.Doc != nil` guard in UpdatePageContentTx and this test
// fails on the first assertion: the call returns nil.
func TestUpdatePageContentTx_RefusesPageThatHoldsADocument(t *testing.T) {
	f := newPageTxFixture(t)

	doc := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`)
	published, err := f.adapter.PublishPageTx(f.ctx, wiki.PublishPageTxInput{
		PageID: f.pageID, AuthorID: f.author,
		Title: "Now a document", Content: "now a document",
		Doc: doc, BaseVersion: 1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), published.Version)

	_, err = f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID:          f.pageID,
		ExpectedVersion: 2, // correct version — the refusal is about `doc`, not staleness
		Title:           "Markdown overwrite",
		Content:         "markdown overwrite",
		AuthorID:        f.author,
	})
	require.ErrorIs(t, err, wiki.ErrPageIsDocumentBacked)

	after := f.page(t)
	require.Equal(t, int32(2), after.Version, "the refused save must not bump the version")
	require.Equal(t, "Now a document", after.Title, "the refused save must not touch the page")
	require.JSONEq(t, string(doc), string(after.Doc), "the stored document must be untouched")
	require.Equal(t, 2, f.revisionCount(t), "the refused save must not write history")
}

// The refusal is strictly `doc IS NOT NULL`. A page that has only ever held
// markdown keeps taking markdown saves — including one that is open in the new
// editor but has never been published, which is exactly the state
// web/e2e/codex-editor.spec.ts exercises when it PUTs markdown and expects 200.
func TestUpdatePageContentTx_AllowsPageWhoseDocumentIsStillNull(t *testing.T) {
	f := newPageTxFixture(t)
	require.Nil(t, f.page(t).Doc, "fixture precondition: the page has no stored document")

	page, err := f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID:          f.pageID,
		ExpectedVersion: 1,
		Title:           "Still markdown",
		Content:         "still markdown",
		AuthorID:        f.author,
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), page.Version)
}

// Zero rows now has three distinguishable causes, and they are decided under
// the row lock rather than inferred afterwards by a second read.
func TestUpdatePageContentTx_DistinguishesMissingFromStale(t *testing.T) {
	f := newPageTxFixture(t)

	_, err := f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID: uuid.New(), ExpectedVersion: 1, Title: "Ghost", Content: "x", AuthorID: f.author,
	})
	require.ErrorIs(t, err, wiki.ErrPageNotFound)

	_, err = f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID: f.pageID, ExpectedVersion: 99, Title: "Stale", Content: "x", AuthorID: f.author,
	})
	require.ErrorIs(t, err, wiki.ErrVersionConflict)

	require.Equal(t, int32(1), f.page(t).Version, "neither refusal may have written")

	// A soft-deleted page is not found, not a conflict: GetPageForUpdate
	// filters deleted_at, so the delete case lands on the same arm as a page
	// that never existed.
	_, err = f.adapter.DeletePageAndRevokeShares(f.ctx, f.pageID, f.spaceID, f.author)
	require.NoError(t, err)
	_, err = f.adapter.UpdatePageContentTx(f.ctx, wiki.UpdatePageInput{
		PageID: f.pageID, ExpectedVersion: 1, Title: "Deleted", Content: "x", AuthorID: f.author,
	})
	require.ErrorIs(t, err, wiki.ErrPageNotFound)
}
