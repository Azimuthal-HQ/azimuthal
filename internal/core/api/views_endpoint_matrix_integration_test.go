package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The per-endpoint matrix for saved views (spec §2.6).
//
// These exist because the route-accounting sweep does NOT prove them.
// TestReadPathSweep_GuardClassMatchesMiddleware compares the claimed class
// against the real middleware chain for the two ADMIN classes only; a row
// claiming "org-member" is accepted on the strength of the row alone. So the
// org-member claim on the seven /views routes, and every ownership and
// visibility rule above it, is asserted here against a live server or it is
// not asserted at all.

const beaconViewQuery = `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`

func viewsPath(orgID uuid.UUID) string {
	return "/api/v1/orgs/" + orgID.String() + "/views"
}

// createViewAs posts a view and returns its id.
func createViewAs(t *testing.T, ts *testServer, token string, orgID uuid.UUID, name, visibility string, teamID *uuid.UUID) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"name":       name,
		"query":      json.RawMessage(beaconViewQuery),
		"visibility": visibility,
	}
	if teamID != nil {
		body["visibility_team_id"] = teamID.String()
	}
	res := ts.postAs(t, token, viewsPath(orgID), body)
	require.Equal(t, http.StatusCreated, res.StatusCode, "create view: %s", res.Body)
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	return out.ID
}

