package wiki_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ---------- mock store ----------

type mockStore struct {
	pages     map[uuid.UUID]generated.Page
	revisions map[uuid.UUID][]generated.PageRevision // keyed by pageID
	mu        sync.Mutex
}

func newMockStore() *mockStore {
	return &mockStore{
		pages:     make(map[uuid.UUID]generated.Page),
		revisions: make(map[uuid.UUID][]generated.PageRevision),
	}
}

func (m *mockStore) CreatePage(_ context.Context, arg generated.CreatePageParams) (generated.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := generated.Page{
		ID:       arg.ID,
		SpaceID:  arg.SpaceID,
		ParentID: arg.ParentID,
		Title:    arg.Title,
		Content:  arg.Content,
		Version:  1,
		AuthorID: arg.AuthorID,
		Position: arg.Position,
	}
	m.pages[p.ID] = p
	return p, nil
}

func (m *mockStore) GetPageByID(_ context.Context, id uuid.UUID) (generated.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pages[id]
	if !ok {
		return generated.Page{}, pgx.ErrNoRows
	}
	return p, nil
}

// UpdatePageContentTx implements the ContentTxStore markdown-save seam in
// memory, faithfully rather than as a stub: the nine UpdatePage unit tests in
// this file run through it, so a stub would turn every one of them into a
// test of nothing.
//
// Faithful means the same four decisions the real transaction makes, in the
// same order and under one lock (which is what the mutex stands in for):
// missing page → ErrPageNotFound, stored document → ErrPageIsDocumentBacked,
// stale version → ErrVersionConflict, otherwise write the page row AND its
// revision together. Holding the lock across the whole sequence is what makes
// the concurrent-editors test meaningful — release it between the check and
// the write and both editors would win.
func (m *mockStore) UpdatePageContentTx(_ context.Context, in wiki.UpdatePageInput) (generated.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.pages[in.PageID]
	if !ok {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	if p.Doc != nil {
		return generated.Page{}, wiki.ErrPageIsDocumentBacked
	}
	if p.Version != in.ExpectedVersion {
		return generated.Page{}, wiki.ErrVersionConflict
	}

	p.Title = in.Title
	p.Content = in.Content
	p.Version++
	m.pages[p.ID] = p

	m.revisions[p.ID] = append(m.revisions[p.ID], generated.PageRevision{
		ID:       uuid.New(),
		PageID:   p.ID,
		Version:  p.Version,
		Title:    p.Title,
		Content:  p.Content,
		AuthorID: in.AuthorID,
	})
	return p, nil
}

// MovePageTx implements the ContentTxStore seam in memory: reparent (and,
// across spaces, re-home) the page. Shares are not modelled here — the
// share invariants are covered by the real-database integration tests; this
// mock exercises the move mechanics the service delegates.
func (m *mockStore) MovePageTx(_ context.Context, in wiki.MovePageInput) (wiki.MovePageTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pages[in.PageID]
	if !ok {
		return wiki.MovePageTxResult{}, wiki.ErrPageNotFound
	}
	crossSpace := p.SpaceID != in.TargetSpaceID
	p.SpaceID = in.TargetSpaceID
	if in.ParentID != nil {
		p.ParentID = pgtype.UUID{Bytes: *in.ParentID, Valid: true}
	} else {
		p.ParentID = pgtype.UUID{}
	}
	p.Position = in.Position
	m.pages[p.ID] = p
	return wiki.MovePageTxResult{CrossSpace: crossSpace}, nil
}

// DeletePageAndRevokeShares implements the ContentTxStore delete seam in
// memory.
func (m *mockStore) DeletePageAndRevokeShares(_ context.Context, pageID, _ uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pages, pageID)
	return 0, nil
}

func (m *mockStore) ListPagesBySpace(_ context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []generated.ListPagesBySpaceRow
	for _, p := range m.pages {
		if p.SpaceID == spaceID {
			result = append(result, generated.ListPagesBySpaceRow{
				ID:       p.ID,
				SpaceID:  p.SpaceID,
				ParentID: p.ParentID,
				Title:    p.Title,
				Version:  p.Version,
				AuthorID: p.AuthorID,
				Position: p.Position,
			})
		}
	}
	return result, nil
}

