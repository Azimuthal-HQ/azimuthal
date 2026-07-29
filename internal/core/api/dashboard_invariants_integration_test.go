package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/dashboards"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// THE INVARIANT P5 INHERITS WHOLE FROM P4, RE-PROVEN HERE.
//
// Sharing a dashboard shares the ARRANGEMENT. Every gadget re-resolves its
// query against the VIEWER on every render, so two people opening one shared
// dashboard legitimately see different rows and different numbers. Nothing
// consults the dashboard's owner.
//
// These tests drive the real HTTP surface end to end, because the invariant is
// only worth anything if it holds on the path a browser takes: the dashboard
// response hands out a query, and the query is resolved by /views/preview and
// /views/aggregate against whoever asked.

type gadgetPayload struct {
	GadgetKey string          `json:"gadget_key"`
	State     string          `json:"state"`
	Title     string          `json:"title"`
	Render    string          `json:"render"`
	Query     json.RawMessage `json:"query"`
	ViewName  string          `json:"view_name"`
}

func dashboardGadgets(t *testing.T, ts *testServer, token, path string) []gadgetPayload {
	t.Helper()
	res := ts.getAs(t, token, path)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	var out struct {
		Gadgets []gadgetPayload `json:"gadgets"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	return out.Gadgets
}

// previewKeys runs a gadget's own query as the given caller and returns the
// item keys it resolves to. This is the same call the browser makes.
func previewKeys(t *testing.T, ts *testServer, token string, orgID uuid.UUID, query json.RawMessage) map[string]bool {
	t.Helper()
	res := ts.postAs(t, token, viewsPath(orgID)+"/preview", map[string]any{"query": query})
	require.Equal(t, http.StatusOK, res.StatusCode, "preview: %s", res.Body)
	var out struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	got := map[string]bool{}
	for _, r := range out.Results {
		got[r.Title] = true
	}
	return got
}

func aggregateTotal(t *testing.T, ts *testServer, token string, orgID uuid.UUID, query json.RawMessage, groupBy string) (int64, map[string]int64) {
	t.Helper()
	body := map[string]any{"query": query}
	if groupBy != "" {
		body["group_by"] = groupBy
	}
	res := ts.postAs(t, token, viewsPath(orgID)+"/aggregate", body)
	require.Equal(t, http.StatusOK, res.StatusCode, "aggregate: %s", res.Body)
	var out struct {
		Total   int64 `json:"total"`
		Buckets []struct {
			Key   string `json:"key"`
			Count int64  `json:"count"`
		} `json:"buckets"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	buckets := map[string]int64{}
	for _, b := range out.Buckets {
		buckets[b.Key] = b.Count
	}
	return out.Total, buckets
}

func seedTicket(t *testing.T, ts *testServer, spaceID, title string, assignee *uuid.UUID) {
	t.Helper()
	body := map[string]any{"title": title, "priority": "high"}
	if assignee != nil {
		body["assignee_id"] = assignee.String()
	}
	res := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID), body, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "seed ticket: %s", res.Body)
}

// A shared dashboard carrying a `me`-token gadget shows each viewer their own
// work. This is the property that makes one dashboard useful to a whole team
// instead of being a snapshot of whoever built it.
//
// Fails-before: resolve the `me` token at write time (substituting the
// author's id into the stored document) and the second viewer sees the first
// viewer's tickets.
func TestDashboardInvariant_SharedMeTokenGadgetResolvesPerViewer(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	spaceID := createScopedSpace(t, ts, "Shared Work", "shared-work", "beacon")
	spaceUUID := uuid.MustParse(spaceID)

	reader := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	readerTok := ts.tokenFor(t, reader.ID, reader.Email)
	_, err := ts.GrantService.Create(ctx, ts.OrgID, spaceUUID,
		access.SubjectUser, reader.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)

	seedTicket(t, ts, spaceID, "Owner's ticket", &ts.UserID)
	seedTicket(t, ts, spaceID, "Reader's ticket", &reader.ID)

	// One dashboard, shared org-wide, holding one My-work gadget.
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Team board", "org")
	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "my_work"}}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	path := dashboardsPath(ts.OrgID) + "/" + id.String()

	ownerGadgets := dashboardGadgets(t, ts, ts.Token, path)
	readerGadgets := dashboardGadgets(t, ts, readerTok, path)
	require.Len(t, ownerGadgets, 1)
	require.Len(t, readerGadgets, 1)
	require.JSONEq(t, string(ownerGadgets[0].Query), string(readerGadgets[0].Query),
		"the two viewers are handed the SAME document — the difference is in resolving it")

	ownerRows := previewKeys(t, ts, ts.Token, ts.OrgID, ownerGadgets[0].Query)
	readerRows := previewKeys(t, ts, readerTok, ts.OrgID, readerGadgets[0].Query)

	require.True(t, ownerRows["Owner's ticket"])
	require.False(t, ownerRows["Reader's ticket"])
	require.True(t, readerRows["Reader's ticket"])
	require.False(t, readerRows["Owner's ticket"],
		"one shared dashboard, two people, two different sets of rows — that is the design")
}

