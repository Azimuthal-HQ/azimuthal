package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The read and delete halves of the tier configuration API.
//
// These exist because the first pass built the endpoints and tested only the
// create paths, which is precisely the shape CLAUDE.md §2 warns about: an
// endpoint that is mounted, accounted, and never reached reads as covered.

// tierBase is the configuration path for the fixture's transition.
func (f *tierAPIFixture) tierBase(kind string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/%s",
		f.ts.OrgID, f.workflowID, f.edge, kind)
}

// createTier posts a configuration row and returns its id.
func (f *tierAPIFixture) createTier(t *testing.T, kind string, body map[string]any) string {
	t.Helper()
	r := f.ts.post(t, f.tierBase(kind), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create %s: %s", kind, r.Body)
	var created map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &created))
	return created["id"].(string)
}

// listTier reads a configuration collection.
func (f *tierAPIFixture) listTier(t *testing.T, kind string) []map[string]any {
	t.Helper()
	r := f.ts.get(t, f.tierBase(kind), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list %s: %s", kind, r.Body)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &out), "body: %s", r.Body)
	return out
}

// Each collection answers an empty ARRAY when nothing is configured, never
// null. Go marshals a nil slice as null, and web/e2e/null-collections.spec.ts
// hunts exactly that: a client mapping over null crashes the page.
func TestTierCRUD_EmptyCollectionsAreArraysNotNull(t *testing.T) {
	f := setupTierAPI(t)

	for _, kind := range []string{"guards", "post-functions", "approvers"} {
		t.Run(kind, func(t *testing.T) {
			r := f.ts.get(t, f.tierBase(kind), true)
			require.Equal(t, http.StatusOK, r.StatusCode)
			require.JSONEq(t, "[]", string(r.Body),
				"an unconfigured %s collection must serialise as [] and not null", kind)
		})
	}
}

// Create, read back, delete, and confirm the delete. A repeated delete answers
// 404 rather than reading as success.
func TestTierCRUD_GuardLifecycle(t *testing.T) {
	f := setupTierAPI(t)

	id := f.createTier(t, "guards", map[string]any{
		"guard_class": "validator", "kind": "field_required", "field_key": "due_at",
	})

	listed := f.listTier(t, "guards")
	require.Len(t, listed, 1)
	require.Equal(t, id, listed[0]["id"])
	require.Equal(t, "validator", listed[0]["guard_class"])
	require.Equal(t, "field_required", listed[0]["kind"])
	require.Equal(t, "due_at", listed[0]["field_key"])

	// The guard bites while it exists.
	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusUnprocessableEntity, code)

	r := f.ts.delete(t, f.tierBase("guards")+"/"+id, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.Empty(t, f.listTier(t, "guards"))

	// And stops biting once removed — which is what proves the delete reached
	// the evaluator and not just the table.
	code, _ = f.transition(t, "in_progress")
	require.Equal(t, http.StatusOK, code)

	r = f.ts.delete(t, f.tierBase("guards")+"/"+id, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "a repeated delete must not read as success")
}

func TestTierCRUD_PostFunctionLifecycle(t *testing.T) {
	f := setupTierAPI(t)

	id := f.createTier(t, "post-functions", map[string]any{
		"kind": "set_field", "field_key": "tags", "field_value": "escalated",
	})

	listed := f.listTier(t, "post-functions")
	require.Len(t, listed, 1)
	require.Equal(t, id, listed[0]["id"])
	require.Equal(t, "set_field", listed[0]["kind"])
	require.Equal(t, "tags", listed[0]["field_key"])

	r := f.ts.delete(t, f.tierBase("post-functions")+"/"+id, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.Empty(t, f.listTier(t, "post-functions"))

	r = f.ts.delete(t, f.tierBase("post-functions")+"/"+id, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}

func TestTierCRUD_ApproverLifecycle(t *testing.T) {
	f := setupTierAPI(t)

	id := f.createTier(t, "approvers", map[string]any{
		"subject_type": "user", "subject_id": f.ts.UserID.String(),
	})

	listed := f.listTier(t, "approvers")
	require.Len(t, listed, 1)
	require.Equal(t, id, listed[0]["id"])
	require.Equal(t, "user", listed[0]["subject_type"])
	// The display name is resolved at read time, never stored — the same pair
	// access.Grant carries.
	require.NotEmpty(t, listed[0]["subject_name"], "a live subject must resolve a display name")

	// The approver gate is live while it exists.
	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusAccepted, code)

	r := f.ts.delete(t, f.tierBase("approvers")+"/"+id, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	require.Empty(t, f.listTier(t, "approvers"))

	r = f.ts.delete(t, f.tierBase("approvers")+"/"+id, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}

// ─── Approver subject validation (migration 047's unmet promise) ──────────────
//
// Migration 047's header claimed "the API validates the subject exists in the
// org before insert, using the same … checks grants use." It never did:
// CreateApprover went decode → shape check → store, so a UUID naming a foreign
// user, a foreign team, or nothing at all could be written as an approver. These
// tests pin the check the header always described, sharing the grant surface's
// IsOrgMember / TeamExistsInOrg via access.ValidateSubjectInOrg.

// A subject that lives in the org — a member, or one of its teams — is accepted.
// This is the "still passes when the check is right" half; the refusal tests
// below are the other half.
func TestTierCRUD_ApproverSubjectMustBeInOrg_Accepts(t *testing.T) {
	f := setupTierAPI(t)
	base := f.tierBase("approvers")

	// A same-org user (the harness owner).
	r := f.ts.post(t, base, map[string]any{"subject_type": "user", "subject_id": f.ts.UserID.String()}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "a same-org user is a valid approver subject: %s", r.Body)

	// A same-org team (the org's default team).
	var teamID uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(t.Context(),
		`SELECT id FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`, f.ts.OrgID).Scan(&teamID))
	r = f.ts.post(t, base, map[string]any{"subject_type": "team", "subject_id": teamID.String()}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "a same-org team is a valid approver subject: %s", r.Body)
}

// The no-oracle property: a real user in ANOTHER org and a UUID that names
// nothing are refused with byte-identical envelopes once the per-request id is
// stripped. Both checks are org-scoped, so "exists elsewhere" and "does not
// exist" must be indistinguishable — otherwise an org admin could enumerate
// which UUIDs are real users somewhere in the deployment.
func TestTierCRUD_ApproverSubjectValidation_RefusesForeignSubjectWithoutAnOracle(t *testing.T) {
	f := setupTierAPI(t)
	base := f.tierBase("approvers")

	// A real user in another org.
	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	foreignUser := testutil.CreateTestUser(t, f.ts.DB.Pool, otherOrg.ID)
	// A UUID that names nothing anywhere.
	ghost := uuid.New()

	foreign := f.ts.post(t, base, map[string]any{"subject_type": "user", "subject_id": foreignUser.ID.String()}, true)
	nothing := f.ts.post(t, base, map[string]any{"subject_type": "user", "subject_id": ghost.String()}, true)

	require.Equal(t, http.StatusBadRequest, foreign.StatusCode, "a foreign-org user must be refused: %s", foreign.Body)
	require.Equal(t, http.StatusBadRequest, nothing.StatusCode, "a nonexistent UUID must be refused: %s", nothing.Body)

	fCode, fMsg, fReq := errEnvelope(t, foreign.Body)
	nCode, nMsg, nReq := errEnvelope(t, nothing.Body)
	require.Equal(t, nCode, fCode, "the two refusals must carry the same code")
	require.Equal(t, nMsg, fMsg, "the two refusals must carry the same message — no oracle over other orgs")
	// The request ids are the only thing that may differ, and they must: proving
	// these were two independent requests rather than one body compared to itself.
	require.NotEmpty(t, fReq)
	require.NotEmpty(t, nReq)
	require.NotEqual(t, fReq, nReq)
}

// The read path is org-aware too: a subject_id that is a real user in another
// org — the historical shape, a row written before this check existed — reads
// as missing rather than resolving the foreign display name. Inserted straight
// through the adapter so it bypasses the create-time check this row predates.
func TestTierCRUD_ApproverSubjectMissing_IsOrgAware(t *testing.T) {
	f := setupTierAPI(t)
	ctx := t.Context()

	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	foreignUser := testutil.CreateTestUser(t, f.ts.DB.Pool, otherOrg.ID)
	_, err := f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.edge,
		SubjectType:  workflow.ApproverUser,
		SubjectID:    foreignUser.ID,
	})
	require.NoError(t, err)

	listed := f.listTier(t, "approvers")
	require.Len(t, listed, 1)
	require.Equal(t, true, listed[0]["subject_missing"],
		"a real user in another org must read as missing — the join is org-scoped, not a bare id match")
	require.Empty(t, listed[0]["subject_name"], "a foreign subject must not leak its display name")
}

// errEnvelope decodes the standard error body into (code, message, request_id).
func errEnvelope(t *testing.T, body []byte) (code, message, requestID string) {
	t.Helper()
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "body: %s", body)
	return env.Error.Code, env.Error.Message, env.Error.RequestID
}

