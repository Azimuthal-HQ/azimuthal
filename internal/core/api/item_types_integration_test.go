package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

type itemTypeDTO struct {
	ID         string  `json:"id"`
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archived_at"`
}

// TestItemTypes_AdminCRUDAndItemValidation covers the item-types admin surface
// end-to-end: the seeded set is present, an org admin can create/rename/archive/
// delete types, item creation validates the chosen type against the org's active
// types, and a referenced type cannot be hard-deleted (409).
func TestItemTypes_AdminCRUDAndItemValidation(t *testing.T) {
	ts := newTestServer(t)
	orgBase := fmt.Sprintf("/api/v1/orgs/%s/item-types", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Types Proj", "types-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)

	// Seeded default set is present.
	r := ts.get(t, orgBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list types: %s", r.Body)
	var seeded []itemTypeDTO
	require.NoError(t, json.Unmarshal(r.Body, &seeded))
	slugs := map[string]bool{}
	for _, s := range seeded {
		slugs[s.Slug] = true
	}
	for _, want := range []string{"task", "story", "bug", "epic"} {
		require.Truef(t, slugs[want], "seeded type %q missing", want)
	}

	// Create a custom type.
	r = ts.post(t, orgBase, map[string]string{"name": "Spike"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create type: %s", r.Body)
	var spike itemTypeDTO
	require.NoError(t, json.Unmarshal(r.Body, &spike))
	require.Equal(t, "spike", spike.Slug)

	// Item creation accepts the new type…
	r = ts.post(t, itemsBase, map[string]string{"title": "Investigate", "kind": "spike", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item with custom type: %s", r.Body)

	// …and rejects an undefined type.
	r = ts.post(t, itemsBase, map[string]string{"title": "Bad", "kind": "nonsense", "priority": "medium"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "undefined type must be rejected: %s", r.Body)

	// A referenced type cannot be hard-deleted — archive it instead (409).
	r = ts.delete(t, orgBase+"/"+spike.ID, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "referenced type delete must 409: %s", r.Body)

	// Rename changes the display name, not the slug.
	r = ts.patch(t, orgBase+"/"+spike.ID, map[string]any{"name": "Investigation"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "rename: %s", r.Body)
	var renamed itemTypeDTO
	require.NoError(t, json.Unmarshal(r.Body, &renamed))
	require.Equal(t, "Investigation", renamed.Name)
	require.Equal(t, "spike", renamed.Slug)

	// Archive it → items can no longer be created with it.
	r = ts.patch(t, orgBase+"/"+spike.ID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "archive: %s", r.Body)
	r = ts.post(t, itemsBase, map[string]string{"title": "After archive", "kind": "spike", "priority": "medium"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "archived type must be rejected on create: %s", r.Body)

	// An unreferenced type deletes cleanly.
	r = ts.post(t, orgBase, map[string]string{"name": "Throwaway"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var throwaway itemTypeDTO
	require.NoError(t, json.Unmarshal(r.Body, &throwaway))
	r = ts.delete(t, orgBase+"/"+throwaway.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "unreferenced delete: %s", r.Body)
}

// TestItemTypes_MemberCannotMutate: non-admins read types but cannot mutate them.
func TestItemTypes_MemberCannotMutate(t *testing.T) {
	ts := newTestServer(t)
	orgBase := fmt.Sprintf("/api/v1/orgs/%s/item-types", ts.OrgID)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)

	// Members can read (pickers/filters need it).
	r := ts.getAs(t, memTok, orgBase)
	require.Equal(t, http.StatusOK, r.StatusCode, "member read: %s", r.Body)

	// But cannot create — org-admin only (orgAdminGuard → 403, matching the
	// teams and workflow mutation convention).
	r = ts.postAs(t, memTok, orgBase, map[string]string{"name": "Sneaky"})
	require.Equal(t, http.StatusForbidden, r.StatusCode, "member create must be refused: %d %s", r.StatusCode, r.Body)
}
