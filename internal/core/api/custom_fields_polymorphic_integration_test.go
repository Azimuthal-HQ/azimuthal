package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The A2 (v0.4.2) surface: polymorphic field values over tickets and items,
// scopes, and required-at-the-write — proved over the full production router
// and a real database.

// A ticket holds, reads back and clears a custom field value end to end: the
// capability migration 053 exists for, exercised through the API alone.
func TestCustomFields_TicketHoldsReadsBackAndClearsAValue(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Desk", "desk-fields", "beacon")
	ticketsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID)
	ticketID := createTicket(t, ts, ticketsBase)
	fieldsBase := fmt.Sprintf("%s/%s/fields", ticketsBase, ticketID)

	r := ts.post(t, defsBase, map[string]any{"name": "Environment", "field_type": "single_select", "options": []string{"prod", "dev"}}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create def: %s", r.Body)
	var env fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &env))
	attachField(t, ts, env.ID, spaceID, "ticket", false)

	// The ticket form shows the field, unset.
	var rendered []renderedFieldDTO
	r = ts.get(t, fieldsBase, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "get ticket fields: %s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.Equal(t, "environment", rendered[0].Slug)
	require.Empty(t, rendered[0].Value)

	// Type validation holds on the ticket path exactly as on items.
	r = ts.put(t, fieldsBase+"/environment", map[string]string{"value": "staging"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "invalid option must 400: %s", r.Body)

	// Hold...
	r = ts.put(t, fieldsBase+"/environment", map[string]string{"value": "prod"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "set: %s", r.Body)
	// ...read back...
	r = ts.get(t, fieldsBase, true)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.Equal(t, "prod", rendered[0].Value)
	// ...and clear.
	r = ts.put(t, fieldsBase+"/environment", map[string]string{"value": ""}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "clear: %s", r.Body)
	r = ts.get(t, fieldsBase, true)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.Empty(t, rendered[0].Value, "a cleared value reads back absent")
}

// The write-path authorization, as its own named test. An upsert and a clear
// addressed at an entity in a space the caller cannot read affect zero rows
// and answer 404, with an error envelope byte-identical to the one a
// nonexistent entity gets — no oracle. Before this track, the two write
// statements carried no space predicate at all; the handler's own resolve
// was the entire authorization.
func TestCustomFields_CrossSpaceWritesAffectNothingAndAnswerTheAbsentEnvelope(t *testing.T) {
	f := newScopeFixture(t)

	storedValue := func(entityType string, entityID uuid.UUID) string {
		var v string
		err := f.ts.DB.Pool.QueryRow(t.Context(),
			`SELECT value FROM entity_field_values WHERE entity_type = $1 AND entity_id = $2 AND field_slug = 'probe'`,
			entityType, entityID).Scan(&v)
		require.NoError(t, err)
		return v
	}

	cases := []struct {
		name       string
		urlSpace   uuid.UUID
		entityType string
		farEntity  uuid.UUID
		secret     string
		path       func(space, entity uuid.UUID) string
	}{
		{
			name: "item", urlSpace: f.spaceA.ID, entityType: "project_item",
			farEntity: f.itemB, secret: secretField,
			path: func(space, entity uuid.UUID) string {
				return f.base(space) + "/projects/items/" + entity.String() + "/fields/probe"
			},
		},
		{
			name: "ticket", urlSpace: f.beaconA.ID, entityType: "ticket",
			farEntity: f.ticketB, secret: secretTicketField,
			path: func(space, entity uuid.UUID) string {
				return f.base(space) + "/tickets/" + entity.String() + "/fields/probe"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := storedValue(tc.entityType, tc.farEntity)

			// The upsert, addressed through a space the caller CAN write.
			crossed := f.ts.putAs(t, f.memberTok, tc.path(tc.urlSpace, tc.farEntity), map[string]string{"value": "OVERWRITTEN"})
			require.Equal(t, http.StatusNotFound, crossed.StatusCode, "body: %s", crossed.Body)
			require.NotContains(t, string(crossed.Body), tc.secret)

			// Byte-identical to the envelope for an id that names nothing.
			absent := f.ts.putAs(t, f.memberTok, tc.path(tc.urlSpace, uuid.New()), map[string]string{"value": "OVERWRITTEN"})
			require.Equal(t, absent.StatusCode, crossed.StatusCode,
				"an existing-but-unreadable entity must not be distinguishable by status")
			require.Equal(t, withoutRequestID(t, absent.Body), withoutRequestID(t, crossed.Body),
				"an existing-but-unreadable entity must not be distinguishable by body")

			// Zero rows affected: the far value is untouched.
			require.Equal(t, before, storedValue(tc.entityType, tc.farEntity),
				"a refused upsert must not have written")

			// The clear direction: an empty value is the delete path, and it
			// must refuse the same way with the same envelope.
			cleared := f.ts.putAs(t, f.memberTok, tc.path(tc.urlSpace, tc.farEntity), map[string]string{"value": ""})
			require.Equal(t, http.StatusNotFound, cleared.StatusCode)
			require.Equal(t, withoutRequestID(t, absent.Body), withoutRequestID(t, cleared.Body))
			require.Equal(t, before, storedValue(tc.entityType, tc.farEntity),
				"a refused clear must not have deleted")
		})
	}
}

// Required, both directions, over the wire. The write that omits (clears) a
// required field is refused with an error the UI can render against the
// field; a read of a pre-existing entity missing the value succeeds with the
// value absent — the never-retroactively rule under test.
func TestCustomFields_RequiredRefusesTheWriteAndNeverTheRead(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Req Proj", "req-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	filled := createItem(t, ts, itemsBase)
	incomplete := createItem(t, ts, itemsBase)

	r := ts.post(t, defsBase, map[string]any{"name": "Severity", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var sev fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &sev))
	attachField(t, ts, sev.ID, spaceID, "project_item", false)

	r = ts.put(t, fmt.Sprintf("%s/%s/fields/severity", itemsBase, filled), map[string]string{"value": "high"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	// Flip the attachment to required AFTER one item holds a value and one
	// does not. The flip itself must reject nothing — that is the point.
	attachField(t, ts, sev.ID, spaceID, "project_item", true)

	// Refused direction: clearing the required field.
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/severity", itemsBase, filled), map[string]string{"value": ""}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "clearing a required field must 400: %s", r.Body)
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &envelope))
	require.Equal(t, "VALIDATION_ERROR", envelope.Error.Code)
	require.Contains(t, envelope.Error.Message, "severity",
		"the refusal must name the field so the form can render it against the control")

	// Never-retroactive direction: the incomplete item reads back fine — the
	// value absent, the flag on the control, no error, no synthesized default.
	r = ts.get(t, fmt.Sprintf("%s/%s/fields", itemsBase, incomplete), true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"a pre-existing row missing a required value must still read: %s", r.Body)
	var rendered []renderedFieldDTO
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 1)
	require.True(t, rendered[0].Required, "the render carries the required flag")
	require.Empty(t, rendered[0].Value, "the absent value surfaces as absent")

	// Supplying a value is always accepted.
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/severity", itemsBase, incomplete), map[string]string{"value": "low"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
}

// A field required in space A and not scoped to space B is not required in
// space B: requiredness is a property of one attachment, and an attachment
// governs exactly one (space, entity type) form.
func TestCustomFields_RequiredDoesNotLeakAcrossSpaces(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceA := createScopedSpace(t, ts, "Req A", "req-a", "vector")
	spaceB := createScopedSpace(t, ts, "Req B", "req-b", "vector")
	spaceC := createScopedSpace(t, ts, "Req C", "req-c", "vector")
	itemIn := func(space string) (string, string) {
		base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, space)
		return base, createItem(t, ts, base)
	}
	baseA, itemA := itemIn(spaceA)
	baseB, itemB := itemIn(spaceB)
	baseC, itemC := itemIn(spaceC)

	r := ts.post(t, defsBase, map[string]any{"name": "Env", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var env fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &env))
	attachField(t, ts, env.ID, spaceA, "project_item", true)  // required here
	attachField(t, ts, env.ID, spaceB, "project_item", false) // attached, not required
	// spaceC: not attached at all.

	seed := func(base, item string) {
		r := ts.put(t, fmt.Sprintf("%s/%s/fields/env", base, item), map[string]string{"value": "prod"}, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	}
	seed(baseA, itemA)
	seed(baseB, itemB)

	// A refuses the clear; B allows the identical request.
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/env", baseA, itemA), map[string]string{"value": ""}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "required in A: %s", r.Body)
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/env", baseB, itemB), map[string]string{"value": ""}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "not required in B: %s", r.Body)

	// C, where the field is not scoped: it appears on no form and takes no
	// writes — refused as unattached, which is not a requiredness refusal.
	r = ts.get(t, fmt.Sprintf("%s/%s/fields", baseC, itemC), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Contains(t, []string{"[]", "[]\n"}, string(r.Body),
		"a field scoped elsewhere must not appear on C's form")
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/env", baseC, itemC), map[string]string{"value": "prod"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "unattached field must not be writable: %s", r.Body)
}

// The D48 property — values survive their definitions — now over both entity
// types: archiving and then deleting a definition leaves the item's AND the
// ticket's stored values readable (legacy, read-only), and the write path
// refuses both.
func TestCustomFields_ValuesSurviveTheirDefinitionOnBothEntityTypes(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	vectorID := createScopedSpace(t, ts, "D48 Vec", "d48-vec", "vector")
	beaconID := createScopedSpace(t, ts, "D48 Desk", "d48-desk", "beacon")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, vectorID)
	ticketsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, beaconID)
	itemID := createItem(t, ts, itemsBase)
	ticketID := createTicket(t, ts, ticketsBase)
	itemFields := fmt.Sprintf("%s/%s/fields", itemsBase, itemID)
	ticketFields := fmt.Sprintf("%s/%s/fields", ticketsBase, ticketID)

	r := ts.post(t, defsBase, map[string]any{"name": "Squad", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var squad fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &squad))
	attachField(t, ts, squad.ID, vectorID, "project_item", false)
	attachField(t, ts, squad.ID, beaconID, "ticket", false)

	r = ts.put(t, itemFields+"/squad", map[string]string{"value": "Falcon"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	r = ts.put(t, ticketFields+"/squad", map[string]string{"value": "Hawk"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	assertLegacy := func(fieldsURL, want string) {
		t.Helper()
		var rendered []renderedFieldDTO
		res := ts.get(t, fieldsURL, true)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		require.NoError(t, json.Unmarshal(res.Body, &rendered))
		require.Len(t, rendered, 1)
		require.True(t, rendered[0].Legacy, "the surviving value must render read-only")
		require.Equal(t, want, rendered[0].Value, "no silent data loss")
	}

	// Archive → both surfaces keep the value, read-only.
	r = ts.patch(t, defsBase+"/"+squad.ID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	assertLegacy(itemFields, "Falcon")
	assertLegacy(ticketFields, "Hawk")
	r = ts.put(t, itemFields+"/squad", map[string]string{"value": "X"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "archived field must be read-only on items")
	r = ts.put(t, ticketFields+"/squad", map[string]string{"value": "X"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "archived field must be read-only on tickets")

	// Delete the definition outright (its scope rows cascade away) — the
	// values still survive on both entity types.
	r = ts.delete(t, defsBase+"/"+squad.ID, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	assertLegacy(itemFields, "Falcon")
	assertLegacy(ticketFields, "Hawk")
}

// formScopeDTO is the wire shape of one form's scope row, shared by the
// form-order tests below.
type formScopeDTO struct {
	FieldID    string `json:"field_id"`
	SpaceID    string `json:"space_id"`
	EntityType string `json:"entity_type"`
	Required   bool   `json:"required"`
	Position   int    `json:"position"`
}

// The form-order surface end to end: the GET reads one form in order, the PUT
// rewrites the order, and — the part that makes ordering real — the entity
// render follows it, because ListCustomFieldScopesForSpaceEntity orders by
// position and RenderForEntity composes in that order.
func TestCustomFields_FormOrderRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Order Proj", "order-proj", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)
	formBase := fmt.Sprintf("%s/forms/%s/project_item", defsBase, spaceID)

	mk := func(name string) fieldDefDTO {
		r := ts.post(t, defsBase, map[string]any{"name": name, "field_type": "text"}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "create %s: %s", name, r.Body)
		var d fieldDefDTO
		require.NoError(t, json.Unmarshal(r.Body, &d))
		attachField(t, ts, d.ID, spaceID, "project_item", false)
		return d
	}
	alpha, beta, gamma := mk("Alpha"), mk("Beta"), mk("Gamma")

	fieldOrder := func(scopes []formScopeDTO) []string {
		out := make([]string, len(scopes))
		for i, sc := range scopes {
			out[i] = sc.FieldID
		}
		return out
	}
	getForm := func() []formScopeDTO {
		t.Helper()
		var scopes []formScopeDTO
		r := ts.get(t, formBase, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "get form: %s", r.Body)
		require.NoError(t, json.Unmarshal(r.Body, &scopes))
		return scopes
	}

	// The form starts in definition order — first attach takes the
	// definition's position.
	require.Equal(t, []string{alpha.ID, beta.ID, gamma.ID}, fieldOrder(getForm()))

	// PUT a new order; the response is the re-listed form, and a fresh GET
	// agrees with it.
	r := ts.put(t, formBase+"/order",
		map[string]any{"field_ids": []string{gamma.ID, alpha.ID, beta.ID}}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "reorder: %s", r.Body)
	var reordered []formScopeDTO
	require.NoError(t, json.Unmarshal(r.Body, &reordered))
	require.Equal(t, []string{gamma.ID, alpha.ID, beta.ID}, fieldOrder(reordered))
	require.Equal(t, []string{gamma.ID, alpha.ID, beta.ID}, fieldOrder(getForm()))

	// The ordering consumer: the item's rendered fields follow the new order.
	var rendered []renderedFieldDTO
	r = ts.get(t, fmt.Sprintf("%s/%s/fields", itemsBase, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &rendered))
	require.Len(t, rendered, 3)
	require.Equal(t, []string{"gamma", "alpha", "beta"},
		[]string{rendered[0].Slug, rendered[1].Slug, rendered[2].Slug},
		"the entity render must follow the form order")

	// A partial order is refused whole — nothing about the form moved.
	r = ts.put(t, formBase+"/order", map[string]any{"field_ids": []string{alpha.ID, beta.ID}}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "partial order must 400: %s", r.Body)
	require.Equal(t, []string{gamma.ID, alpha.ID, beta.ID}, fieldOrder(getForm()))

	// The preservation pair, over the wire. Toggling required keeps the
	// reordered position (the upsert's pinned no-reshuffle property)...
	attachField(t, ts, alpha.ID, spaceID, "project_item", true)
	afterFlag := getForm()
	require.Equal(t, []string{gamma.ID, alpha.ID, beta.ID}, fieldOrder(afterFlag),
		"toggling required must not reshuffle the form")
	require.True(t, afterFlag[1].Required, "the flag itself must have landed")

	// ...and its converse: a reorder changes position and does not change
	// required.
	r = ts.put(t, formBase+"/order",
		map[string]any{"field_ids": []string{beta.ID, gamma.ID, alpha.ID}}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	afterReorder := getForm()
	require.Equal(t, []string{beta.ID, gamma.ID, alpha.ID}, fieldOrder(afterReorder))
	for _, sc := range afterReorder {
		require.Equal(t, sc.FieldID == alpha.ID, sc.Required,
			"a reorder must not change any required flag")
	}
}

// The form-order routes hold the scope family's cross-org discipline: another
// org's space answers 404 with the envelope of a space that does not exist,
// in both directions, and nothing is written.
func TestCustomFields_FormOrderRefusesCrossOrg(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)

	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	otherUser := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, ts.DB.Pool, otherOrg.ID, otherUser.ID, "vector")

	formPath := func(space string) string {
		return fmt.Sprintf("%s/forms/%s/project_item", defsBase, space)
	}

	// GET direction: foreign and nonexistent spaces are indistinguishable.
	foreign := ts.get(t, formPath(otherSpace.ID.String()), true)
	require.Equal(t, http.StatusNotFound, foreign.StatusCode, "%s", foreign.Body)
	absent := ts.get(t, formPath(uuid.NewString()), true)
	require.Equal(t, absent.StatusCode, foreign.StatusCode)
	require.Equal(t, withoutRequestID(t, absent.Body), withoutRequestID(t, foreign.Body),
		"a foreign space must not be distinguishable from a nonexistent one")

	// PUT direction, same parity — and the foreign form's rows are untouched.
	fBody := map[string]any{"field_ids": []string{}}
	foreignPut := ts.put(t, formPath(otherSpace.ID.String())+"/order", fBody, true)
	require.Equal(t, http.StatusNotFound, foreignPut.StatusCode, "%s", foreignPut.Body)
	absentPut := ts.put(t, formPath(uuid.NewString())+"/order", fBody, true)
	require.Equal(t, absentPut.StatusCode, foreignPut.StatusCode)
	require.Equal(t, withoutRequestID(t, absentPut.Body), withoutRequestID(t, foreignPut.Body))
}

// The scopes admin surface round-trip: attach with required, list, re-flag,
// detach — and detaching returns the field to invisible without touching the
// values written while it was attached.
func TestCustomFields_ScopeAdminRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	defsBase := fmt.Sprintf("/api/v1/orgs/%s/custom-fields", ts.OrgID)
	spaceID := createScopedSpace(t, ts, "Scope RT", "scope-rt", "vector")
	itemsBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID)
	itemID := createItem(t, ts, itemsBase)

	r := ts.post(t, defsBase, map[string]any{"name": "Cost Centre", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var cc fieldDefDTO
	require.NoError(t, json.Unmarshal(r.Body, &cc))

	scopesBase := defsBase + "/" + cc.ID + "/scopes"
	type scopeDTO struct {
		FieldID    string `json:"field_id"`
		SpaceID    string `json:"space_id"`
		EntityType string `json:"entity_type"`
		Required   bool   `json:"required"`
		Position   int    `json:"position"`
	}

	// Empty to start.
	var scopes []scopeDTO
	res := ts.get(t, scopesBase, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	require.NoError(t, json.Unmarshal(res.Body, &scopes))
	require.Empty(t, scopes)

	// Attach; the row lists back.
	attachField(t, ts, cc.ID, spaceID, "project_item", false)
	res = ts.get(t, scopesBase, true)
	require.NoError(t, json.Unmarshal(res.Body, &scopes))
	require.Len(t, scopes, 1)
	require.Equal(t, spaceID, scopes[0].SpaceID)
	require.Equal(t, "project_item", scopes[0].EntityType)
	require.False(t, scopes[0].Required)

	// Attaching a ticket scope to a vector space is refused — the row would
	// govern a form that cannot exist in that space.
	r = ts.put(t, fmt.Sprintf("%s/%s/ticket", scopesBase, spaceID), map[string]bool{"required": false}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "module mismatch must 400: %s", r.Body)
	// Pages are in the storage vocabulary but have no field surface to attach to.
	r = ts.put(t, fmt.Sprintf("%s/%s/page", scopesBase, spaceID), map[string]bool{"required": false}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "page attach must 400: %s", r.Body)

	// Value written while attached...
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/cost_centre", itemsBase, itemID), map[string]string{"value": "CC-42"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	// ...survives detach, read-only, with the field gone from the form.
	r = ts.delete(t, fmt.Sprintf("%s/%s/project_item", scopesBase, spaceID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	var rendered []renderedFieldDTO
	res = ts.get(t, fmt.Sprintf("%s/%s/fields", itemsBase, itemID), true)
	require.NoError(t, json.Unmarshal(res.Body, &rendered))
	require.Len(t, rendered, 1)
	require.True(t, rendered[0].Legacy, "a detached field's value renders read-only")
	require.Equal(t, "CC-42", rendered[0].Value)
	require.Equal(t, "Cost Centre", rendered[0].Name,
		"a defined-but-unattached field renders under its real name")
	r = ts.put(t, fmt.Sprintf("%s/%s/fields/cost_centre", itemsBase, itemID), map[string]string{"value": "CC-1"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "a detached field takes no writes")

	// Detaching again 404s — the row is gone.
	r = ts.delete(t, fmt.Sprintf("%s/%s/project_item", scopesBase, spaceID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "%s", r.Body)
}
