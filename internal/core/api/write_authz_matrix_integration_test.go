package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The WRITE half of the cross-space authorisation matrix.
//
// The read half is entity_space_scoping_integration_test.go and
// relations_xspace_integration_test.go. This file is its mirror: for every
// mutation route that reaches an entity by an id the middleware did not
// authorise, one case that must be refused and one that must still work.
//
// # Why both directions, every time
//
// A route that refused everything would satisfy the refusal half of every case
// here. The paired positive is what stops that, and it is not a formality — one
// of these predicates was written too tightly on the first attempt and only the
// positive direction caught it.
//
// # Why this persona
//
// testutil.CreateTestUser makes an org OWNER. The org-admin bypass in the
// middleware cannot observe a space boundary at all, so every case in this file
// would pass as that user with every fix in this branch deleted. The actor here
// is an org MEMBER holding an explicit grant on one space and nothing on the
// other — the weakest persona that still clears RequireWriteFloor.
//
// The decide case needs the opposite adjustment and gets its own note.
//
// # Why the assertions read state rather than status codes alone
//
// Several of these routes are :exec writes that answer 200 or 204 whether or
// not a row matched — deliberately, because a status that varied with what
// exists is the oracle these predicates remove. For those the status code
// proves nothing and the STATE is the assertion: the entity in the other space
// must be unchanged.

type writeMatrix struct {
	ts *testServer
	// mine is the space the actor holds a grant on; theirs is the space they
	// hold nothing on. Both are in the harness org unless a case says otherwise.
	mine, theirs testutil.Space
	actor        testutil.User
	token        string
	q            *generated.Queries
}

func newWriteMatrix(t *testing.T) *writeMatrix {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()

	m := &writeMatrix{ts: ts, q: generated.New(ts.DB.Pool)}
	m.mine = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")
	m.theirs = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")

	// RoleAgent, not RoleContributor. edit_any_item's minimum role is agent
	// (access.capability.go), and the sprint and backlog routes check it
	// in-handler — so a contributor is refused 403 BEFORE any reconciliation
	// runs, and a test written with one would pass with every predicate in this
	// branch deleted while appearing to exercise them.
	//
	// This is the capability-test trap with its polarity reversed: usually the
	// persona must fall SHORT of the capability under test, and here it must
	// clear it, so that the space predicate is the only thing left to refuse.
	// The actor is still an org member with one space-scoped grant, which is
	// well below the org-admin bypass.
	m.actor = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(ctx, ts.OrgID, m.mine.ID,
		access.SubjectUser, m.actor.ID, access.RoleAgent, ts.UserID)
	require.NoError(t, err)
	m.token = ts.tokenFor(t, m.actor.ID, m.actor.Email)
	return m
}

func (m *writeMatrix) base(space uuid.UUID) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, space)
}

func (m *writeMatrix) item(t *testing.T, space uuid.UUID, title string) uuid.UUID {
	t.Helper()
	row, err := m.q.CreateProjectItem(context.Background(), generated.CreateProjectItemParams{
		ID: uuid.New(), SpaceID: space, Kind: "task", Title: title,
		Description: "", Status: "open", Priority: "medium",
		ReporterID: m.ts.UserID, Labels: []string{}, Rank: "a",
	})
	require.NoError(t, err)
	return row.ID
}

func (m *writeMatrix) sprint(t *testing.T, space uuid.UUID, name string) uuid.UUID {
	t.Helper()
	row, err := m.q.CreateSprint(context.Background(), generated.CreateSprintParams{
		ID: uuid.New(), SpaceID: space, Name: name, Status: "planned", CreatedBy: m.ts.UserID,
	})
	require.NoError(t, err)
	return row.ID
}

