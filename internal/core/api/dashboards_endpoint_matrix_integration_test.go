package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/dashboards"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The per-endpoint matrix for dashboards (spec §2.6).
//
// These exist because the route-accounting sweep does NOT prove them.
// TestReadPathSweep_GuardClassMatchesMiddleware compares the claimed class
// against the real middleware chain for the two ADMIN classes only; a row
// claiming "org-member" is accepted on the strength of the row alone. So the
// org-member claim on the seven /dashboards routes, and every ownership and
// visibility rule above it, is asserted here against a live server or it is
// not asserted at all.

func dashboardsPath(orgID uuid.UUID) string {
	return "/api/v1/orgs/" + orgID.String() + "/dashboards"
}

type dashboardBody struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Module    string    `json:"module"`
	IsDefault bool      `json:"is_default"`
	IsSeeded  bool      `json:"is_seeded"`
	IsOwner   bool      `json:"is_owner"`
	IsValid   bool      `json:"is_valid"`
	Gadgets   []struct {
		ID          uuid.UUID       `json:"id"`
		GadgetKey   string          `json:"gadget_key"`
		Position    int32           `json:"position"`
		ColSpan     int32           `json:"col_span"`
		SavedViewID *uuid.UUID      `json:"saved_view_id"`
		State       string          `json:"state"`
		Title       string          `json:"title"`
		Render      string          `json:"render"`
		Query       json.RawMessage `json:"query"`
		ViewName    string          `json:"view_name"`
		Config      struct {
			Title   string `json:"title"`
			Limit   *int   `json:"limit"`
			GroupBy string `json:"group_by"`
			Body    string `json:"body"`
		} `json:"config"`
	} `json:"gadgets"`
}

func createDashboardAs(t *testing.T, ts *testServer, token string, orgID uuid.UUID, name, visibility string) uuid.UUID {
	t.Helper()
	res := ts.postAs(t, token, dashboardsPath(orgID), map[string]any{
		"name": name, "visibility": visibility,
	})
	require.Equal(t, http.StatusCreated, res.StatusCode, "create dashboard: %s", res.Body)
	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	return out.ID
}

func getDashboard(t *testing.T, ts *testServer, token, path string) dashboardBody {
	t.Helper()
	res := ts.getAs(t, token, path)
	require.Equal(t, http.StatusOK, res.StatusCode, "get dashboard: %s", res.Body)
	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	return out
}

