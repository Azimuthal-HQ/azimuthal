package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// THE VECTOR HALF OF THE AGGREGATE FAN-OUT.
//
// /views/aggregate has two halves — one per module — and until this file only
// the Beacon one was ever driven end to end. The halves are not symmetric:
// project_items carries a `kind` column that tickets do not, which is why
// GroupKind exists, why AllowedFor refuses it alongside Beacon, and why the two
// breakdown queries are two queries rather than one.
//
// So the Beacon tests do not stand in for these. A vector count that returned
// the wrong number, a vector breakdown that grouped on the wrong column, or a
// cross-module merge that dropped one half's buckets would all have passed the
// existing suite untouched.
//
// Everything here goes through HTTP, for the reason the invariant tests do: the
// per-viewer resolution is the point, and it only exists on that path.

const (
	vectorOnlyQuery = `{"v":1,"filter":{"modules":["vector"]},` +
		`"sort":{"field":"updated_at","dir":"desc"}}`
	bothModulesQuery = `{"v":1,"filter":{"modules":["beacon","vector"]},` +
		`"sort":{"field":"updated_at","dir":"desc"}}`
)

// seedProjectItem creates one project item with an explicit kind and priority,
// so a breakdown has something to group on.
func seedProjectItem(t *testing.T, ts *testServer, spaceID, title, kind, priority string) {
	t.Helper()
	res := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID),
		map[string]any{"title": title, "kind": kind, "priority": priority}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "seed item: %s", res.Body)
}

// A count over a Vector view is counted in the database, and a breakdown by
// `kind` groups on the column only project_items has.
//
// Fails-before: point the vector half at CountTickets/BreakdownTickets — the
// halves take the identical function shape, so the swap compiles — and the
// total drops to the ticket count and every bucket disappears.
func TestAggregateVector_CountsAndGroupsProjectItems(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Vector Aggregates", "vector-aggregates", "vector")

	seedProjectItem(t, ts, spaceID, "Bug one", "bug", "high")
	seedProjectItem(t, ts, spaceID, "Bug two", "bug", "low")
	seedProjectItem(t, ts, spaceID, "A task", "task", "high")

	query := json.RawMessage(vectorOnlyQuery)

	total, buckets := aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "")
	require.EqualValues(t, 3, total, "the count is the whole set, not a page of it")
	require.Empty(t, buckets, "no group_by asked for means no buckets")

	total, buckets = aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "kind")
	require.EqualValues(t, 3, total)
	require.Equal(t, map[string]int64{"bug": 2, "task": 1}, buckets,
		"kind is the vector-only column — grouping on it is the whole reason GroupKind exists")

	// Priority groups on both modules, so it is the field that proves the
	// vector half is reached rather than the beacon one standing in for it.
	total, buckets = aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "priority")
	require.EqualValues(t, 3, total)
	require.Equal(t, map[string]int64{"high": 2, "low": 1}, buckets)
}

// A view naming both modules sums the two counts and merges the two bucket
// sets by key. ADR-0009 requires the merge to happen in the API layer rather
// than by unifying the tables (ADR-0003), so this is the assertion that the
// merge exists at all.
//
// Fails-before: drop mergeBuckets' summing branch and the shared `high` bucket
// reports one module's count instead of both.
func TestAggregateVector_ACrossModuleBreakdownSumsBothHalves(t *testing.T) {
	ts := newTestServer(t)
	beaconSpace := createScopedSpace(t, ts, "Merge Desk", "merge-desk", "beacon")
	vectorSpace := createScopedSpace(t, ts, "Merge Proj", "merge-proj", "vector")

	// seedTicket posts priority "high".
	seedTicket(t, ts, beaconSpace, "Ticket one", nil)
	seedTicket(t, ts, beaconSpace, "Ticket two", nil)
	seedProjectItem(t, ts, vectorSpace, "Item high", "task", "high")
	seedProjectItem(t, ts, vectorSpace, "Item low", "task", "low")

	query := json.RawMessage(bothModulesQuery)

	total, _ := aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "")
	require.EqualValues(t, 4, total, "both halves are counted, and the totals add")

	total, buckets := aggregateTotal(t, ts, ts.Token, ts.OrgID, query, "priority")
	require.EqualValues(t, 4, total)
	require.Equal(t, map[string]int64{"high": 3, "low": 1}, buckets,
		"`high` spans both modules, so its bucket is the sum and not either half")

	var sum int64
	for _, n := range buckets {
		sum += n
	}
	require.EqualValues(t, total, sum, "the buckets must still account for every counted row")
}

// `kind` alongside Beacon is refused rather than answered with every ticket in
// an untyped bucket — the same rule, and the same reasoning, as naming `kinds`
// in a filter alongside Beacon.
//
// Fails-before: delete AllowedFor's check and this answers 200 with a bucket
// whose key is the empty string, which reads as data rather than as a mistake.
func TestAggregateVector_KindAlongsideBeaconIsRefused(t *testing.T) {
	ts := newTestServer(t)
	createScopedSpace(t, ts, "Refusal Desk", "refusal-desk", "beacon")

	res := ts.postAs(t, ts.Token, viewsPath(ts.OrgID)+"/aggregate", map[string]any{
		"query":    json.RawMessage(bothModulesQuery),
		"group_by": "kind",
	})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
		"a kind breakdown across both modules is a validation error: %s", res.Body)

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &env))
	require.Equal(t, "VALIDATION_ERROR", env.Error.Code)
	require.Contains(t, env.Error.Message, "Vector",
		"the refusal names the module it applies to, so the author can act on it")

	// Vector alone is fine — the refusal is about the combination, not the field.
	res = ts.postAs(t, ts.Token, viewsPath(ts.OrgID)+"/aggregate", map[string]any{
		"query":    json.RawMessage(vectorOnlyQuery),
		"group_by": "kind",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
}

// A viewer who can reach no space and holds no share is answered zero without a
// round trip. Worth its own case on the vector side: the short-circuit consults
// SharedItemIDs there and SharedTicketIDs on the beacon side, so a copy-paste
// slip between the two halves would leak a count across the boundary.
func TestAggregateVector_AViewerWhoCanReachNothingIsAnsweredZero(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Private Proj", "private-proj", "vector")
	seedProjectItem(t, ts, spaceID, "Owner only", "task", "high")

	stranger := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	strangerTok := ts.tokenFor(t, stranger.ID, stranger.Email)

	total, _ := aggregateTotal(t, ts, ts.Token, ts.OrgID, json.RawMessage(vectorOnlyQuery), "")
	require.EqualValues(t, 1, total, "premise: the owner sees their own item")

	total, buckets := aggregateTotal(t, ts, strangerTok, ts.OrgID, json.RawMessage(vectorOnlyQuery), "kind")
	require.Zero(t, total, "a viewer with no readable space and no share counts nothing")
	require.Empty(t, buckets)
}