// The other half of the invariant: a viewer with LESS access silently sees
// fewer rows, and the dashboard does not present that as a failure. Here the
// two viewers run the identical unfiltered query and the reader is simply
// unable to read one of the spaces.
//
// Fails-before: resolve gadget results against the dashboard owner's access
// rather than the caller's, and the reader sees the hidden space's ticket.
func TestDashboardInvariant_ASharedDashboardNeverLeaksAHiddenSpace(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	open := createScopedSpace(t, ts, "Open Space", "open-space", "beacon")
	hidden := createScopedSpace(t, ts, "Hidden Space", "hidden-space", "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, uuid.MustParse(hidden), "hidden")

	reader := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	readerTok := ts.tokenFor(t, reader.ID, reader.Email)
	_, err := ts.GrantService.Create(ctx, ts.OrgID, uuid.MustParse(open),
		access.SubjectUser, reader.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)

	seedTicket(t, ts, open, "Everyone can see this", nil)
	seedTicket(t, ts, hidden, "Nobody outside can see this", nil)

	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Everything", "org")
	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "recent_work"}}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	path := dashboardsPath(ts.OrgID) + "/" + id.String()
	ownerQuery := dashboardGadgets(t, ts, ts.Token, path)[0].Query
	readerQuery := dashboardGadgets(t, ts, readerTok, path)[0].Query

	ownerRows := previewKeys(t, ts, ts.Token, ts.OrgID, ownerQuery)
	require.True(t, ownerRows["Nobody outside can see this"],
		"the owner CAN read the hidden space — so the reader's absence below is a filter, not an empty result")

	readerRows := previewKeys(t, ts, readerTok, ts.OrgID, readerQuery)
	require.True(t, readerRows["Everyone can see this"])
	require.False(t, readerRows["Nobody outside can see this"],
		"a gadget on a shared dashboard resolves against the CALLER's readable spaces")
}

// The A1/P4 hidden-space pattern applied to aggregates. A count is a
// disclosure: "there are four things" about work somebody cannot read is
// information they should not have. So is a bucket KEY — a status that exists
// only in a hidden space names something about it.
//
// Fails-before: drop the readable-space predicate from CountViewTickets and
// the reader's total becomes 2.
func TestDashboardInvariant_AggregatesLeakNothingFromAHiddenSpace(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	open := createScopedSpace(t, ts, "Open Space", "agg-open", "beacon")
	hidden := createScopedSpace(t, ts, "Hidden Space", "agg-hidden", "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, uuid.MustParse(hidden), "hidden")

	reader := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	readerTok := ts.tokenFor(t, reader.ID, reader.Email)
	_, err := ts.GrantService.Create(ctx, ts.OrgID, uuid.MustParse(open),
		access.SubjectUser, reader.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)

	seedTicket(t, ts, open, "Readable", nil)
	seedTicket(t, ts, hidden, "Unreadable", nil)
	// Give the hidden ticket a status that exists nowhere else, so a leaked
	// bucket key is unambiguous.
	_, err = ts.DB.Pool.Exec(ctx,
		`UPDATE tickets SET status = 'embargoed' WHERE space_id = $1`, uuid.MustParse(hidden))
	require.NoError(t, err)

	query := json.RawMessage(beaconViewQuery)

	ownerTotal, ownerBuckets := aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "status")
	require.Equal(t, int64(2), ownerTotal,
		"the owner counts both — so the reader's smaller count below is a filter, not an empty result")
	require.Contains(t, ownerBuckets, "embargoed")

	readerTotal, readerBuckets := aggregateTotal(t, ts, readerTok, ts.OrgID, query, "status")
	require.Equal(t, int64(1), readerTotal,
		"a count must not include work the caller cannot read")
	require.NotContains(t, readerBuckets, "embargoed",
		"a bucket key is itself a disclosure — a status that exists only in a hidden space must not appear")

	// And a count gadget on a shared dashboard shows the two of them different
	// numbers, which is the same invariant one layer up.
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Counts", "org")
	viewID := createViewAs(t, ts, ts.Token, ts.OrgID, "Everything", "org", nil)
	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{
			map[string]any{"gadget_key": "view_count", "saved_view_id": viewID.String()},
		}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	path := dashboardsPath(ts.OrgID) + "/" + id.String()
	ownerGadget := dashboardGadgets(t, ts, ts.Token, path)[0]
	readerGadget := dashboardGadgets(t, ts, readerTok, path)[0]
	require.Equal(t, "stat", readerGadget.Render)

	ownerN, _ := aggregateTotal(t, ts, ts.Token, ts.OrgID, ownerGadget.Query, "")
	readerN, _ := aggregateTotal(t, ts, readerTok, ts.OrgID, readerGadget.Query, "")
	require.Equal(t, int64(2), ownerN)
	require.Equal(t, int64(1), readerN,
		"one shared count gadget, two people, two different numbers")
}

