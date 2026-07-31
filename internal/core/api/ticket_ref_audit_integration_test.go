package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	adminapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/admin"
	grantsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/grants"
	invitesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/invites"
	sharesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/shares"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	teamsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/tiergate"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/invites"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	coreteams "github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// A3 + A2 — the operator ticket reference on administrative mutations.
//
// The reference travels as the `ticket_ref` query parameter and lands in the
// dedicated audit_log.ticket_ref column (migration 025), NOT in the payload
// JSONB. These tests drive the six extended handlers — admin people
// lifecycle, teams, spaces, invites, and (since B3) grants and shares —
// through the real router against a real database and read the column back
// with SQL.
//
// Three properties are load-bearing and easy to regress:
//
//   - One request can emit several events (a three-field person PATCH, a
//     rename-and-reparent team PATCH, a bulk invite). Every event of one
//     request must carry the same reference; a handler that resolved the
//     reference per write, or forgot to pass it to one of its logEvent calls,
//     would leave a hole here.
//   - Under the required policy (A2) a missing reference must mean *nothing
//     happened*. A 400 returned after the write is the exact failure the
//     feature exists to prevent, and only a state assertion catches it.
//   - The reference is checked AFTER the authorisation gate and never before
//     it. A 400 saying "ticket_ref is required" for a resource the caller may
//     not see answers differently from that resource's 403 or 404, and is
//     therefore an existence oracle. Nothing asserted this before B3.

// --- helpers ---

// ticketRefAuditRow is one audit_log row, reduced to what these tests assert.
// Ref is a pointer so SQL NULL (no reference supplied) stays distinguishable
// from the empty string — internal/core/audit/db_logger.go converts "" to
// nil, and that conversion is itself under test.
type ticketRefAuditRow struct {
	Action string
	Ref    *string
}

// ticketRefAuditRowsFor returns every audit row the org recorded for one
// action, oldest first.
func ticketRefAuditRowsFor(t *testing.T, ts *testServer, event audit.EventType) []ticketRefAuditRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(t.Context(),
		`SELECT action, ticket_ref FROM audit_log
		 WHERE org_id = $1 AND action = $2 ORDER BY created_at ASC`,
		ts.OrgID, string(event))
	require.NoError(t, err)
	defer rows.Close()

	var out []ticketRefAuditRow
	for rows.Next() {
		var row ticketRefAuditRow
		require.NoError(t, rows.Scan(&row.Action, &row.Ref))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// ticketRefAuditRowsForEntity returns every audit row about one entity,
// used where a single request emits several events about the same subject.
func ticketRefAuditRowsForEntity(t *testing.T, ts *testServer, entityID uuid.UUID) []ticketRefAuditRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(t.Context(),
		`SELECT action, ticket_ref FROM audit_log
		 WHERE org_id = $1 AND entity_id = $2 ORDER BY action ASC`,
		ts.OrgID, entityID)
	require.NoError(t, err)
	defer rows.Close()

	var out []ticketRefAuditRow
	for rows.Next() {
		var row ticketRefAuditRow
		require.NoError(t, rows.Scan(&row.Action, &row.Ref))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// ticketRefAuditRequireSole asserts the org recorded exactly one event of
// this type and that its ticket_ref column holds want.
func ticketRefAuditRequireSole(t *testing.T, ts *testServer, event audit.EventType, want string) {
	t.Helper()
	rows := ticketRefAuditRowsFor(t, ts, event)
	require.Len(t, rows, 1, "expected exactly one %s event", event)
	require.NotNil(t, rows[0].Ref, "%s must store the reference, not NULL", event)
	require.Equal(t, want, *rows[0].Ref, "%s carries the wrong reference", event)
}

// The two rejection messages ticketref.Policy writes. Asserting on them is
// what makes a 400 mean "the reference was refused" rather than "something
// about this request was invalid" — a body validation error, a dead team and
// a missing reference are all 400 VALIDATION_ERROR otherwise.
const (
	ticketRefAuditMissingMessage = "ticket_ref is required"
	ticketRefAuditTooLongMessage = "ticket_ref must be 200 characters or fewer"
)

// ticketRefAuditRequireRejected asserts the response is the ticket-reference
// policy's own 400: status, error code, and a message naming the reference.
func ticketRefAuditRequireRejected(t *testing.T, r httpResult, wantMessage string) {
	t.Helper()
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "error envelope expected, got: %s", r.Body)
	require.Contains(t, body.Error.Message, wantMessage,
		"the request was rejected, but not by the ticket-reference policy: %s", r.Body)
}

// ticketRefAuditOrgRowCount counts every audit row in the org — the
// "nothing at all was written" assertion.
func ticketRefAuditOrgRowCount(t *testing.T, ts *testServer) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_log WHERE org_id = $1`, ts.OrgID).Scan(&n))
	return n
}

// ticketRefAuditTeamsPath is the org's team collection.
func ticketRefAuditTeamsPath(ts *testServer) string {
	return fmt.Sprintf("/api/v1/orgs/%s/teams", ts.OrgID)
}

// ticketRefAuditCreateTeam posts a team create, appending query (which may
// be "" for no reference at all) to the URL.
func ticketRefAuditCreateTeam(t *testing.T, ts *testServer, slug, query string) httpResult {
	t.Helper()
	return ts.post(t, ticketRefAuditTeamsPath(ts)+query,
		map[string]string{"slug": slug, "name": strings.ToUpper(slug)}, true)
}

// ticketRefAuditTeamCount counts live teams with this slug.
func ticketRefAuditTeamCount(t *testing.T, ts *testServer, slug string) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM teams WHERE org_id = $1 AND slug = $2 AND deleted_at IS NULL`,
		ts.OrgID, slug).Scan(&n))
	return n
}

