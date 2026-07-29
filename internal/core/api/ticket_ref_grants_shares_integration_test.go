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

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// B3 — the operator ticket reference on grant and share mutations.
//
// These two families were the hole in the A2/A3 work. The bulk-grants apply
// endpoint took a reference from its body; individual grant and share
// mutations took none at all. So a deployment that turned
// AZIMUTHAL_TICKET_REF_REQUIRED on got an audit log in which the most
// consequential access changes in the product — who can read a space, and who
// can read an entity with no space access at all — were the only unreferenced
// rows in it.
//
// These live in their own file rather than appended to
// ticket_ref_audit_integration_test.go because one of them is a different kind
// of test: TestTicketRefRequired_AuthorisationAnswersBeforeTheReference is
// about ordering inside the handler, and nothing in the repository asserted
// that for any of the six families before now.

// --- helpers ---

// ticketRefGrantsPath is the grant collection on a space.
func ticketRefGrantsPath(ts *testServer, spaceID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", ts.OrgID, spaceID)
}

// ticketRefSharesPath is the share management collection of the org.
func ticketRefSharesPath(ts *testServer) string {
	return fmt.Sprintf("/api/v1/orgs/%s/shares", ts.OrgID)
}

// ticketRefGrantIsLive reports whether the grant row still exists.
func ticketRefGrantIsLive(t *testing.T, ts *testServer, grantID string) bool {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM space_grants WHERE id = $1`, grantID).Scan(&n))
	return n == 1
}

// ticketRefGrantRole reads a grant's current role straight from the table, so
// a "the PATCH was refused" assertion is about the row and not the response.
func ticketRefGrantRole(t *testing.T, ts *testServer, grantID string) string {
	t.Helper()
	var role string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT role FROM space_grants WHERE id = $1`, grantID).Scan(&role))
	return role
}

// ticketRefGrantCount counts the grants on a space.
func ticketRefGrantCount(t *testing.T, ts *testServer, spaceID string) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM space_grants WHERE space_id = $1`, spaceID).Scan(&n))
	return n
}

// ticketRefShareIsRevoked reports whether the share carries a revoked_at.
func ticketRefShareIsRevoked(t *testing.T, ts *testServer, shareID string) bool {
	t.Helper()
	var revoked bool
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT revoked_at IS NOT NULL FROM entity_shares WHERE id = $1`, shareID).Scan(&revoked))
	return revoked
}

// ticketRefCreateSpace creates a space, carrying a reference.
//
// createScopedSpace cannot be used against the required-mode harness: it sends
// no ticket_ref, so under ticketref.Policy{Required: true} the space create
// itself is refused and the test dies during setup with a 400 that has nothing
// to do with what it is testing.
func ticketRefCreateSpace(t *testing.T, ts *testServer, name, slug, spaceType, ref string) string {
	t.Helper()
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?ticket_ref=%s", ts.OrgID, ref), map[string]string{
		"name": name,
		"slug": slug,
		"type": spaceType,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create space: %s", r.Body)
	var space struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.NotEmpty(t, space.ID)
	return space.ID
}

// ticketRefCreateTicket creates a ticket in a space and returns its id. The
// required-mode harness mounts the ticket handler for exactly this: a share
// needs a real entity, resolved through the real LookupEntity path.
func ticketRefCreateTicket(t *testing.T, ts *testServer, spaceID, title string) string {
	t.Helper()
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID),
		map[string]string{"title": title, "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out.ID
}

// ticketRefShareRevokedRow is one share.revoked audit row plus the payload
// reason that says which of the two writers produced it.
type ticketRefShareRevokedRow struct {
	reason string
	ref    *string
}

