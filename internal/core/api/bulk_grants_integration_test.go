package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	coreteams "github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
)

// P2.5 W6 bulk grant editing, tested before any interface exists (per the
// orchestration guidance). Failure mode 4: a bulk change is ONE transaction
// carrying ONE batch_id — partial application is not acceptable. Failure
// mode 5: a ghosted (inherited) cell has no grant row; acting on it creates
// a direct grant and never edits the descendant's row.

type bulkFixture struct {
	ts             *testServer
	parent, child  coreteams.Team
	spaceA, spaceB string
	spaceAID       uuid.UUID
	spaceBID       uuid.UUID
}

func newBulkFixture(t *testing.T) *bulkFixture {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()
	f := &bulkFixture{ts: ts}

	var err error
	f.parent, err = ts.TeamService.Create(ctx, ts.OrgID, nil, "bulk-parent", "Bulk Parent", "")
	require.NoError(t, err)
	f.child, err = ts.TeamService.Create(ctx, ts.OrgID, &f.parent.ID, "bulk-child", "Bulk Child", "")
	require.NoError(t, err)

	f.spaceA = createScopedSpace(t, ts, "Bulk Space A", "bulk-space-a", "vector")
	f.spaceB = createScopedSpace(t, ts, "Bulk Space B", "bulk-space-b", "codex")
	f.spaceAID = uuid.MustParse(f.spaceA)
	f.spaceBID = uuid.MustParse(f.spaceB)
	return f
}

type bulkResult struct {
	BatchID   *uuid.UUID `json:"batch_id"`
	TicketRef *string    `json:"ticket_ref"`
	Creates   int        `json:"creates"`
	Updates   int        `json:"updates"`
	Revokes   int        `json:"revokes"`
	Noops     int        `json:"noops"`
	Actions   []struct {
		TeamID   uuid.UUID `json:"team_id"`
		SpaceID  uuid.UUID `json:"space_id"`
		Action   string    `json:"action"`
		FromRole string    `json:"from_role"`
		ToRole   string    `json:"to_role"`
	} `json:"actions"`
}

func (f *bulkFixture) preview(t *testing.T, changes []map[string]any) (bulkResult, httpResult) {
	t.Helper()
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/grants/bulk-preview", f.ts.OrgID),
		map[string]any{"changes": changes}, true)
	var res bulkResult
	if r.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(r.Body, &res))
	}
	return res, r
}

func (f *bulkFixture) apply(t *testing.T, changes []map[string]any, ticketRef string) (bulkResult, httpResult) {
	t.Helper()
	body := map[string]any{"changes": changes}
	if ticketRef != "" {
		body["ticket_ref"] = ticketRef
	}
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/grants/bulk-apply", f.ts.OrgID), body, true)
	var res bulkResult
	if r.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(r.Body, &res))
	}
	return res, r
}

func cell(teamID uuid.UUID, spaceID string, role any) map[string]any {
	return map[string]any{"team_id": teamID, "space_id": spaceID, "role": role}
}

func (f *bulkFixture) grantCount(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM space_grants WHERE org_id = $1 AND subject_type = 'team'`, f.ts.OrgID).Scan(&n))
	return n
}

func TestBulkGrants_PreviewCountsMatchApply(t *testing.T) {
	f := newBulkFixture(t)

	// Existing state: child has viewer on A (will be updated) and
	// contributor on B (will be revoked).
	_, r := f.apply(t, []map[string]any{
		cell(f.child.ID, f.spaceA, "viewer"),
		cell(f.child.ID, f.spaceB, "contributor"),
	}, "")
	require.Equal(t, http.StatusOK, r.StatusCode, "seed: %s", r.Body)

	changes := []map[string]any{
		cell(f.parent.ID, f.spaceA, "agent"),      // create
		cell(f.child.ID, f.spaceA, "contributor"), // update viewer→contributor
		cell(f.child.ID, f.spaceB, nil),           // revoke
		cell(f.parent.ID, f.spaceB, nil),          // noop (no grant exists)
	}

	pv, r := f.preview(t, changes)
	require.Equal(t, http.StatusOK, r.StatusCode, "preview: %s", r.Body)
	require.Equal(t, 1, pv.Creates)
	require.Equal(t, 1, pv.Updates)
	require.Equal(t, 1, pv.Revokes)
	require.Equal(t, 1, pv.Noops)
	require.Nil(t, pv.BatchID, "preview must not mint a batch")

	ap, r := f.apply(t, changes, "")
	require.Equal(t, http.StatusOK, r.StatusCode, "apply: %s", r.Body)

	// The preview counts are exactly what applied — same computation, not
	// an estimate (DoD: tested, not assumed).
	require.Equal(t, pv.Creates, ap.Creates)
	require.Equal(t, pv.Updates, ap.Updates)
	require.Equal(t, pv.Revokes, ap.Revokes)
	require.Equal(t, pv.Noops, ap.Noops)
	require.Equal(t, len(pv.Actions), len(ap.Actions))
	for i := range pv.Actions {
		require.Equal(t, pv.Actions[i], ap.Actions[i], "itemised action %d must match the preview", i)
	}
	require.NotNil(t, ap.BatchID)

	// And the database agrees: parent/agent on A created, child updated on
	// A, child's B grant gone.
	var role string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT role FROM space_grants WHERE subject_type='team' AND subject_id=$1 AND space_id=$2`,
		f.parent.ID, f.spaceAID).Scan(&role))
	require.Equal(t, "agent", role)
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT role FROM space_grants WHERE subject_type='team' AND subject_id=$1 AND space_id=$2`,
		f.child.ID, f.spaceAID).Scan(&role))
	require.Equal(t, "contributor", role)
	var n int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM space_grants WHERE subject_type='team' AND subject_id=$1 AND space_id=$2`,
		f.child.ID, f.spaceBID).Scan(&n))
	require.Zero(t, n, "revoked grant must be gone")
}

