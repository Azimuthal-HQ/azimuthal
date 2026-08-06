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
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Type     string `json:"field_type"`
	Value    string `json:"value"`
	Required bool   `json:"required"`
	Legacy   bool   `json:"legacy"`
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

func createTicket(t *testing.T, ts *testServer, ticketsBase string) string {
	t.Helper()
	r := ts.post(t, ticketsBase, map[string]string{"title": "Ticket", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var tk struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &tk))
	return tk.ID
}

// attachField scopes a field to a (space, entity type) form — the step that
// puts a definition on a form at all, since scopes govern rendering and
// writes (migration 053). required rides the same call.
func attachField(t *testing.T, ts *testServer, fieldID, spaceID, entityType string, required bool) {
	t.Helper()
	r := ts.put(t, fmt.Sprintf("/api/v1/orgs/%s/custom-fields/%s/scopes/%s/%s", ts.OrgID, fieldID, spaceID, entityType),
		map[string]bool{"required": required}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "attach field: %s", r.Body)
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

	// A new definition appears on no form until it is attached — there is no
	// "unscoped means everywhere" fallback.
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "get item fields: %s", r.Body)
	var rendered []renderedFieldDTO
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Empty(t, rendered, "an unattached definition must not render on any form")

	// Attach both to this space's item form; now the item shows them, unset.
	attachField(t, ts, points.ID, spaceID, "project_item", false)
	attachField(t, ts, tier.ID, spaceID, "project_item", false)
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "get item fields: %s", r.Body)
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

	// Scopes are org-admin in both directions — the rows carry space ids, so
	// even the read would disclose which private spaces a field is attached
	// to. A member is refused on list and on attach alike.
	def := ts.post(t, defsBase, map[string]any{"name": "Env", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, def.StatusCode, "%s", def.Body)
	var d fieldDefDTO
	require.NoError(t, json.Unmarshal(def.Body, &d))
	spaceID := createScopedSpace(t, ts, "Scopes Perm", "scopes-perm", "vector")

	r = ts.getAs(t, memTok, defsBase+"/"+d.ID+"/scopes")
	require.Equal(t, http.StatusForbidden, r.StatusCode, "member list scopes must 403: %d %s", r.StatusCode, r.Body)
	r = ts.putAs(t, memTok, fmt.Sprintf("%s/%s/scopes/%s/project_item", defsBase, d.ID, spaceID),
		map[string]bool{"required": true})
	require.Equal(t, http.StatusForbidden, r.StatusCode, "member attach scope must 403: %d %s", r.StatusCode, r.Body)
	r = ts.deleteAs(t, memTok, fmt.Sprintf("%s/%s/scopes/%s/project_item", defsBase, d.ID, spaceID))
	require.Equal(t, http.StatusForbidden, r.StatusCode, "member detach scope must 403: %d %s", r.StatusCode, r.Body)
}

