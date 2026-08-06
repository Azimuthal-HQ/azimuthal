package api_test

// Domain-error and second-layer branch coverage for the project surface
// (internal/core/api/projects/handler.go and board_handler.go).
//
// This is the layer BELOW the request-shape refusals in
// projects_negative_integration_test.go. Everything here needs the domain to
// fail — a service refusing a well-formed request, a definition that does not
// exist in this org, a collaborator the handler was never given — and none of
// it is reachable by malforming a path parameter or a body. Where a branch is
// only reachable with a deliberately unwired handler, the test says so and
// says why the branch still matters.
//
// Two conventions carried over from the negative pass, deliberately reused
// rather than reimplemented: projNegRequireError is the positive form of a
// refusal (exact status, JSON envelope, exact error code, and the phrase that
// identifies WHICH branch answered), and every refusal is paired with a
// control that must succeed, so a row cannot pass because the route is simply
// broken for every input.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// --- Fixture ---

// projDomFixture is one vector space plus the org-scoped schema surfaces the
// same handler serves. Every request is made as the org admin: nothing here is
// about permissions, so anything short of full authority would only make a
// refusal ambiguous.
type projDomFixture struct {
	ts      *testServer
	base    string
	orgBase string
	spaceID string
}

func newProjDomFixture(t *testing.T, slug string) *projDomFixture {
	t.Helper()
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Proj Domain "+slug, "proj-domain-"+slug, "vector")
	return &projDomFixture{
		ts:      ts,
		base:    fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects", ts.OrgID, spaceID),
		orgBase: fmt.Sprintf("/api/v1/orgs/%s", ts.OrgID),
		spaceID: spaceID,
	}
}

// projDomRequest issues a request as the org admin with an optional JSON body.
// It exists because ts.delete sends no body and the board column delete
// requires one — remap_to is where the removed column's statuses go.
func (f *projDomFixture) projDomRequest(t *testing.T, method, path string, body any) httpResult {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path), reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+f.ts.Token)
	return f.ts.do(t, req)
}

