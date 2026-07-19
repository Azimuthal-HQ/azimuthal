package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// newBodyReader returns a reader over b, or an empty (not nil) body.
func newBodyReader(b []byte) io.Reader {
	if b == nil {
		return http.NoBody
	}
	return bytes.NewReader(b)
}

// Spec §2.6 — the mandatory matrix for every new endpoint: 401 without
// credentials, 404 with credentials but no access (never leak existence),
// 403 with access but the wrong capability, 2xx happy path, 400 for missing
// and mistyped fields, and lowercase snake_case wire format throughout.
//
// Personas: admin (org owner via the harness), member (org member, no
// grants, no admin), stranger (valid token, different org — no access).

type endpointMatrix struct {
	ts       *testServer
	member   testutil.User
	memTok   string
	stranger testutil.User
	strTok   string
	spaceID  string
}

func newEndpointMatrix(t *testing.T) *endpointMatrix {
	t.Helper()
	ts := newTestServer(t)

	m := &endpointMatrix{ts: ts}
	m.member = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	m.memTok = ts.tokenFor(t, m.member.ID, m.member.Email)

	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	m.stranger = testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	m.strTok = ts.tokenFor(t, m.stranger.ID, m.stranger.Email)

	m.spaceID = createScopedSpace(t, ts, "Matrix EP Space", "matrix-ep-space", "vector")
	return m
}

func (m *endpointMatrix) request(t *testing.T, method, token, path string, body any) httpResult {
	t.Helper()
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, m.ts.url(path), newBodyReader(reqBody))
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return m.ts.do(t, req)
}

// camelKey spots lowerCamelCase or UpperCamel JSON keys — the wire format is
// lowercase snake_case without exception (spec §6).
var camelKey = regexp.MustCompile(`"[a-z0-9_]*[A-Z][A-Za-z0-9]*"\s*:`)

func requireSnakeCaseKeys(t *testing.T, body []byte) {
	t.Helper()
	require.NotRegexp(t, camelKey, string(body), "response keys must be lowercase snake_case")
}

func requireErrorCode(t *testing.T, r httpResult, status int, code string) {
	t.Helper()
	require.Equal(t, status, r.StatusCode, "body: %s", r.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "error envelope expected, got: %s", r.Body)
	require.Equal(t, code, body.Error.Code)
}

