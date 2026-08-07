package projects_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// minimal mocks
type mockItemRepo struct{}

func (m *mockItemRepo) Create(_ context.Context, _ *projects.Item) error { return nil }
func (m *mockItemRepo) GetByID(_ context.Context, _ uuid.UUID) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) GetByIDInSpace(_ context.Context, _, _ uuid.UUID) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) GetByOrgKey(_ context.Context, _ uuid.UUID, _ string) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) Update(_ context.Context, _ *projects.Item) error { return nil }
func (m *mockItemRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ string) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) UpdateSprintInSpace(_ context.Context, _, _ uuid.UUID, _ *uuid.UUID) error {
	return projects.ErrNotFound
}
func (m *mockItemRepo) SoftDeleteInSpace(_ context.Context, _, _ uuid.UUID) error { return nil }
func (m *mockItemRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListByStatus(_ context.Context, _ uuid.UUID, _ string) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListByAssignee(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListBySprint(_ context.Context, _, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) Search(_ context.Context, _ uuid.UUID, _ string, _ int) ([]*projects.Item, error) {
	return nil, nil
}

type mockSprintRepo struct{}

func (m *mockSprintRepo) Create(_ context.Context, _ *projects.Sprint) error { return nil }
func (m *mockSprintRepo) GetByID(_ context.Context, _ uuid.UUID) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) GetByIDInSpace(_ context.Context, _, _ uuid.UUID) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) GetActiveBySpace(_ context.Context, _ uuid.UUID) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) Update(_ context.Context, _ *projects.Sprint) error { return nil }
func (m *mockSprintRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ string) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) CompleteWithDisposition(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _ []string) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Sprint, error) {
	return nil, nil
}

// noopShareDeleter satisfies projects.ShareRevokingDeleter for handler tests.
type noopShareDeleter struct{}

func (noopShareDeleter) DeleteItemAndRevokeShares(_ context.Context, _, _, _ uuid.UUID) error {
	return nil
}

func setupHandler() *projectsapi.Handler {
	ir := &mockItemRepo{}
	sr := &mockSprintRepo{}
	return projectsapi.NewHandler(
		projects.NewItemService(ir, noopShareDeleter{}),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		nil,
	)
}

