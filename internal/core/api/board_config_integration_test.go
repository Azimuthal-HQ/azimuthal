package api_test

// HTTP coverage for per-space board configuration (migration 035).
//
// The service rules have unit coverage in internal/core/projects; what is
// pinned here is everything that only exists once a real request travels the
// wired router against a real database: the status codes a client actually
// sees, that a save is stored rather than echoed, that a rejected save leaves
// the board alone, that a column's statuses cannot be re-homed across a space
// boundary, and the capability split — reading the board follows space read
// access, changing it follows space admin.

import (
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

// boardWorkflowStates is the org default project workflow, in workflow order
// (adapters.seedProjectWorkflow). It deliberately differs from
// projects.DefaultColumnNames — a board that fell back to the hardcoded
// vocabulary instead of reading the space's workflow would open with "open"
// rather than "backlog", and the derived-default assertions below would fail.
var boardWorkflowStates = []string{"backlog", "todo", "in_progress", "in_review", "done"}

// boardSpace is a vector space carrying the default project workflow,
// together with its board-configuration URL.
type boardSpace struct {
	id   uuid.UUID
	base string
}

// boardFixture is one test server whose org has the default workflows seeded,
// plus one such space. The harness user is an org owner, so requests made
// with ts.Token clear the capability checks through the org-admin bypass; the
// persona helper below builds users who do not.
type boardFixture struct {
	ts *testServer
	boardSpace
}

func newBoardFixture(t *testing.T) *boardFixture {
	t.Helper()
	ts := newTestServer(t)
	require.NoError(t, ts.WorkflowAdapter.SeedDefaultWorkflows(context.Background(), ts.OrgID))
	f := &boardFixture{ts: ts}
	f.boardSpace = f.addSpace(t)
	return f
}

// addSpace creates a vector space on the fixture's server and assigns it the
// default project workflow. Tests that need a second board call it again.
func (f *boardFixture) addSpace(t *testing.T) boardSpace {
	t.Helper()
	space := testutil.CreateTestSpace(t, f.ts.DB.Pool, f.ts.OrgID, f.ts.UserID, "vector")
	require.NoError(t, f.ts.WorkflowAdapter.AssignDefaultWorkflowToSpace(
		context.Background(), f.ts.OrgID, "vector", space.ID))
	return boardSpace{
		id:   space.ID,
		base: fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/board/config", f.ts.OrgID, space.ID),
	}
}

// request issues a request as the holder of token, or with no credentials at
// all when token is empty. DELETE carries a body here, which the shared
// ts.delete helper cannot send.
func (f *boardFixture) request(t *testing.T, method, token, path string, body any) httpResult {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path), newBodyReader(raw))
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return f.ts.do(t, req)
}

// grantee returns a token for a fresh user whose only authority over the
// space is the given grant. The org role is "member" on purpose: an owner or
// admin would clear every capability check through the org-admin bypass, and
// the denials below would prove nothing.
func (f *boardFixture) grantee(t *testing.T, space boardSpace, role access.Role) string {
	t.Helper()
	u := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	_, err := f.ts.GrantService.Create(context.Background(), f.ts.OrgID, space.id,
		access.SubjectUser, u.ID, role, f.ts.UserID)
	require.NoError(t, err)
	return f.ts.tokenFor(t, u.ID, u.Email)
}

// --- Wire types ---

type boardColumnJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Position int      `json:"position"`
	WIPLimit *int     `json:"wip_limit"`
	Statuses []string `json:"statuses"`
}

type boardConfigJSON struct {
	Columns    []boardColumnJSON `json:"columns"`
	Customized bool              `json:"customized"`
}

func decodeBoardConfig(t *testing.T, r httpResult) boardConfigJSON {
	t.Helper()
	var cfg boardConfigJSON
	require.NoError(t, json.Unmarshal(r.Body, &cfg), "body: %s", r.Body)
	return cfg
}

func columnNames(cfg boardConfigJSON) []string {
	names := make([]string, 0, len(cfg.Columns))
	for _, c := range cfg.Columns {
		names = append(names, c.Name)
	}
	return names
}

func columnIDs(cfg boardConfigJSON) []string {
	ids := make([]string, 0, len(cfg.Columns))
	for _, c := range cfg.Columns {
		ids = append(ids, c.ID)
	}
	return ids
}