func TestBulkGrants_OneTransactionOneBatchID_AuditBatch(t *testing.T) {
	f := newBulkFixture(t)

	ap, r := f.apply(t, []map[string]any{
		cell(f.parent.ID, f.spaceA, "viewer"),
		cell(f.parent.ID, f.spaceB, "viewer"),
		cell(f.child.ID, f.spaceA, "contributor"),
	}, "BEA-42")
	require.Equal(t, http.StatusOK, r.StatusCode, "apply: %s", r.Body)
	require.NotNil(t, ap.BatchID)

	// S7: the apply response echoes the ticket_ref (alongside batch_id) so the
	// confirmation surface can show the operator it was recorded.
	require.NotNil(t, ap.TicketRef, "apply response must echo ticket_ref: %s", r.Body)
	require.Equal(t, "BEA-42", *ap.TicketRef)

	// Every audit event of the batch carries the ONE batch_id and the
	// ticket_ref, written in the same transaction as the grants.
	rows, err := f.ts.DB.Pool.Query(context.Background(),
		`SELECT action, ticket_ref FROM audit_log WHERE org_id=$1 AND batch_id=$2 ORDER BY created_at`,
		f.ts.OrgID, *ap.BatchID)
	require.NoError(t, err)
	defer rows.Close()
	var events int
	for rows.Next() {
		var action string
		var ticketRef *string
		require.NoError(t, rows.Scan(&action, &ticketRef))
		require.Equal(t, "grant.created", action)
		require.NotNil(t, ticketRef)
		require.Equal(t, "BEA-42", *ticketRef)
		events++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 3, events, "one audit event per applied action, all under one batch_id")

	// The audit viewer renders the batch as ONE expandable row.
	vr := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log?action=grant.created", f.ts.OrgID), true)
	require.Equal(t, http.StatusOK, vr.StatusCode, "viewer: %s", vr.Body)
	var page struct {
		Entries []struct {
			BatchID   *uuid.UUID `json:"batch_id"`
			BatchSize int        `json:"batch_size"`
			TicketRef *string    `json:"ticket_ref"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(vr.Body, &page))
	require.Len(t, page.Entries, 1, "a 3-event batch must collapse to one viewer row")
	require.Equal(t, 3, page.Entries[0].BatchSize)
	require.NotNil(t, page.Entries[0].TicketRef)
	require.Equal(t, "BEA-42", *page.Entries[0].TicketRef)

	// Expanding the batch yields its constituent events.
	br := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log/batches/%s", f.ts.OrgID, ap.BatchID), true)
	require.Equal(t, http.StatusOK, br.StatusCode, "batch expand: %s", br.Body)
	var events2 []json.RawMessage
	require.NoError(t, json.Unmarshal(br.Body, &events2))
	require.Len(t, events2, 3)
}

func TestBulkGrants_MidBatchFailureRollsBackEverything(t *testing.T) {
	f := newBulkFixture(t)

	// A genuine mid-transaction fault: a trigger in the isolated test
	// schema blows up on the third team-grant insert, AFTER two rows have
	// been written inside the transaction.
	_, err := f.ts.DB.Pool.Exec(context.Background(), `
		CREATE FUNCTION boom_on_third() RETURNS trigger AS $$
		BEGIN
			IF (SELECT count(*) FROM space_grants WHERE subject_type = 'team') >= 2 THEN
				RAISE EXCEPTION 'mid-batch fault injected by test';
			END IF;
			RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER boom BEFORE INSERT ON space_grants
			FOR EACH ROW EXECUTE FUNCTION boom_on_third();`)
	require.NoError(t, err)

	_, r := f.apply(t, []map[string]any{
		cell(f.parent.ID, f.spaceA, "viewer"),
		cell(f.parent.ID, f.spaceB, "viewer"),
		cell(f.child.ID, f.spaceA, "viewer"),
	}, "T-ROLLBACK")
	require.Equal(t, http.StatusInternalServerError, r.StatusCode,
		"a mid-batch fault must fail the request: %s", r.Body)

	// NOTHING applied: no grants, no audit rows — the batch rolled back
	// whole, exactly as failure mode 4 demands.
	require.Zero(t, f.grantCount(t), "mid-batch failure must leave zero grants applied")
	var auditRows int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE org_id = $1 AND batch_id IS NOT NULL`, f.ts.OrgID).Scan(&auditRows))
	require.Zero(t, auditRows, "mid-batch failure must leave zero audit rows")

	// Clean up the fault and prove the same batch applies whole.
	_, err = f.ts.DB.Pool.Exec(context.Background(),
		`DROP TRIGGER boom ON space_grants; DROP FUNCTION boom_on_third();`)
	require.NoError(t, err)
	ap, r := f.apply(t, []map[string]any{
		cell(f.parent.ID, f.spaceA, "viewer"),
		cell(f.parent.ID, f.spaceB, "viewer"),
		cell(f.child.ID, f.spaceA, "viewer"),
	}, "")
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, 3, ap.Creates)
	require.Equal(t, 3, f.grantCount(t))
}

