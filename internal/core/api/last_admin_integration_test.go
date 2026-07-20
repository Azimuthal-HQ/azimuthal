package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// P2.5 failure mode 3: the last active org admin can never be removed,
// demoted, or deactivated — enforced in the STORE layer (asserted directly
// against the adapter below), surfaced clearly at the API.

func TestLastAdmin_CannotDeactivate_API(t *testing.T) {
	ts := newTestServer(t)
	// The org has exactly one admin-class member: the owner (ts.UserID).
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, ts.UserID), nil, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "deactivating the last admin must 409: %s", r.Body)
	// Surfaced clearly, not as a generic error (spec W4).
	require.Contains(t, strings.ToLower(string(r.Body)), "last active admin",
		"the error must name the last-admin protection")

	// Still active and still authenticated.
	require.Equal(t, http.StatusOK, ts.get(t, "/api/v1/auth/me", true).StatusCode)
}

func TestLastAdmin_CannotDemoteOrRemove_API(t *testing.T) {
	ts := newTestServer(t)
	admin2 := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	// With two admin-class members, deactivating one works.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, admin2.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "deactivating a non-last admin: %s", r.Body)

	// The owner is now the last ACTIVE admin (admin2 deactivated does not
	// count) — no lifecycle door out of adminhood may open.
	r = ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, ts.UserID), true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "removing the last active admin must 409: %s", r.Body)

	// Owner role changes are blocked outright (owners are provisioning-time
	// only), which also covers the demotion door for this org shape.
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, ts.UserID),
		map[string]string{"org_role": "member"}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "owner role change must 409: %s", r.Body)
}

func TestLastAdmin_DemotionOfLastPlainAdminBlocked_API(t *testing.T) {
	ts := newTestServer(t)
	admin2 := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	// Deactivate the owner so admin2 becomes the last ACTIVE admin. (Two
	// admin-class members exist, so this succeeds; the actor is the owner
	// deactivating themselves.)
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, ts.UserID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "self-deactivation with another admin present: %s", r.Body)

	adminTok, _ := loginAs(t, ts, admin2.Email)

	// admin2 (a plain admin, not owner) is now the last active admin —
	// demotion must be refused.
	req := ts.patchAs(t, adminTok, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, admin2.ID),
		map[string]string{"org_role": "member"})
	require.Equal(t, http.StatusConflict, req.StatusCode, "demoting the last active admin must 409: %s", req.Body)
	require.Contains(t, strings.ToLower(string(req.Body)), "last active admin")

	// Their own deactivation and removal are refused for the same reason.
	req = ts.postAs(t, adminTok, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, admin2.ID), nil)
	require.Equal(t, http.StatusConflict, req.StatusCode)
	req = ts.deleteAs(t, adminTok, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, admin2.ID))
	require.Equal(t, http.StatusConflict, req.StatusCode)
}

// Store-layer enforcement, asserted against the adapter directly — the
// invariant must hold with no HTTP layer involved.
func TestLastAdmin_EnforcedInStoreLayer(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewPeopleAdapter(db.Pool)

	require.ErrorIs(t, store.Deactivate(t.Context(), org.ID, owner.ID), people.ErrLastAdmin,
		"store must refuse deactivating the last active admin")
	require.ErrorIs(t, store.RemoveFromOrg(t.Context(), org.ID, owner.ID), people.ErrLastAdmin,
		"store must refuse removing the last active admin")

	// A second admin unblocks the paths; demoting them again re-blocks.
	admin2 := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "admin")
	require.NoError(t, store.Deactivate(t.Context(), org.ID, admin2.ID))
	require.ErrorIs(t, store.Deactivate(t.Context(), org.ID, owner.ID), people.ErrLastAdmin,
		"a deactivated admin must not count as cover for the last active one")
	require.NoError(t, store.Reactivate(t.Context(), org.ID, admin2.ID))
	require.NoError(t, store.ChangeOrgRole(t.Context(), org.ID, admin2.ID, "member"),
		"demoting a non-last admin works")
	require.NoError(t, store.ChangeOrgRole(t.Context(), org.ID, admin2.ID, "member"),
		"setting the same role again is an idempotent no-op")
}

// Deactivation is global (is_active spans orgs), so the guard must consider
// every org the target administers — not just the org the action came from.
func TestLastAdmin_GlobalDeactivationGuard_CrossOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	orgA := testutil.CreateTestOrg(t, db.Pool)
	orgB := testutil.CreateTestOrg(t, db.Pool)
	ownerA := testutil.CreateTestUser(t, db.Pool, orgA.ID)
	// ownerA is also the sole admin of orgB via a second membership.
	_, err := db.Pool.Exec(t.Context(),
		`INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'admin')`, orgB.ID, ownerA.ID)
	require.NoError(t, err)
	// Another admin in orgA, so orgA alone would permit the deactivation.
	testutil.CreateTestUserWithRole(t, db.Pool, orgA.ID, "admin")

	store := adapters.NewPeopleAdapter(db.Pool)
	require.ErrorIs(t, store.Deactivate(t.Context(), orgA.ID, ownerA.ID), people.ErrLastAdmin,
		"deactivation must be blocked when the target is the last admin of ANY org they administer")
}

// requestAs performs a request with an arbitrary bearer token — the
// persona-scoped counterpart of the authed testServer helpers.
func (ts *testServer) requestAs(t *testing.T, token, method, path string, body any) httpResult {
	t.Helper()
	var reader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(jsonBody)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.url(path), reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return ts.do(t, req)
}

func (ts *testServer) postAs(t *testing.T, token, path string, body any) httpResult {
	t.Helper()
	return ts.requestAs(t, token, http.MethodPost, path, body)
}

func (ts *testServer) patchAs(t *testing.T, token, path string, body any) httpResult {
	t.Helper()
	return ts.requestAs(t, token, http.MethodPatch, path, body)
}

func (ts *testServer) deleteAs(t *testing.T, token, path string) httpResult {
	t.Helper()
	return ts.requestAs(t, token, http.MethodDelete, path, nil)
}
