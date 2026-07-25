package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// A4 — create-time visibility requires set_visibility.
//
// POST /orgs/{orgID}/spaces defaults visibility to discoverable; an EFFECTIVE
// visibility other than that default additionally requires set_visibility,
// which lives outside minRoleFor and is therefore held by no space role at all
// — only by the org-admin bypass, through access.CanOrgWide.
//
// The subject matters more than the assertion here. Space creation authority
// is "org admin, or a lead of the owning team" (canCreateSpace), checked
// BEFORE visibility. A viewer, a plain member, or a stranger is refused by
// that check long before the visibility gate is reached, so a denial from any
// of them passes with the entire set_visibility block deleted and proves
// nothing. The only subject that isolates this gate is a TEAM LEAD: past
// creation authority, short of set_visibility. Every denial below is a lead.

// spaceCreateVisEnv is an org with a squad team and a lead of that squad — the
// one persona that isolates the create-time visibility gate.
type spaceCreateVisEnv struct {
	ts      *testServer
	teamID  uuid.UUID
	lead    testutil.User
	leadTok string
}

func spaceCreateVisNewEnv(t *testing.T) *spaceCreateVisEnv {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()

	squad, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "vis-squad", "Vis Squad", "")
	require.NoError(t, err)

	// A plain org member (not an org admin — no bypass, so no set_visibility)
	// who leads the team that will own the spaces. IsLead() is what
	// canCreateSpace consults.
	lead := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err = ts.TeamService.AddMember(ctx, squad.ID, lead.ID, ts.OrgID, "lead")
	require.NoError(t, err)

	return &spaceCreateVisEnv{
		ts: ts, teamID: squad.ID, lead: lead,
		leadTok: ts.tokenFor(t, lead.ID, lead.Email),
	}
}