// mappedStatuses is every status the board accounts for. The board's whole
// premise is that this stays equal to the space's vocabulary through any
// sequence of edits: a status missing from here is work that has vanished.
func mappedStatuses(cfg boardConfigJSON) []string {
	var all []string
	for _, c := range cfg.Columns {
		all = append(all, c.Statuses...)
	}
	return all
}

func columnByName(t *testing.T, cfg boardConfigJSON, name string) boardColumnJSON {
	t.Helper()
	for _, c := range cfg.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("column %q not found in %v", name, columnNames(cfg))
	return boardColumnJSON{}
}

// --- Request bodies ---

func boardColumnBody(name string, statuses ...string) map[string]any {
	return map[string]any{"name": name, "statuses": statuses}
}

func withWIPLimit(col map[string]any, limit int) map[string]any {
	col["wip_limit"] = limit
	return col
}

// threeColumnBoard is the standard custom layout used throughout: it covers
// the whole workflow vocabulary, so it is a valid save.
func threeColumnBoard() map[string]any {
	return map[string]any{"columns": []map[string]any{
		boardColumnBody("Doing", "backlog", "todo", "in_progress"),
		withWIPLimit(boardColumnBody("Review", "in_review"), 2),
		boardColumnBody("Done", "done"),
	}}
}

// saveThreeColumnBoard stores the standard layout as the org admin and
// returns what came back.
func (f *boardFixture) saveThreeColumnBoard(t *testing.T, base string) boardConfigJSON {
	t.Helper()
	r := f.request(t, http.MethodPut, f.ts.Token, base, threeColumnBoard())
	require.Equal(t, http.StatusOK, r.StatusCode, "save board: %s", r.Body)
	return decodeBoardConfig(t, r)
}

// --- Reads ---

// TestBoardConfig_UntouchedSpaceRendersItsWorkflowStates is the regression
// protection every existing space depends on: before anyone configures a
// board, it must still look exactly like the workflow-derived board that
// shipped before configuration existed — one column per workflow state, in
// workflow order — and say so with customized=false.
func TestBoardConfig_UntouchedSpaceRendersItsWorkflowStates(t *testing.T) {
	f := newBoardFixture(t)

	r := f.request(t, http.MethodGet, f.ts.Token, f.base, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)

	cfg := decodeBoardConfig(t, r)
	require.False(t, cfg.Customized, "a space nobody has configured is not customised")
	require.Equal(t, boardWorkflowStates, columnNames(cfg),
		"the derived board is one column per workflow state, in workflow order")

	for i, c := range cfg.Columns {
		require.Equal(t, i, c.Position, "derived positions are the workflow order")
		require.Equal(t, []string{boardWorkflowStates[i]}, c.Statuses,
			"each derived column owns exactly its own status")
		require.Nil(t, c.WIPLimit, "the derived board imposes no limits")
	}
}

// TestBoardConfig_SaveIsStoredNotEchoed proves the save reached the database:
// a second, independent request returns the same column identities, names,
// order and limits rather than the derived default.
func TestBoardConfig_SaveIsStoredNotEchoed(t *testing.T) {
	f := newBoardFixture(t)

	saved := f.saveThreeColumnBoard(t, f.base)
	require.True(t, saved.Customized, "a stored configuration reports customized=true")
	require.Equal(t, []string{"Doing", "Review", "Done"}, columnNames(saved))

	r := f.request(t, http.MethodGet, f.ts.Token, f.base, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)
	got := decodeBoardConfig(t, r)

	require.True(t, got.Customized)
	require.Equal(t, columnIDs(saved), columnIDs(got),
		"a re-read must return the columns that were stored, with their identities intact")
	require.Equal(t, []string{"Doing", "Review", "Done"}, columnNames(got))
	require.Equal(t, []int{0, 1, 2}, []int{got.Columns[0].Position, got.Columns[1].Position, got.Columns[2].Position},
		"position is the array order the client sent")

	// The store returns a column's statuses alphabetically, so this compares
	// membership rather than order; what matters is that every status the
	// client mapped is still on the column it chose.
	require.ElementsMatch(t, []string{"backlog", "todo", "in_progress"}, columnByName(t, got, "Doing").Statuses)
	require.ElementsMatch(t, boardWorkflowStates, mappedStatuses(got), "the vocabulary stays fully mapped")

	require.Equal(t, 2, *columnByName(t, got, "Review").WIPLimit, "a WIP limit survives the round trip")
	require.Nil(t, columnByName(t, got, "Doing").WIPLimit, "a column saved without a limit keeps none")
}