func withParam(r *http.Request, key, val string) *http.Request {
	rctx, ok := r.Context().Value(chi.RouteCtxKey).(*chi.Context)
	if !ok {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withSpaceAccess adds the spaceID URL param and stamps an org-admin access
// resolution for that space onto the request — the unit-test stand-in for
// the ResolveAccess middleware (in-handler capability checks fail closed
// without a resolution).
func withSpaceAccess(t *testing.T, r *http.Request, spaceID uuid.UUID) *http.Request {
	t.Helper()
	r = withParam(r, "spaceID", spaceID.String())
	return r.WithContext(access.WithResolution(r.Context(), testutil.OrgAdminResolution(t, spaceID)))
}

func TestListItemsInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.ListItems(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetItemInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.GetItem(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDeleteItemInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.DeleteItem(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateItemInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.UpdateItem(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateItemStatusInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.UpdateItemStatus(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAssignToSprintInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.AssignToSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchItemsInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestListSprintsInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.ListSprints(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateSprintInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.CreateSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetSprintInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "sprintID", "bad")
	rr := httptest.NewRecorder()
	h.GetSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateSprintInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", nil), "sprintID", "bad")
	rr := httptest.NewRecorder()
	h.UpdateSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestStartSprintInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "sprintID", "bad")
	rr := httptest.NewRecorder()
	h.StartSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCompleteSprintInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "sprintID", "bad")
	rr := httptest.NewRecorder()
	h.CompleteSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestListSprintItemsInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "sprintID", "bad")
	rr := httptest.NewRecorder()
	h.ListSprintItems(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetActiveSprintInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.GetActiveSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetBacklogInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.GetBacklog(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRoadmapInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=2024-01-01&to=2024-12-31", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.GetRoadmap(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetOverdueItemsInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.GetOverdueItems(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetSprintRoadmapInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.GetSprintRoadmap(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRoutesReturnsRouter(t *testing.T) {
	h := setupHandler()
	if h.Routes() == nil {
		t.Fatal("Routes() returned nil")
	}
}

// --- Happy-path and service-error tests ---

func TestListItemsSuccess(t *testing.T) {
	h := setupHandler()
	spaceID := uuid.New()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", spaceID.String())
	rr := httptest.NewRecorder()
	h.ListItems(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetItemNotFound(t *testing.T) {
	h := setupHandler()
	req := withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"itemID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetItem(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDeleteItemNotFound(t *testing.T) {
	h := setupHandler()
	// DeleteItem fetches the item first (the edit_own/edit_any check needs
	// the reporter), and the mock GetByID always reports not-found — so
	// deleting a nonexistent item is 404.
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	rr := httptest.NewRecorder()
	h.DeleteItem(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestCreateItemNoAuth(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.CreateItem(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestCreateItemInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.CreateItem(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateItemNotFound(t *testing.T) {
	h := setupHandler()
	body := `{"title":"t","description":"d","priority":"high"}`
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateItem(rr, req)
	// mock GetByID returns ErrNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestUpdateItemInvalidBody(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{bad json")), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateItem(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateItemStatusNotFound(t *testing.T) {
	h := setupHandler()
	body := `{"status":"closed"}`
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateItemStatus(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestUpdateItemStatusInvalidBody(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad")), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateItemStatus(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAssignToSprintInvalidBody(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad")), "itemID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.AssignToSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAssignToSprintNotFound(t *testing.T) {
	h := setupHandler()
	body := `{"sprint_id":null}`
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), "itemID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.AssignToSprint(rr, req)
	// mock UpdateSprint returns ErrNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestSearchItemsEmptyQuery(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchItemsSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchItemsWithLimit(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=25", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchItemsInvalidLimit(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=abc", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	// invalid limit should fall back to default, still succeed
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchItemsLimitOutOfRange(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?q=test&limit=999", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.SearchItems(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestListSprintsSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.ListSprints(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCreateSprintNoAuth(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.CreateSprint(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestGetSprintNotFound(t *testing.T) {
	h := setupHandler()
	req := withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"sprintID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetSprint(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestUpdateSprintInvalidBody(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{bad")), "sprintID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateSprintNotFound(t *testing.T) {
	h := setupHandler()
	body := `{"name":"Sprint 1","goal":"ship it"}`
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "sprintID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSprint(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestStartSprintNotFound(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "sprintID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	rr := httptest.NewRecorder()
	h.StartSprint(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestCompleteSprintNotFound(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "sprintID", uuid.New().String())
	req = withSpaceAccess(t, req, uuid.New())
	rr := httptest.NewRecorder()
	h.CompleteSprint(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestListSprintItemsSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"sprintID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.ListSprintItems(rr, req)
	// mock ListBySprint returns nil, nil so this succeeds
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetActiveSprintNotFound(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetActiveSprint(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestGetBacklogSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetBacklog(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestMoveToSprintInvalidBody(t *testing.T) {
	h := setupHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.MoveToSprint(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestMoveToSprintServiceError(t *testing.T) {
	h := setupHandler()
	body := `{"item_id":"` + uuid.New().String() + `","sprint_id":"` + uuid.New().String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.MoveToSprint(rr, req)
	// mock sprint repo GetByID returns ErrNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestMoveToBacklogServiceError(t *testing.T) {
	h := setupHandler()
	body := `{"item_id":"` + uuid.New().String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.MoveToBacklog(rr, req)
	// mock itemRepo.UpdateSprint returns ErrNotFound
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestMoveToBacklogInvalidBody(t *testing.T) {
	h := setupHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	req = withSpaceAccess(t, req, uuid.New())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.MoveToBacklog(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRoadmapMissingDateParams(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetRoadmap(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRoadmapInvalidFromDate(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=bad&to=2024-12-31", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetRoadmap(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRoadmapInvalidToDate(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=2024-01-01&to=bad", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetRoadmap(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetRoadmapSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/?from=2024-01-01&to=2024-12-31", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetRoadmap(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetOverdueItemsSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetOverdueItems(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetSprintRoadmapSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.GetSprintRoadmap(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}
