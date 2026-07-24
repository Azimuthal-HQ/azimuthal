package api_test

// Regression coverage for "PATCH an item's assignee returns 400 and blanks the
// title".
//
// Root cause: updateItemRequest declared Title, Description and Priority as
// plain strings, and UpdateItem assigned all of them onto the stored item
// unconditionally. A body that carried only assignee_id therefore decoded a
// title of "", overwrote the real one, and was rejected by the service's
// title-required rule — so the assignee control on item detail, which sends
// assignee_id alone, failed with 400 every time it was used. The same shape
// would have silently blanked description and priority on any partial update
// that did happen to include a title.
//
// The fix makes those fields pointers and applies only what the body carried.
// These tests fail against the old handler: the first on the 400, the rest on
// the fields that came back empty.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// itemsPath returns the item-collection URL for a fresh vector space.
func itemsPath(t *testing.T, ts *testServer) string {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "vector")
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, space.ID)
}

// createItemForPatch posts an item and returns its collection path and id.
func createItemForPatch(t *testing.T, ts *testServer) (string, string) {
	t.Helper()
	base := itemsPath(t, ts)
	res := ts.post(t, base, map[string]any{
		"title":       "Original Title",
		"description": "Original description",
		"kind":        "task",
		"priority":    "high",
	}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", res.Body)

	created := decodeJSONMap(t, res.Body)
	id, ok := created["id"].(string)
	require.True(t, ok, "created item must carry an id: %s", res.Body)
	return base, id
}

func TestUpdateItem_AssigneeOnlyPatchSucceeds(t *testing.T) {
	ts := newTestServer(t)
	base, id := createItemForPatch(t, ts)

	// Exactly what the item-detail assignee control sends.
	res := ts.patch(t, base+"/"+id, map[string]any{"assignee_id": ts.UserID}, true)

	require.Equal(t, http.StatusOK, res.StatusCode,
		"a patch carrying only assignee_id must succeed; body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Equal(t, "Original Title", got["title"], "an omitted title must be left alone, not blanked")
	require.Equal(t, "Original description", got["description"], "an omitted description must be left alone")
	require.Equal(t, "high", got["priority"], "an omitted priority must be left alone")
	require.Equal(t, ts.UserID.String(), got["assignee_id"], "the assignee must actually have changed")
}

func TestUpdateItem_TitleOnlyPatchKeepsOtherFields(t *testing.T) {
	ts := newTestServer(t)
	base, id := createItemForPatch(t, ts)

	res := ts.patch(t, base+"/"+id, map[string]any{"title": "Renamed"}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Equal(t, "Renamed", got["title"])
	require.Equal(t, "Original description", got["description"], "an omitted description must survive a rename")
	require.Equal(t, "high", got["priority"], "an omitted priority must survive a rename")
}

func TestUpdateItem_ExplicitEmptyTitleIsStillRejected(t *testing.T) {
	ts := newTestServer(t)
	base, id := createItemForPatch(t, ts)

	// The negative half: making the fields optional must not make the
	// title-required rule optional. Sending "" deliberately is still invalid —
	// this is what separates "absent" from "empty".
	res := ts.patch(t, base+"/"+id, map[string]any{"title": ""}, true)

	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"an explicitly empty title must still be rejected; body: %s", res.Body)
}

func TestUpdateItem_ExplicitNullAssigneeUnassigns(t *testing.T) {
	ts := newTestServer(t)
	base, id := createItemForPatch(t, ts)

	assign := ts.patch(t, base+"/"+id, map[string]any{"assignee_id": ts.UserID}, true)
	require.Equal(t, http.StatusOK, assign.StatusCode, "body: %s", assign.Body)

	// Unassigning is a real operation, not an omission.
	res := ts.patch(t, base+"/"+id, map[string]any{"assignee_id": nil}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", res.Body)

	got := decodeJSONMap(t, res.Body)
	require.Nil(t, got["assignee_id"], "an explicit null assignee must clear it")
	require.Equal(t, "Original Title", got["title"], "unassigning must not disturb the title")
}
