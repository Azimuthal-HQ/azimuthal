package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The parts of the saved-view and queue wire format that only appear when the
// underlying row actually carries the field.
//
// A nil due date, a view with no team and a ticket with no type all take the
// SAME branch — the absent one — so a suite that only ever creates the simple
// case tests one half of each optional field and reports the other as covered
// because the function ran. These fill in the other half.
//
// P5 has a direct stake in this: a gadget renders exactly these fields, and a
// timestamp that serialised as `"0001-01-01T00:00:00Z"` instead of being
// omitted would render as a due date in the year one on every tile.

func vwsBody(t *testing.T, res httpResult, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(res.Body, out), "body: %s", res.Body)
}

// Every optional field on a result row, populated. rfc3339Ptr formats a real
// time in one direction and returns nil in the other; only the second is
// reached by a row with no dates.
func TestViewWireShapes_OptionalResultFieldsAreOmittedNotZeroed(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	spaceID := createScopedSpace(t, ts, "Wire Vector", "wire-vector", "vector")

	due := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	res := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", org, spaceID),
		map[string]any{
			"title": "Dated item", "kind": "task", "priority": "high",
			"due_at": due, "assignee_id": ts.UserID.String(),
		}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)

	res = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", org, spaceID),
		map[string]any{"title": "Bare item", "kind": "task", "priority": "low"}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)

	res = ts.post(t, "/api/v1/orgs/"+org+"/views/preview", map[string]any{
		"query": json.RawMessage(
			`{"v":1,"filter":{"modules":["vector"]},"sort":{"field":"updated_at","dir":"desc"}}`),
	}, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var page struct {
		Results []struct {
			Title        string  `json:"title"`
			Kind         *string `json:"kind"`
			DueAt        *string `json:"due_at"`
			ResolvedAt   *string `json:"resolved_at"`
			AssigneeID   *string `json:"assignee_id"`
			AssigneeName *string `json:"assignee_name"`
		} `json:"results"`
	}
	vwsBody(t, res, &page)
	require.Len(t, page.Results, 2)

	byTitle := map[string]int{}
	for i, r := range page.Results {
		byTitle[r.Title] = i
	}

	dated := page.Results[byTitle["Dated item"]]
	require.NotNil(t, dated.DueAt, "a due date that the row carries must reach the wire")
	require.NotEqual(t, "0001-01-01T00:00:00Z", *dated.DueAt,
		"a zero time on the wire renders as a due date in the year one")
	require.Nil(t, dated.ResolvedAt, "an unresolved item carries no resolved_at")
	require.NotNil(t, dated.Kind)
	require.NotNil(t, dated.AssigneeID)
	require.NotNil(t, dated.AssigneeName,
		"the assignee's name is joined in the fan-out — a null here would make every tile look up a user per row")

	bare := page.Results[byTitle["Bare item"]]
	require.Nil(t, bare.DueAt, "an absent date is omitted, not zeroed")
	require.Nil(t, bare.AssigneeID)
	require.Nil(t, bare.AssigneeName)
}

// A team-audience view carries the team's NAME, joined server-side. Without it
// the UI can only show an opaque uuid for "who is this shared with".
func TestViewWireShapes_ATeamAudienceCarriesTheTeamName(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	team := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)

	res := ts.post(t, "/api/v1/orgs/"+org+"/views", map[string]any{
		"name": "Team view", "visibility": "team", "visibility_team_id": team.String(),
		"query": json.RawMessage(beaconViewQuery),
	}, true)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", res.Body)
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	vwsBody(t, res, &created)

	// The name is joined on the LIST and on the detail read, not on the create
	// response — the create knows the id it was handed and nothing more.
	res = ts.get(t, "/api/v1/orgs/"+org+"/views", true)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var list struct {
		Views []struct {
			ID       uuid.UUID `json:"id"`
			TeamName string    `json:"team_name"`
			IsOwner  bool      `json:"is_owner"`
			IsValid  bool      `json:"is_valid"`
		} `json:"views"`
	}
	vwsBody(t, res, &list)

	var found bool
	for _, v := range list.Views {
		if v.ID != created.ID {
			continue
		}
		found = true
		require.NotEmpty(t, v.TeamName, "a team-shared view must name its team, not just its id")
		require.True(t, v.IsOwner)
		require.True(t, v.IsValid)
	}
	require.True(t, found, "the owner's own team view must list for them")
}

