package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The entity-to-space reconciliation, across every module, against the full
// production router and real PostgreSQL.
//
// THE CLASS. Every route in this family is shaped
//
//	/api/v1/orgs/{orgID}/spaces/{spaceID}/<module>/{entityID}...
//
// and RequireSpaceReadable proves the caller may read {spaceID}. Nothing proved
// {entityID} lived there. The reads were `WHERE id = $1 AND deleted_at IS NULL`
// with no space predicate and no org predicate, so a member holding a grant on
// any ONE space could name any entity in the installation — in a hidden space,
// or in another organization entirely — and have it returned in full.
//
// This was found by pulling the thread on the item-relations disclosure, which
// leaked a title and a status. Most members of this family leak considerably
// more: the wiki routes return a page's whole content and document body, and
// the comment route returns every comment on an entity including the
// internal-visibility notes the customer-facing surface deliberately withholds.
//
// Each case below is asserted in both directions. The wrong-space request must
// disclose nothing; the SAME request with the entity's own space in the URL must
// still work. Without the second half, "return 404 for everything" would pass.
//
// The persona is an org MEMBER with a contributor grant on space A and nothing
// at all on space B. testutil.CreateTestUser makes an org OWNER, whose ADR-0007
// middleware bypass reads every space in the org — a test written with that user
// cannot see a space boundary and would pass with the whole fix reverted.

type scopeFixture struct {
	ts *testServer
	q  *generated.Queries

	spaceA, spaceB, beaconA, beaconB testutil.Space
	memberTok                        string

	// Entities in space A: the member may read these.
	itemA   uuid.UUID
	sprintA uuid.UUID
	pageA   uuid.UUID
	ticketA uuid.UUID

	// Entities in space B: the member may read none of these.
	itemB   uuid.UUID
	sprintB uuid.UUID
	pageB   uuid.UUID
	ticketB uuid.UUID
}

// Secrets are distinct per entity so a leak names which read produced it.
const (
	secretItem   = "XSPACE-SECRET-ITEM"
	secretSprint = "XSPACE-SECRET-SPRINT"
	secretPage   = "XSPACE-SECRET-PAGE"
	secretTicket = "XSPACE-SECRET-TICKET"
	secretNote   = "XSPACE-SECRET-COMMENT"
	secretField  = "XSPACE-SECRET-FIELDVALUE"
	// Tag slugs are constrained to ^[a-z0-9][a-z0-9_]*$ (migration 040).
	secretTag = "xspace_secret_tag"
)

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()
	f := &scopeFixture{ts: ts, q: generated.New(ts.DB.Pool)}

	f.spaceA = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")
	f.spaceB = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")
	f.beaconA = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	f.beaconB = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	for _, s := range []testutil.Space{f.spaceA, f.beaconA} {
		_, err := ts.GrantService.Create(ctx, ts.OrgID, s.ID,
			access.SubjectUser, member.ID, access.RoleContributor, ts.UserID)
		require.NoError(t, err)
	}
	f.memberTok = ts.tokenFor(t, member.ID, member.Email)

	f.itemA = f.mkItem(t, f.spaceA.ID, "Ordinary item in A")
	f.itemB = f.mkItem(t, f.spaceB.ID, secretItem)
	f.sprintA = f.mkSprint(t, f.spaceA.ID, "Ordinary sprint in A")
	f.sprintB = f.mkSprint(t, f.spaceB.ID, secretSprint)
	f.pageA = f.mkPage(t, f.spaceA.ID, "Ordinary page in A")
	f.pageB = f.mkPage(t, f.spaceB.ID, secretPage)
	f.ticketA = f.mkTicket(t, f.beaconA.ID, 1, "Ordinary ticket in A")
	f.ticketB = f.mkTicket(t, f.beaconB.ID, 2, secretTicket)

	// Dependents hanging off the space-B entities.
	f.mkComment(t, "project_item", f.itemB, secretNote)
	f.mkFieldValue(t, f.itemB, secretField)
	f.mkTag(t, f.pageB, secretTag)
	// And the matching dependents in A, so the positive direction has something
	// to find.
	f.mkComment(t, "project_item", f.itemA, "Ordinary comment in A")
	f.mkFieldValue(t, f.itemA, "ordinary-value")
	f.mkTag(t, f.pageA, "ordinary_tag")

	return f
}

