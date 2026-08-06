package relations_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	relationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/relations"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// mockRelationRepo is the permissive repository double these tests ran on when
// they lived in the projects handler package. It vouches for every target, so
// the tests here exercise the HANDLER's own parsing and auth paths — the
// readable-space predicate itself is asserted against real PostgreSQL by the
// integration battery (D45).
type mockRelationRepo struct{}

func (m *mockRelationRepo) Create(_ context.Context, _ uuid.UUID, _ *projects.NewRelation) error {
	return nil
}

func (m *mockRelationRepo) TargetIsReadable(_ context.Context, _ uuid.UUID, _ string, _ []uuid.UUID) (bool, error) {
	return true, nil
}

func (m *mockRelationRepo) ListForEntity(_ context.Context, _ uuid.UUID, _ string, _ []uuid.UUID) ([]*projects.Relation, error) {
	return nil, nil
}
func (m *mockRelationRepo) DeleteInSpace(_ context.Context, _, _ uuid.UUID) error { return nil }

func setupHandler() *relationsapi.Handler {
	return relationsapi.NewHandler(projects.NewRelationService(&mockRelationRepo{}))
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
// resolution for that space onto the request — the unit-test stand-in for the
// ResolveAccess middleware.
func withSpaceAccess(t *testing.T, r *http.Request, spaceID uuid.UUID) *http.Request {
	t.Helper()
	r = withParam(r, "spaceID", spaceID.String())
	return r.WithContext(access.WithResolution(r.Context(), testutil.OrgAdminResolution(t, spaceID)))
}

// Every wrapper refuses a malformed entity id the same way, because they share
// one core; asserting all three pins the wrappers to it.
func TestListRelationsInvalidID(t *testing.T) {
	h := setupHandler()
	cases := []struct {
		name    string
		idParam string
		call    func(w http.ResponseWriter, r *http.Request)
	}{
		{"item", "itemID", h.ListItemRelations},
		{"ticket", "ticketID", h.ListTicketRelations},
		{"page", "pageID", h.ListPageRelations},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), tc.idParam, "bad")
			rr := httptest.NewRecorder()
			tc.call(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestCreateRelationInvalidID(t *testing.T) {
	h := setupHandler()
	cases := []struct {
		name    string
		idParam string
		call    func(w http.ResponseWriter, r *http.Request)
	}{
		{"item", "itemID", h.CreateItemRelation},
		{"ticket", "ticketID", h.CreateTicketRelation},
		{"page", "pageID", h.CreatePageRelation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), tc.idParam, "bad")
			rr := httptest.NewRecorder()
			tc.call(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
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

func TestListRelationsSuccess(t *testing.T) {
	h := setupHandler()
	req := withSpaceAccess(t, withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"itemID", uuid.New().String()), uuid.New())
	rr := httptest.NewRecorder()
	h.ListItemRelations(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCreateRelationNoAuth(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "itemID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.CreateItemRelation(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeleteRelationSuccess(t *testing.T) {
	h := setupHandler()
	req := withParam(withParam(httptest.NewRequest(http.MethodDelete, "/", nil),
		"relationID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.DeleteRelation(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("got %d, want %d", rr.Code, http.StatusNoContent)
	}
}