func TestBulkGrants_UnknownTargetRejectsWholeBatch(t *testing.T) {
	f := newBulkFixture(t)
	foreignTeam := uuid.New()

	_, r := f.apply(t, []map[string]any{
		cell(f.parent.ID, f.spaceA, "viewer"), // valid
		cell(foreignTeam, f.spaceA, "viewer"), // unknown team
	}, "")
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "unknown team must 400: %s", r.Body)
	require.Zero(t, f.grantCount(t), "a rejected batch must apply nothing — including its valid rows")
}

func TestBulkGrants_GhostedCellCreatesDirectGrant_NeverEditsInherited(t *testing.T) {
	f := newBulkFixture(t)

	// The child holds the only grant on space A. In the matrix the PARENT's
	// cell for A renders ghosted — the parent's members enjoy access via
	// subject-side expansion, but there is no (parent, A) row.
	_, r := f.apply(t, []map[string]any{cell(f.child.ID, f.spaceA, "viewer")}, "")
	require.Equal(t, http.StatusOK, r.StatusCode)
	var childGrantID uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT id FROM space_grants WHERE subject_type='team' AND subject_id=$1 AND space_id=$2`,
		f.child.ID, f.spaceAID).Scan(&childGrantID))

	// Acting on the ghosted parent cell must CREATE a (parent, A) row.
	ap, r := f.apply(t, []map[string]any{cell(f.parent.ID, f.spaceA, "agent")}, "")
	require.Equal(t, http.StatusOK, r.StatusCode, "ghost-cell apply: %s", r.Body)
	require.Equal(t, 1, ap.Creates, "a ghosted cell has no grant row — the action must be a create")
	require.Zero(t, ap.Updates, "the inherited (descendant) grant must never be edited")

	// The child's grant is byte-for-byte untouched.
	var childRole string
	var idAfter uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT id, role FROM space_grants WHERE subject_type='team' AND subject_id=$1 AND space_id=$2`,
		f.child.ID, f.spaceAID).Scan(&idAfter, &childRole))
	require.Equal(t, childGrantID, idAfter)
	require.Equal(t, "viewer", childRole)

	// And the matrix reports both rows distinctly: the child's original and
	// the parent's new direct grant.
	mr := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/access-matrix", f.ts.OrgID), true)
	require.Equal(t, http.StatusOK, mr.StatusCode, "matrix: %s", mr.Body)
	var matrix struct {
		Teams []struct {
			ID          uuid.UUID   `json:"id"`
			Path        []uuid.UUID `json:"path"`
			MemberCount int         `json:"member_count"`
		} `json:"teams"`
		Grants []struct {
			TeamID  uuid.UUID `json:"team_id"`
			SpaceID uuid.UUID `json:"space_id"`
			Role    string    `json:"role"`
		} `json:"grants"`
	}
	require.NoError(t, json.Unmarshal(mr.Body, &matrix))
	roleByTeam := map[uuid.UUID]string{}
	for _, g := range matrix.Grants {
		if g.SpaceID == f.spaceAID {
			roleByTeam[g.TeamID] = g.Role
		}
	}
	require.Equal(t, "viewer", roleByTeam[f.child.ID])
	require.Equal(t, "agent", roleByTeam[f.parent.ID])
}