func (f *scopeFixture) mkItem(t *testing.T, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	row, err := f.q.CreateProjectItem(context.Background(), generated.CreateProjectItemParams{
		ID: uuid.New(), SpaceID: spaceID, Kind: "task", Title: title,
		Description: title + "-body", Status: "open", Priority: "medium",
		ReporterID: f.ts.UserID, Labels: []string{}, Rank: "a",
	})
	require.NoError(t, err)
	// The item is placed on its space's sprint later by the sprint fixture.
	return row.ID
}

func (f *scopeFixture) mkSprint(t *testing.T, spaceID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	goal := name + "-goal"
	row, err := f.q.CreateSprint(context.Background(), generated.CreateSprintParams{
		ID: uuid.New(), SpaceID: spaceID, Name: name, Goal: &goal,
		Status: "planned", CreatedBy: f.ts.UserID,
	})
	require.NoError(t, err)
	return row.ID
}

func (f *scopeFixture) mkPage(t *testing.T, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`INSERT INTO pages (id, space_id, title, content, author_id, path)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		uuid.New(), spaceID, title, title+"-body", f.ts.UserID,
		"/"+strings.ToLower(strings.ReplaceAll(title, " ", "-"))).Scan(&id))
	return id
}

func (f *scopeFixture) mkTicket(t *testing.T, spaceID uuid.UUID, number int32, title string) uuid.UUID {
	t.Helper()
	reporter := f.ts.UserID
	row, err := f.q.CreateTicket(context.Background(), generated.CreateTicketParams{
		ID: uuid.New(), SpaceID: spaceID, Number: number, Title: title,
		Description: title + "-body", Status: "open", Priority: "medium",
		ReporterID: pgtype.UUID{Bytes: reporter, Valid: true},
		Labels:     []string{}, Rank: "a",
	})
	require.NoError(t, err)
	return row.ID
}

func (f *scopeFixture) mkComment(t *testing.T, entityType string, entityID uuid.UUID, body string) {
	t.Helper()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO comments (id, entity_type, entity_id, author_id, body)
		 VALUES ($1,$2,$3,$4,$5)`,
		uuid.New(), entityType, entityID, f.ts.UserID, body)
	require.NoError(t, err)
}

func (f *scopeFixture) mkFieldValue(t *testing.T, itemID uuid.UUID, value string) {
	t.Helper()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO item_field_values (id, item_id, field_slug, value)
		 VALUES ($1,$2,'probe',$3)`,
		uuid.New(), itemID, value)
	require.NoError(t, err)
}

func (f *scopeFixture) mkTag(t *testing.T, pageID uuid.UUID, slug string) {
	t.Helper()
	var tagID uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`INSERT INTO tags (id, org_id, slug, name) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (org_id, slug) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`,
		uuid.New(), f.ts.OrgID, slug, slug).Scan(&tagID))
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO page_tags (page_id, tag_id) VALUES ($1,$2)
		 ON CONFLICT DO NOTHING`, pageID, tagID)
	require.NoError(t, err)
}

func (f *scopeFixture) base(spaceID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", f.ts.OrgID, spaceID)
}

// scopeCase is one route, expressed twice: reached through the space that owns
// the entity, and reached through a different space the caller may also read.
type scopeCase struct {
	name string
	// path builds the URL from the space to put in the URL and the entity id.
	path func(f *scopeFixture, urlSpace, entityID uuid.UUID) string
	// legit is the (urlSpace, entityID) pair that must keep working.
	legit func(f *scopeFixture) (uuid.UUID, uuid.UUID)
	// crossed is the pair that must disclose nothing: an entity in space B
	// addressed through a space the caller IS authorised for.
	crossed func(f *scopeFixture) (uuid.UUID, uuid.UUID)
	// secret must never appear in the crossed response.
	secret string
	// listRoute marks endpoints that answer 200 with an empty collection
	// rather than 404. Both are non-disclosing; the distinction is only about
	// which status to assert.
	listRoute bool
}

