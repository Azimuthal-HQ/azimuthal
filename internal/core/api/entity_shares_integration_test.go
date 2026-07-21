package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Entity-share leak tests (v0.3 spec §2.5 cases 11–18, ADR-0008). Every
// denial is proven positively — a 404 with the API error envelope — and
// every "no leak" claim is asserted field-by-field on the actual response,
// never inferred from the absence of a fragment that could be missing for
// other reasons.
//
// The recurring fixture: an org-admin owner (ts) who owns a discoverable
// space and its content, and an `outsider` — an org member with NO grant on
// that space. The outsider therefore cannot read the space at all; a share
// is the only thing that can ever let them read one of its entities.

// shareFixture is the standard two-persona setup.
type shareFixture struct {
	ts          *testServer
	spaceID     string
	outsider    testutil.User
	outsiderTok string
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	ts := newTestServer(t)
	outsider := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	return &shareFixture{
		ts:          ts,
		spaceID:     createScopedSpace(t, ts, "Shared Space", "shared-space", "codex"),
		outsider:    outsider,
		outsiderTok: ts.tokenFor(t, outsider.ID, outsider.Email),
	}
}

// createPage creates a page in the fixture space (as the owner) and returns
// its id and materialized path.
func (f *shareFixture) createPage(t *testing.T, title, content string, parentID *string) (string, string) {
	t.Helper()
	body := map[string]interface{}{"title": title, "content": content}
	if parentID != nil {
		body["parent_id"] = *parentID
	}
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, f.spaceID), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create page: %s", r.Body)
	var page struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))
	require.NotEmpty(t, page.ID)
	return page.ID, page.Path
}

// createShare creates a share via the API as the owner and returns its id.
func (f *shareFixture) createShare(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/shares", f.ts.OrgID), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create share: %s", r.Body)
	var share struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &share))
	return share.ID
}

// readShared fetches the standalone shared-entity route as the outsider.
func (f *shareFixture) readShared(t *testing.T, entityType, entityID string) httpResult {
	t.Helper()
	return f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/%s/%s", f.ts.OrgID, entityType, entityID))
}

// Case 11 — an org-audience share makes the entity readable to the outsider,
// while the space itself stays unreadable.
func TestShare11_OrgAudienceReadable_SpaceStillNot(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Org Memo", "# All hands", nil)

	// Premise: before any share, the outsider can read neither the entity
	// nor the space.
	requireAPINotFound(t, f.readShared(t, "page", pageID))
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", f.ts.OrgID, f.spaceID)))

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})

	// The share makes the entity readable...
	r := f.readShared(t, "page", pageID)
	require.Equal(t, http.StatusOK, r.StatusCode, "org-audience share must grant read: %s", r.Body)

	// ...but the space is STILL not readable, via any of its own routes.
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", f.ts.OrgID, f.spaceID)))
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.spaceID, pageID)))
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/tree", f.ts.OrgID, f.spaceID)))
}

// Case 12 — a cascade share on a folder makes its descendant pages readable.
func TestShare12_CascadeCoversDescendants(t *testing.T) {
	f := newShareFixture(t)
	rootID, _ := f.createPage(t, "Handbook", "root", nil)
	childID, _ := f.createPage(t, "Onboarding", "child", &rootID)
	grandchildID, _ := f.createPage(t, "Week One", "grandchild", &childID)

	// A non-cascade sibling of the root, NOT under it.
	siblingID, _ := f.createPage(t, "Unrelated", "sibling", nil)

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": rootID, "audience": "org", "cascade": true,
	})

	// Root, child, and grandchild are all readable.
	require.Equal(t, http.StatusOK, f.readShared(t, "page", rootID).StatusCode, "root readable")
	require.Equal(t, http.StatusOK, f.readShared(t, "page", childID).StatusCode, "child readable via cascade")
	require.Equal(t, http.StatusOK, f.readShared(t, "page", grandchildID).StatusCode, "grandchild readable via cascade")

	// The sibling outside the subtree is NOT readable.
	requireAPINotFound(t, f.readShared(t, "page", siblingID))
}