// sprintOf reads an item's sprint straight from the database, because the
// routes under test answer the same thing either way.
func (m *writeMatrix) sprintOf(t *testing.T, itemID uuid.UUID) *uuid.UUID {
	t.Helper()
	var got *uuid.UUID
	require.NoError(t, m.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT sprint_id FROM project_items WHERE id = $1`, itemID).Scan(&got))
	return got
}

func pgtypeUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func (m *writeMatrix) postAs(t *testing.T, path string, body any) httpResult {
	t.Helper()
	return m.ts.postAs(t, m.token, path, body)
}

// ─── Sprint assignment: the URL item id ───────────────────────────────────────

// The item comes from the URL and the sprint from the body, and neither was
// reconciled with the {spaceID} the capability was checked against.
func TestWriteMatrix_AssignToSprint_IsScopedToTheSpace(t *testing.T) {
	m := newWriteMatrix(t)

	theirItem := m.item(t, m.theirs.ID, "Their item")
	theirSprint := m.sprint(t, m.theirs.ID, "Their sprint")
	myItem := m.item(t, m.mine.ID, "My item")
	mySprint := m.sprint(t, m.mine.ID, "My sprint")

	// Refused: their item, addressed through MY space, onto MY sprint. The
	// route answers 200 either way by design, so the state is the assertion.
	r := m.postAs(t, m.base(m.mine.ID)+"/projects/items/"+theirItem.String()+"/sprint",
		map[string]any{"sprint_id": mySprint.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Nil(t, m.sprintOf(t, theirItem),
		"an item in another space must not be moved onto this space's sprint")

	// Refused the other way round: MY item onto THEIR sprint. The item is
	// reachable, so only the sprint predicate can refuse this one.
	r = m.postAs(t, m.base(m.mine.ID)+"/projects/items/"+myItem.String()+"/sprint",
		map[string]any{"sprint_id": theirSprint.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Nil(t, m.sprintOf(t, myItem),
		"an item must not be moved onto a sprint in another space")

	// And the wholly-legitimate move still works, or refusing everything would
	// have passed both assertions above.
	r = m.postAs(t, m.base(m.mine.ID)+"/projects/items/"+myItem.String()+"/sprint",
		map[string]any{"sprint_id": mySprint.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	got := m.sprintOf(t, myItem)
	require.NotNil(t, got, "the caller's own item and sprint must still assign")
	require.Equal(t, mySprint, *got)
}

// ─── Backlog moves: BOTH ids come from the request body ───────────────────────

func TestWriteMatrix_BacklogMoveToSprint_IsScopedToTheSpace(t *testing.T) {
	m := newWriteMatrix(t)

	theirItem := m.item(t, m.theirs.ID, "Their item")
	theirSprint := m.sprint(t, m.theirs.ID, "Their sprint")
	myItem := m.item(t, m.mine.ID, "My item")
	mySprint := m.sprint(t, m.mine.ID, "My sprint")

	// A sprint in another space answers exactly as a sprint that does not
	// exist. Before the fix the lookup was unscoped, so a real foreign id
	// answered 200 and an invented one 404 — an existence oracle over every
	// organisation's sprints. Both halves are asserted so a fix that made them
	// differ the other way round would still fail.
	foreign := m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-sprint",
		map[string]any{"item_id": myItem.String(), "sprint_id": theirSprint.String()})
	absent := m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-sprint",
		map[string]any{"item_id": myItem.String(), "sprint_id": uuid.New().String()})
	require.Equal(t, http.StatusNotFound, foreign.StatusCode, "%s", foreign.Body)
	require.Equal(t, absent.StatusCode, foreign.StatusCode,
		"a real sprint elsewhere and an invented one must be indistinguishable")
	require.Nil(t, m.sprintOf(t, myItem), "a refused move must not have written")

	// Their item, named in the body, onto my sprint.
	r := m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-sprint",
		map[string]any{"item_id": theirItem.String(), "sprint_id": mySprint.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Nil(t, m.sprintOf(t, theirItem), "an item in another space must not be moved")

	// The legitimate move.
	r = m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-sprint",
		map[string]any{"item_id": myItem.String(), "sprint_id": mySprint.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	got := m.sprintOf(t, myItem)
	require.NotNil(t, got)
	require.Equal(t, mySprint, *got)
}

func TestWriteMatrix_BacklogMoveToBacklog_IsScopedToTheSpace(t *testing.T) {
	m := newWriteMatrix(t)

	theirItem := m.item(t, m.theirs.ID, "Their item")
	theirSprint := m.sprint(t, m.theirs.ID, "Their sprint")
	require.NoError(t, m.q.AssignProjectItemToSprintInSpace(context.Background(),
		generated.AssignProjectItemToSprintInSpaceParams{
			ItemID: theirItem, SpaceID: m.theirs.ID,
			SprintID: pgtypeUUID(theirSprint),
		}))

	myItem := m.item(t, m.mine.ID, "My item")
	mySprint := m.sprint(t, m.mine.ID, "My sprint")
	require.NoError(t, m.q.AssignProjectItemToSprintInSpace(context.Background(),
		generated.AssignProjectItemToSprintInSpaceParams{
			ItemID: myItem, SpaceID: m.mine.ID, SprintID: pgtypeUUID(mySprint),
		}))

	// Knocking another space's item off its sprint is the destructive half of
	// this route, and the item id arrives in the BODY, so nothing upstream has
	// looked at it at all.
	r := m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-backlog",
		map[string]any{"item_id": theirItem.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.NotNil(t, m.sprintOf(t, theirItem),
		"an item in another space must keep its sprint")

	r = m.postAs(t, m.base(m.mine.ID)+"/projects/backlog/move-to-backlog",
		map[string]any{"item_id": myItem.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Nil(t, m.sprintOf(t, myItem), "the caller's own item must still move")
}

// ─── Relations: the delete, and the near side of the create ───────────────────

func TestWriteMatrix_DeleteRelation_IsScopedToTheSpace(t *testing.T) {
	m := newWriteMatrix(t)
	ctx := context.Background()

	theirA := m.item(t, m.theirs.ID, "Theirs A")
	theirB := m.item(t, m.theirs.ID, "Theirs B")
	theirRel := uuid.New()
	_, err := m.q.CreateEntityRelation(ctx, generated.CreateEntityRelationParams{
		ID: theirRel, FromID: theirA, FromType: "project_item",
		ToID: theirB, ToType: "project_item", Kind: "relates_to", CreatedBy: m.ts.UserID,
	})
	require.NoError(t, err)

	mineA := m.item(t, m.mine.ID, "Mine A")
	mineB := m.item(t, m.mine.ID, "Mine B")
	myRel := uuid.New()
	_, err = m.q.CreateEntityRelation(ctx, generated.CreateEntityRelationParams{
		ID: myRel, FromID: mineA, FromType: "project_item",
		ToID: mineB, ToType: "project_item", Kind: "relates_to", CreatedBy: m.ts.UserID,
	})
	require.NoError(t, err)

	relationExists := func(id uuid.UUID) bool {
		var n int
		require.NoError(t, m.ts.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM entity_relations WHERE id = $1`, id).Scan(&n))
		return n == 1
	}

	// A relation neither of whose endpoints is in {spaceID}. It answers 204 —
	// the same as deleting something that was never there, which is what keeps
	// this route from reporting whether a relation id is real.
	r := m.ts.deleteAs(t, m.token, m.base(m.mine.ID)+"/projects/relations/"+theirRel.String())
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.True(t, relationExists(theirRel), "another space's relation must survive")

	absent := m.ts.deleteAs(t, m.token, m.base(m.mine.ID)+"/projects/relations/"+uuid.New().String())
	require.Equal(t, r.StatusCode, absent.StatusCode,
		"a relation elsewhere and one that never existed must answer identically")

	r = m.ts.deleteAs(t, m.token, m.base(m.mine.ID)+"/projects/relations/"+myRel.String())
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.False(t, relationExists(myRel), "the caller's own relation must still delete")
}