var scopeCases = []scopeCase{
	{
		name:    "GetItem",
		path:    func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/projects/items/" + e.String() },
		legit:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemA },
		crossed: func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemB },
		secret:  secretItem,
	},
	{
		name: "ItemFields",
		path: func(f *scopeFixture, s, e uuid.UUID) string {
			return f.base(s) + "/projects/items/" + e.String() + "/fields"
		},
		legit:     func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemA },
		crossed:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemB },
		secret:    secretField,
		listRoute: true,
	},
	{
		name: "ItemComments",
		path: func(f *scopeFixture, s, e uuid.UUID) string {
			return f.base(s) + "/projects/items/" + e.String() + "/comments"
		},
		legit:     func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemA },
		crossed:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.itemB },
		secret:    secretNote,
		listRoute: true,
	},
	{
		name:    "GetSprint",
		path:    func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/projects/sprints/" + e.String() },
		legit:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.sprintA },
		crossed: func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.sprintB },
		secret:  secretSprint,
	},
	{
		name: "SprintItems",
		path: func(f *scopeFixture, s, e uuid.UUID) string {
			return f.base(s) + "/projects/sprints/" + e.String() + "/items"
		},
		legit:     func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.sprintA },
		crossed:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.sprintB },
		secret:    secretItem,
		listRoute: true,
	},
	{
		name:    "GetTicket",
		path:    func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/tickets/" + e.String() },
		legit:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.beaconA.ID, f.ticketA },
		crossed: func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.beaconA.ID, f.ticketB },
		secret:  secretTicket,
	},
	{
		name:    "GetPage",
		path:    func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/wiki/" + e.String() },
		legit:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageA },
		crossed: func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageB },
		secret:  secretPage,
	},
	{
		name:    "PageDocument",
		path:    func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/wiki/" + e.String() + "/document" },
		legit:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageA },
		crossed: func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageB },
		secret:  secretPage,
	},
	{
		name:      "PageRevisions",
		path:      func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/wiki/" + e.String() + "/revisions" },
		legit:     func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageA },
		crossed:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageB },
		secret:    secretPage,
		listRoute: true,
	},
	{
		name:      "PageTags",
		path:      func(f *scopeFixture, s, e uuid.UUID) string { return f.base(s) + "/wiki/" + e.String() + "/tags" },
		legit:     func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageA },
		crossed:   func(f *scopeFixture) (uuid.UUID, uuid.UUID) { return f.spaceA.ID, f.pageB },
		secret:    secretTag,
		listRoute: true,
	},
}

// TestEntitySpaceScoping_CrossSpaceReadsDiscloseNothing is the fails-before half
// of the class. Every one of these routes returned the space-B entity in full
// before the reconciliation was added.
func TestEntitySpaceScoping_CrossSpaceReadsDiscloseNothing(t *testing.T) {
	f := newScopeFixture(t)

	for _, tc := range scopeCases {
		t.Run(tc.name, func(t *testing.T) {
			urlSpace, entityID := tc.crossed(f)
			res := f.ts.getAs(t, f.memberTok, tc.path(f, urlSpace, entityID))

			require.NotContains(t, string(res.Body), tc.secret,
				"an entity in a space this caller cannot read must not be disclosed through a space it can")

			if tc.listRoute {
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Contains(t, []string{"[]", "[]\n", "null", "null\n"}, string(res.Body),
					"a collection hanging off an entity in another space must come back empty")
				return
			}
			require.Equal(t, http.StatusNotFound, res.StatusCode,
				"body was: %s", string(res.Body))
		})
	}
}

// TestEntitySpaceScoping_OwnSpaceReadsStillWork is the negative control. Without
// it, refusing everything unconditionally would satisfy the test above while
// removing the product.
func TestEntitySpaceScoping_OwnSpaceReadsStillWork(t *testing.T) {
	f := newScopeFixture(t)

	for _, tc := range scopeCases {
		t.Run(tc.name, func(t *testing.T) {
			urlSpace, entityID := tc.legit(f)
			res := f.ts.getAs(t, f.memberTok, tc.path(f, urlSpace, entityID))
			require.Equal(t, http.StatusOK, res.StatusCode,
				"the same route through the entity's own space must still work; body was: %s",
				string(res.Body))
		})
	}
}

