package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// CapManageQueue finally gets placed in P4 PR-B, and this is the test that
// proves the gate rather than the middleware around it.
//
// THE PERSONA IS A CONTRIBUTOR, AND THAT IS THE WHOLE POINT. A "viewer is
// refused" test would prove nothing here: a viewer is already refused upstream
// by RequireWriteFloor(CapCreateItems), so it passes with the in-handler
// access.Can check deleted. ADR-0007 puts manage_queue at the AGENT role, so
// the subject who is past the write floor and short of the capability is a
// contributor. Every refusal below uses one, and every permission below uses
// an agent in the same space — so the pair distinguishes "the gate works" from
// "nothing works".

func queuesPath(orgID, spaceID uuid.UUID) string {
	return "/api/v1/orgs/" + orgID.String() + "/spaces/" + spaceID.String() + "/queues"
}

// grantSpaceRole gives a user an explicit role on a space.
func grantSpaceRole(t *testing.T, ts *testServer, spaceID, userID uuid.UUID, role string) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO space_grants (org_id, space_id, subject_type, subject_id, role, created_by)
		 VALUES ($1,$2,'user',$3,$4,$5)`,
		ts.OrgID, spaceID, userID, role, ts.UserID)
	require.NoError(t, err)
}

// personaOn creates a non-admin org member holding one space role, and returns
// their token.
func personaOn(t *testing.T, ts *testServer, spaceID uuid.UUID, role string) string {
	t.Helper()
	u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, spaceID, u.ID, role)
	return ts.tokenFor(t, u.ID, u.Email)
}

func beaconSpaceForQueues(t *testing.T, ts *testServer) uuid.UUID {
	t.Helper()
	s := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	testutil.SetSpaceVisibility(t, ts.DB.Pool, s.ID, "hidden")
	return s.ID
}

const queueQueryDoc = `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`

// TestQueueCapability_ContributorIsRefusedAgentIsAllowed is the mutation-shaped
// pair. Delete the access.Can(CapManageQueue) check in requireManageQueue and
// every "refused" case below turns into a success — that is the fails-before
// evidence for this gate.
func TestQueueCapability_ContributorIsRefusedAgentIsAllowed(t *testing.T) {
	ts := newTestServer(t)
	spaceID := beaconSpaceForQueues(t, ts)

	contributor := personaOn(t, ts, spaceID, "contributor")
	agent := personaOn(t, ts, spaceID, "agent")

	body := map[string]any{"name": "Escalations", "query": json.RawMessage(queueQueryDoc)}

	t.Run("contributor cannot create", func(t *testing.T) {
		res := ts.postAs(t, contributor, queuesPath(ts.OrgID, spaceID), body)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a contributor is past the write floor and short of manage_queue: %s", res.Body)
	})

	t.Run("contributor cannot seed the defaults", func(t *testing.T) {
		res := ts.postAs(t, contributor, queuesPath(ts.OrgID, spaceID)+"/defaults", nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", res.Body)
	})

	t.Run("agent can create", func(t *testing.T) {
		res := ts.postAs(t, agent, queuesPath(ts.OrgID, spaceID), body)
		require.Equal(t, http.StatusCreated, res.StatusCode,
			"manage_queue sits at the agent role in ADR-0007: %s", res.Body)
	})

	t.Run("contributor can still READ the queues", func(t *testing.T) {
		// The capability governs management, not visibility: a queue's
		// audience is the readers of its space.
		res := ts.getAs(t, contributor, queuesPath(ts.OrgID, spaceID))
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		var out struct {
			Queues    []map[string]any `json:"queues"`
			CanManage bool             `json:"can_manage"`
		}
		require.NoError(t, json.Unmarshal(res.Body, &out))
		require.Len(t, out.Queues, 1)
		require.False(t, out.CanManage,
			"the response tells the UI not to render management controls, so it need not reproduce the rule")
	})
}

// TestQueueDefaults_AreIdempotent pins the guarantee migration 039 makes
// structural: the seeding insert is ON CONFLICT DO NOTHING against
// (space_id, name), so pressing the button twice cannot duplicate.
func TestQueueDefaults_AreIdempotent(t *testing.T) {
	ts := newTestServer(t)
	spaceID := beaconSpaceForQueues(t, ts)
	agent := personaOn(t, ts, spaceID, "agent")
	path := queuesPath(ts.OrgID, spaceID)

	created := func(token string) int {
		res := ts.postAs(t, token, path+"/defaults", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)
		var out struct {
			Created int `json:"created"`
		}
		require.NoError(t, json.Unmarshal(res.Body, &out))
		return out.Created
	}

	require.Equal(t, 4, created(agent), "the first press creates the whole default set")
	require.Equal(t, 0, created(agent), "the second press creates nothing")

	res := ts.getAs(t, agent, path)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out struct {
		Queues []struct {
			Name     string `json:"name"`
			Position int32  `json:"position"`
		} `json:"queues"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Len(t, out.Queues, 4, "four presses of the button, four queues")

	names := make([]string, 0, 4)
	for i, q := range out.Queues {
		names = append(names, q.Name)
		require.Equal(t, int32(i), q.Position, "positions must be dense and in listing order")
	}
	require.Equal(t, []string{"All open", "Assigned to me", "Unassigned", "Recently resolved"}, names)
}