func listDashboardIDs(t *testing.T, ts *testServer, token, path string) map[uuid.UUID]bool {
	t.Helper()
	res := ts.getAs(t, token, path)
	require.Equal(t, http.StatusOK, res.StatusCode, "list dashboards: %s", res.Body)
	var out struct {
		Dashboards []dashboardBody `json:"dashboards"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	got := map[uuid.UUID]bool{}
	for _, d := range out.Dashboards {
		got[d.ID] = true
	}
	return got
}

// The org-member guard class the accounting table claims and the sweep does
// not check.
func TestDashboardsMatrix_UnauthenticatedAndNonMember(t *testing.T) {
	ts := newTestServer(t)

	t.Run("unauthenticated is 401", func(t *testing.T) {
		res := ts.get(t, dashboardsPath(ts.OrgID), false)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("member of another org is 404", func(t *testing.T) {
		// Not 403: the org-member guard 404s so the surface does not confirm
		// that this organisation exists to somebody outside it.
		other := testutil.CreateTestOrg(t, ts.DB.Pool)
		outsider := testutil.CreateTestUser(t, ts.DB.Pool, other.ID)
		token := ts.tokenFor(t, outsider.ID, outsider.Email)

		res := ts.getAs(t, token, dashboardsPath(ts.OrgID))
		require.Equal(t, http.StatusNotFound, res.StatusCode,
			"a non-member must not learn whether this org has dashboards")
	})

	t.Run("the Home route is org-member too", func(t *testing.T) {
		res := ts.get(t, dashboardsPath(ts.OrgID)+"/home", false)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}

// Any org member may keep a dashboard: it reads nothing they could not already
// read, so there is no capability to hold.
func TestDashboardsMatrix_AnyMemberMayCreateOneAndItIsPrivateByDefault(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)

	res := ts.postAs(t, token, dashboardsPath(ts.OrgID), map[string]any{"name": "Mine"})
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)

	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Equal(t, "home", out.Module)
	require.True(t, out.IsOwner)
	require.True(t, out.IsValid)
	require.False(t, out.IsSeeded, "a hand-made dashboard is not a seeded one")
}

func TestDashboardsMatrix_ValidationIsStrict(t *testing.T) {
	ts := newTestServer(t)

	cases := map[string]map[string]any{
		"no name":            {"name": ""},
		"whitespace name":    {"name": "   "},
		"unknown module":     {"name": "x", "module": "codex"},
		"unknown visibility": {"name": "x", "visibility": "everyone"},
		"team without team":  {"name": "x", "visibility": "team"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := ts.post(t, dashboardsPath(ts.OrgID), body, true)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
				"expected the dashboard to be refused, got %d: %s", res.StatusCode, res.Body)
		})
	}
}

// Visibility, end to end: private reaches nobody else, org reaches every
// member, and a private one answers 404 rather than 403 so the surface does
// not confirm it exists.
func TestDashboardsMatrix_VisibilityDecidesWhoSeesIt(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)

	private := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Private", "private")
	orgWide := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Org wide", "org")

	seen := listDashboardIDs(t, ts, token, dashboardsPath(ts.OrgID))
	require.False(t, seen[private], "a private dashboard must not list for anybody else")
	require.True(t, seen[orgWide])

	res := ts.getAs(t, token, dashboardsPath(ts.OrgID)+"/"+private.String())
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	res = ts.getAs(t, token, dashboardsPath(ts.OrgID)+"/"+orgWide.String())
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.False(t, out.IsOwner, "is_owner is computed server-side, per reader")
}

// Seeing is not editing. A shared dashboard is 403 to a non-owner on every
// write; a private one is 404 on every write, because a 403 would confirm it
// exists.
func TestDashboardsMatrix_OwnershipGovernsEveryWrite(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)

	private := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Private", "private")
	orgWide := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Org wide", "org")

	t.Run("a shared dashboard is 403 to a non-owner", func(t *testing.T) {
		path := dashboardsPath(ts.OrgID) + "/" + orgWide.String()
		require.Equal(t, http.StatusForbidden,
			ts.patchAs(t, token, path, map[string]any{"name": "Hijacked"}).StatusCode)
		require.Equal(t, http.StatusForbidden, ts.deleteAs(t, token, path).StatusCode)
		require.Equal(t, http.StatusForbidden,
			ts.putAs(t, token, path+"/gadgets", map[string]any{"gadgets": []any{}}).StatusCode)
	})

	t.Run("a private dashboard is 404 to a non-owner", func(t *testing.T) {
		path := dashboardsPath(ts.OrgID) + "/" + private.String()
		require.Equal(t, http.StatusNotFound,
			ts.patchAs(t, token, path, map[string]any{"name": "Hijacked"}).StatusCode)
		require.Equal(t, http.StatusNotFound, ts.deleteAs(t, token, path).StatusCode)
		require.Equal(t, http.StatusNotFound,
			ts.putAs(t, token, path+"/gadgets", map[string]any{"gadgets": []any{}}).StatusCode)
	})

	t.Run("the owner may do all three", func(t *testing.T) {
		path := dashboardsPath(ts.OrgID) + "/" + orgWide.String()
		require.Equal(t, http.StatusOK,
			ts.patchAs(t, ts.Token, path, map[string]any{"name": "Renamed", "visibility": "org"}).StatusCode)
		require.Equal(t, http.StatusOK,
			ts.putAs(t, ts.Token, path+"/gadgets", map[string]any{"gadgets": []any{}}).StatusCode)
		require.Equal(t, http.StatusNoContent, ts.deleteAs(t, ts.Token, path).StatusCode)
	})
}

// The org-admin bypass reaches dashboards as it reaches everything else.
func TestDashboardsMatrix_OrgAdminBypassesOwnership(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)

	// ts.Token is the org owner; the dashboard belongs to an ordinary member.
	theirs := createDashboardAs(t, ts, token, ts.OrgID, "Theirs", "private")
	path := dashboardsPath(ts.OrgID) + "/" + theirs.String()

	require.Equal(t, http.StatusOK, ts.getAs(t, ts.Token, path).StatusCode)
	require.Equal(t, http.StatusOK,
		ts.patchAs(t, ts.Token, path, map[string]any{"name": "Renamed"}).StatusCode)
}

func TestDashboardsMatrix_ListFiltersByModule(t *testing.T) {
	ts := newTestServer(t)

	home := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Home one", "private")
	res := ts.post(t, dashboardsPath(ts.OrgID), map[string]any{"name": "Sprint", "module": "vector"}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)
	var vector dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &vector))

	got := listDashboardIDs(t, ts, ts.Token, dashboardsPath(ts.OrgID)+"?module=vector")
	require.True(t, got[vector.ID])
	require.False(t, got[home], "?module= narrows the list rather than annotating it")

	res = ts.get(t, dashboardsPath(ts.OrgID)+"?module=codex", true)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
		"an unknown module is refused rather than silently returning everything")
}

// Layout writes: the registry's refusals reach the wire as 422s rather than
// being normalised away.
func TestDashboardsMatrix_LayoutWriteRefusals(t *testing.T) {
	ts := newTestServer(t)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Board", "private")
	path := dashboardsPath(ts.OrgID) + "/" + id.String() + "/gadgets"
	viewID := createViewAs(t, ts, ts.Token, ts.OrgID, "Open", "private", nil)

	cases := map[string]any{
		"unknown gadget key":       map[string]any{"gadget_key": "burndown"},
		"span outside the CHECK":   map[string]any{"gadget_key": "my_work", "col_span": 3},
		"view-backed with no view": map[string]any{"gadget_key": "view_results"},
		"view on a note":           map[string]any{"gadget_key": "note", "saved_view_id": viewID.String()},
		"view that does not exist": map[string]any{"gadget_key": "view_results", "saved_view_id": uuid.NewString()},
		"unknown config key":       map[string]any{"gadget_key": "note", "config": map[string]any{"colour": "red"}},
		"config key of another kind": map[string]any{
			"gadget_key": "note", "config": map[string]any{"group_by": "status"},
		},
		"limit out of range": map[string]any{
			"gadget_key": "my_work", "config": map[string]any{"limit": 500},
		},
		"breakdown with no field": map[string]any{
			"gadget_key": "breakdown", "saved_view_id": viewID.String(),
		},
	}
	for name, gadget := range cases {
		t.Run(name, func(t *testing.T) {
			res := ts.putAs(t, ts.Token, path, map[string]any{"gadgets": []any{gadget}})
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
				"expected the gadget to be refused, got %d: %s", res.StatusCode, res.Body)
		})
	}
}

// A layout write is a replacement, and the server assigns positions from the
// order it was sent — so a client cannot produce a gap or a duplicate.
func TestDashboardsMatrix_LayoutIsAWholeCollectionWrite(t *testing.T) {
	ts := newTestServer(t)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Board", "private")
	path := dashboardsPath(ts.OrgID) + "/" + id.String() + "/gadgets"

	res := ts.putAs(t, ts.Token, path, map[string]any{"gadgets": []any{
		map[string]any{"gadget_key": "my_work", "config": map[string]any{"limit": 3}},
		map[string]any{"gadget_key": "recent_work"},
		map[string]any{"gadget_key": "note", "col_span": 4, "config": map[string]any{"body": "# hi"}},
	}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Len(t, out.Gadgets, 3)
	for i, g := range out.Gadgets {
		require.Equal(t, int32(i), g.Position)
	}
	require.Equal(t, int32(4), out.Gadgets[2].ColSpan)
	require.Equal(t, "# hi", out.Gadgets[2].Config.Body)
	require.NotNil(t, out.Gadgets[0].Config.Limit)
	require.Equal(t, 3, *out.Gadgets[0].Config.Limit)

	// Replacing with fewer removes the rest rather than merging.
	res = ts.putAs(t, ts.Token, path, map[string]any{"gadgets": []any{
		map[string]any{"gadget_key": "recent_work"},
	}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Len(t, out.Gadgets, 1)

	// And it survives a re-read.
	back := getDashboard(t, ts, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String())
	require.Len(t, back.Gadgets, 1)
	require.Equal(t, "recent_work", back.Gadgets[0].GadgetKey)
}

// An empty dashboard serialises `gadgets: []`, never `null` — Go's nil slice
// would take down any client that maps over it.
func TestDashboardsMatrix_GadgetsIsAlwaysAnArray(t *testing.T) {
	ts := newTestServer(t)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Empty", "private")

	res := ts.getAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, string(res.Body), `"gadgets":[]`)
}

// A registry-backed gadget is handed the registry's own query; the caller's
// dashboard row carries only a key. That is ADR-0009 decision 2 on the wire.
func TestDashboardsMatrix_GadgetCarriesAQueryButTheRowDoesNot(t *testing.T) {
	ts := newTestServer(t)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Board", "private")

	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "my_work"}}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var out dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Equal(t, string(dashboards.StateReady), out.Gadgets[0].State)
	require.Equal(t, "list", out.Gadgets[0].Render)
	require.Contains(t, string(out.Gadgets[0].Query), `"me"`,
		"the me token travels verbatim so it resolves against whoever runs it")

	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM dashboard_gadgets WHERE dashboard_id = $1 AND config::text LIKE '%modules%'`,
		id).Scan(&n))
	require.Zero(t, n, "a gadget row must never embed a query — the registry supplies it")
}