// The near side of a create is the URL's item id, and only the far side was
// ever resolved.
func TestWriteMatrix_CreateRelation_NearSideIsScopedToTheSpace(t *testing.T) {
	m := newWriteMatrix(t)

	theirItem := m.item(t, m.theirs.ID, "Their item")
	myItem := m.item(t, m.mine.ID, "My item")
	myOther := m.item(t, m.mine.ID, "My other item")

	countFor := func(id uuid.UUID) int {
		var n int
		require.NoError(t, m.ts.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM entity_relations WHERE from_id = $1`, id).Scan(&n))
		return n
	}

	// Hanging a relation off an item in another space, addressed through mine.
	r := m.postAs(t, m.base(m.mine.ID)+"/projects/items/"+theirItem.String()+"/relations",
		map[string]any{"to_id": myItem.String(), "to_type": "project_item", "kind": "relates_to"})
	require.Equal(t, http.StatusNotFound, r.StatusCode, "%s", r.Body)
	require.Zero(t, countFor(theirItem),
		"nothing may be attached to an item in a space the caller has no grant on")

	r = m.postAs(t, m.base(m.mine.ID)+"/projects/items/"+myItem.String()+"/relations",
		map[string]any{"to_id": myOther.String(), "to_type": "project_item", "kind": "relates_to"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	require.Equal(t, 1, countFor(myItem), "a relation within the caller's own space must still be created")
}

// ─── Labels: the boundary is the ORGANISATION, not a space ────────────────────

// The route is open to any org member by design, so nothing above it constrains
// the id at all and the org predicate is the only thing there is.
func TestWriteMatrix_DeleteLabel_IsScopedToTheOrganisation(t *testing.T) {
	m := newWriteMatrix(t)
	ctx := context.Background()

	otherOrg := testutil.CreateTestOrg(t, m.ts.DB.Pool)
	foreign, err := m.q.CreateLabel(ctx, generated.CreateLabelParams{
		ID: uuid.New(), OrgID: otherOrg.ID, Name: "their-label", Color: "#ff0000",
	})
	require.NoError(t, err)
	own, err := m.q.CreateLabel(ctx, generated.CreateLabelParams{
		ID: uuid.New(), OrgID: m.ts.OrgID, Name: "my-label", Color: "#00ff00",
	})
	require.NoError(t, err)

	labelExists := func(id uuid.UUID) bool {
		var n int
		require.NoError(t, m.ts.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM labels WHERE id = $1`, id).Scan(&n))
		return n == 1
	}

	path := fmt.Sprintf("/api/v1/orgs/%s/labels/%s", m.ts.OrgID, foreign.ID)
	r := m.ts.deleteAs(t, m.token, path)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.True(t, labelExists(foreign.ID),
		"a label belonging to another organisation must survive; labels have no soft delete")

	r = m.ts.deleteAs(t, m.token, fmt.Sprintf("/api/v1/orgs/%s/labels/%s", m.ts.OrgID, own.ID))
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.False(t, labelExists(own.ID), "the caller's own organisation's label must still delete")
}