// Cascade prefix boundary (failure mode 2): a cascade on "a.b" covers
// "a.b.c" and must NOT cover a sibling "a.bc". Real page paths are dotted
// UUIDs, so this constructs the adversarial paths directly in the database.
func TestShare_CascadePrefixBoundary(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()

	folderID, _ := f.createPage(t, "Folder", "folder", nil)
	descID, _ := f.createPage(t, "Descendant", "desc", nil)
	siblingID, _ := f.createPage(t, "PrefixSibling", "sibling", nil)

	// Rewrite the three paths so the sibling shares a textual prefix with the
	// folder but is not a subtree member: folder="a.b", descendant="a.b.c",
	// sibling="a.bc". LIKE 'a.b%' would wrongly match the sibling; the
	// implementation must use the dot-boundary check.
	for id, path := range map[string]string{folderID: "a.b", descID: "a.b.c", siblingID: "a.bc"} {
		_, err := f.ts.DB.Pool.Exec(ctx, `UPDATE pages SET path = $2 WHERE id = $1`, uuid.MustParse(id), path)
		require.NoError(t, err)
	}

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": folderID, "audience": "org", "cascade": true,
	})

	require.Equal(t, http.StatusOK, f.readShared(t, "page", folderID).StatusCode, "a.b covers itself")
	require.Equal(t, http.StatusOK, f.readShared(t, "page", descID).StatusCode, "a.b covers a.b.c")
	requireAPINotFound(t, f.readShared(t, "page", siblingID)) // a.b must NOT cover a.bc
}

// Case 13 — a revoked share denies on the very next request (no cache).
func TestShare13_RevokedDeniesNextRequest(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Temporary", "content", nil)
	shareID := f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})

	require.Equal(t, http.StatusOK, f.readShared(t, "page", pageID).StatusCode, "readable before revoke")

	r := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/shares/%s", f.ts.OrgID, shareID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke: %s", r.Body)

	// The very next request is denied — no sweeper, no cache invalidation.
	requireAPINotFound(t, f.readShared(t, "page", pageID))
}