// The queue surface, end to end. Queues are saved views with a space binding,
// and P5 changed the audience rule they resolve through — so the lifecycle is
// re-proven rather than assumed.
func TestViewWireShapes_QueueLifecycle(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	spaceID := createScopedSpace(t, ts, "Queue Desk", "queue-desk", "beacon")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/queues", org, spaceID)

	// The one-click default set is idempotent BY CONSTRUCTION: pressing it
	// twice must add nothing rather than duplicating four queues.
	res := ts.post(t, base+"/defaults", nil, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	var seeded struct {
		Created int `json:"created"`
	}
	vwsBody(t, res, &seeded)
	require.Equal(t, 4, seeded.Created, "the default set is four queues")

	res = ts.post(t, base+"/defaults", nil, true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	vwsBody(t, res, &seeded)
	require.Zero(t, seeded.Created, "pressing it twice adds nothing")

	type queueRow struct {
		ID        uuid.UUID       `json:"id"`
		Name      string          `json:"name"`
		Position  int32           `json:"position"`
		SpaceID   uuid.UUID       `json:"space_id"`
		CanManage bool            `json:"can_manage"`
		Query     json.RawMessage `json:"query"`
	}
	listQueues := func(token string) []queueRow {
		r := ts.getAs(t, token, base)
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
		var out struct {
			Queues    []queueRow `json:"queues"`
			CanManage bool       `json:"can_manage"`
		}
		vwsBody(t, r, &out)
		return out.Queues
	}

	queues := listQueues(ts.Token)
	require.Len(t, queues, 4)
	for i, q := range queues {
		require.Equal(t, int32(i), q.Position, "queues come back in server order, never client-sorted")
		require.Equal(t, spaceID, q.SpaceID.String())
		require.True(t, q.CanManage, "an org admin manages every queue")
	}

	// A reorder must be a PERMUTATION of the space's live queues. A partial
	// list would leave the unmentioned ones at stale positions and silently
	// interleave them.
	ids := make([]string, 0, len(queues))
	for _, q := range queues {
		ids = append(ids, q.ID.String())
	}
	res = ts.putAs(t, ts.Token, base+"/order", map[string]any{"queue_ids": ids[:2]})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
		"a partial reorder must be refused, not half-applied: %s", res.Body)

	reversed := []string{ids[3], ids[2], ids[1], ids[0]}
	res = ts.putAs(t, ts.Token, base+"/order", map[string]any{"queue_ids": reversed})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", res.Body)

	after := listQueues(ts.Token)
	require.Equal(t, queues[3].ID, after[0].ID, "the order the server returns is the order that was set")

	// Rename and re-query one.
	res = ts.patchAs(t, ts.Token, base+"/"+after[0].ID.String(), map[string]any{
		"name": "Renamed queue", "description": "changed",
		"query": json.RawMessage(beaconViewQuery),
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	var updated queueRow
	vwsBody(t, res, &updated)
	require.Equal(t, "Renamed queue", updated.Name)

	// A duplicate name in the same space is a conflict, not a second queue.
	res = ts.post(t, base, map[string]any{
		"name": "Renamed queue", "query": json.RawMessage(beaconViewQuery),
	}, true)
	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", res.Body)

	// A queue may not read Vector: pinned to a Beacon space, a Vector module
	// could never match anything, so it is refused at write time rather than
	// sitting in the sidebar returning nothing forever.
	res = ts.post(t, base, map[string]any{
		"name": "Wrong module",
		"query": json.RawMessage(
			`{"v":1,"filter":{"modules":["vector"]},"sort":{"field":"updated_at","dir":"desc"}}`),
	}, true)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", res.Body)

	// Results resolve per viewer, through the same path a saved view takes.
	res = ts.get(t, base+"/"+after[0].ID.String()+"/results", true)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	require.Contains(t, string(res.Body), `"results"`)

	// Delete, and it is gone from the list.
	res = ts.deleteAs(t, ts.Token, base+"/"+after[0].ID.String())
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", res.Body)
	require.Len(t, listQueues(ts.Token), 3)

	res = ts.deleteAs(t, ts.Token, base+"/"+after[0].ID.String())
	require.Equal(t, http.StatusNotFound, res.StatusCode, "deleting it twice is 404, not 204")
}

// can_manage is computed server-side and answered on the LIST as well as per
// row, so an empty space still tells the UI whether to offer the button. A
// missing capability answer must hide the control, never offer it.
func TestViewWireShapes_QueueCanManageIsAnsweredForAnEmptySpace(t *testing.T) {
	ts := newTestServer(t)
	org := ts.OrgID.String()
	spaceID := createScopedSpace(t, ts, "Empty Desk", "empty-desk", "beacon")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/queues", org, spaceID)

	// A CONTRIBUTOR, not a viewer: manage_queue sits at the agent role, and a
	// viewer is refused upstream by the write floor — a viewer test would
	// assert the middleware and pass with the in-handler gate deleted.
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token := ts.tokenFor(t, member.ID, member.Email)
	_, err := ts.GrantService.Create(t.Context(), ts.OrgID, uuid.MustParse(spaceID),
		access.SubjectUser, member.ID, access.RoleContributor, ts.UserID)
	require.NoError(t, err)

	res := ts.getAs(t, token, base)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
	var out struct {
		Queues    []json.RawMessage `json:"queues"`
		CanManage bool              `json:"can_manage"`
	}
	vwsBody(t, res, &out)
	require.Empty(t, out.Queues)
	require.False(t, out.CanManage,
		"a contributor clears the write floor and still may not manage queues — this is the in-handler gate, not the middleware")

	// And the gate actually refuses the write.
	res = ts.postAs(t, token, base, map[string]any{
		"name": "Nope", "query": json.RawMessage(beaconViewQuery),
	})
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", res.Body)

	res = ts.postAs(t, token, base+"/defaults", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", res.Body)
}
