package doc_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestPageDocumentColumn_StoresDocumentsVerbatim is the test that holds migration
// 036's column type in place.
//
// ADR-0012's byte-identical guarantee is only as good as the weakest layer the
// document crosses, and the storage layer is a layer. PostgreSQL's `jsonb` is a
// parsed, normalised representation: it sorts object keys, rewrites number
// literals, and drops duplicate keys outright. `json` stores the text as given.
// The Go pipeline can be perfect and still lose content if the column is the
// wrong one.
//
// So this asserts on the database rather than on a document comment, and it fails
// if anybody changes `pages.doc` to `jsonb` — including for a perfectly sensible
// reason like wanting a GIN index. If that day comes, the answer is to read this
// test and ADR-0012 first, not to update the expectation.
func TestPageDocumentColumn_StoresDocumentsVerbatim(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex")

	// Every feature of this document is one jsonb would change:
	//   - "zzz" before "a"          → jsonb sorts keys
	//   - 1e2                       → jsonb rewrites it as 100
	//   - "dup" twice               → jsonb keeps only the last
	//   - a space after a colon     → jsonb re-emits canonically
	//   - <, > and & in a string    → must survive as themselves
	const document = `{"type":"doc","content":[{"type":"someMacro",` +
		`"attrs":{"zzz":1, "a":2,"scale":1e2,"dup":1,"dup":2,` +
		`"body":"<x a=\"1\">y & z</x>"}}]}`

	pageID := uuid.New()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO pages (id, space_id, title, content, author_id, path, doc)
		VALUES ($1, $2, 'Fidelity', '', $3, $4, $5)`,
		pageID, space.ID, user.ID, pageID.String(), document)
	require.NoError(t, err)

	var stored string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT doc::text FROM pages WHERE id = $1`, pageID).Scan(&stored))

	require.Equal(t, document, stored,
		"pages.doc did not return the document it was given — the column type is normalising it, "+
			"which is the storage-layer version of the silent data loss ADR-0012 forbids")

	// The same for a revision's document and a draft's, since both are on the
	// path a preserved node travels.
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO page_revisions (id, page_id, version, title, content, doc, author_id)
		VALUES ($1, $2, 1, 'Fidelity', '', $3, $4)`,
		uuid.New(), pageID, document, user.ID)
	require.NoError(t, err)

	var storedRevision string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT doc::text FROM page_revisions WHERE page_id = $1 AND version = 1`, pageID).Scan(&storedRevision))
	require.Equal(t, document, storedRevision, "page_revisions.doc is normalising documents")

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO page_drafts (page_id, author_id, title, doc, base_version)
		VALUES ($1, $2, 'Fidelity', $3, 1)`,
		pageID, user.ID, document)
	require.NoError(t, err)

	var storedDraft string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT doc::text FROM page_drafts WHERE page_id = $1 AND author_id = $2`,
		pageID, user.ID).Scan(&storedDraft))
	require.Equal(t, document, storedDraft, "page_drafts.doc is normalising documents")
}

