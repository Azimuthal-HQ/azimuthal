package tickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
)

type mockTicketRepo struct {
	tickets map[uuid.UUID]*tickets.Ticket
}

func newMockTicketRepo() *mockTicketRepo {
	return &mockTicketRepo{tickets: make(map[uuid.UUID]*tickets.Ticket)}
}

func (m *mockTicketRepo) Create(_ context.Context, t *tickets.Ticket) error {
	m.tickets[t.ID] = t
	return nil
}

func (m *mockTicketRepo) GetByID(_ context.Context, id uuid.UUID) (*tickets.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, tickets.ErrNotFound
	}
	return t, nil
}

func (m *mockTicketRepo) Update(_ context.Context, t *tickets.Ticket) error {
	m.tickets[t.ID] = t
	return nil
}

func (m *mockTicketRepo) UpdateStatus(_ context.Context, id uuid.UUID, status tickets.Status) (*tickets.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, tickets.ErrNotFound
	}
	t.Status = status
	return t, nil
}

func (m *mockTicketRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.tickets, id)
	return nil
}

func (m *mockTicketRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*tickets.Ticket, error) {
	return nil, nil
}

func (m *mockTicketRepo) ListByStatus(_ context.Context, _ uuid.UUID, _ tickets.Status) ([]*tickets.Ticket, error) {
	return nil, nil
}

func (m *mockTicketRepo) ListByAssignee(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]*tickets.Ticket, error) {
	return nil, nil
}

func (m *mockTicketRepo) Search(_ context.Context, _ uuid.UUID, _ string, _ int32) ([]*tickets.Ticket, error) {
	return nil, nil
}

func setupTicketHandler() *ticketsapi.Handler {
	svc := tickets.NewTicketService(newMockTicketRepo())
	return ticketsapi.NewHandler(svc)
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestListInvalidSpaceID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "spaceID", "not-a-uuid")
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetInvalidTicketID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "ticketID", "not-a-uuid")
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateNilBody(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = nil
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	// No claims in context, so will get 401
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeleteInvalidID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "ticketID", "bad-uuid")
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTransitionStatusInvalidID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "ticketID", "bad")
	rr := httptest.NewRecorder()
	h.TransitionStatus(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAssignInvalidID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "ticketID", "bad")
	rr := httptest.NewRecorder()
	h.Assign(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUnassignInvalidID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "ticketID", "bad")
	rr := httptest.NewRecorder()
	h.Unassign(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateInvalidID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req = withChiParam(req, "ticketID", "bad")
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchInvalidSpaceID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/?q=test", nil)
	req = withChiParam(req, "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestKanbanInvalidSpaceID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Kanban(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRoutesReturnsRouter(t *testing.T) {
	h := setupTicketHandler()
	r := h.Routes()
	if r == nil {
		t.Fatal("Routes() returned nil")
	}
}

// --- Happy-path, service-error, and validation tests ---

func TestListSuccess(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestGetNotFound(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestCreateInvalidSpaceID(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "spaceID", "bad-uuid")
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateInvalidBody(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	// Need claims context for this path - without it, we get 401
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	// No auth claims, so still 401
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeleteNotFound(t *testing.T) {
	h := setupTicketHandler()
	// The mock repo Delete just deletes from map, so non-existent ID won't error
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestUpdateInvalidBody(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateNotFound(t *testing.T) {
	h := setupTicketHandler()
	body := `{"title":"test","description":"d","priority":"low"}`
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestTransitionStatusInvalidBody(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.TransitionStatus(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTransitionStatusNotFound(t *testing.T) {
	h := setupTicketHandler()
	body := `{"status":"in_progress"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.TransitionStatus(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestAssignInvalidBody(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Assign(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUnassignNotFound(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "ticketID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Unassign(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestSearchSuccess(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/?q=test", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchWithLimit(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/?q=test&limit=25", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchInvalidLimit(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/?q=test&limit=abc", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	// falls back to default limit, still succeeds
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchLimitOutOfRange(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/?q=test&limit=999", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestKanbanSuccess(t *testing.T) {
	h := setupTicketHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Kanban(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}
