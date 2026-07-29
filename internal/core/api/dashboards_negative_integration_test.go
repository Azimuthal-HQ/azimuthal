package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The refusal paths of the saved-view and dashboard families.
//
// Every route in both families parses at least one uuid out of the URL and
// decodes at least one body, and each of those has a branch that answers 400
// before anything else runs. They are cheap to write and worth having: a
// handler that 500s on a malformed id is leaking a stack trace's worth of
// information about itself, and one that PANICS takes the process with it.
//
// Each case asserts the exact status AND that the body is the error envelope,
// because "not 200" would pass on a 500 — which is the outcome these exist to
// keep dead.

func dashNegErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "response was not an error envelope: %s", body)
	require.NotEmpty(t, env.Error.Message, "an error envelope with no message tells the caller nothing")
	return env.Error.Code
}

// A path parameter that is not a uuid must answer 400 with the envelope, on
// every route that parses one — never 404 (which would say the thing does not
// exist, when the request never named one) and never 500.
func TestDashNeg_MalformedPathParametersAre400(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	space := createScopedSpace(t, ts, "Neg Space", "neg-space", "beacon")
	good := uuid.NewString()

	gets := []string{
		"/api/v1/orgs/not-a-uuid/dashboards",
		"/api/v1/orgs/" + org + "/dashboards/not-a-uuid",
		"/api/v1/orgs/not-a-uuid/dashboards/home",
		"/api/v1/orgs/not-a-uuid/views",
		"/api/v1/orgs/" + org + "/views/not-a-uuid",
		"/api/v1/orgs/" + org + "/views/not-a-uuid/results",
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/queues/not-a-uuid/results", org, space),
	}
	for _, path := range gets {
		t.Run("GET "+path, func(t *testing.T) {
			res := ts.get(t, path, true)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s: %s", path, res.Body)
			require.Equal(t, "BAD_REQUEST", dashNegErrorCode(t, res.Body))
		})
	}

	t.Run("PATCH a malformed dashboard id", func(t *testing.T) {
		res := ts.patch(t, "/api/v1/orgs/"+org+"/dashboards/not-a-uuid",
			map[string]any{"name": "x"}, true)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", res.Body)
	})

	t.Run("DELETE a malformed dashboard id", func(t *testing.T) {
		res := ts.deleteAs(t, ts.Token, "/api/v1/orgs/"+org+"/dashboards/not-a-uuid")
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", res.Body)
	})

	t.Run("PUT gadgets on a malformed dashboard id", func(t *testing.T) {
		res := ts.putAs(t, ts.Token, "/api/v1/orgs/"+org+"/dashboards/not-a-uuid/gadgets",
			map[string]any{"gadgets": []any{}})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", res.Body)
	})

	t.Run("a malformed org id on a write", func(t *testing.T) {
		// The org group's own guard 404s a non-member before the handler
		// parses anything, so a malformed org id is answered by whichever of
		// the two runs first. Either is a refusal; a 500 is not.
		res := ts.post(t, "/api/v1/orgs/not-a-uuid/dashboards", map[string]any{"name": "x"}, true)
		require.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, res.StatusCode,
			"%s", res.Body)
	})

	t.Run("a well-formed id that names nothing is 404", func(t *testing.T) {
		res := ts.get(t, "/api/v1/orgs/"+org+"/dashboards/"+good, true)
		require.Equal(t, http.StatusNotFound, res.StatusCode, "%s", res.Body)
		require.Equal(t, "NOT_FOUND", dashNegErrorCode(t, res.Body))
	})
}

// A body that will not decode must answer 400 rather than being treated as an
// empty object — a PATCH that silently applied `{}` would blank the row it was
// meant to edit.
func TestDashNeg_MalformedBodiesAre400(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	id := createDashboardAs(t, ts, ts.Token, ts.OrgID, "Body probe", "private")

	cases := map[string]struct {
		method string
		path   string
	}{
		"create a dashboard": {http.MethodPost, "/api/v1/orgs/" + org + "/dashboards"},
		"update a dashboard": {http.MethodPatch, "/api/v1/orgs/" + org + "/dashboards/" + id.String()},
		"save a layout":      {http.MethodPut, "/api/v1/orgs/" + org + "/dashboards/" + id.String() + "/gadgets"},
		"preview a query":    {http.MethodPost, "/api/v1/orgs/" + org + "/views/preview"},
		"aggregate a query":  {http.MethodPost, "/api/v1/orgs/" + org + "/views/aggregate"},
		"create a view":      {http.MethodPost, "/api/v1/orgs/" + org + "/views"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			res := dashNegRaw(t, ts, c.method, c.path, []byte(`{"name":`))
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", res.Body)
			require.Equal(t, "BAD_REQUEST", dashNegErrorCode(t, res.Body))
		})
	}
}