// --- A3: the reference reaches the column, one test per mutation family ---

// TestTicketRefAudit_PersonUpdate_AllThreeEventsCarryTheReference pins the
// property most likely to regress: UpdatePerson emits three separate events
// for one request (org role, display name, primary team) and every one of
// them carries the operator's single reference. A handler that resolved the
// reference inside the per-field loop, or that passed it to two of the three
// logEvent calls, fails here.
func TestTicketRefAudit_PersonUpdate_AllThreeEventsCarryTheReference(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	team, err := ts.TeamService.Create(t.Context(), ts.OrgID, nil, "tr-people", "TR People", "")
	require.NoError(t, err)

	const ref = "OPS-1"
	r := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s?ticket_ref=%s", ts.OrgID, target.ID, ref),
		map[string]any{
			"org_role":        "admin",
			"display_name":    "Renamed Person",
			"primary_team_id": team.ID.String(),
		}, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "update person: %s", r.Body)

	rows := ticketRefAuditRowsForEntity(t, ts, target.ID)
	require.Len(t, rows, 3,
		"one PATCH changing three fields must emit three events; got %d", len(rows))

	// Asserting the action names too keeps "all rows carry the reference"
	// from being vacuously true over the wrong set of rows.
	byAction := make(map[string]*string, len(rows))
	for _, row := range rows {
		byAction[row.Action] = row.Ref
	}
	for _, event := range []audit.EventType{
		audit.EventTypeUserOrgRoleChanged,
		audit.EventTypeUserProfileChanged,
		audit.EventTypeUserPrimaryTeamChanged,
	} {
		stored, ok := byAction[string(event)]
		require.True(t, ok, "no %s event was written; got %v", event, byAction)
		require.NotNil(t, stored, "%s must store the reference, not NULL", event)
		require.Equal(t, ref, *stored, "%s carries the wrong reference", event)
	}
}

// TestTicketRefAudit_PersonLifecycle_EachActionCarriesItsOwnReference walks
// the deactivate / reactivate / force-logout / remove family. Each request
// sends a DIFFERENT reference, so a handler that stamped a stale or shared
// value — not merely one that dropped it — fails too.
func TestTicketRefAudit_PersonLifecycle_EachActionCarriesItsOwnReference(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	base := fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID)

	posts := []struct {
		path  string
		ref   string
		event audit.EventType
	}{
		{base + "/deactivate", "OPS-DEACT", audit.EventTypeUserDeactivated},
		{base + "/reactivate", "OPS-REACT", audit.EventTypeUserReactivated},
		{base + "/force-logout", "OPS-FORCELOGOUT", audit.EventTypeUserForceLogout},
	}
	for _, p := range posts {
		r := ts.post(t, p.path+"?ticket_ref="+p.ref, nil, true)
		require.Equal(t, http.StatusNoContent, r.StatusCode, "%s: %s", p.path, r.Body)
	}

	r := ts.delete(t, base+"?ticket_ref=OPS-REMOVE", true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "remove from org: %s", r.Body)

	for _, p := range posts {
		ticketRefAuditRequireSole(t, ts, p.event, p.ref)
	}
	ticketRefAuditRequireSole(t, ts, audit.EventTypeUserRemovedFromOrg, "OPS-REMOVE")
}

