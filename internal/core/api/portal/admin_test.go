package portal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	portalapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// stubStore implements only what GetConfig's path touches. The embedded nil
// interface makes any other store call panic, which is the honest failure for
// a test that reached something it did not mean to.
type stubStore struct {
	portal.Store
	bySpace    portal.Portal
	bySpaceErr error
}

func (s *stubStore) PortalBySpace(context.Context, uuid.UUID) (portal.Portal, error) {
	if s.bySpaceErr != nil {
		return portal.Portal{}, s.bySpaceErr
	}
	return s.bySpace, nil
}

// getConfig drives Handler.GetConfig directly, with the chi spaceID param and
// the org-admin access resolution the real middleware chain would provide.
func getConfig(t *testing.T, store portal.Store) *httptest.ResponseRecorder {
	t.Helper()
	h := portalapi.NewHandler(portal.NewService(store, nil, nil, portal.Config{}))
	spaceID := uuid.New()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("spaceID", spaceID.String())
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(access.WithResolution(r.Context(), testutil.OrgAdminResolution(t, spaceID)))

	rr := httptest.NewRecorder()
	h.GetConfig(rr, r)
	return rr
}

// A store failure surfaces as 500, never as the 404 the create affordance
// keys on. usePortalConfig reads 404 as "no portal yet" and renders the
// CREATE form, so before this split a transient database error showed an
// agent an offer to create a portal over a space that has a live one — the
// exact asymmetry UpdateConfig had already fixed for PATCH in this file.
//
// FAILS-BEFORE: against the pre-split GetConfig (every error → 404) the
// store-error case below reported 404. Verified in both directions during
// development.
func TestPortalConfig_GetStoreErrorIs500NotTheCreateAffordance(t *testing.T) {
	rr := getConfig(t, &stubStore{bySpaceErr: errors.New("connection reset")})
	require.Equal(t, http.StatusInternalServerError, rr.Code, "body: %s", rr.Body.String())
	require.NotContains(t, rr.Body.String(), "no customer portal",
		"a store failure must not wear the no-portal-yet message")
}

// The sentinel still answers 404: ErrPortalNotFound is the one error that
// MEANS "no portal yet", and the adapter maps pgx.ErrNoRows to it before the
// service wraps it — errors.Is sees through the wrap.
func TestPortalConfig_GetNoPortalIs404(t *testing.T) {
	rr := getConfig(t, &stubStore{bySpaceErr: portal.ErrPortalNotFound})
	require.Equal(t, http.StatusNotFound, rr.Code, "body: %s", rr.Body.String())
	require.Contains(t, rr.Body.String(), "no customer portal")
}

// And the happy path through the same harness, so the two refusal tests
// cannot both be passing because the harness never reaches the handler.
func TestPortalConfig_GetReturnsTheConfiguration(t *testing.T) {
	rr := getConfig(t, &stubStore{bySpace: portal.Portal{
		ID: uuid.New(), Key: "portalportalportal00", Name: "Acme Support", Enabled: true,
	}})
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
	require.Contains(t, rr.Body.String(), "Acme Support")
}