// TestEntitySpaceScoping_CrossOrgReadsDiscloseNothing carries the same probe
// across the organization boundary. The queries had no org predicate either, so
// tenancy rested entirely on the space predicate that was missing.
func TestEntitySpaceScoping_CrossOrgReadsDiscloseNothing(t *testing.T) {
	f := newScopeFixture(t)

	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	otherUser := testutil.CreateTestUser(t, f.ts.DB.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, f.ts.DB.Pool, otherOrg.ID, otherUser.ID, "vector")

	foreignItem, err := f.q.CreateProjectItem(context.Background(), generated.CreateProjectItemParams{
		ID: uuid.New(), SpaceID: otherSpace.ID, Kind: "task", Title: "FOREIGN-ORG-SECRET",
		Description: "FOREIGN-ORG-BODY", Status: "open", Priority: "medium",
		ReporterID: otherUser.ID, Labels: []string{}, Rank: "a",
	})
	require.NoError(t, err)

	// The caller's own org and a space they genuinely hold a grant on — only
	// the entity id belongs to somebody else.
	res := f.ts.getAs(t, f.memberTok,
		f.base(f.spaceA.ID)+"/projects/items/"+foreignItem.ID.String())
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.NotContains(t, string(res.Body), "FOREIGN-ORG-SECRET")
}

// TestEntitySpaceScoping_WorkflowTransitionIsSpaceScoped covers the two routes
// where the missing reconciliation was not only a disclosure but a MUTATION.
//
// ApplyWorkflowTransitionToTicket and ...ToItem loaded the entity with a bare id
// and then applied the URL space's workflow to it. The capability check is
// against {spaceID}, which the caller genuinely holds — so an agent in space A
// could drive the state machine of a ticket or item in any other space, or any
// other organization, and the audit row would name the wrong space.
//
// Asserted at the boundary rather than by outcome: the refusal has to arrive
// before anything is written, so the far entity's status is re-read afterwards
// and must be untouched.
func TestEntitySpaceScoping_WorkflowTransitionIsSpaceScoped(t *testing.T) {
	f := newScopeFixture(t)

	// The persona has to CLEAR the capability check to reach the code under
	// test. A contributor is refused 403 by RequireWriteFloor/CapTransitionAnyItem
	// before the entity is ever loaded, so a test written with the fixture's
	// default persona would assert the capability gate and would pass with the
	// space reconciliation deleted. This one holds `agent` on space A — every
	// capability the route asks for, in the space the URL names — and still
	// must not reach an entity in space B.
	ctx := context.Background()
	agent := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	for _, s := range []testutil.Space{f.spaceA, f.beaconA} {
		_, err := f.ts.GrantService.Create(ctx, f.ts.OrgID, s.ID,
			access.SubjectUser, agent.ID, access.RoleAgent, f.ts.UserID)
		require.NoError(t, err)
	}
	agentTok := f.ts.tokenFor(t, agent.ID, agent.Email)

	// The URL space needs a workflow assigned, and the body needs a state the
	// workflow actually contains. Without both, the handler answers 404 from
	// "no workflow assigned to space" or 409 from an unknown state BEFORE it
	// ever loads the entity — and the test would pass with the reconciliation
	// deleted, asserting nothing. Verified by reverting the fix: this test must
	// fail, and it does.
	wf := f.ts.WorkflowAdapter
	require.NoError(t, wf.SeedDefaultWorkflows(ctx, f.ts.OrgID))
	require.NoError(t, wf.AssignDefaultWorkflowToSpace(ctx, f.ts.OrgID, "beacon", f.beaconA.ID))
	require.NoError(t, wf.AssignDefaultWorkflowToSpace(ctx, f.ts.OrgID, "vector", f.spaceA.ID))

	// A state the URL space's workflow really offers from its initial state.
	targetState := func(t *testing.T, module string) string {
		t.Helper()
		w, err := wf.GetDefaultWorkflow(ctx, f.ts.OrgID, module)
		require.NoError(t, err)
		initial, err := wf.GetInitialState(ctx, w.ID)
		require.NoError(t, err)
		transitions, err := wf.ListAvailableTransitions(ctx, w.ID, initial.ID)
		require.NoError(t, err)
		require.NotEmpty(t, transitions)
		return transitions[0].ToStateID.String()
	}

	statusOf := func(t *testing.T, table string, id uuid.UUID) string {
		t.Helper()
		var status string
		require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
			`SELECT status FROM `+table+` WHERE id = $1`, id).Scan(&status))
		return status
	}

	t.Run("ticket", func(t *testing.T) {
		before := statusOf(t, "tickets", f.ticketB)
		res := f.ts.postAs(t, agentTok, fmt.Sprintf(
			"%s/tickets/%s/workflow-state", f.base(f.beaconA.ID), f.ticketB),
			map[string]any{"state_id": targetState(t, "tickets")})
		require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", string(res.Body))
		require.NotContains(t, string(res.Body), secretTicket)
		require.Equal(t, before, statusOf(t, "tickets", f.ticketB),
			"a refused transition must not have moved the far ticket")
	})

	t.Run("item", func(t *testing.T) {
		before := statusOf(t, "project_items", f.itemB)
		res := f.ts.postAs(t, agentTok, fmt.Sprintf(
			"%s/projects/items/%s/workflow-state", f.base(f.spaceA.ID), f.itemB),
			map[string]any{"state_id": targetState(t, "project_items")})
		require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", string(res.Body))
		require.NotContains(t, string(res.Body), secretItem)
		require.Equal(t, before, statusOf(t, "project_items", f.itemB),
			"a refused transition must not have moved the far item")
	})
}