func (m *mockStore) ListRootPagesBySpace(_ context.Context, spaceID uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []generated.ListRootPagesBySpaceRow
	for _, p := range m.pages {
		if p.SpaceID == spaceID && !p.ParentID.Valid {
			result = append(result, generated.ListRootPagesBySpaceRow{
				ID:       p.ID,
				SpaceID:  p.SpaceID,
				ParentID: p.ParentID,
				Title:    p.Title,
				Version:  p.Version,
				AuthorID: p.AuthorID,
				Position: p.Position,
			})
		}
	}
	return result, nil
}

func (m *mockStore) ListChildPages(_ context.Context, parentID pgtype.UUID) ([]generated.ListChildPagesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []generated.ListChildPagesRow
	for _, p := range m.pages {
		if p.ParentID.Valid && p.ParentID.Bytes == parentID.Bytes {
			result = append(result, generated.ListChildPagesRow{
				ID:       p.ID,
				SpaceID:  p.SpaceID,
				ParentID: p.ParentID,
				Title:    p.Title,
				Version:  p.Version,
				AuthorID: p.AuthorID,
				Position: p.Position,
			})
		}
	}
	return result, nil
}

func (m *mockStore) CreatePageRevision(_ context.Context, arg generated.CreatePageRevisionParams) (generated.PageRevision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rev := generated.PageRevision{
		ID:       arg.ID,
		PageID:   arg.PageID,
		Version:  arg.Version,
		Title:    arg.Title,
		Content:  arg.Content,
		AuthorID: arg.AuthorID,
	}
	m.revisions[arg.PageID] = append(m.revisions[arg.PageID], rev)
	return rev, nil
}

func (m *mockStore) GetPageRevision(_ context.Context, arg generated.GetPageRevisionParams) (generated.PageRevision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.revisions[arg.PageID] {
		if r.Version == arg.Version {
			return r, nil
		}
	}
	return generated.PageRevision{}, pgx.ErrNoRows
}

func (m *mockStore) ListPageRevisions(_ context.Context, pageID uuid.UUID) ([]generated.ListPageRevisionsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []generated.ListPageRevisionsRow
	for i := len(m.revisions[pageID]) - 1; i >= 0; i-- {
		r := m.revisions[pageID][i]
		result = append(result, generated.ListPageRevisionsRow{
			ID:       r.ID,
			PageID:   r.PageID,
			Version:  r.Version,
			Title:    r.Title,
			AuthorID: r.AuthorID,
		})
	}
	return result, nil
}

func (m *mockStore) SearchPages(_ context.Context, arg generated.SearchPagesParams) ([]generated.SearchPagesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []generated.SearchPagesRow
	for _, p := range m.pages {
		if p.SpaceID == arg.SpaceID {
			// Simulate basic text search — real search uses tsvector.
			if containsIgnoreCase(p.Title, arg.PlaintoTsquery) || containsIgnoreCase(p.Content, arg.PlaintoTsquery) {
				result = append(result, generated.SearchPagesRow{
					ID:       p.ID,
					SpaceID:  p.SpaceID,
					ParentID: p.ParentID,
					Title:    p.Title,
					Version:  p.Version,
					AuthorID: p.AuthorID,
					Position: p.Position,
				})
			}
		}
		if len(result) >= int(arg.Limit) {
			break
		}
	}
	return result, nil
}