// Case 14 — a share past its expiry denies immediately, with NO sweeper
// having run. The row is inserted directly with a past created_at/expires_at
// pair (the API rightly refuses to create an already-expired share), then
// read on the very next request.
func TestShare14_ExpiredDeniesWithoutSweeper(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	pageID, _ := f.createPage(t, "Stale", "content", nil)

	// created 2h ago, expired 1h ago — satisfies expires_at > created_at yet
	// is in the past. No background job runs in this test.
	_, err := f.ts.DB.Pool.Exec(ctx, `
		INSERT INTO entity_shares (id, org_id, space_id, entity_type, entity_id,
		                           audience, cascade, created_by, created_at, expires_at)
		SELECT $1, $2, p.space_id, 'page', p.id, 'org', false, $3,
		       now() - interval '2 hours', now() - interval '1 hour'
		FROM pages p WHERE p.id = $4`,
		uuid.New(), f.ts.OrgID, f.ts.UserID, uuid.MustParse(pageID))
	require.NoError(t, err)

	// Sanity: the row is present and active (unrevoked) — denial is purely
	// from expiry evaluated in the resolution query.
	var revoked *time.Time
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT revoked_at FROM entity_shares WHERE entity_id = $1`, uuid.MustParse(pageID)).Scan(&revoked))
	require.Nil(t, revoked, "premise: the expired share is not revoked")

	requireAPINotFound(t, f.readShared(t, "page", pageID))
}

// Case 15 — a shared entity is read-only. There is no write route under
// /shared, and the in-space write routes remain denied to the outsider.
func TestShare15_ReadOnly_WritesRejected(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "ReadOnly", "content", nil)
	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})
	require.Equal(t, http.StatusOK, f.readShared(t, "page", pageID).StatusCode, "readable")

	// In-space write routes are unreachable (space not readable) → 404.
	requireAPINotFound(t, f.ts.requestAs(t, f.outsiderTok, http.MethodPut,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.spaceID, pageID),
		map[string]interface{}{"title": "hacked", "content": "x", "expected_version": 1}))
	requireAPINotFound(t, f.ts.requestAs(t, f.outsiderTok, http.MethodDelete,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.spaceID, pageID), nil))
	// Commenting on the shared page's in-space route is likewise denied.
	requireAPINotFound(t, f.ts.postAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/comments", f.ts.OrgID, f.spaceID, pageID),
		map[string]interface{}{"body": "leak"}))

	// The /shared read route itself exposes no write verbs: a POST 405s (the
	// route is registered GET-only) — never mutates.
	r := f.ts.postAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s", f.ts.OrgID, pageID), map[string]interface{}{})
	require.Equal(t, http.StatusMethodNotAllowed, r.StatusCode, "shared route is GET-only: %s", r.Body)
}

// Case 16 — the shared read response leaks NO container information: no
// space id, no parent, no path/breadcrumbs, no siblings, no comments.
// Asserted field-by-field on the raw JSON keys, not inferred.
func TestShare16_NoContainerLeak_FieldByField(t *testing.T) {
	f := newShareFixture(t)
	parentID, _ := f.createPage(t, "Parent", "parent body", nil)
	pageID, _ := f.createPage(t, "Shared Child", "# Child\nBody text", &parentID)
	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})

	r := f.readShared(t, "page", pageID)
	require.Equal(t, http.StatusOK, r.StatusCode, "readable: %s", r.Body)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(r.Body, &raw))

	// Present: only presentational fields.
	require.Contains(t, raw, "id")
	require.Contains(t, raw, "entity_type")
	require.Contains(t, raw, "title")
	require.Contains(t, raw, "body")

	// Absent: every container-revealing field, by exact key.
	for _, forbidden := range []string{
		"space_id", "parent_id", "path", "breadcrumbs", "ancestors",
		"children", "siblings", "tree", "comments", "author_id", "search_vector",
	} {
		require.NotContains(t, raw, forbidden,
			"shared response must not carry %q (container/tree/sibling/comment leak)", forbidden)
	}

	// And the values that DO ship must not smuggle the container: the body is
	// the page content, the id is the page id — nothing names the space.
	var view struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &view))
	require.Equal(t, pageID, view.ID)
	require.Equal(t, "Shared Child", view.Title)
}

// Case 17 — moving a shared entity to another space revokes all its shares
// in the same transaction (ADR-0008 rule 9).
func TestShare17_MoveRevokesShares_SameTransaction(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	pageID, _ := f.createPage(t, "Movable", "content", nil)
	shareID := f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})
	require.Equal(t, http.StatusOK, f.readShared(t, "page", pageID).StatusCode, "readable before move")

	// A second space to move into (org admin creates it → readable to owner).
	targetSpace := createScopedSpace(t, f.ts, "Sensitive", "sensitive-space", "codex")

	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/move", f.ts.OrgID, f.spaceID, pageID),
		map[string]interface{}{"target_space_id": targetSpace, "position": 0}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "move: %s", r.Body)
	var moveResp struct {
		CrossSpace    bool  `json:"cross_space"`
		RevokedShares int64 `json:"revoked_shares"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &moveResp))
	require.True(t, moveResp.CrossSpace, "the move crossed spaces")
	require.Equal(t, int64(1), moveResp.RevokedShares, "the move revoked the share")

	// The share is revoked in the database, and access is gone immediately.
	var revokedAt *time.Time
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT revoked_at FROM entity_shares WHERE id = $1`, uuid.MustParse(shareID)).Scan(&revokedAt))
	require.NotNil(t, revokedAt, "the share row is revoked in the same transaction as the move")
	requireAPINotFound(t, f.readShared(t, "page", pageID))

	// The revocation wrote a share.revoked audit event (adapter-layer, in-tx).
	requireAuditAction(t, f.ts, "share.revoked", shareID)
}

// Case 18 — deleting a shared entity revokes its shares in the same
// transaction (ADR-0008 rule 10).
func TestShare18_DeleteRevokesShares_SameTransaction(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	pageID, _ := f.createPage(t, "Doomed", "content", nil)
	shareID := f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})
	require.Equal(t, http.StatusOK, f.readShared(t, "page", pageID).StatusCode, "readable before delete")

	r := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s", f.ts.OrgID, f.spaceID, pageID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "delete page: %s", r.Body)

	var revokedAt *time.Time
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT revoked_at FROM entity_shares WHERE id = $1`, uuid.MustParse(shareID)).Scan(&revokedAt))
	require.NotNil(t, revokedAt, "the share row is revoked in the same transaction as the delete")
	requireAPINotFound(t, f.readShared(t, "page", pageID))
	requireAuditAction(t, f.ts, "share.revoked", shareID)
}