// TestPageDocumentColumn_JsonbWouldHaveLostContent is the negative half, and it
// is the reason the test above is not merely a tautology about a column type.
// It runs the identical document through `jsonb` in the same database and shows
// what would have been lost.
func TestPageDocumentColumn_JsonbWouldHaveLostContent(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	ctx := context.Background()

	const document = `{"zzz":1, "a":2,"scale":1e2,"dup":1,"dup":2,"body":"<x a=\"1\">y & z</x>"}`

	var asJSONB, asJSON string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT ($1::text::jsonb)::text, ($1::text::json)::text`, document).Scan(&asJSONB, &asJSON))

	require.NotEqual(t, document, asJSONB,
		"jsonb returned the input unchanged — then this whole distinction is moot and the migration comment is wrong")
	require.NotContains(t, asJSONB, `"dup":1`,
		"jsonb kept the duplicate key; the documented loss is not happening")
	require.NotContains(t, asJSONB, "1e2",
		"jsonb kept the exponent literal; the documented rewrite is not happening")
	require.Equal(t, document, asJSON,
		"json is meant to be the verbatim option; if it is not, migration 036 needs rethinking")
}

// TestPageDrafts_OneDraftPerAuthorPerPage pins the uniqueness to the key rather
// than to application code: two tabs autosaving must upsert, not accumulate.
func TestPageDrafts_OneDraftPerAuthorPerPage(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, db.Pool)
	author := testutil.CreateTestUser(t, db.Pool, org.ID)
	other := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, author.ID, "codex")

	pageID := uuid.New()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO pages (id, space_id, title, content, author_id, path)
		VALUES ($1, $2, 'Shared page', '', $3, $4)`,
		pageID, space.ID, author.ID, pageID.String())
	require.NoError(t, err)

	insert := func(userID uuid.UUID, title string) error {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO page_drafts (page_id, author_id, title, doc, base_version)
			VALUES ($1, $2, $3, '{"type":"doc","content":[]}', 1)`,
			pageID, userID, title)
		return err
	}

	require.NoError(t, insert(author.ID, "first"))
	require.Error(t, insert(author.ID, "second"),
		"a second draft for the same author on the same page must be refused by the primary key")

	// A different author on the same page is a different draft, not a conflict —
	// that is what makes drafts per-user.
	require.NoError(t, insert(other.ID, "someone else's draft"))

	var count int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM page_drafts WHERE page_id = $1`, pageID).Scan(&count))
	require.Equal(t, 2, count)
}

// TestPageDrafts_UpdatedAtMovesOnEveryAutosave — the saved indicator and the
// Drafts view both order by it, so a trigger that does not fire on the upsert
// path would leave every draft looking equally stale.
func TestPageDrafts_UpdatedAtMovesOnEveryAutosave(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, db.Pool)
	author := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, author.ID, "codex")

	pageID := uuid.New()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO pages (id, space_id, title, content, author_id, path)
		VALUES ($1, $2, 'Autosaved', '', $3, $4)`,
		pageID, space.ID, author.ID, pageID.String())
	require.NoError(t, err)

	upsert := func(title string) {
		_, err := db.Pool.Exec(ctx, `
			INSERT INTO page_drafts (page_id, author_id, title, doc, base_version)
			VALUES ($1, $2, $3, '{"type":"doc","content":[]}', 1)
			ON CONFLICT (page_id, author_id) DO UPDATE
			  SET title = EXCLUDED.title, doc = EXCLUDED.doc, base_version = EXCLUDED.base_version`,
			pageID, author.ID, title)
		require.NoError(t, err)
	}

	upsert("first")
	var created, firstUpdate string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT created_at::text, updated_at::text FROM page_drafts WHERE page_id = $1`, pageID).
		Scan(&created, &firstUpdate))

	// pg_sleep rather than a Go sleep: now() is transaction-scoped, so the
	// advance has to happen between statements on the server.
	_, err = db.Pool.Exec(ctx, `SELECT pg_sleep(0.01)`)
	require.NoError(t, err)

	upsert("second")
	var stillCreated, secondUpdate, title string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT created_at::text, updated_at::text, title FROM page_drafts WHERE page_id = $1`, pageID).
		Scan(&stillCreated, &secondUpdate, &title))

	require.Equal(t, "second", title, "the upsert must replace the draft, not ignore it")
	require.Equal(t, created, stillCreated, "created_at is when the draft began and must not move")
	require.NotEqual(t, firstUpdate, secondUpdate,
		"trg_page_drafts_updated_at did not fire on the ON CONFLICT DO UPDATE path")
}

// TestPageDrafts_RejectAnImpossibleBaseVersion — a base version of zero or below
// means the caller did not read a page first, and letting it through would make
// the conflict comparison meaningless.
func TestPageDrafts_RejectAnImpossibleBaseVersion(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, db.Pool)
	author := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, author.ID, "codex")

	pageID := uuid.New()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO pages (id, space_id, title, content, author_id, path)
		VALUES ($1, $2, 'Versioned', '', $3, $4)`,
		pageID, space.ID, author.ID, pageID.String())
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO page_drafts (page_id, author_id, title, doc, base_version)
		VALUES ($1, $2, 'bad', '{"type":"doc","content":[]}', 0)`,
		pageID, author.ID)
	require.Error(t, err, "page_drafts_base_version_positive should refuse base_version 0")
}