// TestTicketRefAudit_TeamMutations_EveryEventCarriesTheReference covers the
// whole team family: create, update, member add, member remove, delete. The
// update sends a rename AND a reparent in one PATCH, which emits both
// team.updated and team.reparented — both must carry the one reference the
// operator supplied.
func TestTicketRefAudit_TeamMutations_EveryEventCarriesTheReference(t *testing.T) {
	ts := newTestServer(t)
	base := ticketRefAuditTeamsPath(ts)

	// Created through the service, not the API, so the team.created assertion
	// below concerns exactly one event: the referenced one.
	parent, err := ts.TeamService.Create(t.Context(), ts.OrgID, nil, "tr-parent", "TR Parent", "")
	require.NoError(t, err)

	r := ticketRefAuditCreateTeam(t, ts, "tr-child", "?ticket_ref=TEAM-CREATE")
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
	var child struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &child))
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamCreated, "TEAM-CREATE")

	// One PATCH, two writes, two events, one reference.
	r = ts.patch(t, fmt.Sprintf("%s/%s?ticket_ref=TEAM-UPDATE", base, child.ID),
		map[string]any{"name": "TR Child Renamed", "parent_id": parent.ID.String()}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "rename+reparent: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamUpdated, "TEAM-UPDATE")
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamReparented, "TEAM-UPDATE")

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberPath := fmt.Sprintf("%s/%s/members/%s", base, child.ID, member.ID)

	r = ts.put(t, memberPath+"?ticket_ref=TEAM-MEMBER-ADD", map[string]any{"role": "member"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "add member: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamMemberAdded, "TEAM-MEMBER-ADD")

	r = ts.delete(t, memberPath+"?ticket_ref=TEAM-MEMBER-REMOVE", true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "remove member: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamMemberRemoved, "TEAM-MEMBER-REMOVE")

	r = ts.delete(t, fmt.Sprintf("%s/%s?ticket_ref=TEAM-DELETE", base, child.ID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete team: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamDeleted, "TEAM-DELETE")
}

// TestTicketRefAudit_SpaceCreateAndVisibilityChange covers the two audited
// space governance mutations. The visibility change is also asserted to have
// actually happened, so the event under test is about a real change rather
// than a no-op that happened to log.
func TestTicketRefAudit_SpaceCreateAndVisibilityChange(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?ticket_ref=SPACE-CREATE", ts.OrgID),
		map[string]any{"name": "Ref Space", "slug": "ref-space", "type": "vector"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create space: %s", r.Body)
	var space struct {
		ID         uuid.UUID `json:"id"`
		Visibility string    `json:"visibility"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.Equal(t, access.VisibilityDiscoverable, space.Visibility)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeSpaceCreated, "SPACE-CREATE")

	// set_visibility is org-level: no space role holds it, only the org-admin
	// bypass — which the harness user has.
	r = ts.put(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s?ticket_ref=SPACE-VIS", ts.OrgID, space.ID),
		map[string]any{"name": "Ref Space", "visibility": access.VisibilityOrg}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "change visibility: %s", r.Body)

	var stored string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT visibility FROM spaces WHERE id = $1`, space.ID).Scan(&stored))
	require.Equal(t, access.VisibilityOrg, stored, "the visibility change must have been applied")
	ticketRefAuditRequireSole(t, ts, audit.EventTypeSpaceVisibilityChanged, "SPACE-VIS")
}

// TestTicketRefAudit_SpaceDeleteRecordsTheReference closes the third of the
// space mutations A3 names. Deleting a whole space used to leave less of a
// trace than changing its visibility did — the handler wrote no event at all,
// so under required mode an operator was compelled to supply a reference that
// was then discarded.
//
// The metadata assertions matter as much as the reference. The row is read
// before the soft delete precisely so the event can say what was destroyed,
// and someone reading this event later has no other way to find out.
func TestTicketRefAudit_SpaceDeleteRecordsTheReference(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID),
		map[string]any{"name": "Doomed Space", "slug": "doomed-space", "type": "vector"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create space: %s", r.Body)
	var space struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))

	r = ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s?ticket_ref=SPACE-DEL", ts.OrgID, space.ID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete space: %s", r.Body)

	// The delete really happened — otherwise the event below is about nothing.
	var deletedAt *time.Time
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT deleted_at FROM spaces WHERE id = $1`, space.ID).Scan(&deletedAt))
	require.NotNil(t, deletedAt, "the space must actually be soft-deleted")

	ticketRefAuditRequireSole(t, ts, audit.EventTypeSpaceDeleted, "SPACE-DEL")

	var payload []byte
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT payload FROM audit_log WHERE org_id = $1 AND action = $2`,
		ts.OrgID, string(audit.EventTypeSpaceDeleted)).Scan(&payload))
	var meta map[string]string
	require.NoError(t, json.Unmarshal(payload, &meta))
	require.Equal(t, "Doomed Space", meta["name"], "the event must name what was destroyed")
	require.Equal(t, "vector", meta["type"])
	require.Equal(t, access.VisibilityDiscoverable, meta["visibility"])
}

