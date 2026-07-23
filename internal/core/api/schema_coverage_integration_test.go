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

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// putAs issues an authenticated PUT as the given persona token.
func (ts *testServer) putAs(t *testing.T, token, path string, body any) httpResult {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.url(path), bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// TestCustomFields_DeletePreservesLegacyValuesOverHTTP: deleting a definition
// removes it from the active set but its stored values survive read-only as
// legacy — the zero-data-loss guarantee, exercised through the HTTP surface.
func TestCustomFields_DeletePreservesLegacyValuesOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "CF Delete", "cf-delete", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)
	fieldsBase := fmt.Sprintf("%s/%s/fields", itemsBase, itemID)

	r := ts.post(t, defsBase, map[string]any{"name": "Squad", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create field: %s", r.Body)
	var squad fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &squad))

	r = ts.put(t, fieldsBase+"/squad", map[string]string{"value": "Falcon"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "set value: %s", r.Body)

	// Delete the definition.
	r = ts.delete(t, defsBase+"/"+squad.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete def: %s", r.Body)

	// The value survives read-only as legacy.
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var rendered []renderedFieldDTO
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.True(t, rendered[0].Legacy, "value must be legacy after def deletion")
	require.Equal(t, "Falcon", rendered[0].Value, "no silent data loss")

	// Writing to the deleted (legacy) field is refused.
	r = ts.put(t, fieldsBase+"/squad", map[string]string{"value": "Hawk"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "legacy field is read-only: %s", r.Body)
}

// TestCustomFields_RenameAndValidationErrors: rename keeps the slug; the
// validation and not-found branches answer the right codes.
func TestCustomFields_RenameAndValidationErrors(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)

	r := ts.post(t, defsBase, map[string]any{"name": "Squad", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	var squad fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &squad))

	// Rename → 200, name changes, slug unchanged.
	r = ts.patch(t, defsBase+"/"+squad.ID, map[string]any{"name": "Team"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "rename: %s", r.Body)
	var renamed struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &renamed))
	require.Equal(t, "Team", renamed.Name)
	require.Equal(t, "squad", renamed.Slug)

	// Nothing to update → 400.
	r = ts.patch(t, defsBase+"/"+squad.ID, map[string]any{}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "nothing-to-update: %s", r.Body)

	// Invalid field ID → 400.
	r = ts.patch(t, defsBase+"/not-a-uuid", map[string]any{"name": "X"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid id: %s", r.Body)

	// Unknown (well-formed) field ID → 404.
	r = ts.patch(t, defsBase+"/"+uuid.New().String(), map[string]any{"name": "X"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "unknown id: %s", r.Body)

	// Invalid field type → 400.
	r = ts.post(t, defsBase, map[string]any{"name": "Bad", "field_type": "formula"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid type: %s", r.Body)

	// Duplicate name → 409.
	r = ts.post(t, defsBase, map[string]any{"name": "squad", "field_type": "text"}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "duplicate: %s", r.Body)
}

// TestItemTypes_ValidationAndNotFound covers the item-type error branches.
func TestItemTypes_ValidationAndNotFound(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/item-types", ts.OrgID)

	// Empty name → 400.
	r := ts.post(t, base, map[string]string{"name": "   "}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "empty name: %s", r.Body)

	// Duplicate of a seeded slug → 409.
	r = ts.post(t, base, map[string]string{"name": "Task"}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "duplicate: %s", r.Body)

	// Create one, then exercise the update/delete error branches.
	r = ts.post(t, base, map[string]string{"name": "Spike"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var spike itemTypeDTO
	require.NoError(t, json.Unmarshal(r.Body, &spike))

	// Nothing to update → 400.
	r = ts.patch(t, base+"/"+spike.ID, map[string]any{}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "nothing-to-update: %s", r.Body)

	// Invalid type ID → 400.
	r = ts.patch(t, base+"/not-a-uuid", map[string]any{"name": "X"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid id patch: %s", r.Body)
	r = ts.delete(t, base+"/not-a-uuid", true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid id delete: %s", r.Body)

	// Unknown (well-formed) type ID → 404.
	r = ts.patch(t, base+"/"+uuid.New().String(), map[string]any{"name": "X"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "unknown id: %s", r.Body)
}

// TestCustomFields_MemberCannotSetValueOnUneditableItem: a viewer can reach the
// item but not edit it — setting a field value is an item edit, so it's 403.
func TestCustomFields_MemberCannotSetValueOnUneditableItem(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "CF Perms", "cf-perms", "vector")
	spaceUUID := uuid.MustParse(spaceID)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)

	// Admin defines a field and creates an item (admin is the reporter).
	require.Equal(t, http.StatusCreated,
		ts.post(t, defsBase, map[string]any{"name": "Squad", "field_type": "text"}, true).StatusCode)
	itemID := createItem(t, ts, itemsBase)

	// A viewer can read the space but not edit others' items.
	viewer := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, spaceUUID,
		access.SubjectUser, viewer.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)
	viewerTok := ts.tokenFor(t, viewer.ID, viewer.Email)

	r := ts.putAs(t, viewerTok, fmt.Sprintf("%s/%s/fields/squad", itemsBase, itemID),
		map[string]string{"value": "Falcon"})
	require.Equal(t, http.StatusForbidden, r.StatusCode, "viewer cannot edit: %d %s", r.StatusCode, r.Body)
}

// TestItemKey_ResolveCrossSpaceGuarded: resolution is org-wide but guarded on
// the resolved item's own space — a caller who cannot read that space gets 404,
// never a leak of an item from a space they have no access to.
func TestItemKey_ResolveCrossSpaceGuarded(t *testing.T) {
	ts := newTestServer(t)
	spaceA := createScopedSpace(t, ts, "Space A", "space-a", "vector")
	spaceB := createScopedSpace(t, ts, "Space B", "space-b", "vector")
	itemsA := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceA)
	itemsB := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceB)

	// One item in each space (admin).
	rA := ts.post(t, itemsA, map[string]string{"title": "In A", "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, rA.StatusCode)
	var inA struct {
		ItemKey string `json:"item_key"`
	}
	require.NoError(t, json.Unmarshal(rA.Body, &inA))

	rB := ts.post(t, itemsB, map[string]string{"title": "In B", "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, rB.StatusCode)
	var inB struct {
		ItemKey string `json:"item_key"`
	}
	require.NoError(t, json.Unmarshal(rB.Body, &inB))

	// A member who can read space A only.
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, uuid.MustParse(spaceA),
		access.SubjectUser, member.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)
	memTok := ts.tokenFor(t, member.ID, member.Email)

	// Resolving A's own key via A's URL succeeds (control).
	r := ts.getAs(t, memTok, itemsA+"/resolve?key="+inA.ItemKey)
	require.Equal(t, http.StatusOK, r.StatusCode, "resolve readable item: %s", r.Body)

	// Resolving B's key via A's URL is refused with 404 — the item exists in the
	// org but lives in a space the member cannot read.
	r = ts.getAs(t, memTok, itemsA+"/resolve?key="+inB.ItemKey)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "cross-space resolve must 404: %d %s", r.StatusCode, r.Body)
}