// TestEntitySpaceScoping_ApprovalHistoryIsSpaceScoped covers the approval
// surface, which shipped in #99 after the rest of this audit was written and
// carried the same shape.
//
// GET /workflow/entities/{entityType}/{entityID}/approvals parsed {spaceID}
// into `_` — validating that the URL was well formed and then discarding it —
// and read the history keyed on the entity id alone. An approval row records
// who asked, who decided, when, and the decline reason, and workflow_approvals
// has carried a NOT NULL space_id since migration 047: the column that closes
// this was already on the row, and the sibling query for the space's pending
// list already filtered on it.
//
// The persona holds `agent` on space A so the route's own guards pass; what it
// must not reach is an approval belonging to space B.
func TestEntitySpaceScoping_ApprovalHistoryIsSpaceScoped(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	agent := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	_, err := f.ts.GrantService.Create(ctx, f.ts.OrgID, f.spaceA.ID,
		access.SubjectUser, agent.ID, access.RoleAgent, f.ts.UserID)
	require.NoError(t, err)
	agentTok := f.ts.tokenFor(t, agent.ID, agent.Email)

	// An approval on the space-B item, carrying a decline reason — the field
	// with the most to disclose.
	const declineReason = "XSPACE-SECRET-APPROVAL-REASON"
	_, err = f.ts.DB.Pool.Exec(ctx,
		`INSERT INTO workflow_approvals
		   (id, space_id, entity_type, entity_id, from_status, to_status,
		    requested_by, decided_by, decided_at, decision, reason)
		 VALUES ($1,$2,'item',$3,'open','in_review',$4,$4,now(),'declined',$5)`,
		uuid.New(), f.spaceB.ID, f.itemB, f.ts.UserID, declineReason)
	require.NoError(t, err)

	res := f.ts.getAs(t, agentTok, fmt.Sprintf(
		"%s/workflow/entities/item/%s/approvals", f.base(f.spaceA.ID), f.itemB))
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", string(res.Body))
	require.NotContains(t, string(res.Body), declineReason,
		"an approval history must not be readable through a space that does not own it")
	require.Contains(t, []string{"[]", "[]\n"}, string(res.Body),
		"the history of an entity in another space must come back empty")
}

// TestEntitySpaceScoping_GivesNoExistenceOracle pins the no-oracle property
// across the family: an entity that exists in a space the caller cannot read
// must be indistinguishable from an id that names nothing at all.
//
// This is what makes 404-not-403 load-bearing rather than cosmetic — a
// distinguishable "exists but forbidden" turns each of these routes into a probe
// for whether an id is real.
func TestEntitySpaceScoping_GivesNoExistenceOracle(t *testing.T) {
	f := newScopeFixture(t)

	for _, tc := range scopeCases {
		if tc.listRoute {
			continue // collections answer 200/[] either way; nothing to compare
		}
		t.Run(tc.name, func(t *testing.T) {
			urlSpace, realButForbidden := tc.crossed(f)
			forbidden := f.ts.getAs(t, f.memberTok, tc.path(f, urlSpace, realButForbidden))
			absent := f.ts.getAs(t, f.memberTok, tc.path(f, urlSpace, uuid.New()))

			require.Equal(t, absent.StatusCode, forbidden.StatusCode,
				"an existing-but-forbidden entity must not be distinguishable by status")
			require.Equal(t, withoutRequestID(t, absent.Body), withoutRequestID(t, forbidden.Body),
				"an existing-but-forbidden entity must not be distinguishable by body")
		})
	}
}
