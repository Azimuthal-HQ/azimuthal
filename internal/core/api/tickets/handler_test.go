package tickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