// A malformed id in the path is a 400, not a 404 and not a 500.
func TestTierCRUD_MalformedIDsAreBadRequests(t *testing.T) {
	f := setupTierAPI(t)

	for _, kind := range []string{"guards", "post-functions", "approvers"} {
		r := f.ts.delete(t, f.tierBase(kind)+"/not-a-uuid", true)
		require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s: %s", kind, r.Body)
	}

	// A well-formed id that names nothing is a 404.
	for _, kind := range []string{"guards", "post-functions", "approvers"} {
		r := f.ts.delete(t, f.tierBase(kind)+"/"+uuid.NewString(), true)
		require.Equal(t, http.StatusNotFound, r.StatusCode, "%s: %s", kind, r.Body)
	}
}

// A transition id that belongs to a different workflow is not reachable through
// this workflow's path, even when both are in the caller's org.
func TestTierCRUD_TransitionMustBelongToTheNamedWorkflow(t *testing.T) {
	f := setupTierAPI(t)

	// The org's OTHER seeded workflow — same org, different workflow.
	other, err := f.ts.WorkflowAdapter.GetDefaultWorkflow(t.Context(), f.ts.OrgID, "project_items")
	require.NoError(t, err)

	path := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/guards",
		f.ts.OrgID, other.ID, f.edge)
	r := f.ts.get(t, path, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"a transition of another workflow must not be reachable here: %s", r.Body)
}

// A malformed transition id in the path is a 400 before anything is looked up.
func TestTierCRUD_MalformedTransitionIDIsABadRequest(t *testing.T) {
	f := setupTierAPI(t)

	path := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/not-a-uuid/guards",
		f.ts.OrgID, f.workflowID)
	r := f.ts.get(t, path, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
}
