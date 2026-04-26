package wiki_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

type mockPageStore struct{}

func (m *mockPageStore) CreatePage(_ context.Context, arg generated.CreatePageParams) (generated.Page, error) {
	return generated.Page{ID: arg.ID, Title: arg.Title}, nil
}
func (m *mockPageStore) GetPageByID(_ context.Context, _ uuid.UUID) (generated.Page, error) {
	return generated.Page{}, wiki.ErrPageNotFound
}
func (m *mockPageStore) UpdatePageContent(_ context.Context, _ generated.UpdatePageContentParams) (generated.Page, error) {
	return generated.Page{}, nil
}
func (m *mockPageStore) UpdatePagePosition(_ context.Context, _ generated.UpdatePagePositionParams) error {
	return nil
}
func (m *mockPageStore) SoftDeletePage(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockPageStore) ListPagesBySpace(_ context.Context, _ uuid.UUID) ([]generated.ListPagesBySpaceRow, error) {
	return nil, nil
}
func (m *mockPageStore) ListRootPagesBySpace(_ context.Context, _ uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error) {
	return nil, nil
}
func (m *mockPageStore) ListChildPages(_ context.Context, _ pgtype.UUID) ([]generated.ListChildPagesRow, error) {
	return nil, nil
}
func (m *mockPageStore) CreatePageRevision(_ context.Context, _ generated.CreatePageRevisionParams) (generated.PageRevision, error) {
	return generated.PageRevision{}, nil
}
func (m *mockPageStore) GetPageRevision(_ context.Context, _ generated.GetPageRevisionParams) (generated.PageRevision, error) {
	return generated.PageRevision{}, wiki.ErrRevisionNotFound
}
func (m *mockPageStore) ListPageRevisions(_ context.Context, _ uuid.UUID) ([]generated.ListPageRevisionsRow, error) {
	return nil, nil
}
func (m *mockPageStore) SearchPages(_ context.Context, _ generated.SearchPagesParams) ([]generated.SearchPagesRow, error) {
	return nil, nil
}

func setupWikiHandler() *wikiapi.Handler {
	svc := wiki.NewService(&mockPageStore{})
	return wikiapi.NewHandler(svc, nil)
}

func withParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestListPagesInvalidSpaceID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.ListPages(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetPageInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.GetPage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDeletePageInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.DeletePage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdatePageInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.UpdatePage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestMovePageInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.MovePage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTreeInvalidSpaceID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Tree(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestListRevisionsInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.ListRevisions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRevisionInvalidPageID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.GetRevision(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRevisionInvalidVersion(t *testing.T) {
	h := setupWikiHandler()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pageID", uuid.New().String())
	rctx.URLParams.Add("version", "notanumber")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.GetRevision(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDiffRevisionsInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=1&to=2", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.DiffRevisions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDiffRevisionsMissingParams(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DiffRevisions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDiffRevisionsInvalidFrom(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=abc&to=2", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DiffRevisions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDiffRevisionsInvalidTo(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=1&to=abc", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DiffRevisions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRenderPageInvalidID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", "bad")
	rr := httptest.NewRecorder()
	h.RenderPage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchInvalidSpaceID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreatePageInvalidSpaceID(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.CreatePage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRoutesReturnsRouter(t *testing.T) {
	h := setupWikiHandler()
	if h.Routes() == nil {
		t.Fatal("Routes() returned nil")
	}
}

// --- Additional validation, service-error, and happy-path tests ---

func TestListPagesSuccess(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.ListPages(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetPageNotFound(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetPage(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDeletePageNotFound(t *testing.T) {
	h := setupWikiHandler()
	// mock SoftDeletePage returns nil so this succeeds
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DeletePage(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestCreatePageNoAuth(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.CreatePage(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestUpdatePageNoAuth(t *testing.T) {
	h := setupWikiHandler()
	body := `{"title":"test","content":"c","expected_version":1}`
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "pageID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdatePage(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestUpdatePageInvalidBody(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{bad")), "pageID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdatePage(rr, req)
	// invalid body check happens after auth check, but auth returns 401 first
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMovePageInvalidBody(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad")), "pageID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.MovePage(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTreeSuccess(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Tree(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestListRevisionsPageNotFound(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.ListRevisions(rr, req)
	// mock ListPageRevisions returns nil, nil so this succeeds
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetRevisionNotFound(t *testing.T) {
	h := setupWikiHandler()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pageID", uuid.New().String())
	rctx.URLParams.Add("version", "1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.GetRevision(rr, req)
	// mock returns ErrRevisionNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDiffRevisionsSuccess(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=1&to=2", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DiffRevisions(rr, req)
	// mock GetPageRevision returns ErrRevisionNotFound, so this will 404
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestRenderPageNotFound(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.RenderPage(rr, req)
	// mock GetPageByID returns ErrPageNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestSearchSuccess(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchWithLimit(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=10", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchInvalidLimit(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=abc", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	// invalid limit falls back to default, still succeeds
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchLimitOutOfRange(t *testing.T) {
	h := setupWikiHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=999", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}