// leadBody builds a create payload owned by the squad the lead leads, so
// canCreateSpace passes and the visibility gate is the only thing left to
// decide the outcome. extra keys are merged in; omitting "visibility"
// produces a body with no such field at all.
func (e *spaceCreateVisEnv) leadBody(slug string, extra map[string]string) map[string]string {
	body := map[string]string{
		"name":          "Vis " + slug,
		"slug":          slug,
		"type":          "vector",
		"owner_team_id": e.teamID.String(),
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// spaceCreateVisPostAs POSTs the space-create route as an arbitrary persona.
// ts.post always authenticates as the harness org admin — precisely the
// subject the denial cases must not use.
func spaceCreateVisPostAs(t *testing.T, ts *testServer, token string, body any) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.url(fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)), jsonReader(t, body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// spaceCreateVisDecode pulls the id and visibility out of a 201 body.
func spaceCreateVisDecode(t *testing.T, r httpResult) (id, visibility string) {
	t.Helper()
	var space struct {
		ID         string `json:"id"`
		Visibility string `json:"visibility"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space), "space body expected: %s", r.Body)
	require.NotEmpty(t, space.ID)
	return space.ID, space.Visibility
}

// spaceCreateVisStoredVisibility reads the persisted visibility — the response
// body is what the handler said, this is what the database holds.
func spaceCreateVisStoredVisibility(t *testing.T, ts *testServer, spaceID string) string {
	t.Helper()
	var v string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT visibility FROM spaces WHERE id = $1`, uuid.MustParse(spaceID)).Scan(&v))
	return v
}

// spaceCreateVisSpacesWithSlug counts space rows carrying a slug, so a refusal
// can be shown to have written nothing rather than merely returned an error.
func spaceCreateVisSpacesWithSlug(t *testing.T, ts *testServer, slug string) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM spaces WHERE org_id = $1 AND slug = $2`, ts.OrgID, slug).Scan(&n))
	return n
}

// spaceCreateVisErrorMessage returns the message from an error envelope.
func spaceCreateVisErrorMessage(t *testing.T, r httpResult) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "error envelope expected: %s", r.Body)
	return body.Error.Message
}

// TestSpaceCreateVisibility_TeamLeadCannotChooseNonDefault is the gate's
// fails-before test: a lead of the owning team may create spaces, but may not
// choose their initial visibility. Any non-default value is refused, not just
// "org".
func TestSpaceCreateVisibility_TeamLeadCannotChooseNonDefault(t *testing.T) {
	e := spaceCreateVisNewEnv(t)

	// Persona validation, first and non-optional: if the lead cannot create a
	// DEFAULT-visibility space then they are being refused by canCreateSpace
	// and every denial below would be measuring the wrong check.
	r := spaceCreateVisPostAs(t, e.ts, e.leadTok, e.leadBody("vis-lead-baseline", nil))
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"the lead persona must pass creation authority, or the denials below prove nothing: %s", r.Body)

	for _, visibility := range []string{access.VisibilityOrg, access.VisibilityHidden} {
		t.Run(visibility, func(t *testing.T) {
			slug := "vis-lead-" + visibility
			r := spaceCreateVisPostAs(t, e.ts, e.leadTok,
				e.leadBody(slug, map[string]string{"visibility": visibility}))

			requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")
			// Naming the capability does double duty: an operator can act on
			// it, and it proves the refusal came from THIS gate rather than
			// from the creation-authority check that precedes it.
			require.Contains(t, spaceCreateVisErrorMessage(t, r), "set_visibility",
				"the 403 must name the missing capability")
			require.Zero(t, spaceCreateVisSpacesWithSlug(t, e.ts, slug),
				"a refused create must leave no space row behind")
		})
	}

	// Ordering, pinned: a caller who may not create a space at all is told
	// that, not told about visibility. This is also why a "member is refused"
	// test says nothing about the visibility gate — the member never reaches it.
	member := testutil.CreateTestUserWithRole(t, e.ts.DB.Pool, e.ts.OrgID, "member")
	_, err := e.ts.TeamService.AddMember(context.Background(), e.teamID, member.ID, e.ts.OrgID, "member")
	require.NoError(t, err)
	r = spaceCreateVisPostAs(t, e.ts, e.ts.tokenFor(t, member.ID, member.Email),
		e.leadBody("vis-member-org", map[string]string{"visibility": access.VisibilityOrg}))
	requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")
	require.NotContains(t, spaceCreateVisErrorMessage(t, r), "set_visibility",
		"a non-creator is refused by creation authority first; the visibility gate is never reached")
}

// TestSpaceCreateVisibility_OmittedAndExplicitDefaultAreIndistinguishable:
// the gate compares the EFFECTIVE visibility, so sending "discoverable"
// explicitly is not a visibility decision. A gate written against "was the
// field present in the request" instead would 403 on the second create here.
func TestSpaceCreateVisibility_OmittedAndExplicitDefaultAreIndistinguishable(t *testing.T) {
	e := spaceCreateVisNewEnv(t)

	// No "visibility" key in the payload at all.
	r := spaceCreateVisPostAs(t, e.ts, e.leadTok, e.leadBody("vis-omitted", nil))
	require.Equal(t, http.StatusCreated, r.StatusCode, "omitted visibility: %s", r.Body)
	omittedID, visibility := spaceCreateVisDecode(t, r)
	require.Equal(t, access.VisibilityDiscoverable, visibility, "the default is discoverable")
	require.Equal(t, access.VisibilityDiscoverable, spaceCreateVisStoredVisibility(t, e.ts, omittedID),
		"and it is the value actually stored")

	// The same value, sent explicitly.
	r = spaceCreateVisPostAs(t, e.ts, e.leadTok,
		e.leadBody("vis-explicit-default", map[string]string{"visibility": access.VisibilityDiscoverable}))
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"explicitly asking for the default is not a visibility decision: %s", r.Body)
	explicitID, visibility := spaceCreateVisDecode(t, r)
	require.Equal(t, access.VisibilityDiscoverable, visibility)
	require.Equal(t, access.VisibilityDiscoverable, spaceCreateVisStoredVisibility(t, e.ts, explicitID))
}

// TestSpaceCreateVisibility_OrgAdminChoosesNonDefaultAndTakesNoGrant: the
// org-admin path is unchanged. The bypass grants set_visibility org-wide, so
// a non-default initial visibility is accepted and persisted — and, per
// ADR-0007, the admin still holds zero grant rows: the handler auto-grants
// only a non-admin creator, so the new space carries no grant at all.
func TestSpaceCreateVisibility_OrgAdminChoosesNonDefaultAndTakesNoGrant(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Admin Org Space", "slug": "vis-admin-org", "type": "vector",
		"visibility": access.VisibilityOrg,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"an org admin holds set_visibility through the bypass: %s", r.Body)
	spaceID, visibility := spaceCreateVisDecode(t, r)
	require.Equal(t, access.VisibilityOrg, visibility)
	require.Equal(t, access.VisibilityOrg, spaceCreateVisStoredVisibility(t, ts, spaceID),
		"the requested visibility must be the one stored, not the default")

	var grants int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM space_grants WHERE space_id = $1`, uuid.MustParse(spaceID)).Scan(&grants))
	require.Zero(t, grants,
		"org admins reach spaces through the middleware bypass and hold zero grant rows (ADR-0007)")
}