// ── The aggregate endpoint ──────────────────────────────────────────────────

func TestAggregateMatrix_GuardAndValidation(t *testing.T) {
	ts := newTestServer(t)
	path := viewsPath(ts.OrgID) + "/aggregate"

	t.Run("unauthenticated is 401", func(t *testing.T) {
		res := ts.post(t, path, map[string]any{"query": json.RawMessage(beaconViewQuery)}, false)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("member of another org is 404", func(t *testing.T) {
		other := testutil.CreateTestOrg(t, ts.DB.Pool)
		outsider := testutil.CreateTestUser(t, ts.DB.Pool, other.ID)
		token := ts.tokenFor(t, outsider.ID, outsider.Email)
		res := ts.postAs(t, token, path, map[string]any{"query": json.RawMessage(beaconViewQuery)})
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("an unknown group_by is refused", func(t *testing.T) {
		res := ts.post(t, path, map[string]any{
			"query": json.RawMessage(beaconViewQuery), "group_by": "space",
		}, true)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
	})

	t.Run("kind alongside beacon is refused", func(t *testing.T) {
		both := `{"v":1,"filter":{"modules":["beacon","vector"]},"sort":{"field":"updated_at","dir":"desc"}}`
		res := ts.post(t, path, map[string]any{
			"query": json.RawMessage(both), "group_by": "kind",
		}, true)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
	})

	t.Run("an unknown filter field is refused", func(t *testing.T) {
		res := ts.post(t, path, map[string]any{
			"query": json.RawMessage(`{"v":1,"filter":{"modules":["beacon"],"assignee":"me"},"sort":{"field":"updated_at","dir":"desc"}}`),
		}, true)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
	})

	t.Run("no group_by returns a total and an empty bucket list", func(t *testing.T) {
		res := ts.post(t, path, map[string]any{"query": json.RawMessage(beaconViewQuery)}, true)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		require.Contains(t, string(res.Body), `"buckets":[]`, "buckets is an array, never null")
	})
}

// TestDashboardsMatrix_RenamingATeamSharedDashboardKeepsItsTeam is
// known-issues #26 over the wire, which is the layer it was reported at, and
// the #25 merge-semantics standard applied to the dashboards twin.
//
// PATCH is a merge, so a request carrying only a new name inherits the row's
// whole audience — the team included. Update inherited the visibility without
// the team it names, producing "team with no team", and the caller was
// answered 422 "a team-visible view must name a team" about a field they had
// not sent.
//
// The second half asserts the merge does not overreach: a request that MOVES
// the dashboard to the org audience drops the team id rather than inheriting
// it. Both halves run against real PostgreSQL, because the service-level twin
// runs against a fake store and a fake cannot show that the column was
// actually written.
func TestDashboardsMatrix_RenamingATeamSharedDashboardKeepsItsTeam(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	// A member, not the org owner: an org admin bypasses the team-membership
	// check in Normalise, so an owner-persona test would pass with the
	// membership half of the rule deleted.
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberToken := ts.tokenFor(t, member.ID, member.Email)

	teamID := uuid.New()
	_, err := ts.DB.Pool.Exec(ctx,
		`INSERT INTO teams (id, org_id, slug, name, path) VALUES ($1,$2,$3,$4,ARRAY[$1]::uuid[])`,
		teamID, ts.OrgID, "dashrenamers-"+uuid.NewString()[:8], "Dash renamers")
	require.NoError(t, err)
	_, err = ts.DB.Pool.Exec(ctx,
		`INSERT INTO team_members (org_id, team_id, user_id) VALUES ($1,$2,$3)`, ts.OrgID, teamID, member.ID)
	require.NoError(t, err)

	res := ts.postAs(t, memberToken, dashboardsPath(ts.OrgID), map[string]any{
		"name": "Squad dashboard", "visibility": "team", "visibility_team_id": teamID.String(),
	})
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)

	var out struct {
		ID               uuid.UUID  `json:"id"`
		Name             string     `json:"name"`
		Visibility       string     `json:"visibility"`
		VisibilityTeamID *uuid.UUID `json:"visibility_team_id"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Equal(t, &teamID, out.VisibilityTeamID)
	id := out.ID

	t.Run("a name-only PATCH keeps the team share", func(t *testing.T) {
		res := ts.patchAs(t, memberToken, dashboardsPath(ts.OrgID)+"/"+id.String(), map[string]any{
			"name": "Squad dashboard v2",
		})
		require.Equal(t, http.StatusOK, res.StatusCode,
			"renaming a team-shared dashboard must not require re-naming its team: %s", res.Body)
		require.NoError(t, json.Unmarshal(res.Body, &out))
		require.Equal(t, "Squad dashboard v2", out.Name)
		require.Equal(t, "team", out.Visibility)
		require.Equal(t, &teamID, out.VisibilityTeamID)

		// And it is what was stored, not only what was echoed.
		res = ts.getAs(t, memberToken, dashboardsPath(ts.OrgID)+"/"+id.String())
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		require.NoError(t, json.Unmarshal(res.Body, &out))
		require.Equal(t, "team", out.Visibility)
		require.Equal(t, &teamID, out.VisibilityTeamID)
	})

	t.Run("moving it to the org audience drops the team", func(t *testing.T) {
		res := ts.patchAs(t, memberToken, dashboardsPath(ts.OrgID)+"/"+id.String(), map[string]any{
			"name": "Everyone's dashboard", "visibility": "org",
		})
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		require.NoError(t, json.Unmarshal(res.Body, &out))
		require.Equal(t, "org", out.Visibility)
		require.Nil(t, out.VisibilityTeamID,
			"a widened dashboard carrying its old team id is a lie the next reader has to interpret")
	})
}