// ─── Comments: the parent id from the request body ────────────────────────────

func TestWriteMatrix_CommentParentIsReconciledAgainstTheEntity(t *testing.T) {
	m := newWriteMatrix(t)
	ctx := context.Background()

	myItem := m.item(t, m.mine.ID, "My item")
	theirItem := m.item(t, m.theirs.ID, "Their item")

	// A comment on the far item, which the actor cannot see at all.
	farParent, err := m.q.CreateComment(ctx, generated.CreateCommentParams{
		ID: uuid.New(), EntityType: "project_item", EntityID: theirItem,
		// migration 045's comments_author_identity requires an author or a
		// requester; the owner stands in for somebody who works in that space.
		AuthorID: pgtypeUUID(m.ts.UserID),
		Body:     "a thread in another space", Visibility: "internal",
	})
	require.NoError(t, err)

	path := m.base(m.mine.ID) + "/projects/items/" + myItem.String() + "/comments"

	// Grafting onto a thread on another entity, and the same request naming a
	// parent that does not exist. Before the reconciliation these differed —
	// 201 versus a foreign-key 500 — which reported whether a comment id was
	// real anywhere in the installation.
	foreign := m.postAs(t, path, map[string]any{"content": "reply", "parent_id": farParent.ID.String()})
	absent := m.postAs(t, path, map[string]any{"content": "reply", "parent_id": uuid.New().String()})
	require.Equal(t, http.StatusNotFound, foreign.StatusCode, "%s", foreign.Body)
	require.Equal(t, absent.StatusCode, foreign.StatusCode,
		"a real comment elsewhere and an invented id must be indistinguishable")

	// A top-level comment, and then a reply to it, both still work.
	root := m.postAs(t, path, map[string]any{"content": "root"})
	require.Equal(t, http.StatusCreated, root.StatusCode, "%s", root.Body)
	var rootBody struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(root.Body, &rootBody))

	reply := m.postAs(t, path, map[string]any{"content": "reply", "parent_id": rootBody.ID})
	require.Equal(t, http.StatusCreated, reply.StatusCode,
		"a reply within the same entity's own thread must still be accepted: %s", reply.Body)
}

// ─── Ticket assignment: the assignee id from the request body ─────────────────