// TestQueueReorder_IsATransactionalPermutation covers the ordering integrity
// the DEFERRABLE constraint exists for, and the refusal that keeps a partial
// reorder from silently interleaving the queues nobody mentioned.
func TestQueueReorder_IsATransactionalPermutation(t *testing.T) {
	ts := newTestServer(t)
	spaceID := beaconSpaceForQueues(t, ts)
	agent := personaOn(t, ts, spaceID, "agent")
	contributor := personaOn(t, ts, spaceID, "contributor")
	path := queuesPath(ts.OrgID, spaceID)

	res := ts.postAs(t, agent, path+"/defaults", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	list := func() []uuid.UUID {
		r := ts.getAs(t, agent, path)
		require.Equal(t, http.StatusOK, r.StatusCode)
		var out struct {
			Queues []struct {
				ID uuid.UUID `json:"id"`
			} `json:"queues"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &out))
		ids := make([]uuid.UUID, 0, len(out.Queues))
		for _, q := range out.Queues {
			ids = append(ids, q.ID)
		}
		return ids
	}

	before := list()
	require.Len(t, before, 4)

	t.Run("a full reversal is applied atomically", func(t *testing.T) {
		// Reversing every position at once is the case a non-deferred unique
		// constraint cannot express without shuffling through temporary slots.
		reversed := []uuid.UUID{before[3], before[2], before[1], before[0]}
		r := ts.putAs(t, agent, path+"/order", map[string]any{"queue_ids": reversed})
		require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
		require.Equal(t, reversed, list(), "the whole order must land")
	})

	t.Run("a partial list is refused", func(t *testing.T) {
		current := list()
		r := ts.putAs(t, agent, path+"/order", map[string]any{"queue_ids": current[:2]})
		require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
			"a partial reorder would leave the unnamed queues at stale positions: %s", r.Body)
		require.Equal(t, current, list(), "and it must change nothing")
	})

	t.Run("a duplicate id is refused", func(t *testing.T) {
		current := list()
		bad := []uuid.UUID{current[0], current[0], current[1], current[2]}
		r := ts.putAs(t, agent, path+"/order", map[string]any{"queue_ids": bad})
		require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode, "%s", r.Body)
	})

	t.Run("a contributor cannot reorder", func(t *testing.T) {
		r := ts.putAs(t, contributor, path+"/order", map[string]any{"queue_ids": list()})
		require.Equal(t, http.StatusForbidden, r.StatusCode, "%s", r.Body)
	})
}

// TestQueueResults_MeTokenResolvesPerAgent is the queue-shaped half of the
// per-viewer contract: one "Assigned to me" queue, two agents, each seeing
// their own work. It is what makes the default set useful rather than a
// per-person copy of the same query.
func TestQueueResults_MeTokenResolvesPerAgent(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	spaceID := beaconSpaceForQueues(t, ts)

	first := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, spaceID, first.ID, "agent")
	firstToken := ts.tokenFor(t, first.ID, first.Email)
	second := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grantSpaceRole(t, ts, spaceID, second.ID, "agent")
	secondToken := ts.tokenFor(t, second.ID, second.Email)

	mk := func(n int32, title string, assignee uuid.UUID) uuid.UUID {
		id := uuid.New()
		_, err := ts.DB.Pool.Exec(ctx,
			`INSERT INTO tickets (id, space_id, number, title, reporter_id, status, priority, assignee_id)
			 VALUES ($1,$2,$3,$4,$5,'open','medium',$6)`, id, spaceID, n, title, ts.UserID, assignee)
		require.NoError(t, err)
		return id
	}
	firstTicket := mk(1, "first agent work", first.ID)
	secondTicket := mk(2, "second agent work", second.ID)

	path := queuesPath(ts.OrgID, spaceID)
	require.Equal(t, http.StatusOK, ts.postAs(t, firstToken, path+"/defaults", nil).StatusCode)

	// Find the "Assigned to me" queue — one row, shared by both agents.
	res := ts.getAs(t, firstToken, path)
	var listed struct {
		Queues []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"queues"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &listed))
	var mine uuid.UUID
	for _, q := range listed.Queues {
		if q.Name == "Assigned to me" {
			mine = q.ID
		}
	}
	require.NotEqual(t, uuid.Nil, mine)

	read := func(token string) map[uuid.UUID]bool {
		r := ts.getAs(t, token, path+"/"+mine.String()+"/results")
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
		var out struct {
			Results []struct {
				ID uuid.UUID `json:"id"`
			} `json:"results"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &out))
		got := map[uuid.UUID]bool{}
		for _, x := range out.Results {
			got[x.ID] = true
		}
		return got
	}

	a, b := read(firstToken), read(secondToken)
	require.True(t, a[firstTicket])
	require.False(t, a[secondTicket], "one stored queue must mean 'mine' for each agent separately")
	require.True(t, b[secondTicket])
	require.False(t, b[firstTicket])
}