// TestTicketRefAudit_InviteLifecycle_EveryEmailCarriesTheReference covers
// create, revoke, and resend. One bulk create is one operator action: three
// emails produce three invite.created events, and all three carry the single
// reference that request supplied.
func TestTicketRefAudit_InviteLifecycle_EveryEmailCarriesTheReference(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID)

	r := ts.post(t, base+"?ticket_ref=INV-CREATE", map[string]any{
		"emails":   []string{"one@example.com", "two@example.com", "three@example.com"},
		"org_role": "member",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create invites: %s", r.Body)
	var outcomes []inviteOutcome
	require.NoError(t, json.Unmarshal(r.Body, &outcomes))
	require.Len(t, outcomes, 3)
	for _, o := range outcomes {
		require.Equal(t, "created", o.Status, "seed invite %s: %s", o.Email, o.Status)
	}

	created := ticketRefAuditRowsFor(t, ts, audit.EventTypeInviteCreated)
	require.Len(t, created, 3, "one invite.created event per email")
	for i, row := range created {
		require.NotNil(t, row.Ref, "invite.created[%d] must store the reference, not NULL", i)
		require.Equal(t, "INV-CREATE", *row.Ref, "invite.created[%d] carries the wrong reference", i)
	}

	r = ts.delete(t, fmt.Sprintf("%s/%s?ticket_ref=INV-REVOKE", base, outcomes[0].Invite.ID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke invite: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeInviteRevoked, "INV-REVOKE")

	r = ts.post(t, fmt.Sprintf("%s/%s/resend?ticket_ref=INV-RESEND", base, outcomes[1].Invite.ID), nil, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "resend invite: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeInviteResent, "INV-RESEND")
}

// --- A3: the reference's own rules ---

// TestTicketRefAudit_AbsentReferenceStoresNull is the default posture: no
// ticket_ref at all is accepted, and the column is SQL NULL rather than the
// empty string. The "" → nil conversion in audit.dbLogger is exactly what
// this pins — delete it and the column stores an empty string instead,
// failing both the NULL check below and the org-wide empty-string count.
func TestTicketRefAudit_AbsentReferenceStoresNull(t *testing.T) {
	ts := newTestServer(t)

	r := ticketRefAuditCreateTeam(t, ts, "tr-unreferenced", "")
	require.Equal(t, http.StatusCreated, r.StatusCode, "unreferenced team create: %s", r.Body)

	rows := ticketRefAuditRowsFor(t, ts, audit.EventTypeTeamCreated)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].Ref,
		"an absent reference must store SQL NULL, not the empty string (got %q)", derefOrEmpty(rows[0].Ref))

	// A second handler, same posture — the rule is the logger's, not one
	// handler's.
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	pr := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]any{"org_role": "admin"}, true)
	require.Equal(t, http.StatusNoContent, pr.StatusCode, "unreferenced person update: %s", pr.Body)

	roleRows := ticketRefAuditRowsFor(t, ts, audit.EventTypeUserOrgRoleChanged)
	require.Len(t, roleRows, 1)
	require.Nil(t, roleRows[0].Ref, "an absent reference must store SQL NULL here too")

	// Nothing in the org stored an empty string on any row.
	var empties int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_log WHERE org_id = $1 AND ticket_ref = ''`, ts.OrgID).Scan(&empties))
	require.Zero(t, empties, "the empty string is never stored — an absent reference is NULL")
}

// derefOrEmpty renders a nullable reference for failure messages.
func derefOrEmpty(s *string) string {
	if s == nil {
		return "<NULL>"
	}
	return *s
}

// TestTicketRefAudit_WhitespaceIsTrimmed pins ticketref.FromRequest's trim:
// a reference pasted with surrounding spaces is stored without them, so
// "OPS-9" and "  OPS-9  " are one reference in the audit log, not two.
func TestTicketRefAudit_WhitespaceIsTrimmed(t *testing.T) {
	ts := newTestServer(t)

	r := ticketRefAuditCreateTeam(t, ts, "tr-trimmed", "?ticket_ref=%20%20OPS-9%20%20")
	require.Equal(t, http.StatusCreated, r.StatusCode, "create with padded reference: %s", r.Body)

	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamCreated, "OPS-9")
}

// TestTicketRefAudit_LengthBoundaryAt200 pins the cap exactly: 200
// characters is accepted and stored whole, 201 is a 400 and nothing is
// written. The literals are deliberate — a test written against
// ticketref.MaxLen would keep passing if the constant moved.
func TestTicketRefAudit_LengthBoundaryAt200(t *testing.T) {
	ts := newTestServer(t)
	require.Equal(t, 200, ticketref.MaxLen, "the documented cap is 200 characters")

	over := strings.Repeat("B", 201)
	r := ticketRefAuditCreateTeam(t, ts, "tr-overlong", "?ticket_ref="+over)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditTooLongMessage)
	require.Zero(t, ticketRefAuditTeamCount(t, ts, "tr-overlong"),
		"a rejected over-length reference must leave no team behind")
	require.Empty(t, ticketRefAuditRowsFor(t, ts, audit.EventTypeTeamCreated),
		"a rejected over-length reference must leave no audit row behind")

	at := strings.Repeat("A", 200)
	r = ticketRefAuditCreateTeam(t, ts, "tr-atlimit", "?ticket_ref="+at)
	require.Equal(t, http.StatusCreated, r.StatusCode, "200 characters must be accepted: %s", r.Body)

	rows := ticketRefAuditRowsFor(t, ts, audit.EventTypeTeamCreated)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Ref)
	require.Equal(t, at, *rows[0].Ref, "the reference is stored whole, never truncated")
	require.Len(t, *rows[0].Ref, 200)
}

// TestTicketRefAudit_ReferenceSurvivesDeletionOfWhatItNames is the locked
// design decision (migration 025): the reference is free text with no
// foreign key, and the audit log is self-contained.
//
// The counterpart for bulk grants is
// TestBulkGrants_TicketRefSurvivesTicketDeletion; this is the same property
// for one of the newly extended mutations. The row is deleted outright, not
// only through the API's soft delete — an ON DELETE CASCADE would take the
// audit row with it, an ON DELETE SET NULL would blank the reference, and a
// RESTRICT would make the DELETE fail. All three would fail this test.
func TestTicketRefAudit_ReferenceSurvivesDeletionOfWhatItNames(t *testing.T) {
	ts := newTestServer(t)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")

	tr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, space.ID),
		map[string]string{"title": "Reorg approval", "priority": "low"}, true)
	require.Equal(t, http.StatusCreated, tr.StatusCode, "seed ticket: %s", tr.Body)
	var ticket struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(tr.Body, &ticket))
	ref := "ticket:" + ticket.ID.String()

	r := ticketRefAuditCreateTeam(t, ts, "tr-survives", "?ticket_ref="+ref)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamCreated, ref)

	dr := ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s", ts.OrgID, space.ID, ticket.ID), true)
	require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, dr.StatusCode, "delete ticket: %s", dr.Body)

	tag, err := ts.DB.Pool.Exec(t.Context(), `DELETE FROM tickets WHERE id = $1`, ticket.ID)
	require.NoError(t, err, "hard-deleting the referenced ticket must not be blocked by a foreign key")
	require.EqualValues(t, 1, tag.RowsAffected())

	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamCreated, ref)

	// And the viewer still renders it: the reference is a string the audit
	// surface owns, not a lookup that now dangles.
	ar := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log", ts.OrgID), true)
	require.Equal(t, http.StatusOK, ar.StatusCode, "audit viewer: %s", ar.Body)
	require.Contains(t, string(ar.Body), ref,
		"the audit viewer must still show the reference to the deleted ticket")
}

// --- A2: required mode ---

// newTicketRefRequiredServer builds a server whose six reference-accepting
// handlers run under ticketref.Policy{Required: true} — the boot-time posture
// AZIMUTHAL_TICKET_REF_REQUIRED selects in cmd/server/main.go.
//
// newTestServer builds them with the permissive zero value, which the whole
// A3 suite depends on, so the required posture needs its own construction. It
// is the smallest wiring that still runs the real handlers, the real router
// and the real database: the six handlers under test plus the authenticator
// and access resolver they sit behind.
//
// Handlers left nil are simply unmounted (the router registers only method
// values for them), and that is a trap rather than a convenience: a mutation
// family missing from here is not merely untested under the required policy,
// its routes 404, so a test written against it fails on the status rather
// than reporting the gap. That is exactly how grants and shares went
// uncovered until B3. TestHarness_EveryTicketRefHandlerIsUnderTheRequiredPolicy
// now fails by name when a handler that accepts a reference is missing here.
//
// TicketHandler is mounted despite accepting no reference of its own: a share
// needs a real entity to point at, and creating one through the API exercises
// the space resolution shares.LookupEntity performs. Inserting a row with raw
// SQL instead would couple these tests to the tickets schema and skip it.
func newTicketRefRequiredServer(t *testing.T) *testServer {
	t.Helper()
	db := testutil.NewTestDB(t)
	pool := db.Pool
	org := testutil.CreateTestOrg(t, pool)
	user := testutil.CreateTestUser(t, pool, org.ID)
	queries := generated.New(pool)

	// One key per test binary, not one per server — see testSigningKey.
	privateKey := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  24 * time.Hour,
		RefreshTTL: 7 * 24 * time.Hour,
		Issuer:     "azimuthal-test",
	})

	userAdapter := adapters.NewUserAdapter(pool, org.ID)
	sessionSvc := auth.NewSessionService(adapters.NewSessionAdapter(queries), auth.SessionConfig{TTL: 24 * time.Hour})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc, userAdapter)

	accessAdapter := adapters.NewAccessAdapter(pool)
	shareAdapter := adapters.NewShareAdapter(pool)
	accessResolver := access.NewResolver(accessAdapter).WithShareStore(shareAdapter)
	grantSvc := access.NewGrantService(accessAdapter)
	explainer := access.NewExplainer(accessAdapter, accessAdapter)
	shareSvc := access.NewShareService(shareAdapter)
	teamSvc := coreteams.NewService(adapters.NewTeamAdapter(pool))

	// The share routes need a readable entity and a reader that can project
	// it, so the three module services come along. contentTx carries the
	// ADR-0008 revoke-on-delete/move invariants, exactly as production wires
	// them.
	contentTx := adapters.NewContentTxAdapter(pool)
	ticketSvc := tickets.NewTicketService(adapters.NewTicketAdapter(queries), contentTx)
	itemSvc := projects.NewItemService(adapters.NewItemAdapter(queries), contentTx)
	wikiSvc := wiki.NewService(queries, contentTx)
	peopleSvc := people.NewService(adapters.NewPeopleAdapter(pool))
	inviteSvc := invites.NewService(adapters.NewInviteAdapter(pool), nil, invites.Config{
		TTL:     7 * 24 * time.Hour,
		BaseURL: "http://localhost:8082",
	})
	auditLog := audit.NewDBLogger(queries)

	// The one value every handler shares — the same shape main.go builds.
	required := ticketref.Policy{Required: true}

	// The ADR-0011 tier gate, for the reason the TicketHandler comment below
	// gives: this builder mirrors newTestServerOn's collaborators, and
	// TestHarness_NoDarkDependencies cannot see this one because it walks
	// newTestServer only. A nil gate here would make every status transition in
	// these tests answer 500 rather than transitioning ungated.
	refTierStore := adapters.NewWorkflowTierAdapter(queries)
	refTierGate := tiergate.New(workflow.NewTierService(refTierStore), refTierStore, jobs.NoopNotificationEnqueuer{})
	refTransitionTx := adapters.NewWorkflowTransitionTxAdapter(pool)

	cfg := api.RouterConfig{
		Authenticator: authenticator,
		SpaceHandler: spacesapi.NewHandler(queries).
			WithTeamService(teamSvc).
			WithGrantService(grantSvc).
			WithSpaceCreateTx(adapters.NewSpaceCreateAdapter(pool)).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		TeamHandler: teamsapi.NewHandler(teamSvc).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		AdminHandler: adminapi.NewHandler(peopleSvc,
			access.NewBulkService(adapters.NewBulkGrantAdapter(pool)),
			audit.NewReader(adapters.NewAuditReaderAdapter(queries))).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		InviteHandler: invitesapi.NewHandler(inviteSvc, jwtSvc).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		GrantHandler: grantsapi.NewHandler(grantSvc, explainer).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		ShareHandler: sharesapi.NewHandler(shareSvc, sharesapi.NewServiceReader(wikiSvc, ticketSvc, itemSvc)).
			WithAuditLogger(auditLog).
			WithTicketRefPolicy(required),
		// Accepts no reference of its own — here so the share tests have a
		// real entity to share. See the doc comment above. It carries every
		// collaborator newTestServerOn gives it: mounting the handler mounts
		// the whole ticket subtree, and a nil one here would be a dark
		// dependency TestHarness_NoDarkDependencies cannot see, because that
		// test walks newTestServer only.
		TicketHandler: ticketsapi.NewHandler(ticketSvc).
			WithAuditLogger(auditLog).
			WithNotificationEnqueuer(jobs.NoopNotificationEnqueuer{}).
			WithSuggestions(tickets.NewSuggestionService(adapters.NewTicketAdapter(queries))).
			WithWorkflowTiers(refTierGate, refTransitionTx),
		SpaceOrgResolver: func(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error) {
			s, err := queries.GetSpaceByID(ctx, spaceID)
			if err != nil {
				return uuid.Nil, fmt.Errorf("resolving space org: %w", err)
			}
			return s.OrgID, nil
		},
		AccessResolver: accessResolver,
	}
	router := api.NewRouter(cfg)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, org.ID.String(), "member", 0)
	require.NoError(t, err)

	return &testServer{
		Server: srv, Handler: router, DB: db, OrgID: org.ID, UserID: user.ID,
		Token: pair.AccessToken, JWT: jwtSvc, TeamService: teamSvc,
		GrantService: grantSvc, RouterCfg: cfg, AuditLog: auditLog,
	}
}

// TestTicketRefRequired_MissingReference_RejectsAndWritesNothing is the
// reason the requirement exists. Every one of the four handlers must refuse
// a reference-less mutation with 400 AND leave the world untouched: the 400
// is worthless if the write already happened, and only re-reading the
// resource catches that.
func TestTicketRefRequired_MissingReference_RejectsAndWritesNothing(t *testing.T) {
	ts := newTicketRefRequiredServer(t)

	// Teams.
	r := ticketRefAuditCreateTeam(t, ts, "req-team", "")
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	require.Zero(t, ticketRefAuditTeamCount(t, ts, "req-team"), "no team may exist after the rejection")

	// People — the membership role must be exactly what it was.
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]any{"org_role": "admin"}, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	var role string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, ts.OrgID, target.ID).Scan(&role))
	require.Equal(t, "member", role, "the role change must not have been applied")

	// Spaces.
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID),
		map[string]any{"name": "Req Space", "slug": "req-space", "type": "vector"}, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	var spaces int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM spaces WHERE org_id = $1`, ts.OrgID).Scan(&spaces))
	require.Zero(t, spaces, "no space may exist after the rejection")

	// Invites.
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID),
		map[string]any{"emails": []string{"nope@example.com"}, "org_role": "member"}, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	var invited int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM invites WHERE org_id = $1`, ts.OrgID).Scan(&invited))
	require.Zero(t, invited, "no invite may exist after the rejection")

	// Nothing was audited either: four refusals, zero rows.
	require.Zero(t, ticketRefAuditOrgRowCount(t, ts),
		"a rejected mutation must write no audit row at all")
}

// TestTicketRefRequired_WithReference_Succeeds is the other half: under the
// same required policy, a request that carries a reference behaves exactly
// as it always did and the reference lands in the column.
func TestTicketRefRequired_WithReference_Succeeds(t *testing.T) {
	ts := newTicketRefRequiredServer(t)

	r := ticketRefAuditCreateTeam(t, ts, "req-ok-team", "?ticket_ref=CHG-77")
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team with reference: %s", r.Body)
	require.Equal(t, 1, ticketRefAuditTeamCount(t, ts, "req-ok-team"))
	ticketRefAuditRequireSole(t, ts, audit.EventTypeTeamCreated, "CHG-77")

	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	pr := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s?ticket_ref=CHG-78", ts.OrgID, target.ID),
		map[string]any{"org_role": "admin"}, true)
	require.Equal(t, http.StatusNoContent, pr.StatusCode, "role change with reference: %s", pr.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeUserOrgRoleChanged, "CHG-78")

	sr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?ticket_ref=CHG-79", ts.OrgID),
		map[string]any{"name": "Req Space", "slug": "req-space", "type": "vector"}, true)
	require.Equal(t, http.StatusCreated, sr.StatusCode, "create space with reference: %s", sr.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeSpaceCreated, "CHG-79")

	ir := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites?ticket_ref=CHG-80", ts.OrgID),
		map[string]any{"emails": []string{"yes@example.com"}, "org_role": "member"}, true)
	require.Equal(t, http.StatusCreated, ir.StatusCode, "create invite with reference: %s", ir.Body)
	ticketRefAuditRequireSole(t, ts, audit.EventTypeInviteCreated, "CHG-80")
}

// TestTicketRefAudit_DefaultPolicy_MissingReferenceStillSucceeds is the
// behaviour-unchanged guarantee. On the default server — the permissive zero
// value, which is what a deployment that never set the flag runs — every one
// of the four families still accepts a mutation with no reference at all.
func TestTicketRefAudit_DefaultPolicy_MissingReferenceStillSucceeds(t *testing.T) {
	ts := newTestServer(t)

	r := ticketRefAuditCreateTeam(t, ts, "default-team", "")
	require.Equal(t, http.StatusCreated, r.StatusCode, "team create without reference: %s", r.Body)

	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	pr := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]any{"org_role": "admin"}, true)
	require.Equal(t, http.StatusNoContent, pr.StatusCode, "role change without reference: %s", pr.Body)

	sr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID),
		map[string]any{"name": "Default Space", "slug": "default-space", "type": "vector"}, true)
	require.Equal(t, http.StatusCreated, sr.StatusCode, "space create without reference: %s", sr.Body)

	ir := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID),
		map[string]any{"emails": []string{"default@example.com"}, "org_role": "member"}, true)
	require.Equal(t, http.StatusCreated, ir.StatusCode, "invite create without reference: %s", ir.Body)

	// All four landed, and none of them invented a reference.
	for _, event := range []audit.EventType{
		audit.EventTypeTeamCreated,
		audit.EventTypeUserOrgRoleChanged,
		audit.EventTypeSpaceCreated,
		audit.EventTypeInviteCreated,
	} {
		rows := ticketRefAuditRowsFor(t, ts, event)
		require.Len(t, rows, 1, "%s must have been written", event)
		require.Nil(t, rows[0].Ref, "%s must store NULL when no reference was sent", event)
	}
}

// TestTicketRefAudit_MalformedReferenceIsRefusedNotSilentlyDropped closes a
// hole that turns the audit guarantee inside out.
//
// A query parameter carries raw percent-decoded bytes, so ?ticket_ref=%FF
// arrives as an ordinary non-empty Go string: it passes a length check, and it
// even satisfies required mode. It only fails at the audit INSERT, because
// audit_log.ticket_ref is `text` and PostgreSQL rejects invalid UTF-8. By then
// the mutation has committed, and audit.Logger's contract is to swallow the
// error rather than interrupt the request — so the administrative change lands
// with no audit row at all. One query parameter, and the record disappears.
//
// The fix rejects it up front, which is why this test asserts the 400 AND that
// the mutation did not happen. Asserting only the status code would pass even
// if the request were refused after the write.
func TestTicketRefAudit_MalformedReferenceIsRefusedNotSilentlyDropped(t *testing.T) {
	ts := newTestServer(t)

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"invalid utf-8", "%FF"},
		{"nul byte", "%00"},
		{"invalid utf-8 inside an otherwise sane reference", "OPS-%FF-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ticketRefAuditOrgRowCount(t, ts)

			slug := fmt.Sprintf("malformed-%s", strings.ReplaceAll(tc.name, " ", "-"))
			r := ticketRefAuditCreateTeam(t, ts, slug, "?ticket_ref="+tc.raw)
			require.Equal(t, http.StatusBadRequest, r.StatusCode,
				"a malformed ticket_ref must be refused up front: %s", r.Body)

			// The team must not exist. This is the assertion that distinguishes
			// "refused" from "committed, then the audit row was lost".
			var teams int
			require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
				`SELECT count(*) FROM teams WHERE org_id = $1 AND slug = $2`, ts.OrgID, slug).Scan(&teams))
			require.Zero(t, teams, "the mutation must not have committed")

			require.Equal(t, before, ticketRefAuditOrgRowCount(t, ts),
				"no audit row should have been written either")
		})
	}
}