func ticketRefShareRevokedRows(t *testing.T, ts *testServer) []ticketRefShareRevokedRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(t.Context(),
		`SELECT COALESCE(payload->>'reason', ''), ticket_ref FROM audit_log
		 WHERE org_id = $1 AND action = $2 ORDER BY created_at ASC`,
		ts.OrgID, string(audit.EventTypeShareRevoked))
	require.NoError(t, err)
	defer rows.Close()

	var out []ticketRefShareRevokedRow
	for rows.Next() {
		var row ticketRefShareRevokedRow
		require.NoError(t, rows.Scan(&row.reason, &row.ref))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// --- A3 for grants and shares: the reference reaches the column ---

// TestTicketRefAudit_GrantMutations_EveryEventCarriesTheReference drives the
// whole grant lifecycle through the real router with three DIFFERENT
// references, so a handler that stamped a stale or shared value fails just as
// loudly as one that stamped none.
func TestTicketRefAudit_GrantMutations_EveryEventCarriesTheReference(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Grant Refs", "grant-refs", "beacon")
	subject := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	r := ts.post(t, ticketRefGrantsPath(ts, spaceID)+"?ticket_ref=GRANT-CREATE", map[string]any{
		"subject_type": "user",
		"subject_id":   subject.ID.String(),
		"role":         "viewer",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create grant: %s", r.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))

	r = ts.patch(t, ticketRefGrantsPath(ts, spaceID)+"/"+created.ID+"?ticket_ref=GRANT-UPDATE",
		map[string]string{"role": "contributor"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "update grant: %s", r.Body)

	r = ts.delete(t, ticketRefGrantsPath(ts, spaceID)+"/"+created.ID+"?ticket_ref=GRANT-REVOKE", true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke grant: %s", r.Body)

	ticketRefAuditRequireSole(t, ts, audit.EventTypeGrantCreated, "GRANT-CREATE")
	ticketRefAuditRequireSole(t, ts, audit.EventTypeGrantUpdated, "GRANT-UPDATE")
	ticketRefAuditRequireSole(t, ts, audit.EventTypeGrantRevoked, "GRANT-REVOKE")
}

// TestTicketRefAudit_ShareMutations_EveryEventCarriesTheReference does the
// same for the share family, and asserts the world changed as well as the log:
// an audit row about a revocation that did not happen would be worse than none.
func TestTicketRefAudit_ShareMutations_EveryEventCarriesTheReference(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Referenced page", "body", nil)

	r := f.ts.post(t, ticketRefSharesPath(f.ts)+"?ticket_ref=SHARE-CREATE", map[string]any{
		"entity_type": "page",
		"entity_id":   pageID,
		"audience":    "org",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create share: %s", r.Body)
	var share struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &share))

	r = f.ts.delete(t, ticketRefSharesPath(f.ts)+"/"+share.ID+"?ticket_ref=SHARE-REVOKE", true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke share: %s", r.Body)

	ticketRefAuditRequireSole(t, f.ts, audit.EventTypeShareCreated, "SHARE-CREATE")
	ticketRefAuditRequireSole(t, f.ts, audit.EventTypeShareRevoked, "SHARE-REVOKE")
	require.True(t, ticketRefShareIsRevoked(t, f.ts, share.ID),
		"the share.revoked event must describe a revocation that actually happened")
}

// TestShare_RevokeOnDelete_WritesNoTicketRef pins the deliberate split
// recorded on writeShareRevokedTx: an operator revoke carries a reference, the
// ADR-0008 revoke-on-delete invariant does not.
//
// The share that gets deleted out from under is the one created WITH a
// reference, and that arrangement is the whole point. If a later change
// plumbed the create-time reference into the in-transaction event, THIS is the
// row that would stop being NULL. Creating it without a reference would leave
// nothing to leak, and the NULL assertion would hold for the wrong reason
// while the regression it claims to guard went through.
func TestShare_RevokeOnDelete_WritesNoTicketRef(t *testing.T) {
	f := newShareFixture(t)
	doomedID, _ := f.createPage(t, "Doomed page", "body", nil)
	keptID, _ := f.createPage(t, "Kept page", "body", nil)

	doomed := f.ts.post(t, ticketRefSharesPath(f.ts)+"?ticket_ref=OPS-CREATED-WITH-A-REF", map[string]any{
		"entity_type": "page", "entity_id": doomedID, "audience": "org",
	}, true)
	require.Equal(t, http.StatusCreated, doomed.StatusCode, "share the doomed page: %s", doomed.Body)

	keptShare := f.ts.post(t, ticketRefSharesPath(f.ts), map[string]any{
		"entity_type": "page", "entity_id": keptID, "audience": "org",
	}, true)
	require.Equal(t, http.StatusCreated, keptShare.StatusCode, "share the kept page: %s", keptShare.Body)
	var kept struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(keptShare.Body, &kept))

	// Deleting the page revokes its share inside the delete's own transaction.
	r := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.spaceID, doomedID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete the doomed page: %s", r.Body)

	// And revoke the other one the ordinary way, with a reference.
	r = f.ts.delete(t, ticketRefSharesPath(f.ts)+"/"+kept.ID+"?ticket_ref=OPS-REVOKE", true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke the kept share: %s", r.Body)

	rows := ticketRefShareRevokedRows(t, f.ts)
	require.Len(t, rows, 2, "expected one in-transaction revoke and one operator revoke")

	byReason := map[string]*string{}
	for _, row := range rows {
		byReason[row.reason] = row.ref
	}
	require.Contains(t, byReason, "entity_deleted")
	require.Nil(t, byReason["entity_deleted"],
		"revoke-on-delete is the system enforcing ADR-0008, not an operator change: it must not inherit the reference the share was created with")
	require.Contains(t, byReason, "", "the operator revoke writes no reason")
	require.NotNil(t, byReason[""], "the operator revoke must carry its reference")
	require.Equal(t, "OPS-REVOKE", *byReason[""])
}

// --- A2 for grants and shares: a refusal means nothing happened ---

// TestTicketRefRequired_GrantsAndShares_RejectAndWriteNothing is the A2
// precedent applied to the two new families: every reference-less mutation is
// refused with the policy's own 400, and the resource is re-read to prove the
// refusal was not merely reported after the write.
func TestTicketRefRequired_GrantsAndShares_RejectAndWriteNothing(t *testing.T) {
	ts := newTicketRefRequiredServer(t)
	spaceID := ticketRefCreateSpace(t, ts, "Required Grants", "required-grants", "beacon", "SETUP")
	subject := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	grantBody := map[string]any{
		"subject_type": "user",
		"subject_id":   subject.ID.String(),
		"role":         "viewer",
	}

	// Create, with no reference at all.
	r := ts.post(t, ticketRefGrantsPath(ts, spaceID), grantBody, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	require.Zero(t, ticketRefGrantCount(t, ts, spaceID),
		"the grant must not exist after the rejection")

	// Now make one, with a reference, so update and delete have a target.
	r = ts.post(t, ticketRefGrantsPath(ts, spaceID)+"?ticket_ref=OPS-1", grantBody, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create grant with a reference: %s", r.Body)
	var grant struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &grant))

	// Update, with no reference: the role must be exactly what it was.
	r = ts.patch(t, ticketRefGrantsPath(ts, spaceID)+"/"+grant.ID, map[string]string{"role": "space_admin"}, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	require.Equal(t, "viewer", ticketRefGrantRole(t, ts, grant.ID),
		"the refused PATCH must not have changed the role")

	// Delete, with no reference: the grant must still be live.
	r = ts.delete(t, ticketRefGrantsPath(ts, spaceID)+"/"+grant.ID, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	require.True(t, ticketRefGrantIsLive(t, ts, grant.ID),
		"the refused DELETE must have left the grant live")

	// Shares. The entity is a real ticket, resolved through LookupEntity.
	ticketID := ticketRefCreateTicket(t, ts, spaceID, "Shareable")
	shareBody := map[string]any{"entity_type": "ticket", "entity_id": ticketID, "audience": "org"}

	r = ts.post(t, ticketRefSharesPath(ts), shareBody, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	var shares int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM entity_shares WHERE org_id = $1`, ts.OrgID).Scan(&shares))
	require.Zero(t, shares, "the share must not exist after the rejection")

	r = ts.post(t, ticketRefSharesPath(ts)+"?ticket_ref=OPS-2", shareBody, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create share with a reference: %s", r.Body)
	var share struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &share))

	r = ts.delete(t, ticketRefSharesPath(ts)+"/"+share.ID, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
	require.False(t, ticketRefShareIsRevoked(t, ts, share.ID),
		"the refused DELETE must have left the share active")

	// An over-long reference is refused the same way, and is also a write of
	// nothing. Both families go through the one shared ticketref.Policy, so
	// this asserts the cap reached them rather than re-testing the cap itself.
	tooLong := "?ticket_ref=" + strings.Repeat("x", ticketRefTooLongLength)
	r = ts.delete(t, ticketRefSharesPath(ts)+"/"+share.ID+tooLong, true)
	ticketRefAuditRequireRejected(t, r, ticketRefAuditTooLongMessage)
	require.False(t, ticketRefShareIsRevoked(t, ts, share.ID))
}

// ticketRefTooLongLength is one character past ticketref.MaxLen.
const ticketRefTooLongLength = 201

// --- the ordering rule ---

// TestTicketRefRequired_AuthorisationAnswersBeforeTheReference is the test
// nothing in this repository had.
//
// Turning AZIMUTHAL_TICKET_REF_REQUIRED on is supposed to change what gets
// recorded, not who is allowed to do what. If a handler resolved the reference
// before its authorisation gate, every reference-less request would come back
// 400 VALIDATION_ERROR — so a caller who should have seen 403 "manage_shares
// required" sees a validation error instead, and a caller who should have seen
// 404 for an entity they cannot see sees one too. The authorisation answer
// disappears behind the flag, and the endpoint matrix silently changes shape
// on a setting that is supposed to be about audit records.
//
// Where the check is worth the most: the share family carries NO space guard
// (mountShareResources adds none — resolveManageable and
// authorizeShareManagement perform the whole 404-then-403 split inside the
// handler), so Resolve-first there would rewrite both answers with nothing
// upstream to catch it. On grants, readableGuard answers the unreadable-space
// case as middleware whatever the handler does, which is why the grant rows
// below use a persona that CLEARS readableGuard: only the in-handler
// capability check is under test, and only that row can detect the defect. A
// "member with no grant gets 404" row would look like an ordering test and
// assert the middleware.
//
// Persona: access.RoleAgent. Not a viewer — the grants routes carry no
// RequireWriteFloor today, so a viewer would reach the in-handler gate and the
// test would pass, but it would start asserting the middleware the moment a
// floor was added. An agent is the strongest role short of space_admin, and
// manage_grants and manage_shares both require space_admin
// (internal/core/access/capability.go).
func TestTicketRefRequired_AuthorisationAnswersBeforeTheReference(t *testing.T) {
	ts := newTicketRefRequiredServer(t)
	spaceID := ticketRefCreateSpace(t, ts, "Ordering", "ordering", "beacon", "SETUP")
	spaceUUID := uuid.MustParse(spaceID)

	// Someone who can read and write in the space but cannot manage grants
	// or shares. Every request below carries NO ticket_ref.
	agent := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, spaceUUID,
		access.SubjectUser, agent.ID, access.RoleAgent, ts.UserID)
	require.NoError(t, err)
	agentTok := ts.tokenFor(t, agent.ID, agent.Email)

	// A grant and a share for the update/delete rows to aim at, created by the
	// owner with a reference.
	subject := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	r := ts.post(t, ticketRefGrantsPath(ts, spaceID)+"?ticket_ref=SETUP", map[string]any{
		"subject_type": "user", "subject_id": subject.ID.String(), "role": "viewer",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "setup grant: %s", r.Body)
	var grant struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &grant))

	ticketID := ticketRefCreateTicket(t, ts, spaceID, "Ordering ticket")
	r = ts.post(t, ticketRefSharesPath(ts)+"?ticket_ref=SETUP", map[string]any{
		"entity_type": "ticket", "entity_id": ticketID, "audience": "org",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "setup share: %s", r.Body)
	var share struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &share))

	t.Run("grants: the capability answer survives required mode", func(t *testing.T) {
		// The agent clears spaceGuard and readableGuard, so these 403s are
		// grants.requireManageGrants speaking — the only rows in this test that
		// can detect a Resolve moved above the in-handler gate.
		r := ts.postAs(t, agentTok, ticketRefGrantsPath(ts, spaceID), map[string]any{
			"subject_type": "user", "subject_id": subject.ID.String(), "role": "viewer",
		})
		requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")

		r = ts.patchAs(t, agentTok, ticketRefGrantsPath(ts, spaceID)+"/"+grant.ID,
			map[string]string{"role": "contributor"})
		requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")

		r = ts.deleteAs(t, agentTok, ticketRefGrantsPath(ts, spaceID)+"/"+grant.ID)
		requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")
	})

	t.Run("shares: both authorisation answers survive required mode", func(t *testing.T) {
		// 403 — readable space, no manage_shares. Nothing upstream produces
		// this; resolveManageable does, inside the handler.
		r := ts.postAs(t, agentTok, ticketRefSharesPath(ts), map[string]any{
			"entity_type": "ticket", "entity_id": ticketID, "audience": "org",
		})
		requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")

		r = ts.deleteAs(t, agentTok, ticketRefSharesPath(ts)+"/"+share.ID)
		requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")

		// 404 — an org member with no grant at all cannot see the space, so
		// the entity does not exist as far as they can tell. There is no
		// middleware on this route family: were the reference resolved first,
		// this would be a 400, and the share routes would have begun
		// distinguishing a stranger from a nonexistent id.
		outsider := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		outsiderTok := ts.tokenFor(t, outsider.ID, outsider.Email)

		r = ts.postAs(t, outsiderTok, ticketRefSharesPath(ts), map[string]any{
			"entity_type": "ticket", "entity_id": ticketID, "audience": "org",
		})
		requireAPINotFound(t, r)

		r = ts.deleteAs(t, outsiderTok, ticketRefSharesPath(ts)+"/"+share.ID)
		requireAPINotFound(t, r)
	})

	// None of the refusals above may have written anything either.
	require.Equal(t, "viewer", ticketRefGrantRole(t, ts, grant.ID))
	require.False(t, ticketRefShareIsRevoked(t, ts, share.ID))
}

// --- B4: the S10 auto-space orchestration under required mode ---

// ticketRefAuditAllRows returns every audit row in the org, oldest first.
//
// ticketRefAuditRequireSole takes an event type, so a test built only from it
// asserts nothing about an event the orchestration emits that the author
// forgot to list. Reading the whole table and asserting the exact multiset is
// what makes "every event carries the reference" a total claim rather than a
// claim about the rows someone remembered.
func ticketRefAuditAllRows(t *testing.T, ts *testServer) []ticketRefAuditRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(t.Context(),
		`SELECT action, ticket_ref FROM audit_log WHERE org_id = $1 ORDER BY created_at ASC`,
		ts.OrgID)
	require.NoError(t, err)
	defer rows.Close()

	var out []ticketRefAuditRow
	for rows.Next() {
		var row ticketRefAuditRow
		require.NoError(t, rows.Scan(&row.Action, &row.Ref))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestTicketRefRequired_TeamWithAutoSpaces_OneReferenceCoversTheWholeOrchestration
// is the end-to-end proof that flipping AZIMUTHAL_TICKET_REF_REQUIRED is safe
// for S10 — the create-team-with-auto-spaces flow, which is the only surface in
// the product where one operator action performs several administrative writes
// through different handlers.
//
// It drives exactly what web/src/lib/api.ts useCreateTeamWithSpaces issues, in
// the same order, with two modules so the per-module loop is real: one team
// create, then a space create and a grant create per module.
//
// # The trap this test is shaped around
//
// The obvious negative case — replay the orchestration with no reference and
// assert a 400 — proves nothing. The team create is refused first, so the
// grant endpoint is never reached, and the test passes with B3's gate on
// grants deleted. It would read as proof that the flip is safe while asserting
// only the teams handler.
//
// So the third subtest creates the team and the space WITH a reference and
// then attacks the grant endpoint alone. That is the step that can only be
// reached once the earlier ones have succeeded, and it is the step B3 added.
func TestTicketRefRequired_TeamWithAutoSpaces_OneReferenceCoversTheWholeOrchestration(t *testing.T) {
	const ref = "CHG-4242"

	t.Run("the whole orchestration succeeds and every event carries the one reference", func(t *testing.T) {
		ts := newTicketRefRequiredServer(t)

		r := ts.post(t, ticketRefAuditTeamsPath(ts)+"?ticket_ref="+ref,
			map[string]string{"slug": "orch", "name": "ORCH"}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
		var team struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &team))

		for _, module := range []string{"beacon", "codex"} {
			// The composite omits `key` deliberately, so the backend derives
			// and dedupes it — exercised here for free, since both spaces
			// share a name.
			r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?ticket_ref=%s", ts.OrgID, ref), map[string]any{
				"name":          team.Name,
				"slug":          team.Slug,
				"type":          module,
				"owner_team_id": team.ID,
			}, true)
			require.Equal(t, http.StatusCreated, r.StatusCode, "create %s space: %s", module, r.Body)
			var space struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal(r.Body, &space))

			r = ts.post(t, ticketRefGrantsPath(ts, space.ID)+"?ticket_ref="+ref, map[string]any{
				"subject_type": "team",
				"subject_id":   team.ID,
				"role":         "contributor",
			}, true)
			require.Equal(t, http.StatusCreated, r.StatusCode, "grant on the %s space: %s", module, r.Body)
		}

		// The world actually changed.
		require.Equal(t, 1, ticketRefAuditTeamCount(t, ts, "orch"))
		var spaces, grants int
		require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM spaces WHERE org_id = $1`, ts.OrgID).Scan(&spaces))
		require.Equal(t, 2, spaces)
		require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM space_grants WHERE subject_type = 'team' AND subject_id = $1`,
			team.ID).Scan(&grants))
		require.Equal(t, 2, grants)

		// And every row the orchestration wrote carries the one reference —
		// asserted over the whole table, with the exact action multiset, so a
		// forgotten event cannot hide behind the ones that were remembered.
		rows := ticketRefAuditAllRows(t, ts)
		require.Len(t, rows, 5, "expected team.created + 2x space.created + 2x grant.created")

		counts := map[string]int{}
		for _, row := range rows {
			require.NotNil(t, row.Ref, "%s stored SQL NULL: one operator action, one reference, no exceptions", row.Action)
			require.Equal(t, ref, *row.Ref, "%s carries the wrong reference", row.Action)
			counts[row.Action]++
		}
		require.Equal(t, map[string]int{
			string(audit.EventTypeTeamCreated):  1,
			string(audit.EventTypeSpaceCreated): 2,
			string(audit.EventTypeGrantCreated): 2,
		}, counts)
	})

	t.Run("a reference-less orchestration fails on its first request, before anything exists", func(t *testing.T) {
		ts := newTicketRefRequiredServer(t)

		r := ticketRefAuditCreateTeam(t, ts, "orch-none", "")
		ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)

		require.Zero(t, ticketRefAuditTeamCount(t, ts, "orch-none"))
		var spaces int
		require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM spaces WHERE org_id = $1`, ts.OrgID).Scan(&spaces))
		require.Zero(t, spaces, "no auto-space may exist: the refusal came before the team did")
		require.Zero(t, ticketRefAuditOrgRowCount(t, ts))
	})

	t.Run("the grant leg is gated on its own, not by the steps before it", func(t *testing.T) {
		// THE SUBTEST THAT CANNOT BE OMITTED. Team and space are created WITH
		// a reference so the orchestration actually reaches the grant
		// endpoint; only then is the grant attempted without one. Drop this
		// and the subtest above still passes with B3's gate on grants deleted.
		ts := newTicketRefRequiredServer(t)

		r := ts.post(t, ticketRefAuditTeamsPath(ts)+"?ticket_ref="+ref,
			map[string]string{"slug": "reach", "name": "REACH"}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "create team: %s", r.Body)
		var team struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &team))

		spaceID := ticketRefCreateSpace(t, ts, "REACH", "reach", "beacon", ref)

		grantBody := map[string]any{
			"subject_type": "team",
			"subject_id":   team.ID,
			"role":         "contributor",
		}

		r = ts.post(t, ticketRefGrantsPath(ts, spaceID), grantBody, true)
		ticketRefAuditRequireRejected(t, r, ticketRefAuditMissingMessage)
		require.Zero(t, ticketRefGrantCount(t, ts, spaceID),
			"the refused grant must not have been written")
		require.Equal(t, 2, ticketRefAuditOrgRowCount(t, ts),
			"only team.created and space.created may exist: the refusal wrote nothing")

		// And the same request with a reference succeeds, which is what proves
		// the refusal above was about the reference and not about the request
		// being malformed in some other way.
		r = ts.post(t, ticketRefGrantsPath(ts, spaceID)+"?ticket_ref="+ref, grantBody, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "the same grant, with a reference: %s", r.Body)
		ticketRefAuditRequireSole(t, ts, audit.EventTypeGrantCreated, ref)
	})
}
