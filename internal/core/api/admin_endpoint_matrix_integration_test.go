package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Spec §2.6 for the P2.5 administration surface. The admin routes use the
// org-admin-404 class: valid credentials without admin authority read as
// 404 (never 403 — the surface's existence is privileged), which collapses
// the "no access" and "wrong capability" rows into one.

var adminCamelKey = regexp.MustCompile(`"[a-z]+[A-Z]\w*"\s*:`)

func requireAdminSnakeCase(t *testing.T, body []byte) {
	t.Helper()
	require.Empty(t, adminCamelKey.FindAll(body, -1), "wire format must be lowercase snake_case: %s", body)
}

func TestAdminEndpointMatrix_AuthAndAccess(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)
	stranger := func() string {
		otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
		u := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
		return ts.tokenFor(t, u.ID, u.Email)
	}()

	adminGETs := []string{
		fmt.Sprintf("/api/v1/orgs/%s/users", ts.OrgID),
		fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID),
		fmt.Sprintf("/api/v1/orgs/%s/access-matrix", ts.OrgID),
		fmt.Sprintf("/api/v1/orgs/%s/audit-log", ts.OrgID),
	}
	for _, path := range adminGETs {
		// No credentials → 401.
		r := ts.getAs(t, "", path)
		require.Equal(t, http.StatusUnauthorized, r.StatusCode, "unauthenticated %s", path)
		// A member of another org → 404 (ResolveAccess: not an org member).
		r = ts.getAs(t, stranger, path)
		require.Equal(t, http.StatusNotFound, r.StatusCode, "stranger %s: %s", path, r.Body)
		// A member without admin → 404, never 403 (org-admin-404 class).
		r = ts.getAs(t, memTok, path)
		require.Equal(t, http.StatusNotFound, r.StatusCode, "non-admin %s: %s", path, r.Body)
		// Admin → 200 with snake_case keys.
		r = ts.get(t, path, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "admin %s: %s", path, r.Body)
		requireAdminSnakeCase(t, r.Body)
	}

	// Mutations follow the same class.
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	mutations := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil},
		{http.MethodPatch, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), map[string]string{"org_role": "admin"}},
		{http.MethodDelete, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), nil},
		{http.MethodPost, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID), map[string]any{"emails": []string{"m@example.com"}}},
		{http.MethodPost, fmt.Sprintf("/api/v1/orgs/%s/grants/bulk-apply", ts.OrgID), map[string]any{"changes": []any{}}},
	}
	for _, m := range mutations {
		r := ts.requestAs(t, "", m.method, m.path, m.body)
		require.Equal(t, http.StatusUnauthorized, r.StatusCode, "unauthenticated %s %s", m.method, m.path)
		r = ts.requestAs(t, memTok, m.method, m.path, m.body)
		require.Equal(t, http.StatusNotFound, r.StatusCode, "non-admin %s %s: %s", m.method, m.path, r.Body)
	}
}

func TestAdminEndpointMatrix_Validation(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	requireErr := func(r httpResult, wantStatus int, wantCode string) {
		t.Helper()
		require.Equal(t, wantStatus, r.StatusCode, "%s", r.Body)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &body))
		require.Equal(t, wantCode, body.Error.Code)
	}

	// Missing required field.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID), map[string]any{"emails": []string{}}, true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), map[string]any{}, true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Wrong field type (emails as a string, changes as an object).
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID), map[string]any{"emails": "not-a-list"}, true)
	requireErr(r, http.StatusBadRequest, "BAD_REQUEST")
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/grants/bulk-apply", ts.OrgID), map[string]any{"changes": "nope"}, true)
	requireErr(r, http.StatusBadRequest, "BAD_REQUEST")

	// Unknown org role.
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), map[string]string{"org_role": "sovereign"}, true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Unknown role in a bulk change.
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/grants/bulk-apply", ts.OrgID), map[string]any{
		"changes": []map[string]any{{"team_id": uuid.New(), "space_id": uuid.New(), "role": "emperor"}},
	}, true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Invalid audit filter.
	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log?actor_id=not-a-uuid", ts.OrgID), true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")
	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log?from=yesterday", ts.OrgID), true)
	requireErr(r, http.StatusBadRequest, "VALIDATION_ERROR")
}

// Pagination (§2.6): cursor behaviour at first, middle, and empty pages.
func TestAdminEndpointMatrix_AuditLogPagination(t *testing.T) {
	ts := newTestServer(t)

	// Seed 5 singleton audit events via team creation (team.created).
	for i := 0; i < 5; i++ {
		r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/teams", ts.OrgID), map[string]string{
			"name": fmt.Sprintf("Page Team %d", i), "slug": fmt.Sprintf("page-team-%d", i),
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "seed team: %s", r.Body)
	}

	page := func(cursor string) (entries []json.RawMessage, next string) {
		t.Helper()
		path := fmt.Sprintf("/api/v1/orgs/%s/audit-log?action=team.created&limit=2", ts.OrgID)
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := ts.get(t, path, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
		var body struct {
			Entries    []json.RawMessage `json:"entries"`
			NextCursor string            `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &body))
		return body.Entries, body.NextCursor
	}

	// First page: full, with a cursor.
	first, cur1 := page("")
	require.Len(t, first, 2)
	require.NotEmpty(t, cur1, "a full first page must carry a cursor")

	// Middle page: full, advancing.
	second, cur2 := page(cur1)
	require.Len(t, second, 2)
	require.NotEmpty(t, cur2)
	require.NotEqual(t, cur1, cur2)

	// Final page: one row; keyset means no phantom repeats.
	third, cur3 := page(cur2)
	require.Len(t, third, 1)

	// Walking past the end yields an empty page.
	if cur3 != "" {
		empty, _ := page(cur3)
		require.Empty(t, empty)
	}

	// No entry appears on two pages.
	seen := map[string]bool{}
	for _, raw := range append(append(first, second...), third...) {
		var e struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &e))
		require.False(t, seen[e.ID], "entry %s repeated across pages", e.ID)
		seen[e.ID] = true
	}
	require.Len(t, seen, 5)
}

// Administrative actions land in the audit log (D2: append-only, entity.verb).
func TestAdminLifecycle_WritesAuditEvents(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	actions := []struct {
		call   func() httpResult
		action string
	}{
		{func() httpResult {
			return ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/force-logout", ts.OrgID, target.ID), nil, true)
		}, "user.force_logout"},
		{func() httpResult {
			return ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
		}, "user.deactivated"},
		{func() httpResult {
			return ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/reactivate", ts.OrgID, target.ID), nil, true)
		}, "user.reactivated"},
	}
	for _, a := range actions {
		r := a.call()
		require.Equal(t, http.StatusNoContent, r.StatusCode, "%s: %s", a.action, r.Body)
		var n int
		require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM audit_log WHERE org_id = $1 AND action = $2 AND entity_id = $3`,
			ts.OrgID, a.action, target.ID).Scan(&n))
		require.Equal(t, 1, n, "exactly one %s audit event", a.action)
	}

	// Invite lifecycle events.
	out := createInvite(t, ts, "audit-me@example.com", nil)
	require.Equal(t, "created", out.Status)
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_log WHERE org_id = $1 AND action = 'invite.created'`, ts.OrgID).Scan(&n))
	require.Equal(t, 1, n)
}
