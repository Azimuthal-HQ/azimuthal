package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// W7 — audit events (spec §6): every mutation of a team, membership, grant,
// space visibility, or space owner team writes an append-only audit_log row
// with the documented action name, conforming to the existing 008 schema
// (entity_kind and entity_id NOT NULL, payload JSONB).

// auditRow mirrors the queried audit_log columns.
type auditRow struct {
	Action     string
	EntityKind string
	EntityID   uuid.UUID
	ActorID    *uuid.UUID
	Payload    map[string]string
}

func auditRowsFor(t *testing.T, ts *testServer, action string) []auditRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(context.Background(),
		`SELECT action, entity_kind, entity_id, actor_id, payload
		 FROM audit_log WHERE org_id = $1 AND action = $2 ORDER BY created_at ASC`,
		ts.OrgID, action)
	require.NoError(t, err)
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var payload []byte
		require.NoError(t, rows.Scan(&r.Action, &r.EntityKind, &r.EntityID, &r.ActorID, &payload))
		require.NoError(t, json.Unmarshal(payload, &r.Payload))
		out = append(out, r)
	}
	return out
}

// TestAuditEvents_TeamLifecycle drives team mutations through the API and
// asserts each writes its named event with the entity populated.
func TestAuditEvents_TeamLifecycle(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/teams", ts.OrgID)

	// create
	r := ts.post(t, base+"/", map[string]string{"slug": "audited", "name": "Audited"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	var team struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &team))
	teamID := uuid.MustParse(team.ID)

	created := auditRowsFor(t, ts, "team.created")
	require.Len(t, created, 1)
	require.Equal(t, "team", created[0].EntityKind)
	require.Equal(t, teamID, created[0].EntityID)
	require.NotNil(t, created[0].ActorID)
	require.Equal(t, ts.UserID, *created[0].ActorID)
	require.Equal(t, "audited", created[0].Payload["slug"])

	// rename → team.updated
	r = ts.patch(t, base+"/"+team.ID, map[string]string{"name": "Audited 2"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Len(t, auditRowsFor(t, ts, "team.updated"), 1)

	// reparent → team.reparented
	r = ts.post(t, base+"/", map[string]string{"slug": "parent", "name": "Parent"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var parent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &parent))
	r = ts.patch(t, base+"/"+team.ID, map[string]any{"parent_id": parent.ID}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "reparent: %s", r.Body)
	reparented := auditRowsFor(t, ts, "team.reparented")
	require.Len(t, reparented, 1)
	require.Equal(t, parent.ID, reparented[0].Payload["new_parent_id"])

	// membership add / remove → team_member.added / team_member.removed
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberPath := fmt.Sprintf("%s/%s/members/%s", base, team.ID, member.ID)
	rr := ts.put(t, memberPath, map[string]string{"role": "member"}, true)
	require.Equal(t, http.StatusOK, rr.StatusCode, "put member: %s", rr.Body)
	added := auditRowsFor(t, ts, "team_member.added")
	require.Len(t, added, 1)
	require.Equal(t, "team_member", added[0].EntityKind)
	require.Equal(t, member.ID.String(), added[0].Payload["user_id"])

	r = ts.delete(t, memberPath, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	require.Len(t, auditRowsFor(t, ts, "team_member.removed"), 1)

	// delete → team.deleted (reparent back to root first so it has no children)
	r = ts.patch(t, base+"/"+team.ID, json.RawMessage(`{"parent_id": null}`), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "move to root: %s", r.Body)
	r = ts.delete(t, base+"/"+team.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete: %s", r.Body)
	require.Len(t, auditRowsFor(t, ts, "team.deleted"), 1)
}

// TestAuditEvents_GrantLifecycle: grant.created / grant.updated /
// grant.revoked, each naming the grant entity.
func TestAuditEvents_GrantLifecycle(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Audit Grant Space", "audit-grant-space", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", ts.OrgID, spaceID)

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	r := ts.post(t, base+"/", map[string]string{
		"subject_type": "user", "subject_id": member.ID.String(), "role": "viewer",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create grant: %s", r.Body)
	var grant struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &grant))

	created := auditRowsFor(t, ts, "grant.created")
	require.Len(t, created, 1)
	require.Equal(t, "grant", created[0].EntityKind)
	require.Equal(t, uuid.MustParse(grant.ID), created[0].EntityID)
	require.Equal(t, "viewer", created[0].Payload["role"])
	require.Equal(t, member.ID.String(), created[0].Payload["subject_id"])

	r = ts.patch(t, base+"/"+grant.ID, map[string]string{"role": "agent"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	updated := auditRowsFor(t, ts, "grant.updated")
	require.Len(t, updated, 1)
	require.Equal(t, "agent", updated[0].Payload["role"])

	r = ts.delete(t, base+"/"+grant.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	require.Len(t, auditRowsFor(t, ts, "grant.revoked"), 1)
}

// TestAuditEvents_SpaceGovernance: space.visibility_changed and
// space.owner_team_changed, with from/to payloads.
func TestAuditEvents_SpaceGovernance(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Audit Gov Space", "audit-gov-space", "vector")

	// New owning team.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/teams/", ts.OrgID),
		map[string]string{"slug": "owners", "name": "Owners"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var team struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &team))

	spacePath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	r = ts.put(t, spacePath, map[string]any{
		"name":          "Audit Gov Space",
		"visibility":    "org",
		"owner_team_id": team.ID,
	}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "governance update: %s", r.Body)

	vis := auditRowsFor(t, ts, "space.visibility_changed")
	require.Len(t, vis, 1)
	require.Equal(t, "space", vis[0].EntityKind)
	require.Equal(t, uuid.MustParse(spaceID), vis[0].EntityID)
	require.Equal(t, "discoverable", vis[0].Payload["from"])
	require.Equal(t, "org", vis[0].Payload["to"])

	owner := auditRowsFor(t, ts, "space.owner_team_changed")
	require.Len(t, owner, 1)
	require.Equal(t, team.ID, owner[0].Payload["to"])

	// A no-op update (same values) writes no additional events.
	r = ts.put(t, spacePath, map[string]any{
		"name":          "Audit Gov Space",
		"visibility":    "org",
		"owner_team_id": team.ID,
	}, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Len(t, auditRowsFor(t, ts, "space.visibility_changed"), 1, "no-op must not re-log")
	require.Len(t, auditRowsFor(t, ts, "space.owner_team_changed"), 1, "no-op must not re-log")
}