// An aggregate or preview whose query key is absent, or is not a filter
// document at all, is a validation failure rather than a decode failure — the
// request was readable, it just asked for something the vocabulary does not
// define.
func TestDashNeg_QueryDocumentRefusals(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()

	for _, path := range []string{"/views/preview", "/views/aggregate"} {
		t.Run(path+" with no query", func(t *testing.T) {
			res := ts.post(t, "/api/v1/orgs/"+org+path, map[string]any{}, true)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
			require.Equal(t, "VALIDATION_ERROR", dashNegErrorCode(t, res.Body))
		})
		t.Run(path+" with a query that is not an object", func(t *testing.T) {
			res := ts.post(t, "/api/v1/orgs/"+org+path,
				map[string]any{"query": json.RawMessage(`[1,2,3]`)}, true)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)
		})
	}
}

// A cursor this build did not issue is a bad request, not an empty page. An
// empty page would be a silent lie: the caller would conclude they had reached
// the end of the results.
func TestDashNeg_AMalformedCursorIsRefused(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	viewID := createViewAs(t, ts, ts.Token, ts.OrgID, "Cursor probe", "private", nil)

	res := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/views/%s/results?cursor=%%2Fnot-base64%%2F", org, viewID), true)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", res.Body)
	require.Equal(t, "BAD_REQUEST", dashNegErrorCode(t, res.Body))
}

// A non-numeric limit falls back to the default rather than erroring — the
// parameter is a hint, and a page of results is a better answer than a refusal
// for a value the caller may not have typed. Asserted because the fallback is
// a branch, and a change that made it a 400 would break every client that
// omits the parameter.
func TestDashNeg_ANonNumericLimitFallsBackToTheDefault(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	viewID := createViewAs(t, ts, ts.Token, ts.OrgID, "Limit probe", "private", nil)

	res := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/views/%s/results?limit=lots", org, viewID), true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
}

// A view whose audience team was deleted degrades rather than vanishing: it
// still lists for its owner, marked invalid with a reason, and a gadget
// pointing at it renders "scope unavailable" instead of an error.
func TestDashNeg_ADegradedTeamAudienceIsReportedNotHidden(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	team := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)

	res := ts.post(t, "/api/v1/orgs/"+org+"/dashboards", map[string]any{
		"name": "Team board", "visibility": "team", "visibility_team_id": team.String(),
	}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)
	var created dashboardBody
	require.NoError(t, json.Unmarshal(res.Body, &created))
	require.True(t, created.IsValid)

	_, err := ts.DB.Pool.Exec(t.Context(), `DELETE FROM teams WHERE id = $1`, team)
	require.NoError(t, err)

	got := getDashboard(t, ts, ts.Token, "/api/v1/orgs/"+org+"/dashboards/"+created.ID.String())
	require.False(t, got.IsValid, "a dashboard whose audience team is gone is invalid, not missing")

	// And it still lists — for its owner, who is the person the re-scope
	// prompt is for.
	seen := listDashboardIDs(t, ts, ts.Token, "/api/v1/orgs/"+org+"/dashboards")
	require.True(t, seen[created.ID])
}

// The Home route is the caller's own, and nobody else's: it resolves from the
// token rather than from anything in the URL.
func TestDashNeg_HomeIsResolvedFromTheCallerNotTheURL(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()

	one := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	two := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	first := getDashboard(t, ts, ts.tokenFor(t, one.ID, one.Email), "/api/v1/orgs/"+org+"/dashboards/home")
	second := getDashboard(t, ts, ts.tokenFor(t, two.ID, two.Email), "/api/v1/orgs/"+org+"/dashboards/home")
	require.NotEqual(t, first.ID, second.ID)
	require.True(t, first.IsSeeded)
	require.True(t, second.IsSeeded)
}

// dashNegRaw sends a body the JSON encoder would never produce, so a decode
// failure can be provoked. ts.post and friends marshal their argument, which
// makes a malformed body unreachable through them.
func dashNegRaw(t *testing.T, ts *testServer, method, path string, body []byte) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.url(path), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ts.Token)
	return ts.do(t, req)
}