func containsIgnoreCase(haystack, needle string) bool {
	return len(needle) > 0 &&
		(len(haystack) >= len(needle) &&
			(haystack == needle ||
				findSubstring(haystack, needle)))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------- helpers ----------

func testService() (*wiki.Service, *mockStore) {
	store := newMockStore()
	// The mock store doubles as the ContentTxStore so move/delete delegation
	// operates on the same in-memory page map.
	svc := wiki.NewService(store, store)
	return svc, store
}

func createTestPage(t *testing.T, svc *wiki.Service, spaceID, authorID uuid.UUID, title, content string, parentID *uuid.UUID) generated.Page {
	t.Helper()
	page, err := svc.CreatePage(context.Background(), wiki.CreatePageInput{
		SpaceID:  spaceID,
		ParentID: parentID,
		Title:    title,
		Content:  content,
		AuthorID: authorID,
	})
	if err != nil {
		t.Fatalf("creating test page: %v", err)
	}
	return page
}

// ---------- page CRUD tests ----------

func TestCreatePage(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page, err := svc.CreatePage(ctx, wiki.CreatePageInput{
		SpaceID:  spaceID,
		Title:    "Getting Started",
		Content:  "# Welcome\nHello world",
		AuthorID: authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Title != "Getting Started" {
		t.Errorf("expected title 'Getting Started', got %q", page.Title)
	}
	if page.Version != 1 {
		t.Errorf("expected version 1, got %d", page.Version)
	}
	if page.SpaceID != spaceID {
		t.Errorf("expected space ID %s, got %s", spaceID, page.SpaceID)
	}
}

func TestCreatePage_ValidationErrors(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	tests := []struct {
		name  string
		input wiki.CreatePageInput
		want  error
	}{
		{
			name:  "empty title",
			input: wiki.CreatePageInput{SpaceID: uuid.New(), AuthorID: uuid.New(), Title: ""},
			want:  wiki.ErrEmptyTitle,
		},
		{
			name:  "whitespace-only title",
			input: wiki.CreatePageInput{SpaceID: uuid.New(), AuthorID: uuid.New(), Title: "   "},
			want:  wiki.ErrEmptyTitle,
		},
		{
			name:  "nil space ID",
			input: wiki.CreatePageInput{AuthorID: uuid.New(), Title: "Test"},
			want:  wiki.ErrInvalidSpaceID,
		},
		{
			name:  "nil author ID",
			input: wiki.CreatePageInput{SpaceID: uuid.New(), Title: "Test"},
			want:  wiki.ErrInvalidAuthorID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreatePage(ctx, tc.input)
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestGetPage(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	created := createTestPage(t, svc, spaceID, authorID, "Test Page", "content", nil)

	got, err := svc.GetPage(ctx, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("page ID mismatch")
	}
}

func TestGetPage_NotFound(t *testing.T) {
	svc, _ := testService()
	_, err := svc.GetPage(context.Background(), uuid.New())
	if !errors.Is(err, wiki.ErrPageNotFound) {
		t.Errorf("expected ErrPageNotFound, got %v", err)
	}
}

func TestUpdatePage(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Draft", "initial", nil)

	updated, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Final",
		Content:         "updated content",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("expected version 2, got %d", updated.Version)
	}
	if updated.Title != "Final" {
		t.Errorf("expected title 'Final', got %q", updated.Title)
	}
}

func TestUpdatePage_EmptyTitle(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()
	page := createTestPage(t, svc, spaceID, authorID, "Draft", "content", nil)

	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "",
		Content:         "x",
		AuthorID:        authorID,
	})
	if !errors.Is(err, wiki.ErrEmptyTitle) {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
}

func TestDeletePage(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "To Delete", "gone", nil)

	if err := svc.DeletePage(ctx, page.ID, authorID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := svc.GetPage(ctx, page.ID)
	if !errors.Is(err, wiki.ErrPageNotFound) {
		t.Errorf("expected ErrPageNotFound after delete, got %v", err)
	}
}

func TestMovePage(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	parent := createTestPage(t, svc, spaceID, authorID, "Parent", "p", nil)
	child := createTestPage(t, svc, spaceID, authorID, "Child", "c", nil)

	parentID := parent.ID
	if _, err := svc.MovePage(ctx, wiki.MovePageInput{
		OrgID:         uuid.New(),
		TargetSpaceID: spaceID,
		PageID:        child.ID,
		ParentID:      &parentID,
		Position:      0,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := svc.GetPage(ctx, child.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.ParentID.Valid || got.ParentID.Bytes != parent.ID {
		t.Error("expected child to be under parent")
	}
}

// ---------- tree tests ----------

func TestBuildTree(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	root := createTestPage(t, svc, spaceID, authorID, "Root", "r", nil)
	rootID := root.ID
	createTestPage(t, svc, spaceID, authorID, "Child A", "a", &rootID)
	createTestPage(t, svc, spaceID, authorID, "Child B", "b", &rootID)

	tree, err := svc.BuildTree(ctx, spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	if len(tree[0].Children) != 2 {
		t.Errorf("expected 2 children, got %d", len(tree[0].Children))
	}
}

func TestListRootPages(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	root := createTestPage(t, svc, spaceID, authorID, "Root", "r", nil)
	rootID := root.ID
	createTestPage(t, svc, spaceID, authorID, "Child", "c", &rootID)

	roots, err := svc.ListRootPages(ctx, spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roots) != 1 {
		t.Errorf("expected 1 root page, got %d", len(roots))
	}
}

func TestListChildPages(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	root := createTestPage(t, svc, spaceID, authorID, "Root", "r", nil)
	rootID := root.ID
	createTestPage(t, svc, spaceID, authorID, "Child 1", "c1", &rootID)
	createTestPage(t, svc, spaceID, authorID, "Child 2", "c2", &rootID)

	children, err := svc.ListChildPages(ctx, root.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

// ---------- revision tests ----------

func TestCreatePage_CreatesInitialRevision(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Rev Test", "initial", nil)

	revs, err := svc.ListRevisions(ctx, page.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	if revs[0].Version != 1 {
		t.Errorf("expected revision version 1, got %d", revs[0].Version)
	}
}

func TestUpdatePage_CreatesRevision(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Rev Test", "v1", nil)

	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Rev Test v2",
		Content:         "v2 content",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	revs, err := svc.ListRevisions(ctx, page.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revs))
	}
}

func TestGetRevision(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Title", "content v1", nil)

	rev, err := svc.GetRevision(ctx, page.ID, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rev.Content != "content v1" {
		t.Errorf("expected content 'content v1', got %q", rev.Content)
	}
}

func TestGetRevision_NotFound(t *testing.T) {
	svc, _ := testService()
	_, err := svc.GetRevision(context.Background(), uuid.New(), 999)
	if !errors.Is(err, wiki.ErrRevisionNotFound) {
		t.Errorf("expected ErrRevisionNotFound, got %v", err)
	}
}

func TestDiffRevisions(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Title", "Hello world", nil)

	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Title",
		Content:         "Hello universe",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	diff, err := svc.DiffRevisions(ctx, page.ID, 1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff.FromVersion != 1 || diff.ToVersion != 2 {
		t.Errorf("wrong version range in diff")
	}
	if diff.ContentDiff == "" {
		t.Error("expected non-empty content diff")
	}
}

// ---------- conflict tests ----------

func TestUpdatePage_VersionConflict(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Draft", "initial", nil)

	// First update succeeds (version 1 → 2).
	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Updated Once",
		Content:         "v2",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("first update failed: %v", err)
	}

	// Second update with stale version (expects 1, but current is 2) → conflict.
	_, err = svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Stale Update",
		Content:         "stale",
		AuthorID:        authorID,
	})
	if !errors.Is(err, wiki.ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestUpdatePageOrConflict_ReturnsConflictDetail(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Draft", "initial", nil)

	// Update to version 2.
	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Updated",
		Content:         "v2 content",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("first update failed: %v", err)
	}

	// Stale update via UpdatePageOrConflict.
	_, conflict, err := svc.UpdatePageOrConflict(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Stale",
		Content:         "stale",
		AuthorID:        authorID,
	})
	if !errors.Is(err, wiki.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	if conflict == nil {
		t.Fatal("expected conflict detail, got nil")
	}
	if conflict.CurrentPage.Version != 2 {
		t.Errorf("expected current version 2 in conflict, got %d", conflict.CurrentPage.Version)
	}
	if conflict.ExpectedVersion != 1 {
		t.Errorf("expected expected_version 1 in conflict, got %d", conflict.ExpectedVersion)
	}
}

func TestConcurrentEdits_OneWinsOneConflicts(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	author1 := uuid.New()
	author2 := uuid.New()

	page := createTestPage(t, svc, spaceID, author1, "Shared Page", "original", nil)

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	// Two concurrent editors, both reading version 1.
	go func() {
		defer wg.Done()
		_, err1 = svc.UpdatePage(ctx, wiki.UpdatePageInput{
			PageID:          page.ID,
			ExpectedVersion: 1,
			Title:           "Edit by Author 1",
			Content:         "author 1 content",
			AuthorID:        author1,
		})
	}()

	go func() {
		defer wg.Done()
		_, err2 = svc.UpdatePage(ctx, wiki.UpdatePageInput{
			PageID:          page.ID,
			ExpectedVersion: 1,
			Title:           "Edit by Author 2",
			Content:         "author 2 content",
			AuthorID:        author2,
		})
	}()

	wg.Wait()

	// Exactly one should succeed and one should get a conflict.
	successCount := 0
	conflictCount := 0
	for _, err := range []error{err1, err2} {
		if err == nil {
			successCount++
		} else if errors.Is(err, wiki.ErrVersionConflict) {
			conflictCount++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successCount != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCount)
	}
	if conflictCount != 1 {
		t.Errorf("expected exactly 1 conflict, got %d", conflictCount)
	}
}

// ---------- render tests ----------

func TestRenderPage_Markdown(t *testing.T) {
	svc, _ := testService()

	html, err := svc.RenderPage("# Hello\n\nworld")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html == "" {
		t.Fatal("expected non-empty HTML")
	}
	// Check for heading tag.
	if !findSubstringTest(html, "<h1>Hello</h1>") {
		t.Errorf("expected <h1> in output, got %q", html)
	}
}

func TestRenderPage_GFMTable(t *testing.T) {
	svc, _ := testService()
	md := "| A | B |\n|---|---|\n| 1 | 2 |"
	html, err := svc.RenderPage(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !findSubstringTest(html, "<table>") {
		t.Errorf("expected table in output, got %q", html)
	}
}

func TestRenderPage_EmptyContent(t *testing.T) {
	svc, _ := testService()
	html, err := svc.RenderPage("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html != "" {
		t.Errorf("expected empty output for empty input, got %q", html)
	}
}

func TestRenderer_Standalone(t *testing.T) {
	r := wiki.NewRenderer()
	html, err := r.RenderHTML("**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !findSubstringTest(html, "<strong>bold</strong>") {
		t.Errorf("expected <strong> in output, got %q", html)
	}
}

// TestRenderHTML_RawHTMLIsNotPassedThrough pins the safety boundary the wiki
// markdown renderer actually relies on (S4).
//
// There is no sanitiser in internal/core/wiki/render.go. Author-supplied
// markup is kept out of the rendered page solely by goldmark's default
// Unsafe=false, which is a library default rather than anything visible at
// the call site. Adding html.WithUnsafe() to NewRenderer would turn every
// wiki page into a stored-XSS sink while leaving render.go looking correct.
//
// This test fails in exactly that case. Verified in both directions: with
// html.WithUnsafe() added to NewRenderer's renderer options, every subtest
// below fails; without it, all pass.
func TestRenderHTML_RawHTMLIsNotPassedThrough(t *testing.T) {
	r := wiki.NewRenderer()

	cases := []struct {
		name     string
		markdown string
		// forbidden is the executable markup that must never reach the output.
		forbidden []string
	}{
		{
			name:      "raw script block",
			markdown:  "<script>alert('xss')</script>",
			forbidden: []string{"<script>", "</script>"},
		},
		{
			name:      "inline event handler on a raw tag",
			markdown:  "text <img src=x onerror=\"alert(1)\"> more",
			forbidden: []string{"<img src=x", "onerror=\"alert(1)\""},
		},
		{
			name:      "raw iframe block",
			markdown:  "<iframe src=\"https://evil.example\"></iframe>",
			forbidden: []string{"<iframe", "</iframe>"},
		},
		{
			name:      "svg with inline handler",
			markdown:  "<svg onload=\"alert(1)\"></svg>",
			forbidden: []string{"<svg", "onload="},
		},
		{
			name:      "script smuggled inside a blockquote",
			markdown:  "> quoted\n>\n> <script>alert('nested')</script>",
			forbidden: []string{"<script>", "</script>"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.RenderHTML(tc.markdown)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, bad := range tc.forbidden {
				if findSubstringTest(out, bad) {
					t.Errorf("raw HTML reached the output: %q present in %q\n"+
						"This means the renderer is no longer dropping raw HTML. "+
						"Check whether html.WithUnsafe() was added to NewRenderer.",
						bad, out)
				}
			}
		})
	}
}

// TestRenderHTML_MarkdownStillRendersAfterTheRawHTMLPin guards against the
// lazy way to make the test above pass: neutering the renderer entirely.
// Legitimate markdown must still produce real HTML.
func TestRenderHTML_MarkdownStillRendersAfterTheRawHTMLPin(t *testing.T) {
	r := wiki.NewRenderer()
	out, err := r.RenderHTML("# Title\n\nSome **bold** and a [link](https://example.com).")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"<h1", "Title", "<strong>bold</strong>", `href="https://example.com"`} {
		if !findSubstringTest(out, want) {
			t.Errorf("expected %q in rendered output, got %q", want, out)
		}
	}
}

// ---------- search tests ----------

func TestSearchPages(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	createTestPage(t, svc, spaceID, authorID, "Kubernetes Guide", "deploy pods", nil)
	createTestPage(t, svc, spaceID, authorID, "Docker Guide", "build containers", nil)

	results, err := svc.SearchPages(ctx, wiki.SearchInput{
		SpaceID: spaceID,
		Query:   "Kubernetes",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchPages_EmptyQuery(t *testing.T) {
	svc, _ := testService()
	results, err := svc.SearchPages(context.Background(), wiki.SearchInput{
		SpaceID: uuid.New(),
		Query:   "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestSearchPages_DefaultLimit(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	createTestPage(t, svc, spaceID, authorID, "Match", "content match", nil)

	results, err := svc.SearchPages(ctx, wiki.SearchInput{
		SpaceID: spaceID,
		Query:   "Match",
		Limit:   0, // should use DefaultSearchLimit
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 1 {
		t.Errorf("expected at least 1 result")
	}
}

// ---------- list pages tests ----------

func TestListPagesBySpace(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	createTestPage(t, svc, spaceID, authorID, "Page A", "a", nil)
	createTestPage(t, svc, spaceID, authorID, "Page B", "b", nil)

	// Different space — should not appear.
	createTestPage(t, svc, uuid.New(), authorID, "Other Space", "x", nil)

	pages, err := svc.ListPagesBySpace(ctx, spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pages) != 2 {
		t.Errorf("expected 2 pages, got %d", len(pages))
	}
}

func TestListPagesBySpace_Empty(t *testing.T) {
	svc, _ := testService()
	pages, err := svc.ListPagesBySpace(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("expected 0 pages, got %d", len(pages))
	}
}

func TestUpdatePageOrConflict_Success(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Title", "content", nil)

	updated, conflict, err := svc.UpdatePageOrConflict(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "New Title",
		Content:         "new content",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conflict != nil {
		t.Error("expected no conflict")
	}
	if updated.Version != 2 {
		t.Errorf("expected version 2, got %d", updated.Version)
	}
}

func TestUpdatePage_NotFound(t *testing.T) {
	svc, _ := testService()
	_, err := svc.UpdatePage(context.Background(), wiki.UpdatePageInput{
		PageID:          uuid.New(),
		ExpectedVersion: 1,
		Title:           "Ghost",
		Content:         "nope",
		AuthorID:        uuid.New(),
	})
	if !errors.Is(err, wiki.ErrPageNotFound) {
		t.Errorf("expected ErrPageNotFound, got %v", err)
	}
}

func TestCreatePage_WithParent(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	parent := createTestPage(t, svc, spaceID, authorID, "Parent", "p", nil)
	parentID := parent.ID

	child, err := svc.CreatePage(ctx, wiki.CreatePageInput{
		SpaceID:  spaceID,
		ParentID: &parentID,
		Title:    "Child",
		Content:  "c",
		AuthorID: authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !child.ParentID.Valid || child.ParentID.Bytes != parent.ID {
		t.Error("expected child to reference parent")
	}
}

// ---------- S3: the markdown save refuses a document-backed page ----------

// setDoc puts a stored document on a page, which is what publishing through
// the document editor does to it.
func (m *mockStore) setDoc(pageID uuid.UUID, doc json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pages[pageID]
	p.Doc = doc
	m.pages[pageID] = p
}

// A page that holds a document cannot take a markdown save. The markdown path
// writes `content` and never `doc`, and `doc` is authoritative (ADR-0012), so
// accepting the write would report success and then discard the author's text
// on the next document read.
func TestUpdatePage_RefusesDocumentBackedPage(t *testing.T) {
	svc, store := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Doc page", "markdown body", nil)
	store.setDoc(page.ID, json.RawMessage(`{"type":"doc","content":[]}`))

	_, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Markdown overwrite",
		Content:         "markdown overwrite",
		AuthorID:        authorID,
	})
	if !errors.Is(err, wiki.ErrPageIsDocumentBacked) {
		t.Fatalf("expected ErrPageIsDocumentBacked, got %v", err)
	}

	// And it wrote nothing: no version bump, no history row.
	after, err := svc.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("re-reading page: %v", err)
	}
	if after.Version != 1 {
		t.Errorf("refused save bumped the version to %d", after.Version)
	}
	if after.Content != "markdown body" {
		t.Errorf("refused save changed the content to %q", after.Content)
	}
	revs, err := svc.ListRevisions(ctx, page.ID)
	if err != nil {
		t.Fatalf("listing revisions: %v", err)
	}
	if len(revs) != 1 {
		t.Errorf("refused save wrote history: expected 1 revision, got %d", len(revs))
	}
}

// The refusal is strictly `doc IS NOT NULL`: a page whose document has never
// been published still takes markdown saves. web/e2e/codex-editor.spec.ts
// depends on this — it PUTs markdown to pages the editor has opened but not
// published, and expects 200.
func TestUpdatePage_AllowsPageWithNoStoredDocument(t *testing.T) {
	svc, store := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Markdown page", "body", nil)
	if store.pages[page.ID].Doc != nil {
		t.Fatal("fixture precondition: a freshly created page has no stored document")
	}

	updated, err := svc.UpdatePage(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Markdown page v2",
		Content:         "body v2",
		AuthorID:        authorID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("expected version 2, got %d", updated.Version)
	}
}

// UpdatePageOrConflict passes the document refusal through unchanged: it is
// not a version conflict, so it must not be dressed up as one — a merge UI
// offering "reload and re-apply" would be the wrong advice for a page that
// cannot take this kind of write at all.
func TestUpdatePageOrConflict_DocumentRefusalIsNotAConflictDetail(t *testing.T) {
	svc, store := testService()
	ctx := context.Background()
	spaceID := uuid.New()
	authorID := uuid.New()

	page := createTestPage(t, svc, spaceID, authorID, "Doc page", "body", nil)
	store.setDoc(page.ID, json.RawMessage(`{"type":"doc","content":[]}`))

	_, conflict, err := svc.UpdatePageOrConflict(ctx, wiki.UpdatePageInput{
		PageID:          page.ID,
		ExpectedVersion: 1,
		Title:           "Overwrite",
		Content:         "overwrite",
		AuthorID:        authorID,
	})
	if !errors.Is(err, wiki.ErrPageIsDocumentBacked) {
		t.Fatalf("expected ErrPageIsDocumentBacked, got %v", err)
	}
	if conflict != nil {
		t.Errorf("expected no conflict detail, got %+v", conflict)
	}
}

// findSubstringTest checks if sub is in s — test helper.
func findSubstringTest(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