// TestEndpointMatrix_Teams runs the §2.6 rows over the team endpoints.
func TestEndpointMatrix_Teams(t *testing.T) {
	m := newEndpointMatrix(t)
	teamsPath := fmt.Sprintf("/api/v1/orgs/%s/teams", m.ts.OrgID)

	// 401 — no credentials.
	requireErrorCode(t, m.request(t, http.MethodGet, "", teamsPath+"/", nil), http.StatusUnauthorized, "UNAUTHORIZED")
	requireErrorCode(t, m.request(t, http.MethodPost, "", teamsPath+"/", map[string]string{"slug": "x", "name": "X"}), http.StatusUnauthorized, "UNAUTHORIZED")

	// 404 — valid credentials, not an org member: existence never leaks.
	requireErrorCode(t, m.request(t, http.MethodGet, m.strTok, teamsPath+"/", nil), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, m.request(t, http.MethodPost, m.strTok, teamsPath+"/",
		map[string]string{"slug": "x", "name": "X"}), http.StatusNotFound, "NOT_FOUND")

	// 403 — org member without admin: reads pass, mutations are forbidden.
	r := m.request(t, http.MethodGet, m.memTok, teamsPath+"/", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "members may list teams: %s", r.Body)
	requireErrorCode(t, m.request(t, http.MethodPost, m.memTok, teamsPath+"/",
		map[string]string{"slug": "squad", "name": "Squad"}), http.StatusForbidden, "FORBIDDEN")

	// 400 — missing required fields, then a wrong field type.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, teamsPath+"/",
		map[string]string{"slug": "no-name"}), http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, teamsPath+"/",
		map[string]any{"slug": 123, "name": "Bad Slug Type"}), http.StatusBadRequest, "BAD_REQUEST")

	// 201 — happy path, snake_case body.
	r = m.request(t, http.MethodPost, m.ts.Token, teamsPath+"/",
		map[string]string{"slug": "squad", "name": "Squad", "description": "d"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)
	var team struct {
		ID        string   `json:"id"`
		Slug      string   `json:"slug"`
		Path      []string `json:"path"`
		IsDefault bool     `json:"is_default"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &team))
	require.Equal(t, "squad", team.Slug)
	require.Len(t, team.Path, 1)
	require.False(t, team.IsDefault)

	// GET one: member reads it; stranger 404s; foreign team id 404s.
	r = m.request(t, http.MethodGet, m.memTok, teamsPath+"/"+team.ID, nil)
	require.Equal(t, http.StatusOK, r.StatusCode)
	requireSnakeCaseKeys(t, r.Body)
	requireErrorCode(t, m.request(t, http.MethodGet, m.strTok, teamsPath+"/"+team.ID, nil), http.StatusNotFound, "NOT_FOUND")

	// PATCH: member 403; admin renames 200.
	requireErrorCode(t, m.request(t, http.MethodPatch, m.memTok, teamsPath+"/"+team.ID,
		map[string]string{"name": "Nope"}), http.StatusForbidden, "FORBIDDEN")
	r = m.request(t, http.MethodPatch, m.ts.Token, teamsPath+"/"+team.ID,
		map[string]string{"name": "Renamed Squad"})
	require.Equal(t, http.StatusOK, r.StatusCode, "rename: %s", r.Body)

	// Members sub-resource: PUT enrols (admin), member 403, bad role 400.
	memberPath := fmt.Sprintf("%s/%s/members/%s", teamsPath, team.ID, m.member.ID)
	requireErrorCode(t, m.request(t, http.MethodPut, m.memTok, memberPath,
		map[string]string{"role": "member"}), http.StatusForbidden, "FORBIDDEN")
	requireErrorCode(t, m.request(t, http.MethodPut, m.ts.Token, memberPath,
		map[string]string{"role": "emperor"}), http.StatusBadRequest, "VALIDATION_ERROR")
	r = m.request(t, http.MethodPut, m.ts.Token, memberPath, map[string]string{"role": "lead"})
	require.Equal(t, http.StatusOK, r.StatusCode, "enrol member: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)

	r = m.request(t, http.MethodGet, m.memTok, fmt.Sprintf("%s/%s/members", teamsPath, team.ID), nil)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var members []struct {
		UserID    string `json:"user_id"`
		Role      string `json:"role"`
		IsPrimary bool   `json:"is_primary"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &members))
	require.Len(t, members, 1)
	require.Equal(t, m.member.ID.String(), members[0].UserID)

	// DELETE member: admin only; then DELETE team: member 403, admin 204.
	requireErrorCode(t, m.request(t, http.MethodDelete, m.memTok, memberPath, nil), http.StatusForbidden, "FORBIDDEN")
	r = m.request(t, http.MethodDelete, m.ts.Token, memberPath, nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode)

	requireErrorCode(t, m.request(t, http.MethodDelete, m.memTok, teamsPath+"/"+team.ID, nil), http.StatusForbidden, "FORBIDDEN")
	r = m.request(t, http.MethodDelete, m.ts.Token, teamsPath+"/"+team.ID, nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
}

