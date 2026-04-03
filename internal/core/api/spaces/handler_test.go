package spaces_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
)

func withParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// Handler with nil queries — will panic if it tries to use DB, but
// we're testing the input validation paths before DB calls.
func setupHandler() *spacesapi.Handler {
	return spacesapi.NewHandler(nil)
}

func TestListInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCreateInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDeleteInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestListMembersInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.ListMembers(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAddMemberInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.AddMember(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRemoveMemberInvalidSpaceID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "spaceID", "bad")
	rr := httptest.NewRecorder()
	h.RemoveMember(rr, req)
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