func listViewIDs(t *testing.T, ts *testServer, token string, orgID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	res := ts.getAs(t, token, viewsPath(orgID))
	require.Equal(t, http.StatusOK, res.StatusCode, "list views: %s", res.Body)
	var out struct {
		Views []struct {
			ID uuid.UUID `json:"id"`
		} `json:"views"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	got := map[uuid.UUID]bool{}
	for _, v := range out.Views {
		got[v.ID] = true
	}
	return got
}

// TestViewsMatrix_UnauthenticatedAndNonMember pins the org-member guard class
// that the accounting table claims and the sweep does not check.
func TestViewsMatrix_UnauthenticatedAndNonMember(t *testing.T) {
	ts := newTestServer(t)

	t.Run("unauthenticated is 401", func(t *testing.T) {
		res := ts.get(t, viewsPath(ts.OrgID), false)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("member of another org is 404", func(t *testing.T) {
		// Not 403: the org-member guard 404s so the surface does not confirm
		// that this organisation exists to somebody outside it.
		other := testutil.CreateTestOrg(t, ts.DB.Pool)
		outsider := testutil.CreateTestUser(t, ts.DB.Pool, other.ID)
		token := ts.tokenFor(t, outsider.ID, outsider.Email)

		res := ts.getAs(t, token, viewsPath(ts.OrgID))
		require.Equal(t, http.StatusNotFound, res.StatusCode,
			"a non-member must not learn whether this org has saved views")
	})
}

// TestViewsMatrix_PrivateIsPrivate is the default the whole feature rests on.
func TestViewsMatrix_PrivateIsPrivate(t *testing.T) {
	ts := newTestServer(t)
	other := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	otherToken := ts.tokenFor(t, other.ID, other.Email)

	id := createViewAs(t, ts, ts.Token, ts.OrgID, "My private view", "private", nil)

	require.True(t, listViewIDs(t, ts, ts.Token, ts.OrgID)[id], "the owner must see their own view")
	require.False(t, listViewIDs(t, ts, otherToken, ts.OrgID)[id],
		"a private view must not appear in another member's list")

	res := ts.getAs(t, otherToken, viewsPath(ts.OrgID)+"/"+id.String())
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"a private view must be indistinguishable from one that does not exist")
}

// TestViewsMatrix_SharingWidensToOrgAndTeam covers both audiences, including
// the ADR-0007 expansion on the team one.
func TestViewsMatrix_SharingWidensToOrgAndTeam(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberToken := ts.tokenFor(t, member.ID, member.Email)
	stranger := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	strangerToken := ts.tokenFor(t, stranger.ID, stranger.Email)

	t.Run("org audience reaches every member", func(t *testing.T) {
		id := createViewAs(t, ts, ts.Token, ts.OrgID, "Org view", "org", nil)
		require.True(t, listViewIDs(t, ts, memberToken, ts.OrgID)[id])
	})

	t.Run("team audience reaches the team and nobody else", func(t *testing.T) {
		teamID := uuid.New()
		_, err := ts.DB.Pool.Exec(ctx,
			`INSERT INTO teams (id, org_id, slug, name, path) VALUES ($1,$2,$3,$4,ARRAY[$1]::uuid[])`,
			teamID, ts.OrgID, "squad-"+uuid.NewString()[:8], "Squad")
		require.NoError(t, err)
		for _, u := range []uuid.UUID{ts.UserID, member.ID} {
			_, err = ts.DB.Pool.Exec(ctx,
				`INSERT INTO team_members (org_id, team_id, user_id) VALUES ($1,$2,$3)`, ts.OrgID, teamID, u)
			require.NoError(t, err)
		}

		id := createViewAs(t, ts, ts.Token, ts.OrgID, "Squad view", "team", &teamID)
		require.True(t, listViewIDs(t, ts, memberToken, ts.OrgID)[id],
			"a member of the audience team must see it")
		require.False(t, listViewIDs(t, ts, strangerToken, ts.OrgID)[id],
			"a member of the org who is not in the audience team must not")
	})

	t.Run("sharing with a team you do not belong to is refused", func(t *testing.T) {
		foreign := uuid.New()
		_, err := ts.DB.Pool.Exec(ctx,
			`INSERT INTO teams (id, org_id, slug, name, path) VALUES ($1,$2,$3,$4,ARRAY[$1]::uuid[])`,
			foreign, ts.OrgID, "foreign-"+uuid.NewString()[:8], "Foreign")
		require.NoError(t, err)

		res := ts.postAs(t, strangerToken, viewsPath(ts.OrgID), map[string]any{
			"name": "Not mine to share", "query": json.RawMessage(beaconViewQuery),
			"visibility": "team", "visibility_team_id": foreign.String(),
		})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
			"a view may only be shared with a team the author belongs to: %s", res.Body)
	})

	t.Run("team visibility without a team is refused", func(t *testing.T) {
		res := ts.postAs(t, memberToken, viewsPath(ts.OrgID), map[string]any{
			"name": "Teamless", "query": json.RawMessage(beaconViewQuery), "visibility": "team",
		})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
			"the write path enforces the invariant migration 038 deliberately does not CHECK: %s", res.Body)
	})
}

// TestViewsMatrix_OwnerSemantics is V3's rule: editing and deleting belong to
// the owner, with the org-admin bypass that applies everywhere else, and a
// view the caller cannot see 404s rather than 403s.
func TestViewsMatrix_OwnerSemantics(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberToken := ts.tokenFor(t, member.ID, member.Email)

	// The fixture's ts.UserID is an org owner, i.e. an org admin.
	orgShared := createViewAs(t, ts, memberToken, ts.OrgID, "Shared by a member", "org", nil)
	private := createViewAs(t, ts, memberToken, ts.OrgID, "Private to a member", "private", nil)

	patch := map[string]any{"name": "Renamed", "query": json.RawMessage(beaconViewQuery), "visibility": "org"}

	t.Run("a non-owner who can SEE the view gets 403", func(t *testing.T) {
		second := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		res := ts.patchAs(t, ts.tokenFor(t, second.ID, second.Email),
			viewsPath(ts.OrgID)+"/"+orgShared.String(), patch)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"the view is visible, so refusing it is not an existence leak: %s", res.Body)
	})

	t.Run("a non-owner who CANNOT see the view gets 404", func(t *testing.T) {
		second := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		res := ts.patchAs(t, ts.tokenFor(t, second.ID, second.Email),
			viewsPath(ts.OrgID)+"/"+private.String(), patch)
		require.Equal(t, http.StatusNotFound, res.StatusCode,
			"403 here would answer 'does that person have a view with this id'")
	})

	t.Run("the org admin bypasses ownership", func(t *testing.T) {
		res := ts.patchAs(t, ts.Token, viewsPath(ts.OrgID)+"/"+private.String(), patch)
		require.Equal(t, http.StatusOK, res.StatusCode,
			"org admin is a middleware-level bypass everywhere else and here too: %s", res.Body)
	})

	t.Run("the owner may delete", func(t *testing.T) {
		res := ts.deleteAs(t, memberToken, viewsPath(ts.OrgID)+"/"+orgShared.String())
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.False(t, listViewIDs(t, ts, memberToken, ts.OrgID)[orgShared])
	})
}

// TestViewsMatrix_QueryValidationIsStrict pins the two refusals that protect
// the filter vocabulary at the HTTP boundary.
func TestViewsMatrix_QueryValidationIsStrict(t *testing.T) {
	ts := newTestServer(t)

	cases := map[string]string{
		"unknown field": `{"v":1,"filter":{"modules":["beacon"],"assignee":"me"},"sort":{"field":"updated_at","dir":"desc"}}`,
		"sketch operator list": `{"v":1,"filter":{"modules":["beacon"],` +
			`"filters":[{"field":"status","op":"in","value":["open"]}]},"sort":{"field":"updated_at","dir":"desc"}}`,
		"kinds alongside beacon":       `{"v":1,"filter":{"modules":["beacon","vector"],"kinds":["bug"]},"sort":{"field":"updated_at","dir":"desc"}}`,
		"codex is not a view module":   `{"v":1,"filter":{"modules":["codex"]},"sort":{"field":"updated_at","dir":"desc"}}`,
		"arbitrary column as sort":     `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"assignee_id","dir":"asc"}}`,
		"unsupported document version": `{"v":2,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`,
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			res := ts.postAs(t, ts.Token, viewsPath(ts.OrgID), map[string]any{
				"name": "Rejected", "query": json.RawMessage(q),
			})
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
				"expected the filter document to be refused, got %d: %s", res.StatusCode, res.Body)
		})
	}
}

// TestViewsMatrix_ResultsResolvePerViewer is the phase's headline behaviour at
// the HTTP boundary: ONE shared view, two callers, different rows — and a
// ticket in a space the second caller cannot read never reaches them.
func TestViewsMatrix_ResultsResolvePerViewer(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberToken := ts.tokenFor(t, member.ID, member.Email)

	// An org-visible space both can read, and a hidden one only the admin can.
	open := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, open.ID, "org")
	hidden := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, hidden.ID, "hidden")

	mk := func(spaceID uuid.UUID, n int32, title string) uuid.UUID {
		id := uuid.New()
		_, err := ts.DB.Pool.Exec(ctx,
			`INSERT INTO tickets (id, space_id, number, title, reporter_id, status, priority)
			 VALUES ($1,$2,$3,$4,$5,'open','high')`, id, spaceID, n, title, ts.UserID)
		require.NoError(t, err)
		return id
	}
	visible := mk(open.ID, 1, "everyone can see this")
	secret := mk(hidden.ID, 1, "only the admin can see this")

	id := createViewAs(t, ts, ts.Token, ts.OrgID, "Everything", "org", nil)
	resultsPath := viewsPath(ts.OrgID) + "/" + id.String() + "/results"

	read := func(token string) map[uuid.UUID]bool {
		res := ts.getAs(t, token, resultsPath)
		require.Equal(t, http.StatusOK, res.StatusCode, "results: %s", res.Body)
		var out struct {
			Results []struct {
				ID uuid.UUID `json:"id"`
			} `json:"results"`
		}
		require.NoError(t, json.Unmarshal(res.Body, &out))
		got := map[uuid.UUID]bool{}
		for _, r := range out.Results {
			got[r.ID] = true
		}
		return got
	}

	admin := read(ts.Token)
	require.True(t, admin[visible])
	require.True(t, admin[secret], "the org admin reads every space")

	plain := read(memberToken)
	require.True(t, plain[visible], "the shared view still returns what this caller can read")
	require.False(t, plain[secret],
		"a shared view shares the DEFINITION, never the results: the hidden-space ticket must not cross")
}

// TestViewsMatrix_ResultsCarryTheAssigneeName pins the join added for the
// results UI. The name has to come from the fan-out, because resolving it per
// row is exactly the shape spec §2.5 case 23 forbids inside a list handler —
// and the alternative the UI had before this was rendering a raw uuid.
//
// Fails-before: drop `LEFT JOIN users au` (and the selected column) from
// ListViewTickets and assignee_name comes back null while assignee_id does not.
func TestViewsMatrix_ResultsCarryTheAssigneeName(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, space.ID, "org")

	assignee := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	mk := func(n int32, title string, who *uuid.UUID) uuid.UUID {
		id := uuid.New()
		_, err := ts.DB.Pool.Exec(ctx,
			`INSERT INTO tickets (id, space_id, number, title, reporter_id, status, priority, assignee_id)
			 VALUES ($1,$2,$3,$4,$5,'open','high',$6)`, id, space.ID, n, title, ts.UserID, who)
		require.NoError(t, err)
		return id
	}
	assigned := mk(1, "has an owner", &assignee.ID)
	unassigned := mk(2, "has none", nil)

	id := createViewAs(t, ts, ts.Token, ts.OrgID, "Everything", "org", nil)
	res := ts.getAs(t, ts.Token, viewsPath(ts.OrgID)+"/"+id.String()+"/results")
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var out struct {
		Results []struct {
			ID           uuid.UUID  `json:"id"`
			AssigneeID   *uuid.UUID `json:"assignee_id"`
			AssigneeName *string    `json:"assignee_name"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))

	byID := map[uuid.UUID]struct {
		id   *uuid.UUID
		name *string
	}{}
	for _, r := range out.Results {
		byID[r.ID] = struct {
			id   *uuid.UUID
			name *string
		}{r.AssigneeID, r.AssigneeName}
	}

	got := byID[assigned]
	require.NotNil(t, got.id)
	require.NotNil(t, got.name, "an assigned row must carry the assignee's name, not just their id")
	require.Equal(t, assignee.DisplayName, *got.name)

	free := byID[unassigned]
	require.Nil(t, free.id)
	require.Nil(t, free.name, "an unassigned row carries neither")
}