// TestSpaceCreateVisibility_AuditRecordsInitialVisibilityOnEveryCreate: every
// successful create writes space.created carrying the visibility it received —
// the default case included, so "created discoverable and never changed" is a
// recorded fact rather than an absence in the log.
func TestSpaceCreateVisibility_AuditRecordsInitialVisibilityOnEveryCreate(t *testing.T) {
	e := spaceCreateVisNewEnv(t)
	ts := e.ts

	// Non-default create, by the org admin.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Audited Hidden", "slug": "vis-audit-hidden", "type": "codex",
		"visibility": access.VisibilityHidden,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "admin create: %s", r.Body)
	adminSpaceID, _ := spaceCreateVisDecode(t, r)

	// Default create, by the team lead — the case an "audit it only when it is
	// interesting" implementation would silently skip.
	r = spaceCreateVisPostAs(t, ts, e.leadTok, e.leadBody("vis-audit-default", nil))
	require.Equal(t, http.StatusCreated, r.StatusCode, "lead create: %s", r.Body)
	leadSpaceID, _ := spaceCreateVisDecode(t, r)

	rows := auditRowsFor(t, ts, "space.created")
	byEntity := make(map[uuid.UUID]auditRow, len(rows))
	for _, row := range rows {
		byEntity[row.EntityID] = row
	}

	adminEvent, ok := byEntity[uuid.MustParse(adminSpaceID)]
	require.True(t, ok, "the non-default create must be audited; got %d space.created rows", len(rows))
	require.Equal(t, "space", adminEvent.EntityKind)
	require.Equal(t, access.VisibilityHidden, adminEvent.Payload["visibility"],
		"the event records the visibility the space was created with")
	require.NotNil(t, adminEvent.ActorID)
	require.Equal(t, ts.UserID, *adminEvent.ActorID)

	leadEvent, ok := byEntity[uuid.MustParse(leadSpaceID)]
	require.True(t, ok,
		"the DEFAULT create must be audited too; got %d space.created rows", len(rows))
	require.Equal(t, "space", leadEvent.EntityKind)
	require.Equal(t, access.VisibilityDiscoverable, leadEvent.Payload["visibility"])
	require.NotNil(t, leadEvent.ActorID)
	require.Equal(t, e.lead.ID, *leadEvent.ActorID, "the lead is the actor on their own create")
}

// TestSpaceCreateVisibility_InvalidValueStillRejected: an unrecognised
// visibility is still a 400, unchanged by the new gate — and it is a 400 even
// for the caller who holds set_visibility, because the value is validated
// before authority is considered. Nothing is written.
func TestSpaceCreateVisibility_InvalidValueStillRejected(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Bad Vis", "slug": "vis-invalid", "type": "vector",
		"visibility": "sorta-public",
	}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
	require.Zero(t, spaceCreateVisSpacesWithSlug(t, ts, "vis-invalid"),
		"a rejected visibility value must write no space row")
}

// TestSpaceCreate_S10AutoSpaceShapeUnaffectedByVisibilityGate is the S10 blast
// radius check. S10's team auto-space creation depends on this: the frontend's
// useCreateTeamWithSpaces (web/src/lib/api.ts) creates a team and then, once
// per selected module, POSTs {name, slug, type, owner_team_id} to /spaces with
// NO visibility field, followed by a team contributor grant on the result.
// That request never asks for a non-default visibility, so the create-time
// set_visibility gate must never see it. If this test goes red, creating a
// team with modules is broken in the product — not merely in a permission
// edge case.
func TestSpaceCreate_S10AutoSpaceShapeUnaffectedByVisibilityGate(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	// Step 1 of the flow: create the team.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/teams/", ts.OrgID),
		map[string]string{"slug": "platform", "name": "Platform"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
	var team struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &team), "team body: %s", r.Body)

	// Step 2, once per selected module: exactly the frontend's four-key
	// payload, then the grant that follows it.
	for _, module := range []string{"beacon", "codex", "vector"} {
		r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
			"name":          team.Name,
			"slug":          team.Slug,
			"type":          module,
			"owner_team_id": team.ID,
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode,
			"S10 auto-space create for module %s must still succeed: %s", module, r.Body)

		spaceID, visibility := spaceCreateVisDecode(t, r)
		require.Equal(t, access.VisibilityDiscoverable, visibility,
			"an auto-created space takes the default visibility")
		require.Equal(t, access.VisibilityDiscoverable, spaceCreateVisStoredVisibility(t, ts, spaceID))

		r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants/", ts.OrgID, spaceID), map[string]string{
			"subject_type": "team", "subject_id": team.ID, "role": "contributor",
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode,
			"the rest of the S10 sequence must still complete for module %s: %s", module, r.Body)
	}

	// The same shape from a lead of the owning team, who is NOT an org admin
	// and therefore holds no set_visibility — the caller the gate would break
	// if it keyed off the field's presence or demanded the capability
	// unconditionally.
	lead := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.TeamService.AddMember(ctx, uuid.MustParse(team.ID), lead.ID, ts.OrgID, "lead")
	require.NoError(t, err)

	r = spaceCreateVisPostAs(t, ts, ts.tokenFor(t, lead.ID, lead.Email), map[string]string{
		"name":          team.Name,
		"slug":          team.Slug + "-lead",
		"type":          "vector",
		"owner_team_id": team.ID,
	})
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"a non-admin lead must still be able to auto-create module spaces: %s", r.Body)
	leadSpaceID, visibility := spaceCreateVisDecode(t, r)
	require.Equal(t, access.VisibilityDiscoverable, visibility)
	require.Equal(t, access.VisibilityDiscoverable, spaceCreateVisStoredVisibility(t, ts, leadSpaceID))
}
