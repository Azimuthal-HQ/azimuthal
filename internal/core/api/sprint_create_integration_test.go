package api_test

// Integration coverage for the P0 defect "sprint creation returns 400
// invalid request body".
//
// Root cause: the create-sprint dialog posted its <input type="date"> values
// verbatim (bare "YYYY-MM-DD" strings). The handler decodes starts_at/ends_at
// into *time.Time, which accepts only RFC3339 date-times, so the decode failed
// and every dated sprint creation returned 400. The frontend fix converts the
// date inputs to RFC3339 before sending; these tests pin the backend contract
// the frontend must honor, including the exact rejection the defect shipped.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// put issues an authenticated (or not) PUT with a JSON body, mirroring the
// post helper in routes_integration_test.go.
func (ts *testServer) put(t *testing.T, path string, body any, authed bool) httpResult {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.url(path), bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	return ts.do(t, req)
}

// decodeJSONMap unmarshals a JSON object body into a map, failing the test on
// malformed JSON.
func decodeJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body: %s", body)
	return m
}

// decodeErrorObject unmarshals an error response body and returns the nested
// "error" object, failing the test if the envelope is missing.
func decodeErrorObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	m := decodeJSONMap(t, body)
	errObj, ok := m["error"].(map[string]any)
	require.True(t, ok, "error response must have 'error' object, got: %s", body)
	return errObj
}

// sprintCreatePath returns the sprint-creation URL for a fresh project space.
func sprintCreatePath(t *testing.T, ts *testServer) string {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/sprints", ts.OrgID, space.ID)
}

// requireErrorShape asserts the documented 400 error envelope:
// {"error":{"code":"...","message":"..."}} with a JSON content type.
func requireErrorShape(t *testing.T, r httpResult, wantCode string) {
	t.Helper()
	require.Contains(t, r.ContentType, "application/json", "body: %s", r.Body)
	errObj := decodeErrorObject(t, r.Body)
	require.Equal(t, wantCode, errObj["code"], "body: %s", r.Body)
	require.NotEmpty(t, errObj["message"], "body: %s", r.Body)
}