// --- Save validation ---

// TestBoardConfig_SaveRejectsAnUnmappedStatus is the rule the whole feature
// turns on. A layout that drops a status is a client error, not a server
// error, the message has to name the status that lost its column, and the
// stored board must be left exactly as it was.
func TestBoardConfig_SaveRejectsAnUnmappedStatus(t *testing.T) {
	f := newBoardFixture(t)

	r := f.request(t, http.MethodPut, f.ts.Token, f.base, map[string]any{"columns": []map[string]any{
		boardColumnBody("Doing", "backlog", "todo", "in_progress"),
		boardColumnBody("Review", "in_review"),
	}})
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
	require.Contains(t, decodeErrorObject(t, r.Body)["message"], "done",
		"the rejection must name the status that would have vanished")

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.False(t, after.Customized, "a rejected save must not partially configure the board")
	require.Equal(t, boardWorkflowStates, columnNames(after))
}

// TestBoardConfig_SaveRejectsMalformedLayouts covers the remaining shapes the
// board editor can produce, each a 400 rather than a constraint violation
// surfacing as a 500.
func TestBoardConfig_SaveRejectsMalformedLayouts(t *testing.T) {
	cases := []struct {
		name    string
		columns []map[string]any
	}{
		{
			// Names collide case-insensitively: two columns a reader cannot
			// tell apart are the same column.
			name: "duplicate column name",
			columns: []map[string]any{
				boardColumnBody("Doing", "backlog", "todo", "in_progress"),
				boardColumnBody("doing", "in_review"),
				boardColumnBody("Done", "done"),
			},
		},
		{
			name: "wip limit of zero",
			columns: []map[string]any{
				withWIPLimit(boardColumnBody("Doing", "backlog", "todo", "in_progress"), 0),
				boardColumnBody("Review", "in_review"),
				boardColumnBody("Done", "done"),
			},
		},
		{
			name: "negative wip limit",
			columns: []map[string]any{
				withWIPLimit(boardColumnBody("Doing", "backlog", "todo", "in_progress"), -1),
				boardColumnBody("Review", "in_review"),
				boardColumnBody("Done", "done"),
			},
		},
		{
			name:    "no columns at all",
			columns: []map[string]any{},
		},
		{
			name: "blank column name",
			columns: []map[string]any{
				boardColumnBody("   ", "backlog", "todo", "in_progress"),
				boardColumnBody("Review", "in_review"),
				boardColumnBody("Done", "done"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBoardFixture(t)
			r := f.request(t, http.MethodPut, f.ts.Token, f.base, map[string]any{"columns": tc.columns})
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

			after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
			require.False(t, after.Customized, "a rejected layout must not be stored")
		})
	}
}

// --- Reset ---

// TestBoardConfig_ResetReturnsTheDerivedDefault: reset is how a space gets
// back the board it had before anyone customised it, and the removal has to
// outlive the response.
func TestBoardConfig_ResetReturnsTheDerivedDefault(t *testing.T) {
	f := newBoardFixture(t)
	f.saveThreeColumnBoard(t, f.base)

	r := f.request(t, http.MethodPost, f.ts.Token, f.base+"/reset", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)

	reset := decodeBoardConfig(t, r)
	require.False(t, reset.Customized, "reset returns the space to the derived default")
	require.Equal(t, boardWorkflowStates, columnNames(reset))

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.False(t, after.Customized, "the stored configuration is gone, not just absent from the response")
	require.Equal(t, boardWorkflowStates, columnNames(after))
}

// --- Column deletion ---

// TestBoardConfig_DeletingAColumnRehomesItsStatuses: removing a column moves
// its statuses onto the named target, so the vocabulary stays fully mapped.
func TestBoardConfig_DeletingAColumnRehomesItsStatuses(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)
	review := columnByName(t, saved, "Review")
	done := columnByName(t, saved, "Done")

	r := f.request(t, http.MethodDelete, f.ts.Token, f.base+"/columns/"+review.ID,
		map[string]any{"remap_to": done.ID})
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.Equal(t, []string{"Doing", "Done"}, columnNames(after), "the removed column is gone")
	require.ElementsMatch(t, []string{"done", "in_review"}, columnByName(t, after, "Done").Statuses,
		"the removed column's statuses moved to the target")
	require.ElementsMatch(t, boardWorkflowStates, mappedStatuses(after),
		"no status may be lost when a column is removed")
}