func (f *projDomFixture) createItem(t *testing.T, title string) string {
	t.Helper()
	r := f.ts.post(t, f.base+"/items",
		map[string]any{"title": title, "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	var item struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &item))
	require.NotEmpty(t, item.ID)
	return item.ID
}

// projDomItem is the slice of an item's wire form these tests assert on.
type projDomItem struct {
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
}

func projDomDecodeItem(t *testing.T, r httpResult) projDomItem {
	t.Helper()
	var item projDomItem
	require.NoError(t, json.Unmarshal(r.Body, &item), "body: %s", r.Body)
	return item
}

// --- Board column identity ---

// TestProjectsDomain_BoardColumnIdentitySurvivesAResaveWithIDs covers the one
// branch of SaveBoardConfig that decides whether a board edit preserves or
// destroys column identity: `if c.ID != nil { col.ID = *c.ID }`.
//
// A board save is a whole-configuration PUT, so the ordinary edit — rename one
// column, move a status — is the client sending back the configuration it just
// read, with one field changed. The ids in that body are the ONLY thing that
// tells the service "these are the same columns"; the domain mints a fresh
// uuid for any column whose id arrives as uuid.Nil.
//
// Defect it catches: the handler dropping the supplied id (or the request type
// losing its `id` field). Nothing would look broken — the save still answers
// 200 with a valid board — but every column would silently become a NEW column
// on every edit. The concrete consequence is asserted at the end: the delete
// and remap the client had in hand, addressed by ids it read moments earlier,
// stops resolving and 404s. Per-column client state (collapse, WIP editing,
// drag targets) breaks the same way.
//
// The middle step is the control that makes this test mean anything: re-saving
// the identical shape WITHOUT ids must produce different ids. Without it, "the
// ids did not change" could be satisfied by a service that never changes ids at
// all, and the assertion would prove nothing about the echo.
func TestProjectsDomain_BoardColumnIdentitySurvivesAResaveWithIDs(t *testing.T) {
	f := newProjDomFixture(t, "board")
	cfgPath := f.base + "/board/config"

	derived := decodeBoardConfig(t, f.ts.get(t, cfgPath, true))
	require.False(t, derived.Customized, "a fresh space starts on the derived board")
	require.GreaterOrEqual(t, len(derived.Columns), 2,
		"the delete-and-remap step needs a board with more than one column")

	// A body carrying names and statuses but no ids — a client building a
	// board from scratch.
	anonymousBody := func(cfg boardConfigJSON) []map[string]any {
		cols := make([]map[string]any, 0, len(cfg.Columns))
		for _, c := range cfg.Columns {
			cols = append(cols, map[string]any{"name": c.Name, "statuses": c.Statuses})
		}
		return cols
	}

	r := f.ts.putAs(t, f.ts.Token, cfgPath, map[string]any{"columns": anonymousBody(derived)})
	require.Equal(t, http.StatusOK, r.StatusCode, "first save: %s", r.Body)
	first := decodeBoardConfig(t, r)
	require.True(t, first.Customized)
	require.Len(t, first.Columns, len(derived.Columns))

	// Control: the same shape saved again, still without ids, is a set of
	// brand-new columns. This is what an id-losing handler would do on EVERY
	// save, and it is why the echo below is worth asserting.
	r = f.ts.putAs(t, f.ts.Token, cfgPath, map[string]any{"columns": anonymousBody(first)})
	require.Equal(t, http.StatusOK, r.StatusCode, "second anonymous save: %s", r.Body)
	reminted := decodeBoardConfig(t, r)
	require.NotEqual(t, columnIDs(first), columnIDs(reminted),
		"a column with no id must be a new column — otherwise the echo below proves nothing")

	// The real edit: the client sends back exactly what it read, with one
	// column renamed, ids included.
	renamed := "Renamed " + reminted.Columns[0].Name
	withIDs := make([]map[string]any, 0, len(reminted.Columns))
	for i, c := range reminted.Columns {
		name := c.Name
		if i == 0 {
			name = renamed
		}
		withIDs = append(withIDs, map[string]any{"id": c.ID, "name": name, "statuses": c.Statuses})
	}
	r = f.ts.putAs(t, f.ts.Token, cfgPath, map[string]any{"columns": withIDs})
	require.Equal(t, http.StatusOK, r.StatusCode, "save with ids: %s", r.Body)
	kept := decodeBoardConfig(t, r)
	require.Equal(t, columnIDs(reminted), columnIDs(kept),
		"an id the client supplied must be the id it gets back")
	require.Equal(t, renamed, kept.Columns[0].Name, "the rename must still have been applied")

	// And it is the stored identity, not just what the response echoed.
	after := decodeBoardConfig(t, f.ts.get(t, cfgPath, true))
	require.Equal(t, columnIDs(reminted), columnIDs(after),
		"the preserved ids must be what a fresh read returns")

	// The consequence, stated as a request: a column id the client held before
	// the rename is still a valid delete target afterwards.
	r = f.projDomRequest(t, http.MethodDelete, cfgPath+"/columns/"+reminted.Columns[0].ID,
		map[string]any{"remap_to": reminted.Columns[1].ID})
	require.Equal(t, http.StatusOK, r.StatusCode,
		"an id held across a save must still address the column: %s", r.Body)
	afterDelete := decodeBoardConfig(t, r)
	require.Len(t, afterDelete.Columns, len(kept.Columns)-1)
	require.ElementsMatch(t, mappedStatuses(kept), mappedStatuses(afterDelete),
		"the removed column's statuses must have been re-homed, not dropped")
}

// --- The item PATCH's label tri-state ---

// TestProjectsDomain_ItemLabelsPatchDistinguishesAbsentFromEmpty covers
// applyItemPatch's `if req.Labels != nil` guard, in both directions.
//
// Labels are the third field on the item PATCH whose absence must not be
// treated as a value — the same shape that already destroyed data twice here
// (due_at and assignee_id, see updateItemRequest's comment). A []string is nil
// when the key is absent and non-nil-but-empty when the client sends [], and
// that difference is the whole guard.
//
// Defect it catches: removing the nil check so labels are assigned
// unconditionally. Every PATCH that never mentions labels — which is every
// PATCH the product actually sends: a rename, a board drag, an assignee
// change — would then silently strip the item's labels. Nothing would fail;
// the response is a 200 and the labels are simply gone. The middle step is the
// assertion that catches it, and the last step is what stops the fix from
// going the other way: an explicit [] must still clear them, or the label
// editor can never remove the last label.
func TestProjectsDomain_ItemLabelsPatchDistinguishesAbsentFromEmpty(t *testing.T) {
	f := newProjDomFixture(t, "labels")
	itemPath := f.base + "/items/" + f.createItem(t, "Labelled Item")

	r := f.ts.patch(t, itemPath, map[string]any{"labels": []string{"alpha", "beta"}}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "set labels: %s", r.Body)
	require.ElementsMatch(t, []string{"alpha", "beta"},
		projDomDecodeItem(t, f.ts.get(t, itemPath, true)).Labels,
		"a PATCH carrying labels must store them")

	// A PATCH that never mentions labels must leave them exactly as they were.
	r = f.ts.patch(t, itemPath, map[string]any{"title": "Renamed, labels untouched"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "rename: %s", r.Body)
	reread := projDomDecodeItem(t, f.ts.get(t, itemPath, true))
	require.Equal(t, "Renamed, labels untouched", reread.Title,
		"the rename must have been applied — otherwise the labels below survived a no-op")
	require.ElementsMatch(t, []string{"alpha", "beta"}, reread.Labels,
		"a PATCH that omits labels must not strip them")

	// An explicit empty array is a value, not an absence: it clears.
	r = f.ts.patch(t, itemPath, map[string]any{"labels": []string{}}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "clear labels: %s", r.Body)
	require.Empty(t, projDomDecodeItem(t, f.ts.get(t, itemPath, true)).Labels,
		"an explicit [] must clear the labels, or the last one can never be removed")
}

// --- Sprint update: a service refusal on a well-formed request ---

// TestProjectsDomain_UpdateSprintWithoutANameIsRefused covers UpdateSprint's
// error branch on the write, which no malformed-request test can reach: the
// path parses, the body decodes, the sprint exists, the caller is authorised,
// and the SERVICE refuses.
//
// The sprint PUT is a full replace — the handler assigns req.Name over the
// stored name unconditionally — so a body that omits "name" is asking to blank
// it, and SprintService.UpdateSprint answers ErrNameRequired before touching
// the database.
//
// Defect it catches: the handler ignoring that error. `updated` would be nil
// and the client would receive 200 with a null body while the sprint kept its
// old name — a rename that reports success and did not happen, which is worse
// than a failure because nobody retries it. The read-back is the load-bearing
// half: it proves the refusal happened before any write, not after a partial
// one.
func TestProjectsDomain_UpdateSprintWithoutANameIsRefused(t *testing.T) {
	f := newProjDomFixture(t, "sprint")

	r := f.ts.post(t, f.base+"/sprints", map[string]any{"name": "Original", "goal": "Ship it"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create sprint: %s", r.Body)
	var sprint struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &sprint))
	sprintPath := f.base + "/sprints/" + sprint.ID

	projNegRequireError(t, f.ts.putAs(t, f.ts.Token, sprintPath,
		map[string]any{"goal": "a goal and no name"}),
		http.StatusBadRequest, "VALIDATION_ERROR", "name is required")

	var stored struct {
		Name string `json:"name"`
		Goal string `json:"goal"`
	}
	r = f.ts.get(t, sprintPath, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "read back the sprint: %s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &stored))
	require.Equal(t, "Original", stored.Name, "a refused update must not have blanked the name")
	require.Equal(t, "Ship it", stored.Goal, "a refused update must not have written the new goal either")

	// Control: the same PUT with a name is applied, so the refusal above was
	// the missing name and not a broken route.
	r = f.ts.putAs(t, f.ts.Token, sprintPath, map[string]any{"name": "Renamed", "goal": "a goal and a name"})
	require.Equal(t, http.StatusOK, r.StatusCode, "a named update must be accepted: %s", r.Body)
	require.NoError(t, json.Unmarshal(f.ts.get(t, sprintPath, true).Body, &stored))
	require.Equal(t, "Renamed", stored.Name)
	require.Equal(t, "a goal and a name", stored.Goal)
}

// --- Archiving a definition that does not exist ---

// TestProjectsDomain_ArchivingAMissingDefinitionIs404 covers the error branch
// of the ARCHIVE half of the two schema PATCH handlers — UpdateItemType and
// UpdateCustomField.
//
// Both handlers are two independent updates behind one PATCH: a rename branch
// and an archive branch, each with its own service call and its own error
// check. The existing suites only ever reach the rename branch's error (a
// PATCH carrying "name"), so a body carrying ONLY "archived" is the single way
// into the second one.
//
// Defect it catches: the archive branch's `if err != nil` being dropped. Both
// handlers fall through to `respond.JSON(w, 200, result)` with result still
// nil, so archiving a type that does not exist — or one belonging to another
// organisation, which getOwned refuses identically — would answer 200 with a
// null body. An admin console would report the archive as done and the type
// would still be offered in every picker. The message assertions name which
// service refused, so a 404 raised by the wrong lookup would fail here.
func TestProjectsDomain_ArchivingAMissingDefinitionIs404(t *testing.T) {
	f := newProjDomFixture(t, "archive")
	missing := uuid.NewString()

	projNegRequireError(t, f.ts.patch(t, f.orgBase+"/item-types/"+missing,
		map[string]any{"archived": true}, true),
		http.StatusNotFound, "NOT_FOUND", "item type not found")

	projNegRequireError(t, f.ts.patch(t, f.orgBase+"/custom-fields/"+missing,
		map[string]any{"archived": true}, true),
		http.StatusNotFound, "NOT_FOUND", "custom field not found")

	// Controls: the identical body against definitions that DO exist archives
	// them. Without these, both refusals above would be satisfied by an
	// archive branch that is simply broken for every input.
	r := f.ts.post(t, f.orgBase+"/item-types", map[string]any{"name": "Spike"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item type: %s", r.Body)
	var itemType projDomDefinition
	require.NoError(t, json.Unmarshal(r.Body, &itemType))
	require.Nil(t, itemType.ArchivedAt, "a new type starts active")

	r = f.ts.patch(t, f.orgBase+"/item-types/"+itemType.ID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "archive an existing type: %s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &itemType))
	require.NotNil(t, itemType.ArchivedAt, "the archive must be reflected in the response")

	r = f.ts.post(t, f.orgBase+"/custom-fields",
		map[string]any{"name": "Points", "field_type": "number"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create custom field: %s", r.Body)
	var field projDomDefinition
	require.NoError(t, json.Unmarshal(r.Body, &field))
	require.Nil(t, field.ArchivedAt, "a new field starts active")

	r = f.ts.patch(t, f.orgBase+"/custom-fields/"+field.ID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "archive an existing field: %s", r.Body)
	require.NoError(t, json.Unmarshal(r.Body, &field))
	require.NotNil(t, field.ArchivedAt, "the archive must be reflected in the response")
}

// projDomDefinition is the shared slice of an item type's and a custom field's
// wire form. Archiving is a nullable timestamp on both, not a boolean: the
// request says "archived": true and the response answers with archived_at, so
// a test asserting a boolean field would read the zero value and pass against
// an archive that never happened.
type projDomDefinition struct {
	ID         string  `json:"id"`
	ArchivedAt *string `json:"archived_at"`
}

// --- Writing a field value onto an item that is not there ---

// TestProjectsDomain_SetFieldOnAMissingItemIs404 covers SetItemField's FIRST
// error branch — the item lookup — which is distinct from the field lookup the
// negative pass already covers.
//
// SetItemField loads the item before it does anything else, because the
// edit_own/edit_any split needs the reporter. A missing item therefore has to
// be answered by handleProjectError, not by the custom-field mapping, and the
// two produce different messages: "getting item: not found" against "no active
// custom field with this slug". Asserting the message is what tells them
// apart — both are 404 NOT_FOUND.
//
// Defect it catches: dropping that error check. `existing` would be nil and
// the very next line dereferences existing.ReporterID, so the endpoint would
// panic rather than answer — a 500 (or a dropped connection) on a request that
// has a perfectly good 404 to give. Either way this test fails, which is the
// point.
func TestProjectsDomain_SetFieldOnAMissingItemIs404(t *testing.T) {
	f := newProjDomFixture(t, "setfield")

	r := f.ts.post(t, f.orgBase+"/custom-fields",
		map[string]any{"name": "Squad", "field_type": "text"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "define custom field: %s", r.Body)
	var def struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &def))
	require.Equal(t, "squad", def.Slug)
	attachField(t, f.ts, def.ID, f.spaceID, "project_item", false)

	projNegRequireError(t, f.ts.putAs(t, f.ts.Token,
		f.base+"/items/"+uuid.NewString()+"/fields/"+def.Slug, map[string]any{"value": "platform"}),
		http.StatusNotFound, "NOT_FOUND", "getting item")

	// Control: the identical write against a real item is stored. The refusal
	// above is therefore about the item and not about the field or the route.
	itemPath := f.base + "/items/" + f.createItem(t, "Field Target")
	r = f.ts.putAs(t, f.ts.Token, itemPath+"/fields/"+def.Slug, map[string]any{"value": "platform"})
	require.Equal(t, http.StatusOK, r.StatusCode, "write the field on a real item: %s", r.Body)

	r = f.ts.get(t, itemPath+"/fields", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "read the item's fields: %s", r.Body)
	var fields []struct {
		Slug  string `json:"slug"`
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &fields))
	var found bool
	for _, fl := range fields {
		if fl.Slug == def.Slug {
			found = true
			require.Equal(t, "platform", fl.Value)
		}
	}
	require.True(t, found, "the written field must come back on the item")
}

// --- The board handler's own guards, driven directly ---

// projDomBoardRoutes mounts a project handler under the real URL shape with no
// middleware in front of it, so the handler's own guards are the only thing
// answering. This is deliberately NOT newTestServer: both branches below sit
// underneath something the production router does first, and mounting the
// handler bare is the only way to make the handler itself answer.
func projDomBoardRoutes(h *projectsapi.Handler) http.Handler {
	r := chi.NewRouter()
	r.Mount("/orgs/{orgID}/spaces/{spaceID}/projects", h.Routes())
	return r
}

// projDomServe drives a handler with an in-process request and returns the
// same httpResult shape the server-backed helpers produce, so the shared
// refusal assertion applies unchanged.
func projDomServe(t *testing.T, h http.Handler, method, path, body string) httpResult {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return httpResult{
		StatusCode:  res.StatusCode,
		Body:        b,
		ContentType: res.Header.Get("Content-Type"),
		Header:      res.Header,
	}
}

// projDomBoardRoute is one row of the board table below.
type projDomBoardRoute struct {
	name   string
	method string
	suffix string
	body   string
}

// projDomBoardRoutesTable is the whole board-configuration family. Each of the
// four handlers repeats the same two guards, so each needs its own row: a
// guard restored in one handler and forgotten in another is exactly the class
// of mistake a single-route test misses.
func projDomBoardRoutesTable(columnID string) []projDomBoardRoute {
	return []projDomBoardRoute{
		{"get config", http.MethodGet, "/board/config", ""},
		{"save config", http.MethodPut, "/board/config", `{"columns":[]}`},
		{"reset config", http.MethodPost, "/board/config/reset", ""},
		{"delete column", http.MethodDelete, "/board/config/columns/" + columnID,
			`{"remap_to":"` + uuid.NewString() + `"}`},
	}
}

// TestProjectsDomain_BoardSurfaceRefusesWhenUnwiredOrUnresolved covers the two
// guards every board-configuration route shares — boardSpace's
// feature-not-enabled check and the capability check above it — by driving the
// handler with the collaborator, or the access resolution, deliberately absent.
//
// Both branches are unreachable through the wired router, and for opposite
// reasons, so the reason is recorded here rather than left to be rediscovered:
//
//   - h.boardConfig is nil only when WithBoardConfig was never called.
//     cmd/server/main.go always calls it, and TestHarness_NoDarkDependencies
//     fails the build of any test harness that does not. That is the correct
//     arrangement and this test does not weaken it: it constructs its own
//     handler precisely because the shared harness must never be in this state.
//   - access.Can(CapReadItems) inside GetBoardConfig can never answer false
//     behind the real chain, because RequireSpaceReadable has already answered
//     404 for any caller whose readable set does not contain the space, and
//     every role that puts a space in that set grants read_items.
//
// Defect each catches. Without the nil check, an unwired deployment would not
// report a disabled feature — it would dereference a nil service and panic on
// the first board request, taking the connection with it; the 404 body is the
// difference between "this build has no board configuration" and a crash. And
// the capability check is the handler's fail-closed half: with no resolution
// on the context, access.Can returns false, and a check rewritten to treat a
// missing resolution as permission would serve the board to a request that was
// never authorised at all. The assertions are on the exact code and message,
// so chi's own bare 404 for an unmounted route cannot stand in for either.
func TestProjectsDomain_BoardSurfaceRefusesWhenUnwiredOrUnresolved(t *testing.T) {
	base := "/orgs/" + uuid.NewString() + "/spaces/" + uuid.NewString() + "/projects"
	columnID := uuid.NewString()

	t.Run("board configuration was never wired", func(t *testing.T) {
		// No WithBoardConfig: the feature is off in this build.
		h := projDomBoardRoutes(projectsapi.NewHandler(nil, nil, nil, nil, nil))
		for _, tc := range projDomBoardRoutesTable(columnID) {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, projDomServe(t, h, tc.method, base+tc.suffix, tc.body),
					http.StatusNotFound, "NOT_FOUND", "board configuration is not enabled")
			})
		}
	})

	t.Run("no access resolution on the context", func(t *testing.T) {
		// The service IS wired, so boardSpace passes and the capability check
		// is what answers. Its repositories are nil on purpose: if a guard
		// were removed the handler would reach them and panic, so this test
		// cannot pass with the checks deleted.
		h := projDomBoardRoutes(projectsapi.NewHandler(nil, nil, nil, nil, nil).
			WithBoardConfig(projects.NewBoardConfigService(nil, nil)))
		for _, tc := range projDomBoardRoutesTable(columnID) {
			t.Run(tc.name, func(t *testing.T) {
				projNegRequireError(t, projDomServe(t, h, tc.method, base+tc.suffix, tc.body),
					http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
			})
		}
	})
}