// TestViewsMatrix_ScopeUnavailableDegradesRatherThanErrors pins ADR-0009 case
// C1 through the API: a view whose every named space was deleted still lists
// and still opens, marked invalid.
func TestViewsMatrix_ScopeUnavailableDegradesRatherThanErrors(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	q := `{"v":1,"filter":{"modules":["beacon"],"space_ids":["` + space.ID.String() + `"]},` +
		`"sort":{"field":"updated_at","dir":"desc"}}`
	res := ts.postAs(t, ts.Token, viewsPath(ts.OrgID), map[string]any{
		"name": "Scoped", "query": json.RawMessage(q),
	})
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)
	var created struct {
		ID      uuid.UUID `json:"id"`
		IsValid bool      `json:"is_valid"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &created))
	require.True(t, created.IsValid)

	_, err := ts.DB.Pool.Exec(ctx, `UPDATE spaces SET deleted_at = now() WHERE id = $1`, space.ID)
	require.NoError(t, err)

	after := ts.getAs(t, ts.Token, viewsPath(ts.OrgID)+"/"+created.ID.String())
	require.Equal(t, http.StatusOK, after.StatusCode,
		"a view whose scope is gone must still OPEN — ADR-0009 case C1 says it never errors: %s", after.Body)
	var got struct {
		IsValid       bool   `json:"is_valid"`
		InvalidReason string `json:"invalid_reason"`
	}
	require.NoError(t, json.Unmarshal(after.Body, &got))
	require.False(t, got.IsValid, "it must be MARKED invalid, not silently returned as healthy")
	require.NotEmpty(t, got.InvalidReason, "the owner is prompted to re-scope, so the reason is shown")
}