// TestBoardConfig_DeletingAColumnRequiresARemapTarget: there is no variant
// that drops a column's statuses, so a delete that does not say where the
// work goes is refused before anything is touched.
func TestBoardConfig_DeletingAColumnRequiresARemapTarget(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)
	reviewPath := f.base + "/columns/" + columnByName(t, saved, "Review").ID

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"remap_to omitted", map[string]any{}},
		{"remap_to is the zero uuid", map[string]any{"remap_to": uuid.Nil.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.request(t, http.MethodDelete, f.ts.Token, reviewPath, tc.body)
			requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
			require.Contains(t, decodeErrorObject(t, r.Body)["message"], "remap_to",
				"the rejection must name the field the client left out")
		})
	}

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.Equal(t, columnIDs(saved), columnIDs(after), "a refused delete changes nothing")
	require.ElementsMatch(t, boardWorkflowStates, mappedStatuses(after))
}

// TestBoardConfig_RemapTargetFromAnotherSpaceIsRefused is the cross-space
// guard: a column id is only meaningful inside its own space, and a delete
// that names a target belonging to a different space must be refused rather
// than silently re-homing one space's work onto another space's board.
func TestBoardConfig_RemapTargetFromAnotherSpaceIsRefused(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)

	other := f.addSpace(t)
	otherSaved := f.saveThreeColumnBoard(t, other.base)
	foreignTarget := columnByName(t, otherSaved, "Done")

	r := f.request(t, http.MethodDelete, f.ts.Token,
		f.base+"/columns/"+columnByName(t, saved, "Review").ID,
		map[string]any{"remap_to": foreignTarget.ID})
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.Equal(t, columnIDs(saved), columnIDs(after), "the source board is untouched")
	require.ElementsMatch(t, []string{"in_review"}, columnByName(t, after, "Review").Statuses,
		"the statuses stayed on their own column")

	otherAfter := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, other.base, nil))
	require.Equal(t, columnIDs(otherSaved), columnIDs(otherAfter), "the target board is untouched")
	require.ElementsMatch(t, []string{"done"}, columnByName(t, otherAfter, "Done").Statuses,
		"nothing crossed the space boundary")
}

// --- Access control ---

