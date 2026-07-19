package spaces_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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
	if h.Routes(nil, nil) == nil {
		t.Fatal("Routes() returned nil")
	}
}

// --- Additional validation and error-path tests ---

func TestCreateNoAuth(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", nil), "orgID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// The mutating handlers check the manage_space capability before reading the
// body, and a request with no resolution on its context must fail closed
// with 403 — if the permission check were deleted, these would reach the nil
// queries and panic, so a green run proves the check runs first. The 400
// validation paths behind the check are covered by the endpoint-matrix
// integration tests with a real authorized user
// (endpoint_matrix_integration_test.go).

func TestUpdateNoResolutionFailsClosed(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{bad")), "spaceID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestAddMemberNoResolutionFailsClosed(t *testing.T) {
	h := setupHandler()
	body := `{"user_id":"` + uuid.New().String() + `","role":"member"}`
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), "spaceID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.AddMember(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRemoveMemberNoResolutionFailsClosed(t *testing.T) {
	h := setupHandler()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("spaceID", uuid.New().String())
	rctx.URLParams.Add("userID", "bad-uuid")
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.RemoveMember(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestDeleteNoResolutionFailsClosed(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodDelete, "/", nil), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestCreateInvalidBody(t *testing.T) {
	// We can't easily test past the auth check without setting claims,
	// but we can verify that an invalid orgID yields 400 before auth check.
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad")), "orgID", "bad-uuid")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetOrgInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodGet, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.GetOrg(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateOrgInvalidOrgID(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPatch, "/", nil), "orgID", "bad")
	rr := httptest.NewRecorder()
	h.UpdateOrg(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateOrgInvalidBody(t *testing.T) {
	h := setupHandler()
	req := withParam(httptest.NewRequest(http.MethodPatch, "/", strings.NewReader("{bad")), "orgID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateOrg(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestUpdateOrgEmptyName(t *testing.T) {
	h := setupHandler()
	body := `{"name":""}`
	req := withParam(httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body)), "orgID", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateOrg(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}
