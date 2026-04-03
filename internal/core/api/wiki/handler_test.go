package wiki_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	return wikiapi.NewHandler(svc)
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