// TestBoardConfig_RoutesRequireAuthentication: all four routes sit behind the
// authenticated group.
func TestBoardConfig_RoutesRequireAuthentication(t *testing.T) {
	f := newBoardFixture(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"read", http.MethodGet, f.base, nil},
		{"save", http.MethodPut, f.base, threeColumnBoard()},
		{"reset", http.MethodPost, f.base + "/reset", nil},
		{"delete column", http.MethodDelete, f.base + "/columns/" + uuid.NewString(),
			map[string]any{"remap_to": uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireErrorCode(t, f.request(t, tc.method, "", tc.path, tc.body),
				http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

// TestBoardConfig_ViewerReadsTheBoardButCannotConfigureIt: reading the board's
// shape is ordinary read access, so a viewer sees it; no write is.
//
// The denials here are the subtree's write floor, not the board's own check —
// verified by mutation: disabling canManageBoard leaves this test green. That
// is not a defect in the test, it is what a viewer's refusal genuinely is, and
// it belongs on the record. The board-specific gate is pinned by the
// contributor test below, which is the persona that gets past the floor.
func TestBoardConfig_ViewerReadsTheBoardButCannotConfigureIt(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)
	viewerTok := f.grantee(t, f.boardSpace, access.RoleViewer)

	r := f.request(t, http.MethodGet, viewerTok, f.base, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "a viewer must be able to read the board: %s", r.Body)
	require.Equal(t, columnIDs(saved), columnIDs(decodeBoardConfig(t, r)))

	requireAPIForbidden(t, f.request(t, http.MethodPut, viewerTok, f.base, threeColumnBoard()))
	requireAPIForbidden(t, f.request(t, http.MethodPost, viewerTok, f.base+"/reset", nil))
	requireAPIForbidden(t, f.request(t, http.MethodDelete, viewerTok,
		f.base+"/columns/"+columnByName(t, saved, "Review").ID,
		map[string]any{"remap_to": columnByName(t, saved, "Done").ID}))

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.Equal(t, columnIDs(saved), columnIDs(after), "a denied write must not have written")
	require.True(t, after.Customized)
}

// TestBoardConfig_ContributorClearsTheWriteFloorButStillCannotConfigure is the
// test that actually exercises the board's own gate.
//
// The subtree's write floor is RequireWriteFloor(CapCreateItems), which a
// viewer fails on any mutating method — so the viewer denials above are the
// floor talking, and they would still be 403 if canManageBoard were deleted
// outright. A contributor is the persona that separates the two: it holds
// CapCreateItems, so it passes the floor and reaches the handler, and it does
// not hold CapManageSpace, so only the board's own check can refuse it.
//
// Delete the access.Can(CapManageSpace) check in canManageBoard and this test
// fails while every other denial here keeps passing — which is the point.
// Without it, anyone who can file an item could re-map another team's board.
func TestBoardConfig_ContributorClearsTheWriteFloorButStillCannotConfigure(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)
	contributorTok := f.grantee(t, f.boardSpace, access.RoleContributor)

	// The floor is genuinely cleared: the same persona may create an item in
	// this space, which is a mutating request on the same subtree. Without
	// this, a 403 below could still be the floor rather than the board gate.
	itemsPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", f.ts.OrgID, f.id)
	created := f.request(t, http.MethodPost, contributorTok, itemsPath,
		map[string]any{"title": "Contributor can file this", "kind": "task", "priority": "medium"})
	require.Equal(t, http.StatusCreated, created.StatusCode,
		"a contributor must clear the subtree write floor: %s", created.Body)

	// Reading the board is ordinary read access, so this is allowed.
	r := f.request(t, http.MethodGet, contributorTok, f.base, nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "a contributor reads the board: %s", r.Body)

	// Configuring it is not.
	requireAPIForbidden(t, f.request(t, http.MethodPut, contributorTok, f.base, threeColumnBoard()))
	requireAPIForbidden(t, f.request(t, http.MethodPost, contributorTok, f.base+"/reset", nil))
	requireAPIForbidden(t, f.request(t, http.MethodDelete, contributorTok,
		f.base+"/columns/"+columnByName(t, saved, "Review").ID,
		map[string]any{"remap_to": columnByName(t, saved, "Done").ID}))

	after := decodeBoardConfig(t, f.request(t, http.MethodGet, f.ts.Token, f.base, nil))
	require.Equal(t, columnIDs(saved), columnIDs(after), "a denied write must not have written")
	require.True(t, after.Customized)
}

// TestBoardConfig_SpaceAdminGrantIsWhatUnlocksTheWrites is the other half of
// the gate. This persona differs from the viewer above in exactly one
// respect — the role on its space grant — so the 403s there are the
// capability and nothing else. Neither persona is an org admin; if the org
// bypass leaked to ordinary members, the viewer would have been allowed too.
func TestBoardConfig_SpaceAdminGrantIsWhatUnlocksTheWrites(t *testing.T) {
	f := newBoardFixture(t)
	saved := f.saveThreeColumnBoard(t, f.base)
	adminTok := f.grantee(t, f.boardSpace, access.RoleSpaceAdmin)

	r := f.request(t, http.MethodDelete, adminTok,
		f.base+"/columns/"+columnByName(t, saved, "Review").ID,
		map[string]any{"remap_to": columnByName(t, saved, "Done").ID})
	require.Equal(t, http.StatusOK, r.StatusCode, "space admin removes a column: %s", r.Body)

	r = f.request(t, http.MethodPut, adminTok, f.base, threeColumnBoard())
	require.Equal(t, http.StatusOK, r.StatusCode, "space admin saves a layout: %s", r.Body)
	require.Equal(t, []string{"Doing", "Review", "Done"}, columnNames(decodeBoardConfig(t, r)))

	r = f.request(t, http.MethodPost, adminTok, f.base+"/reset", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "space admin resets the board: %s", r.Body)
	require.False(t, decodeBoardConfig(t, r).Customized)
}