// TestIntegration_SprintCreate_WithRFC3339Dates_Succeeds covers the success
// path: RFC3339 date-times are accepted and round-trip through the response.
func TestIntegration_SprintCreate_WithRFC3339Dates_Succeeds(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	starts := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	r := ts.post(t, path, map[string]any{
		"name":      "Sprint 1",
		"goal":      "Ship the thing",
		"starts_at": starts.Format(time.RFC3339),
		"ends_at":   ends.Format(time.RFC3339),
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)

	body := decodeJSONMap(t, r.Body)
	require.NotEmpty(t, body["id"])
	require.Equal(t, "Sprint 1", body["name"])
	require.Equal(t, "Ship the thing", body["goal"])
	require.Equal(t, "planned", body["status"])

	// Wire format is lowercase snake_case; the dates must round-trip.
	gotStarts, err := time.Parse(time.RFC3339, body["starts_at"].(string))
	require.NoError(t, err)
	require.True(t, gotStarts.Equal(starts), "starts_at round-trip: got %v want %v", gotStarts, starts)
	gotEnds, err := time.Parse(time.RFC3339, body["ends_at"].(string))
	require.NoError(t, err)
	require.True(t, gotEnds.Equal(ends), "ends_at round-trip: got %v want %v", gotEnds, ends)
}

// TestIntegration_SprintCreate_WithoutDates_Succeeds covers the minimal
// payload: dates are optional and may be omitted entirely.
func TestIntegration_SprintCreate_WithoutDates_Succeeds(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	r := ts.post(t, path, map[string]any{"name": "Undated Sprint"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)

	body := decodeJSONMap(t, r.Body)
	require.Equal(t, "Undated Sprint", body["name"])
	require.Equal(t, "planned", body["status"])
	require.Nil(t, body["starts_at"])
	require.Nil(t, body["ends_at"])
}

// TestIntegration_SprintCreate_MissingName_Returns400 covers the
// missing-required-field rejection with the documented error shape.
func TestIntegration_SprintCreate_MissingName_Returns400(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	r := ts.post(t, path, map[string]any{"goal": "a sprint with no name"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
	requireErrorShape(t, r, "VALIDATION_ERROR")
}

// TestIntegration_SprintCreate_WrongFieldType_Returns400 covers the
// wrong-field-type rejection: a numeric starts_at cannot decode into a
// timestamp and must produce the documented 400 shape.
func TestIntegration_SprintCreate_WrongFieldType_Returns400(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	r := ts.post(t, path, map[string]any{"name": "Sprint 1", "starts_at": 12345}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
	requireErrorShape(t, r, "BAD_REQUEST")
}

// TestSprintCreate_RejectsDateOnlyString_Regression pins the exact payload the
// pre-fix frontend sent — bare "YYYY-MM-DD" date strings from <input
// type="date"> — which the RFC3339 contract rejects with the documented 400
// shape. If this ever starts succeeding, the API contract changed and the
// frontend conversion (SprintsPage toRFC3339) should be revisited together
// with it.
func TestSprintCreate_RejectsDateOnlyString_Regression(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	r := ts.post(t, path, map[string]any{
		"name":      "Sprint 1",
		"starts_at": "2026-07-20",
		"ends_at":   "2026-08-03",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
	requireErrorShape(t, r, "BAD_REQUEST")
}

// --- Sibling routes sharing the sprint handler (blast radius of the fix) ---
//
// The Sprints page journey the fixed dialog lives in also lists sprints,
// reads the active sprint, and starts/completes them through the same
// handler. These pin those paths against the real database.

// TestIntegration_SprintList_ReturnsCreatedSprints covers the list read the
// Sprints page performs immediately after a create.
func TestIntegration_SprintList_ReturnsCreatedSprints(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	for _, name := range []string{"Sprint A", "Sprint B"} {
		r := ts.post(t, path, map[string]any{"name": name}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "create %q: %s", name, r.Body)
	}

	r := ts.get(t, path, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list: %s", r.Body)
	var list []map[string]any
	requireJSONList(t, r.Body, &list)
	require.Len(t, list, 2)
	names := []string{list[0]["name"].(string), list[1]["name"].(string)}
	require.ElementsMatch(t, []string{"Sprint A", "Sprint B"}, names)
}

// TestIntegration_SprintLifecycle_StartAndComplete covers the start/complete
// transitions and the active-sprint read used by the Sprints page.
func TestIntegration_SprintLifecycle_StartAndComplete(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	// No active sprint yet.
	r := ts.get(t, path+"/active", true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "active before start: %s", r.Body)

	r = ts.post(t, path, map[string]any{"name": "Lifecycle Sprint"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	sprintID := decodeJSONMap(t, r.Body)["id"].(string)

	// Start: planned → active, and the active read now returns it.
	r = ts.post(t, fmt.Sprintf("%s/%s/start", path, sprintID), nil, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "start: %s", r.Body)
	require.Equal(t, "active", decodeJSONMap(t, r.Body)["status"])

	r = ts.get(t, path+"/active", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "active after start: %s", r.Body)
	require.Equal(t, sprintID, decodeJSONMap(t, r.Body)["id"])

	// Complete: active → completed, persisted on a fresh read.
	r = ts.post(t, fmt.Sprintf("%s/%s/complete", path, sprintID), nil, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "complete: %s", r.Body)
	require.Equal(t, "completed", decodeJSONMap(t, r.Body)["status"])

	r = ts.get(t, fmt.Sprintf("%s/%s", path, sprintID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "completed", decodeJSONMap(t, r.Body)["status"])
}

// TestIntegration_SprintUpdate_SharesDateContract covers the update route,
// which decodes the same RFC3339 date shape as create: valid date-times are
// accepted and persisted, bare dates are rejected with the documented shape.
func TestIntegration_SprintUpdate_SharesDateContract(t *testing.T) {
	ts := newTestServer(t)
	path := sprintCreatePath(t, ts)

	r := ts.post(t, path, map[string]any{"name": "Before Update"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	sprintID := decodeJSONMap(t, r.Body)["id"].(string)

	starts := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r = ts.put(t, fmt.Sprintf("%s/%s", path, sprintID), map[string]any{
		"name":      "After Update",
		"goal":      "Updated goal",
		"starts_at": starts.Format(time.RFC3339),
	}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "update: %s", r.Body)
	body := decodeJSONMap(t, r.Body)
	require.Equal(t, "After Update", body["name"])
	require.Equal(t, "Updated goal", body["goal"])

	r = ts.put(t, fmt.Sprintf("%s/%s", path, sprintID), map[string]any{
		"name":      "After Update",
		"starts_at": "2026-09-01",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "bare-date update: %s", r.Body)
	requireErrorShape(t, r, "BAD_REQUEST")
}
