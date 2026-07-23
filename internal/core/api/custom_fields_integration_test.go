package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

type fieldDefDTO struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Type string `json:"field_type"`
}

type renderedFieldDTO struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Type   string `json:"field_type"`
	Value  string `json:"value"`
	Legacy bool   `json:"legacy"`
}

func createItem(t *testing.T, ts *testServer, itemsBase string) string {
	t.Helper()
	r := ts.post(t, itemsBase, map[string]string{"title": "Item", "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	var it struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &it))
	return it.ID
}

// TestCustomFields_DefsValuesAndLegacy exercises the full custom-fields flow:
// admin defines fields, item values are validated by type and persisted, and a
// value whose definition is archived survives read-only as a legacy field.
func TestCustomFields_DefsValuesAndLegacy(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Fields Proj", "fields-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)
	fieldsBase := fmt.Sprintf("%s/%s/fields", itemsBase, itemID)

	// Define a number field and a single-select field.
	r := ts.post(t, defsBase, map[string]any{"name": "Story Points", "field_type": "number"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create number field: %s", r.Body)
	var points fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &points))
	require.Equal(t, "story_points", points.Slug)

	r = ts.post(t, defsBase, map[string]any{"name": "Tier", "field_type": "single_select", "options": []string{"gold", "silver"}}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create select field: %s", r.Body)
	var tier fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &tier))

	// A single_select with no options is rejected.
	r = ts.post(t, defsBase, map[string]any{"name": "Empty Select", "field_type": "single_select"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "select without options must 400: %s", r.Body)

	// The item shows both active fields, unset.
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "get item fields: %s", r.Body)
	var rendered []renderedFieldDTO
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 2)

	// Number field: non-numeric rejected, numeric accepted.
	r = ts.put(t, fieldsBase+"/story_points", map[string]string{"value": "big"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "non-numeric must 400: %s", r.Body)
	r = ts.put(t, fieldsBase+"/story_points", map[string]string{"value": "8"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "numeric value: %s", r.Body)

	// Select field: invalid option rejected, valid accepted.
	r = ts.put(t, fieldsBase+"/tier", map[string]string{"value": "bronze"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid option must 400: %s", r.Body)
	r = ts.put(t, fieldsBase+"/tier", map[string]string{"value": "gold"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "valid option: %s", r.Body)

	// Values are reflected on read.
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	values := map[string]string{}
	for _, f := range rendered {
		values[f.Slug] = f.Value
	}
	require.Equal(t, "8", values["story_points"])
	require.Equal(t, "gold", values["tier"])

	// Archive the points field → its value survives read-only as legacy.
	r = ts.patch(t, defsBase+"/"+points.ID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "archive: %s", r.Body)

	r = ts.get(t, fieldsBase, true)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	var legacy *renderedFieldDTO
	for i := range rendered {
		if rendered[i].Slug == "story_points" {
			legacy = &rendered[i]
		}
	}
	require.NotNil(t, legacy, "archived field's value must still render")
	require.True(t, legacy.Legacy, "archived field must be marked legacy (read-only)")
	require.Equal(t, "8", legacy.Value, "no silent data loss")

	// Writing to the now-legacy field is refused (404 — undefined active field).
	r = ts.put(t, fieldsBase+"/story_points", map[string]string{"value": "13"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "legacy field must be read-only: %s", r.Body)
}

// TestCustomFields_MemberPermissions: members read definitions but cannot
// define them; they can set values on items they may edit.
func TestCustomFields_MemberPermissions(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)

	// Member reads definitions.
	r := ts.getAs(t, memTok, defsBase)
	require.Equal(t, http.StatusOK, r.StatusCode, "member read defs: %s", r.Body)

	// Member cannot create a definition (org-admin only → 403).
	r = ts.postAs(t, memTok, defsBase, map[string]any{"name": "Sneaky", "field_type": "text"})
	require.Equal(t, http.StatusForbidden, r.StatusCode, "member create def must 403: %d %s", r.StatusCode, r.Body)
}