// TestEndpointMatrix_Grants runs the §2.6 rows over the grant endpoints.
func TestEndpointMatrix_Grants(t *testing.T) {
	m := newEndpointMatrix(t)
	grantsPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", m.ts.OrgID, m.spaceID)

	// 401 — no credentials.
	requireErrorCode(t, m.request(t, http.MethodGet, "", grantsPath+"/", nil), http.StatusUnauthorized, "UNAUTHORIZED")

	// 404 — stranger (not an org member).
	requireErrorCode(t, m.request(t, http.MethodGet, m.strTok, grantsPath+"/", nil), http.StatusNotFound, "NOT_FOUND")

	// 404 — org member with no read access to the space: the grants surface
	// must not reveal the space exists.
	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok, grantsPath+"/", nil), http.StatusNotFound, "NOT_FOUND")

	// 403 — a viewer can read the space but lacks manage_grants.
	spaceUUID := uuid.MustParse(m.spaceID)
	_, err := m.ts.GrantService.Create(context.Background(), m.ts.OrgID, spaceUUID,
		access.SubjectUser, m.member.ID, access.RoleViewer, m.ts.UserID)
	require.NoError(t, err)
	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok, grantsPath+"/", nil), http.StatusForbidden, "FORBIDDEN")
	requireErrorCode(t, m.request(t, http.MethodPost, m.memTok, grantsPath+"/",
		map[string]string{"subject_type": "user", "subject_id": m.member.ID.String(), "role": "viewer"}),
		http.StatusForbidden, "FORBIDDEN")

	// 400 — missing subject_id, unknown role, unknown subject_type.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]string{"subject_type": "user", "role": "viewer"}), http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]string{"subject_type": "user", "subject_id": m.member.ID.String(), "role": "emperor"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]string{"subject_type": "robot", "subject_id": m.member.ID.String(), "role": "viewer"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	// Wrong field type.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]any{"subject_type": "user", "subject_id": 42, "role": "viewer"}),
		http.StatusBadRequest, "BAD_REQUEST")

	// 201 — happy path (a second grant, to a team), snake_case keys.
	defTeam := testutil.DefaultTeamID(t, m.ts.DB.Pool, m.ts.OrgID)
	r := m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]string{"subject_type": "team", "subject_id": defTeam.String(), "role": "contributor"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create grant: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)
	var grant struct {
		ID          string `json:"id"`
		SubjectType string `json:"subject_type"`
		Role        string `json:"role"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &grant))
	require.Equal(t, "team", grant.SubjectType)
	require.Equal(t, "contributor", grant.Role)

	// 409 — duplicate (same space, same subject).
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, grantsPath+"/",
		map[string]string{"subject_type": "team", "subject_id": defTeam.String(), "role": "viewer"}),
		http.StatusConflict, "CONFLICT")

	// PATCH role: bad role 400; happy 200; unknown grant id 404.
	requireErrorCode(t, m.request(t, http.MethodPatch, m.ts.Token, grantsPath+"/"+grant.ID,
		map[string]string{"role": "emperor"}), http.StatusBadRequest, "VALIDATION_ERROR")
	r = m.request(t, http.MethodPatch, m.ts.Token, grantsPath+"/"+grant.ID,
		map[string]string{"role": "agent"})
	require.Equal(t, http.StatusOK, r.StatusCode, "update grant: %s", r.Body)
	requireErrorCode(t, m.request(t, http.MethodPatch, m.ts.Token, grantsPath+"/"+uuid.NewString(),
		map[string]string{"role": "agent"}), http.StatusNotFound, "NOT_FOUND")

	// A grant id from another space must be indistinguishable from missing.
	otherSpace := createScopedSpace(t, m.ts, "Other EP Space", "other-ep-space", "vector")
	otherGrants := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", m.ts.OrgID, otherSpace)
	r = m.request(t, http.MethodPost, m.ts.Token, otherGrants+"/",
		map[string]string{"subject_type": "user", "subject_id": m.member.ID.String(), "role": "viewer"})
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var foreignGrant struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &foreignGrant))
	requireErrorCode(t, m.request(t, http.MethodDelete, m.ts.Token, grantsPath+"/"+foreignGrant.ID, nil),
		http.StatusNotFound, "NOT_FOUND")

	// DELETE: happy 204, revocation visible on the next request (the viewer
	// grant from above is revoked → member loses the 403-with-read state and
	// returns to 404-no-read).
	r = m.request(t, http.MethodDelete, m.ts.Token, grantsPath+"/"+grant.ID, nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode)

	var memberGrantID string
	require.NoError(t, m.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT id FROM space_grants WHERE space_id = $1 AND subject_type = 'user' AND subject_id = $2`,
		spaceUUID, m.member.ID).Scan(&memberGrantID))
	r = m.request(t, http.MethodDelete, m.ts.Token, grantsPath+"/"+memberGrantID, nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID), nil),
		http.StatusNotFound, "NOT_FOUND")
}

// TestEndpointMatrix_EffectiveAccess covers the §2.6 rows for the
// explanation endpoint.
func TestEndpointMatrix_EffectiveAccess(t *testing.T) {
	m := newEndpointMatrix(t)
	path := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/effective-access", m.ts.OrgID, m.spaceID)

	requireErrorCode(t, m.request(t, http.MethodGet, "", path, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	requireErrorCode(t, m.request(t, http.MethodGet, m.strTok, path, nil), http.StatusNotFound, "NOT_FOUND")
	// Member without read access: 404, not an empty explanation.
	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok, path, nil), http.StatusNotFound, "NOT_FOUND")

	// Bad user_id → 400.
	requireErrorCode(t, m.request(t, http.MethodGet, m.ts.Token, path+"?user_id=banana", nil),
		http.StatusBadRequest, "BAD_REQUEST")

	// Viewer asks about self: 200, snake_case; asks about another: 403.
	_, err := m.ts.GrantService.Create(context.Background(), m.ts.OrgID, uuid.MustParse(m.spaceID),
		access.SubjectUser, m.member.ID, access.RoleViewer, m.ts.UserID)
	require.NoError(t, err)
	r := m.request(t, http.MethodGet, m.memTok, path, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "self-inspection: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)
	var expl struct {
		Access bool   `json:"access"`
		Role   string `json:"role"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &expl))
	require.True(t, expl.Access)
	require.Equal(t, "viewer", expl.Role)

	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok,
		path+"?user_id="+m.ts.UserID.String(), nil), http.StatusForbidden, "FORBIDDEN")
}
