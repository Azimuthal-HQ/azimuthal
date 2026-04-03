package projects_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// minimal mocks
type mockItemRepo struct{}

func (m *mockItemRepo) Create(_ context.Context, _ *projects.Item) error   { return nil }
func (m *mockItemRepo) GetByID(_ context.Context, _ uuid.UUID) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) Update(_ context.Context, _ *projects.Item) error   { return nil }
func (m *mockItemRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ string) (*projects.Item, error) {
	return nil, projects.ErrNotFound
}
func (m *mockItemRepo) UpdateSprint(_ context.Context, _ uuid.UUID, _ *uuid.UUID) error {
	return projects.ErrNotFound
}
func (m *mockItemRepo) SoftDelete(_ context.Context, _ uuid.UUID) error    { return nil }
func (m *mockItemRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListByStatus(_ context.Context, _ uuid.UUID, _ string) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListByAssignee(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}
func (m *mockItemRepo) ListBySprint(_ context.Context, _ uuid.UUID) ([]*projects.Item, error) {
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
func (m *mockSprintRepo) GetActiveBySpace(_ context.Context, _ uuid.UUID) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) Update(_ context.Context, _ *projects.Sprint) error   { return nil }
func (m *mockSprintRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ string) (*projects.Sprint, error) {
	return nil, projects.ErrNotFound
}
func (m *mockSprintRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Sprint, error) {
	return nil, nil
}

type mockRelationRepo struct{}

func (m *mockRelationRepo) Create(_ context.Context, _ *projects.Relation) error { return nil }
func (m *mockRelationRepo) ListByItem(_ context.Context, _ uuid.UUID) ([]*projects.Relation, error) {
	return nil, nil
}
func (m *mockRelationRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

type mockLabelRepo struct{}

func (m *mockLabelRepo) Create(_ context.Context, _ *projects.Label) error { return nil }
func (m *mockLabelRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]*projects.Label, error) {
	return nil, nil
}
func (m *mockLabelRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

func setupHandler() *projectsapi.Handler {
	ir := &mockItemRepo{}
	sr := &mockSprintRepo{}
	return projectsapi.NewHandler(
		projects.NewItemService(ir),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		projects.NewRelationService(&mockRelationRepo{}),
		projects.NewLabelService(&mockLabelRepo{}),
	)
}

func withParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
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

func TestListRelationsInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.ListRelations(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateRelationInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "itemID", "bad")
	rr := httptest.NewRecorder()
	h.CreateRelation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDeleteRelationInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "relationID", "bad")
	rr := httptest.NewRecorder()
	h.DeleteRelation(rr, req)
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

func TestListLabelsInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.ListLabels(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateLabelInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.CreateLabel(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDeleteLabelInvalidID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "labelID", "bad")
	rr := httptest.NewRecorder()
	h.DeleteLabel(rr, req)
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