// Decision log C2: a gadget whose view the viewer cannot read renders "not
// available to you", and THE DASHBOARD STILL LOADS. The state is computed
// server-side so no client re-derives an audience rule, and the private view's
// name and query are withheld — a tile that leaked either would be the
// disclosure the state exists to prevent.
func TestDashboardInvariant_AnUnreadableGadgetDoesNotBreakTheDashboard(t *testing.T) {
	ts := newTestServer(t)

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberTok := ts.tokenFor(t, member.ID, member.Email)

	// The owner's own private view, and a dashboard shared org-wide.
	privateView := createViewAs(t, ts, ts.Token, ts.OrgID, "Owner's secret filter", "private", nil)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Mixed", "org")
	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{
			map[string]any{"gadget_key": "view_results", "saved_view_id": privateView.String()},
			map[string]any{"gadget_key": "my_work"},
		}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	path := dashboardsPath(ts.OrgID) + "/" + id.String()

	owner := dashboardGadgets(t, ts, ts.Token, path)
	require.Equal(t, string(dashboards.StateReady), owner[0].State)
	require.Equal(t, "Owner's secret filter", owner[0].Title)

	gadgets := dashboardGadgets(t, ts, memberTok, path)
	require.Len(t, gadgets, 2, "the dashboard loads with every tile present")
	require.Equal(t, string(dashboards.StateViewUnreadable), gadgets[0].State)
	require.Empty(t, gadgets[0].Query, "the private view's query must not be handed out")
	require.Empty(t, gadgets[0].ViewName, "nor its name")
	require.NotContains(t, gadgets[0].Title, "secret",
		"the tile falls back to the gadget kind's own name, never the private view's")
	require.Equal(t, string(dashboards.StateReady), gadgets[1].State,
		"one unreadable tile must not take the rest of the dashboard down")
}

// Decision log C5: an unknown gadget_key renders an inert labelled placeholder
// and never crashes the dashboard. The row is written straight to SQL, because
// the API refuses such a key on a write — the case being tested is a row an
// older or newer build already left behind.
//
// Fails-before: make resolveGadgets return an error on an unknown key (or add
// a CHECK constraint to migration 042) and this returns 500 with no tiles at
// all, taking every other gadget on the dashboard with it.
func TestDashboardInvariant_AnUnknownGadgetKeyRendersAPlaceholder(t *testing.T) {
	ts := newTestServer(t)

	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Board", "private")
	res := ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "my_work"}}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO dashboard_gadgets (dashboard_id, gadget_key, position, col_span, config)
		 VALUES ($1, 'sprint_burndown', 1, 2, '{"sprint":"current"}')`, id)
	require.NoError(t, err)

	gadgets := dashboardGadgets(t, ts, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String())
	require.Len(t, gadgets, 2, "the dashboard still loads, with the unknown tile in its slot")
	require.Equal(t, string(dashboards.StateReady), gadgets[0].State)
	require.Equal(t, string(dashboards.StateUnknownGadget), gadgets[1].State)
	require.Equal(t, "sprint_burndown", gadgets[1].GadgetKey,
		"the key is carried through verbatim so the placeholder can name what it stands for")
	require.Empty(t, gadgets[1].Render, "an unknown gadget has no render mode to dispatch on")
	require.Empty(t, gadgets[1].Query)

	// And a layout write still refuses to CREATE one — tolerant on read,
	// strict on write.
	res = ts.putAs(t, ts.Token, dashboardsPath(ts.OrgID)+"/"+id.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "sprint_burndown"}}})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
}

// ── Home ────────────────────────────────────────────────────────────────────

// The Home journey: a first visit seeds a starter layout, a second visit
// returns the same dashboard, and a customised one is never re-seeded.
func TestDashboardInvariant_HomeSeedsOnceAndKeepsCustomisation(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)
	homePath := dashboardsPath(ts.OrgID) + "/home"

	first := getDashboard(t, ts, token, homePath)
	require.True(t, first.IsSeeded)
	require.True(t, first.IsDefault)
	require.Len(t, first.Gadgets, 3)
	require.Equal(t, "my_work", first.Gadgets[0].GadgetKey)
	require.Equal(t, "recent_work", first.Gadgets[1].GadgetKey)
	require.Equal(t, "note", first.Gadgets[2].GadgetKey)
	require.NotEmpty(t, first.Gadgets[2].Config.Body)

	second := getDashboard(t, ts, token, homePath)
	require.Equal(t, first.ID, second.ID, "a second visit is the same dashboard, not a second one")

	// Make it theirs, then come back.
	res := ts.putAs(t, token, dashboardsPath(ts.OrgID)+"/"+first.ID.String()+"/gadgets",
		map[string]any{"gadgets": []any{map[string]any{"gadget_key": "recent_work"}}})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	third := getDashboard(t, ts, token, homePath)
	require.Equal(t, first.ID, third.ID)
	require.Len(t, third.Gadgets, 1, "re-seeding would have destroyed the customisation")

	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM dashboards WHERE owner_id = $1 AND module = 'home' AND deleted_at IS NULL`,
		member.ID).Scan(&n))
	require.Equal(t, 1, n, "exactly one Home dashboard, however many times Home is opened")
}