func TestWriteMatrix_AssignRefusesAnAssigneeOutsideTheOrganisation(t *testing.T) {
	m := newWriteMatrix(t)
	ctx := context.Background()

	beacon := testutil.CreateTestSpace(t, m.ts.DB.Pool, m.ts.OrgID, m.ts.UserID, "beacon")
	_, err := m.ts.GrantService.Create(ctx, m.ts.OrgID, beacon.ID,
		access.SubjectUser, m.actor.ID, access.RoleAgent, m.ts.UserID)
	require.NoError(t, err)
	// A beacon space of its own, because tickets live there rather than in the
	// vector spaces the rest of this fixture uses.

	r := m.postAs(t, m.base(beacon.ID)+"/tickets",
		map[string]any{"title": "Assignable", "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))

	otherOrg := testutil.CreateTestOrg(t, m.ts.DB.Pool)
	outsider := testutil.CreateTestUser(t, m.ts.DB.Pool, otherOrg.ID)

	path := m.base(beacon.ID) + "/tickets/" + created.ID + "/assign"
	r = m.postAs(t, path, map[string]any{"assignee_id": outsider.ID.String()})
	require.Equal(t, http.StatusBadRequest, r.StatusCode,
		"a user in another organisation must be refused: %s", r.Body)

	var assignee *uuid.UUID
	require.NoError(t, m.ts.DB.Pool.QueryRow(ctx,
		`SELECT assignee_id FROM tickets WHERE id = $1`, uuid.MustParse(created.ID)).Scan(&assignee))
	require.Nil(t, assignee, "a refused assignment must not have been written")

	// A member of this organisation still assigns, so a check that refused
	// everybody could not pass this test.
	r = m.postAs(t, path, map[string]any{"assignee_id": m.actor.ID.String()})
	require.Equal(t, http.StatusOK, r.StatusCode, "an org member must still be assignable: %s", r.Body)
}

// ─── The decide route, which needs the opposite persona adjustment ────────────

// A configured approver may decide only in the space the request was made in.
//
// The persona here is deliberately the STRONGEST one that must still be
// refused: a user who really is a configured approver on the transition, and
// really does hold the write floor in the space they are calling through. A
// viewer would be refused upstream by RequireWriteFloor and a non-approver by
// the approver check, so either would pass this test with the space predicate
// deleted — measuring a gate that is not the one under test.
//
// That the approver check cannot help is structural rather than incidental:
// approvers hang off a transition, a transition belongs to a workflow, and
// migration 019 assigns one workflow to every space in the org. Being an
// approver is therefore an org-wide fact by construction.
func TestWriteMatrix_DecideApproval_IsScopedToTheApprovalsOwnSpace(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	// A second beacon space on the SAME workflow, which is the default shape.
	other := testutil.CreateTestSpace(t, f.ts.DB.Pool, f.ts.OrgID, f.ts.UserID, "beacon")
	require.NoError(t, f.ts.WorkflowAdapter.AssignDefaultWorkflowToSpace(ctx, f.ts.OrgID, "beacon", other.ID))

	// The approval belongs to f.spaceID. ts.UserID is named as its approver, so
	// the authority check will pass wherever it is asked from.
	approvalID := f.requestApproval(t, f.ts.UserID)
	require.Equal(t, "open", f.statusNow(t))

	// Decided through the OTHER space's URL. The caller is a genuine approver
	// and genuinely privileged in that space; the only thing wrong is that the
	// approval lives somewhere else.
	wrongSpace := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/approvals/%s/decide",
		f.ts.OrgID, other.ID, approvalID)
	r := f.ts.post(t, wrongSpace, map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"an approval in another space must not be decidable: %s", r.Body)

	// Identical to an approval id that names nothing — so the refusal reports
	// no state. Before the predicate these differed: a pending approval
	// elsewhere reached the already-decided and approver branches, which answer
	// 409 and 403.
	absent := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/approvals/%s/decide",
		f.ts.OrgID, other.ID, uuid.New()), map[string]any{"decision": "approved"}, true)
	require.Equal(t, r.StatusCode, absent.StatusCode,
		"an approval elsewhere and one that never existed must answer identically")

	require.Equal(t, "open", f.statusNow(t),
		"the refused decision must not have moved the item, and must not have been recorded")

	// Through its own space it still decides and still applies — without this
	// the assertions above would pass against a route that refused everything.
	r = f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "in_progress", f.statusNow(t),
		"the approval's own space must still decide, and the transition must still apply")
}
