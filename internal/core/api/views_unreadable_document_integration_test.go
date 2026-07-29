package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A stored filter document this build cannot parse.
//
// It is the one server-side failure the saved-view and dashboard surfaces can
// be driven into from outside, and it is worth driving into deliberately,
// because the alternative behaviour is the worst one available: if the store
// swallowed the parse error and returned a zero Query, an EMPTY filter matches
// everything the viewer can read. A view that silently widened to "everything"
// is a disclosure, not a display bug.
//
// The rows are written straight to SQL. The API refuses an unknown field on
// the way in — views.ParseQuery uses DisallowUnknownFields — so the only way
// such a row exists is that a different build wrote it, which is precisely the
// case under test. Every assertion here is that the surface REFUSES rather
// than degrades, and that the refusal carries no internal wording.
//
// These also reach the `default` arm of both handlers' error switches, which
// nothing else can: every other failure in either family is a named sentinel.

const vudBadDocument = `{"v":1,"filter":{"modules":["beacon"],"invented_field":true},` +
	`"sort":{"field":"updated_at","dir":"desc"}}`

// vudInsertBrokenView writes a saved view whose document this build refuses,
// and returns its id.
func vudInsertBrokenView(t *testing.T, ts *testServer, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO saved_views (id, org_id, owner_id, name, query, visibility)
		 VALUES ($1, $2, $3, $4, $5::jsonb, 'private')`,
		id, ts.OrgID, ts.UserID, name, vudBadDocument)
	require.NoError(t, err)
	return id
}

func vudRequireInternal(t *testing.T, res httpResult, path string) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode,
		"%s must refuse an unreadable stored document rather than serve it: %s", path, res.Body)

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &env), "body: %s", res.Body)
	require.Equal(t, "INTERNAL_ERROR", env.Error.Code)
	// The fallback message is written server-side and fixed. The parse error
	// names the row and the offending key, and neither belongs in a response.
	require.NotContains(t, env.Error.Message, "invented_field",
		"the caller must not be told which key the stored document carried")
	require.NotContains(t, env.Error.Message, "unknown field")
}

// Every saved-view route that loads a stored document refuses it.
func TestViewUnreadableDocument_EveryReadRefusesRatherThanWidens(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	id := vudInsertBrokenView(t, ts, "Broken view")

	t.Run("get", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/orgs/%s/views/%s", org, id)
		vudRequireInternal(t, ts.get(t, path, true), path)
	})

	t.Run("list", func(t *testing.T) {
		// The whole page fails rather than quietly omitting the row. Omitting
		// it would hide a broken view from the only person who could fix it.
		path := "/api/v1/orgs/" + org + "/views"
		vudRequireInternal(t, ts.get(t, path, true), path)
	})

	t.Run("results", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/orgs/%s/views/%s/results", org, id)
		vudRequireInternal(t, ts.get(t, path, true), path)
	})

	t.Run("update", func(t *testing.T) {
		// The update loads the existing row before it writes, so it fails on
		// the same parse — a caller cannot repair the row by overwriting it,
		// which is worth knowing rather than discovering.
		path := fmt.Sprintf("/api/v1/orgs/%s/views/%s", org, id)
		res := ts.patch(t, path, map[string]any{
			"name": "Repaired", "query": json.RawMessage(beaconViewQuery), "visibility": "private",
		}, true)
		vudRequireInternal(t, res, path)
	})

	t.Run("delete", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/orgs/%s/views/%s", org, id)
		vudRequireInternal(t, ts.deleteAs(t, ts.Token, path), path)
	})
}

// A queue is a saved view with a space binding, so it fails the same way and
// through the same code.
func TestViewUnreadableDocument_TheQueueSurfaceRefusesToo(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	spaceID := createScopedSpace(t, ts, "Broken Desk", "broken-desk", "beacon")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/queues", org, spaceID)

	id := uuid.New()
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO saved_views (id, org_id, owner_id, space_id, position, name, query, visibility)
		 VALUES ($1, $2, $3, $4, 0, 'Broken queue', $5::jsonb, 'space')`,
		id, ts.OrgID, ts.UserID, uuid.MustParse(spaceID), vudBadDocument)
	require.NoError(t, err)

	vudRequireInternal(t, ts.get(t, base, true), base)

	results := base + "/" + id.String() + "/results"
	vudRequireInternal(t, ts.get(t, results, true), results)

	// A reorder loads the space's queues first, so it fails on the same parse
	// rather than renumbering a row it cannot read.
	order := base + "/order"
	vudRequireInternal(t, ts.putAs(t, ts.Token, order, map[string]any{
		"queue_ids": []string{id.String()},
	}), order)
}

// A dashboard whose gadget names an unreadable view fails on the batch lookup.
//
// This is the one place the dashboard surface can reach its own 500, and the
// behaviour is deliberate: the gadget states exist for a view that is missing
// or private, not for one whose stored document is corrupt. Serving the
// dashboard with that tile silently blank would hide a broken row from its
// owner.
func TestViewUnreadableDocument_ADashboardGadgetNamingOneRefuses(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	viewID := vudInsertBrokenView(t, ts, "Broken gadget view")
	dashboardID := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Broken board", "private")

	// Straight to SQL: the layout write would refuse to attach a view it
	// cannot read, which is the same refusal one layer earlier.
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO dashboard_gadgets (dashboard_id, gadget_key, position, col_span, saved_view_id)
		 VALUES ($1, 'view_results', 0, 2, $2)`, dashboardID, viewID)
	require.NoError(t, err)

	path := fmt.Sprintf("/api/v1/orgs/%s/dashboards/%s", org, dashboardID)
	vudRequireInternal(t, ts.get(t, path, true), path)

	// And a layout write on the same dashboard refuses too — it reads the
	// referenced views before it validates anything.
	gadgets := path + "/gadgets"
	vudRequireInternal(t, ts.putAs(t, ts.Token, gadgets, map[string]any{
		"gadgets": []any{
			map[string]any{"gadget_key": "view_results", "saved_view_id": viewID.String()},
		},
	}), gadgets)
}

// Home resolves the caller's own dashboard, so a broken gadget on it fails the
// landing page rather than the dashboard page. Worth its own case: Home is the
// one route that can create the row it then reads.
func TestViewUnreadableDocument_HomeRefusesABrokenGadget(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	homePath := "/api/v1/orgs/" + org + "/dashboards/home"

	// Seed the starter first, then break it.
	home := getDashboard(t, ts, ts.Token, homePath)
	viewID := vudInsertBrokenView(t, ts, "Broken home view")
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO dashboard_gadgets (dashboard_id, gadget_key, position, col_span, saved_view_id)
		 VALUES ($1, 'view_results', 99, 2, $2)`, home.ID, viewID)
	require.NoError(t, err)

	vudRequireInternal(t, ts.get(t, homePath, true), homePath)
}