// Home is per person. One user's starter must never satisfy another's first
// visit, and neither may see the other's.
func TestDashboardInvariant_HomeIsPerPerson(t *testing.T) {
	ts := newTestServer(t)
	homePath := dashboardsPath(ts.OrgID) + "/home"

	one := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	two := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	a := getDashboard(t, ts, ts.tokenFor(t, one.ID, one.Email), homePath)
	b := getDashboard(t, ts, ts.tokenFor(t, two.ID, two.Email), homePath)
	require.NotEqual(t, a.ID, b.ID)

	res := ts.getAs(t, ts.tokenFor(t, two.ID, two.Email), dashboardsPath(ts.OrgID)+"/"+a.ID.String())
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"a seeded Home dashboard is private to the person it was seeded for")
}

// ── Query constancy (spec §2.5 case 23) ─────────────────────────────────────

// A dashboard renders N gadgets. Resolving one view per gadget is exactly the
// per-item authorisation shape case 23 forbids, and it is the defect this
// feature is most likely to introduce. The tracer proves the query count for a
// two-gadget dashboard equals the count for a twelve-gadget one.
//
// Fails-before: move the ByIDs call inside resolveGadgets' per-gadget loop and
// the twelve-gadget count exceeds the two-gadget count by ten.
func TestMatrixAPI23_DashboardQueriesDoNotGrowWithGadgetCount(t *testing.T) {
	db := testutil.NewTestDB(t)

	counter := &queryCounter{}
	cfg, err := pgxpool.ParseConfig(db.DSN)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = fmt.Sprintf("%q, public", db.Schema)
	cfg.ConnConfig.Tracer = counter
	cfg.MaxConns = 3
	countingPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(countingPool.Close)

	ts := newTestServerOn(t, db, countingPool)
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Counted", "private")
	gadgetsPath := dashboardsPath(ts.OrgID) + "/" + id.String() + "/gadgets"
	readPath := dashboardsPath(ts.OrgID) + "/" + id.String()

	// Each gadget names its OWN saved view, so a per-gadget lookup would be
	// unmistakable in the count. A shared view would let a naive cache hide it.
	setGadgets := func(n int) {
		gadgets := make([]any, 0, n)
		for i := range n {
			v := createViewAs(t, ts, ts.Token, ts.OrgID, fmt.Sprintf("View %d", i), "private", nil)
			gadgets = append(gadgets, map[string]any{
				"gadget_key": "view_count", "saved_view_id": v.String(),
			})
		}
		res := ts.putAs(t, ts.Token, gadgetsPath, map[string]any{"gadgets": gadgets})
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	}

	countedGet := func(wantGadgets int) int64 {
		// Warm request first: connection setup and auth caches must not
		// pollute the measured request.
		res := ts.get(t, readPath, true)
		require.Equal(t, http.StatusOK, res.StatusCode, "warm: %s", res.Body)
		before := counter.n.Load()
		authBefore := counter.authState.Load()

		res = ts.get(t, readPath, true)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var body struct {
			Gadgets []json.RawMessage `json:"gadgets"`
		}
		require.NoError(t, json.Unmarshal(res.Body, &body))
		require.Len(t, body.Gadgets, wantGadgets, "gadget-count premise for the assertion")

		require.Equal(t, int64(1), counter.authState.Load()-authBefore,
			"exactly one GetUserAuthState read per authenticated request")
		return counter.n.Load() - before
	}

	setGadgets(2)
	qAt2 := countedGet(2)
	setGadgets(12)
	qAt12 := countedGet(12)

	require.Equal(t, qAt2, qAt12,
		"dashboard read: query count must not grow with gadget count (N=2: %d, N=12: %d)", qAt2, qAt12)
	require.LessOrEqual(t, qAt2, int64(8), "per-request query budget blown: %d", qAt2)
}