// S12 — a new definition may not reuse a slug that still holds legacy values.
//
// Values are stored by slug and deliberately outlive their definitions
// (migration 033): deleting a field leaves its values behind, rendered
// read-only as legacy fields, so nothing is silently lost. The other half of
// that design was missing. Defining a NEW field whose name derives to the same
// slug adopted every orphaned value at once — values entered under a different
// field's meaning, and under a different type, appearing as the new field's
// values, already populated, having never been through its validation.
//
// Before the fix this returned 201 and the item below showed "8" as the value
// of a single-select field whose options are gold and silver. It now returns
// 409 and names the collision.
func TestCustomFields_NewDefCannotReuseASlugThatHoldsLegacyValues(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Reuse Proj", "reuse-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)
	fieldsBase := fmt.Sprintf("%s/%s/fields", itemsBase, itemID)

	// A number field, attached here, with a value on one item.
	r := ts.post(t, defsBase, map[string]any{"name": "Story Points", "field_type": "number"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	var points fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &points))
	attachField(t, ts, points.ID, spaceID, "project_item", false)

	r = ts.put(t, fieldsBase+"/story_points", map[string]string{"value": "8"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "set value: %s", r.Body)

	// Delete the definition. The value stays, as a legacy field.
	r = ts.delete(t, defsBase+"/"+points.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete def: %s", r.Body)

	var rendered []renderedFieldDTO
	r = ts.get(t, fieldsBase, true)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.True(t, rendered[0].Legacy, "the orphaned value must render as legacy")
	require.Equal(t, "8", rendered[0].Value)

	// Now define a field of a DIFFERENT type whose name derives to the same
	// slug. This is the adoption that must not happen.
	r = ts.post(t, defsBase, map[string]any{
		"name": "story points", "field_type": "single_select",
		"options": []string{"gold", "silver"},
	}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode,
		"a definition whose slug still holds legacy values must be refused: %s", r.Body)

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.Contains(t, body.Error.Message, "story_points",
		"the refusal must name the slug it collided on")
	require.Contains(t, body.Error.Message, "1 item",
		"the refusal must say how much is in the way, so the admin can find it")

	// And the legacy value is untouched — still legacy, still 8.
	r = ts.get(t, fieldsBase, true)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.True(t, rendered[0].Legacy)
	require.Equal(t, "8", rendered[0].Value)
}

// The guard is about ORPHANED values, not about the name. Once the legacy
// values are cleared, the name is free again — otherwise a slug would be
// permanently poisoned by a field that no longer exists and holds nothing.
func TestCustomFields_SlugIsFreeAgainOnceLegacyValuesAreGone(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Free Proj", "free-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)
	fieldsBase := fmt.Sprintf("%s/%s/fields", itemsBase, itemID)

	r := ts.post(t, defsBase, map[string]any{"name": "Tier", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var tier fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &tier))
	attachField(t, ts, tier.ID, spaceID, "project_item", false)

	// Clear the value BEFORE deleting the definition — an empty value deletes
	// the row, which is the supported way to leave nothing behind.
	r = ts.put(t, fieldsBase+"/tier", map[string]string{"value": "gold"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	r = ts.put(t, fieldsBase+"/tier", map[string]string{"value": ""}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	r = ts.delete(t, defsBase+"/"+tier.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)

	r = ts.post(t, defsBase, map[string]any{"name": "Tier", "field_type": "single_select",
		"options": []string{"gold", "silver"}}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"a slug with no values behind it must be reusable: %s", r.Body)
}

// The guard is org-scoped. Another org's item holding the same slug is not
// this org's conflict — item_field_values has no org column, so the scoping
// lives in a join through project_items.org_id, which is exactly the kind of
// thing that is wrong until a real database says otherwise.
func TestCustomFields_SlugReuseGuardIsOrgScoped(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Scoped Proj", "scoped-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)

	// A value under "story_points" belonging to a DIFFERENT org's item is
	// written directly: reaching it through the API would need a second org's
	// whole fixture, and the thing under test is the join, not the route.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	otherUser := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, ts.DB.Pool, otherOrg.ID, otherUser.ID, "vector")

	var otherItemID string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`INSERT INTO project_items (space_id, org_id, number, kind, title, reporter_id, item_key)
		 VALUES ($1, $2, 1, 'task', 'Other org item', $3, 'OTHER-1') RETURNING id`,
		otherSpace.ID, otherOrg.ID, otherUser.ID).Scan(&otherItemID))
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO entity_field_values (entity_type, entity_id, field_slug, value)
		 VALUES ('project_item', $1, 'story_points', '99')`,
		otherItemID)
	require.NoError(t, err)

	// This org may still define story_points: the other org's value is not
	// visible to it and cannot be adopted by it.
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	r := ts.post(t, defsBase, map[string]any{"name": "Story Points", "field_type": "number"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"another org's legacy value must not block this org's field: %s", r.Body)
	var points fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &points))
	attachField(t, ts, points.ID, spaceID, "project_item", false)

	// And this org's item sees nothing of it.
	r = ts.get(t, fmt.Sprintf("%s/%s/fields", itemsBase, itemID), true)
	var rendered []renderedFieldDTO
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.Empty(t, rendered[0].Value, "the other org's value must not leak in")
}