func TestBulkGrants_TicketRefSurvivesTicketDeletion(t *testing.T) {
	f := newBulkFixture(t)

	// A real ticket the operator references, then deletes.
	tr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", f.ts.OrgID, f.spaceA),
		map[string]string{"title": "Access review", "priority": "low"}, true)
	require.Equal(t, http.StatusCreated, tr.StatusCode, "seed ticket: %s", tr.Body)
	var ticket struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(tr.Body, &ticket))
	ref := "ticket:" + ticket.ID.String()

	ap, r := f.apply(t, []map[string]any{cell(f.parent.ID, f.spaceA, "viewer")}, ref)
	require.Equal(t, http.StatusOK, r.StatusCode)

	// Deleting the referenced ticket must not invalidate or orphan the
	// audit row — ticket_ref carries no foreign key.
	dr := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s", f.ts.OrgID, f.spaceA, ticket.ID), true)
	require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, dr.StatusCode, "delete ticket: %s", dr.Body)

	var storedRef string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT ticket_ref FROM audit_log WHERE batch_id = $1 LIMIT 1`, *ap.BatchID).Scan(&storedRef))
	require.Equal(t, ref, storedRef, "the audit row must survive deletion of the referenced ticket")

	// The batch still renders in the viewer.
	br := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/audit-log/batches/%s", f.ts.OrgID, ap.BatchID), true)
	require.Equal(t, http.StatusOK, br.StatusCode)
}

func TestBulkGrants_DuplicateCellRejected(t *testing.T) {
	f := newBulkFixture(t)
	_, r := f.apply(t, []map[string]any{
		cell(f.parent.ID, f.spaceA, "viewer"),
		cell(f.parent.ID, f.spaceA, "agent"),
	}, "")
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "duplicate cell must 400: %s", r.Body)
	require.Zero(t, f.grantCount(t))
}

// TestBulkGrants_TicketRefPolicyIsShared pins the bulk-apply half of the
// ticket-reference policy.
//
// Bulk-apply is the one surface that keeps a JSON body field rather than the
// query parameter every other administrative mutation uses — it is a shipped
// contract with clients already sending it. The whole reason both routes run
// through one ticketref.Policy is so the two transports cannot enforce
// different rules; nothing else stops them drifting, so this asserts the
// shared rules actually apply here.
func TestBulkGrants_TicketRefPolicyIsShared(t *testing.T) {
	f := newBulkFixture(t)

	t.Run("over-length is rejected and writes nothing", func(t *testing.T) {
		before := f.grantCount(t)
		_, r := f.apply(t, []map[string]any{
			cell(f.child.ID, f.spaceA, "viewer"),
		}, strings.Repeat("X", 201))
		requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
		require.Equal(t, before, f.grantCount(t),
			"an over-length reference must be refused before the transaction, not after it")
	})

	t.Run("the 200-character boundary is accepted", func(t *testing.T) {
		res, r := f.apply(t, []map[string]any{
			cell(f.child.ID, f.spaceA, "viewer"),
		}, strings.Repeat("Y", 200))
		require.Equal(t, http.StatusOK, r.StatusCode, "exactly 200 characters must be accepted: %s", r.Body)
		require.NotNil(t, res.TicketRef)
		require.Len(t, *res.TicketRef, 200)
	})

	t.Run("whitespace is trimmed, so both transports store the same value", func(t *testing.T) {
		res, r := f.apply(t, []map[string]any{
			cell(f.child.ID, f.spaceA, "contributor"),
		}, "   OPS-TRIM   ")
		require.Equal(t, http.StatusOK, r.StatusCode, "apply: %s", r.Body)
		require.NotNil(t, res.TicketRef)
		require.Equal(t, "OPS-TRIM", *res.TicketRef,
			"the body field must be trimmed exactly as the query parameter is — otherwise a "+
				"whitespace-only reference would satisfy required mode on one transport and not the other")

		var stored *string
		require.NoError(t, f.ts.DB.Pool.QueryRow(t.Context(),
			`SELECT ticket_ref FROM audit_log WHERE org_id = $1 AND ticket_ref IS NOT NULL
			 ORDER BY created_at DESC LIMIT 1`, f.ts.OrgID).Scan(&stored))
		require.NotNil(t, stored)
		require.Equal(t, "OPS-TRIM", *stored, "the trimmed value is what reaches the column")
	})
}