// Ticket and project-item shares work the same way and delete-revoke too.
func TestShare_TicketAndItem_ShareReadDelete(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()

	// A ticket in a beacon space and an item in a vector space.
	beaconSpace := createScopedSpace(t, f.ts, "Desk", "desk-space", "beacon")
	vectorSpace := createScopedSpace(t, f.ts, "Board", "board-space", "vector")

	tr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", f.ts.OrgID, beaconSpace),
		map[string]string{"title": "Shared ticket", "priority": "high"}, true)
	require.Equal(t, http.StatusCreated, tr.StatusCode, "create ticket: %s", tr.Body)
	var ticket struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(tr.Body, &ticket))

	ir := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", f.ts.OrgID, vectorSpace),
		map[string]interface{}{"title": "Shared item", "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, ir.StatusCode, "create item: %s", ir.Body)
	var item struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(ir.Body, &item))

	// Share both org-wide; the outsider reads both, container-free.
	f.createShare(t, map[string]interface{}{"entity_type": "ticket", "entity_id": ticket.ID, "audience": "org"})
	f.createShare(t, map[string]interface{}{"entity_type": "project_item", "entity_id": item.ID, "audience": "org"})

	for _, tc := range []struct{ typ, id string }{{"ticket", ticket.ID}, {"project_item", item.ID}} {
		r := f.readShared(t, tc.typ, tc.id)
		require.Equal(t, http.StatusOK, r.StatusCode, "%s readable: %s", tc.typ, r.Body)
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(r.Body, &raw))
		require.NotContains(t, raw, "space_id", "%s must not leak its space", tc.typ)
		require.NotContains(t, raw, "reporter_id", "%s must not leak people refs", tc.typ)
	}

	// Cascade is pages-only — rejected for a ticket.
	rr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/shares", f.ts.OrgID),
		map[string]interface{}{"entity_type": "ticket", "entity_id": ticket.ID, "audience": "org", "cascade": true}, true)
	require.Equal(t, http.StatusBadRequest, rr.StatusCode, "cascade on a ticket must 400: %s", rr.Body)

	// Deleting the ticket revokes its share in the same transaction.
	dr := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s", f.ts.OrgID, beaconSpace, ticket.ID), true)
	require.Equal(t, http.StatusNoContent, dr.StatusCode, "delete ticket: %s", dr.Body)
	var n int
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM entity_shares WHERE entity_type='ticket' AND entity_id=$1 AND revoked_at IS NULL`,
		uuid.MustParse(ticket.ID)).Scan(&n))
	require.Zero(t, n, "the ticket's share is revoked on delete")
	requireAPINotFound(t, f.readShared(t, "ticket", ticket.ID))
}

// TestEndpointMatrix_Shares runs the §2.6 rows over the share management and
// shared-read endpoints. It reuses the endpointMatrix personas (admin,
// member without grants, stranger from another org).
func TestEndpointMatrix_Shares(t *testing.T) {
	m := newEndpointMatrix(t)
	sharesPath := fmt.Sprintf("/api/v1/orgs/%s/shares", m.ts.OrgID)

	// A page in the matrix space (as the admin).
	pr := m.request(t, http.MethodPost, m.ts.Token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/", m.ts.OrgID, m.spaceID),
		map[string]interface{}{"title": "Matrix Page", "content": "x"})
	require.Equal(t, http.StatusCreated, pr.StatusCode, "seed page: %s", pr.Body)
	var page struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(pr.Body, &page))
	createBody := map[string]interface{}{"entity_type": "page", "entity_id": page.ID, "audience": "org"}

	// 401 — no credentials.
	requireErrorCode(t, m.request(t, http.MethodPost, "", sharesPath, createBody), http.StatusUnauthorized, "UNAUTHORIZED")

	// 404 — stranger (not an org member).
	requireErrorCode(t, m.request(t, http.MethodPost, m.strTok, sharesPath, createBody), http.StatusNotFound, "NOT_FOUND")

	// 404 — org member with no read access to the entity's space: the share
	// surface must not reveal the entity exists (existence never leaks).
	requireErrorCode(t, m.request(t, http.MethodPost, m.memTok, sharesPath, createBody), http.StatusNotFound, "NOT_FOUND")

	// 403 — a viewer can read the space but lacks manage_shares.
	spaceUUID := uuid.MustParse(m.spaceID)
	_, err := m.ts.GrantService.Create(context.Background(), m.ts.OrgID, spaceUUID,
		access.SubjectUser, m.member.ID, access.RoleViewer, m.ts.UserID)
	require.NoError(t, err)
	requireErrorCode(t, m.request(t, http.MethodPost, m.memTok, sharesPath, createBody), http.StatusForbidden, "FORBIDDEN")

	// 400 — missing entity_id, bad audience, team audience without a team,
	// org audience WITH a team, cascade on a ticket-less unknown type.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "page", "audience": "org"}), http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "page", "entity_id": page.ID, "audience": "everyone"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "page", "entity_id": page.ID, "audience": "team"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "page", "entity_id": page.ID, "audience": "org", "audience_id": uuid.NewString()}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "sasquatch", "entity_id": page.ID, "audience": "org"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
	// Wrong field type for entity_id.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath,
		map[string]interface{}{"entity_type": "page", "entity_id": 42, "audience": "org"}),
		http.StatusBadRequest, "BAD_REQUEST")

	// 201 — happy path, snake_case keys.
	r := m.request(t, http.MethodPost, m.ts.Token, sharesPath, createBody)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create share: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)
	var share struct {
		ID         string `json:"id"`
		EntityType string `json:"entity_type"`
		Audience   string `json:"audience"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &share))
	require.Equal(t, "page", share.EntityType)
	require.Equal(t, "org", share.Audience)

	// 409 — duplicate active share for the same (entity, audience) cell.
	requireErrorCode(t, m.request(t, http.MethodPost, m.ts.Token, sharesPath, createBody),
		http.StatusConflict, "CONFLICT")

	// GET list — happy path returns the share and the cascade page count.
	lr := m.request(t, http.MethodGet, m.ts.Token, sharesPath+"?entity_type=page&entity_id="+page.ID, nil)
	require.Equal(t, http.StatusOK, lr.StatusCode, "list shares: %s", lr.Body)
	requireSnakeCaseKeys(t, lr.Body)
	var listBody struct {
		Shares           []map[string]any `json:"shares"`
		CascadePageCount int64            `json:"cascade_page_count"`
	}
	require.NoError(t, json.Unmarshal(lr.Body, &listBody))
	require.Len(t, listBody.Shares, 1)
	require.Equal(t, int64(1), listBody.CascadePageCount, "one page in the subtree")

	// The shared read route: 401, 404 (member without a share reaching them
	// — the viewer grant does not grant share-read), 200 for the org share.
	sharedPath := fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s", m.ts.OrgID, page.ID)
	requireErrorCode(t, m.request(t, http.MethodGet, "", sharedPath, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	requireErrorCode(t, m.request(t, http.MethodGet, m.strTok, sharedPath, nil), http.StatusNotFound, "NOT_FOUND")
	// The org share reaches every member — including the stranger? No: the
	// stranger is in another org, 404 at ResolveAccess. A same-org member
	// with the org share reads it.
	rr := m.request(t, http.MethodGet, m.memTok, sharedPath, nil)
	require.Equal(t, http.StatusOK, rr.StatusCode, "org member reads the org-shared page: %s", rr.Body)

	// DELETE — unknown id 404, happy 204, then the shared read denies.
	requireErrorCode(t, m.request(t, http.MethodDelete, m.ts.Token, sharesPath+"/"+uuid.NewString(), nil),
		http.StatusNotFound, "NOT_FOUND")
	dr := m.request(t, http.MethodDelete, m.ts.Token, sharesPath+"/"+share.ID, nil)
	require.Equal(t, http.StatusNoContent, dr.StatusCode, "revoke share: %s", dr.Body)
	requireErrorCode(t, m.request(t, http.MethodGet, m.memTok, sharedPath, nil), http.StatusNotFound, "NOT_FOUND")
	// 410 — revoking an already-revoked share.
	requireErrorCode(t, m.request(t, http.MethodDelete, m.ts.Token, sharesPath+"/"+share.ID, nil),
		http.StatusGone, "CONFLICT")
}

// TestShare_BadgeEndpoint_SpaceReadableCascade: the space ShareBadge
// endpoint returns active page shares with their root paths, so any space
// reader (not just admins) can mark a page as shared or cascade-covered
// (ADR-0008 rule 5). A member with a viewer grant — not manage_shares — must
// see the badge data.
func TestShare_BadgeEndpoint_SpaceReadableCascade(t *testing.T) {
	f := newShareFixture(t)

	rootID, _ := f.createPage(t, "Shared Folder", "root", nil)
	childID, _ := f.createPage(t, "Child", "child", &rootID)
	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": rootID, "audience": "org", "cascade": true,
	})

	// A viewer (can read the space, cannot manage shares) reads the badges.
	viewer := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	viewerTok := f.ts.tokenFor(t, viewer.ID, viewer.Email)
	spaceUUID := uuid.MustParse(f.spaceID)
	_, err := f.ts.GrantService.Create(context.Background(), f.ts.OrgID, spaceUUID,
		access.SubjectUser, viewer.ID, access.RoleViewer, f.ts.UserID)
	require.NoError(t, err)

	r := f.ts.getAs(t, viewerTok, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/shares", f.ts.OrgID, f.spaceID))
	require.Equal(t, http.StatusOK, r.StatusCode, "badge endpoint readable by a viewer: %s", r.Body)
	var badges []struct {
		EntityID string `json:"entity_id"`
		Cascade  bool   `json:"cascade"`
		RootPath string `json:"root_path"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &badges))
	require.Len(t, badges, 1, "one active page share in the space")
	require.Equal(t, rootID, badges[0].EntityID)
	require.True(t, badges[0].Cascade)
	require.NotEmpty(t, badges[0].RootPath, "root path present so the client can compute cascade coverage")

	// The child is not itself in the list, but its path is under the root's —
	// the client-side coverage check (mirrored server-side by PathWithinSubtree)
	// marks it shared. Confirm the child's path extends the root's.
	var childPath, rootPath string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT path FROM pages WHERE id = $1`, uuid.MustParse(childID)).Scan(&childPath))
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT path FROM pages WHERE id = $1`, uuid.MustParse(rootID)).Scan(&rootPath))
	require.True(t, strings.HasPrefix(childPath, rootPath+"."), "child path must extend the shared root")
}

// requireAuditAction asserts an append-only audit row exists for the action
// on the given entity id.
func requireAuditAction(t *testing.T, ts *testServer, action, entityID string) {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_id = $2`,
		action, uuid.MustParse(entityID)).Scan(&n))
	require.GreaterOrEqual(t, n, 1, "expected an audit row for %s on %s", action, entityID)
}
