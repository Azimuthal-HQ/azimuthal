package api_test

import (
	"context"
	"encoding/json"
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

// Cross-space read authorization on the relation routes — all three mounts of
// them — against the full production router and real PostgreSQL.
//
// These tests exist because the relations read join was keyed on a bare
// `er.to_id` with no readable-space predicate and no org predicate, while the
// route itself is space-scoped — the trap rather than the mitigation, because
// the {spaceID} in the URL authorises the FROM item and the far side's space
// was never consulted at all. The whole class is asserted here rather than in
// the service tests because a stub repository would only be asserting a Go
// reimplementation of a predicate that lives in SQL (D45).
//
// Since A4 the satellite is mounted per entity subtree — projects items,
// tickets, wiki pages — through one shared core, so every case here runs per
// MOUNT: a guarantee proved on one subtree says nothing about a sibling the
// router wires separately, and the mount loop is what makes "reaches parity"
// a tested property rather than an inference from shared code.
//
// The persona matters as much as the assertions. testutil.CreateTestUser makes
// an org OWNER, and an org admin holds every capability on every space through
// the middleware bypass — so a test written with that user cannot observe a
// space boundary at all, and would pass with the entire fix deleted. Every
// assertion below that must fail before the fix runs as `member`: an org
// `member` (not owner, not admin) holding a viewer grant on space A and nothing
// whatsoever on space B.

type relFixture struct {
	ts *testServer
	q  *generated.Queries

	spaceA, spaceB testutil.Space
	// beaconA is a beacon space the member also reads: the ticket mount's
	// near side lives here, which makes the ticket cases exercise a near and
	// a far side in DIFFERENT readable spaces — linking across spaces is the
	// feature, so the battery should cross one.
	beaconA   testutil.Space
	member    testutil.User
	memberTok string
	ownerTok  string

	// itemA1 and itemA2 live in space A, which member may read.
	itemA1, itemA2 uuid.UUID
	// itemB lives in space B, which member may not read.
	itemB uuid.UUID
	// pageA lives in space A; pageB in space B. Pages are the third entity
	// type the relations schema has always stored and the read path resolved
	// last, so the battery carries one on each side of the boundary.
	pageA, pageB uuid.UUID
	// ticketA1 lives in beaconA and is the ticket mount's near side.
	ticketA1 uuid.UUID
	// foreignItem lives in a different organization entirely.
	foreignItem uuid.UUID
}

func newRelFixture(t *testing.T) *relFixture {
	t.Helper()
	ts := newTestServer(t)
	q := generated.New(ts.DB.Pool)
	ctx := context.Background()

	f := &relFixture{ts: ts, q: q}

	f.spaceA = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")
	f.spaceB = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "vector")
	f.beaconA = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")

	// member is an org member, so no admin bypass. A contributor grant on
	// space A and beacon A and no grant at all on space B is the smallest
	// persona that can observe the boundary from every mount: they clear
	// every guard on the routes and still must not see across it.
	f.member = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	for _, s := range []testutil.Space{f.spaceA, f.beaconA} {
		_, err := ts.GrantService.Create(ctx, ts.OrgID, s.ID,
			access.SubjectUser, f.member.ID, access.RoleContributor, ts.UserID)
		require.NoError(t, err)
	}

	f.memberTok = ts.tokenFor(t, f.member.ID, f.member.Email)
	f.ownerTok = ts.Token

	f.itemA1 = f.mkItem(t, f.spaceA.ID, "Readable A1")
	f.itemA2 = f.mkItem(t, f.spaceA.ID, "Readable A2")
	f.itemB = f.mkItem(t, f.spaceB.ID, "SECRET-TITLE-IN-B")
	f.pageA = f.mkPage(t, f.spaceA.ID, "Readable Page A")
	f.pageB = f.mkPage(t, f.spaceB.ID, "SECRET-PAGE-TITLE-IN-B")
	f.ticketA1 = f.mkTicket(t, f.beaconA.ID, 1, "Readable Ticket A1")

	// A whole separate organization, to prove the org boundary as well as the
	// space one. The direct read of this item correctly 404s for our member;
	// the relations panel used to hand back its title anyway.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	otherUser := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, ts.DB.Pool, otherOrg.ID, otherUser.ID, "vector")
	f.foreignItem = f.mkItemAs(t, otherSpace.ID, "SECRET-TITLE-IN-OTHER-ORG", otherUser.ID)

	return f
}

func (f *relFixture) mkItem(t *testing.T, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	return f.mkItemAs(t, spaceID, title, f.ts.UserID)
}

func (f *relFixture) mkItemAs(t *testing.T, spaceID uuid.UUID, title string, reporter uuid.UUID) uuid.UUID {
	t.Helper()
	item, err := f.q.CreateProjectItem(context.Background(), generated.CreateProjectItemParams{
		ID: uuid.New(), SpaceID: spaceID, Kind: "task", Title: title,
		Description: "", Status: "open", Priority: "medium",
		ReporterID: reporter, Labels: []string{}, Rank: "a",
	})
	require.NoError(t, err)
	return item.ID
}

func (f *relFixture) mkTicket(t *testing.T, spaceID uuid.UUID, number int32, title string) uuid.UUID {
	t.Helper()
	row, err := f.q.CreateTicket(context.Background(), generated.CreateTicketParams{
		ID: uuid.New(), SpaceID: spaceID, Number: number, Title: title,
		Description: title + "-body", Status: "open", Priority: "medium",
		ReporterID: pgtype.UUID{Bytes: f.ts.UserID, Valid: true},
		Labels:     []string{}, Rank: "a",
	})
	require.NoError(t, err)
	return row.ID
}

func (f *relFixture) mkPage(t *testing.T, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`INSERT INTO pages (id, space_id, title, content, author_id, path)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		uuid.New(), spaceID, title, title+"-body", f.ts.UserID,
		"/"+strings.ToLower(strings.ReplaceAll(title, " ", "-"))).Scan(&id))
	return id
}

// link writes a relation row directly, bypassing the API. The write path is
// itself under test elsewhere in this file, and several of the read cases below
// describe rows that the fixed write path would now refuse to create — which is
// exactly why they must be plantable: existing databases already contain them.
func (f *relFixture) link(t *testing.T, fromID, toID uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	return f.linkTyped(t, fromID, "project_item", toID, "project_item", kind)
}

// linkTyped is link with both endpoint types named — the satellite table is
// polymorphic, and the page and ticket cases need rows the item-shaped helper
// cannot write.
func (f *relFixture) linkTyped(t *testing.T, fromID uuid.UUID, fromType string, toID uuid.UUID, toType, kind string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.q.CreateEntityRelation(context.Background(), generated.CreateEntityRelationParams{
		ID: id, FromID: fromID, FromType: fromType,
		ToID: toID, ToType: toType, Kind: kind, CreatedBy: f.ts.UserID,
	})
	require.NoError(t, err)
	return id
}

// relationsPath is the item-mounted route. spaceID is always space A: the
// caller is authorised for A, and the question every test asks is what that
// authorisation is allowed to reach.
func (f *relFixture) relationsPath(itemID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items/%s/relations",
		f.ts.OrgID, f.spaceA.ID, itemID)
}

// relMount is one subtree the relations satellite is mounted under, with the
// near entity the member legitimately reaches through it.
type relMount struct {
	name     string
	nearType string
	nearID   uuid.UUID
	// space is the near entity's own space — the {spaceID} the URL names.
	space uuid.UUID
	path  func(entityID uuid.UUID) string
}

// mounts enumerates the three subtrees. Every cross-space case runs once per
// entry: the router wires each mount separately, so a predicate proved on one
// is not evidence about another.
func (f *relFixture) mounts() []relMount {
	base := func(space uuid.UUID) string {
		return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", f.ts.OrgID, space)
	}
	return []relMount{
		{name: "items", nearType: "project_item", nearID: f.itemA1, space: f.spaceA.ID,
			path: func(e uuid.UUID) string { return fmt.Sprintf("%s/projects/items/%s/relations", base(f.spaceA.ID), e) }},
		{name: "tickets", nearType: "ticket", nearID: f.ticketA1, space: f.beaconA.ID,
			path: func(e uuid.UUID) string { return fmt.Sprintf("%s/tickets/%s/relations", base(f.beaconA.ID), e) }},
		{name: "pages", nearType: "page", nearID: f.pageA, space: f.spaceA.ID,
			path: func(e uuid.UUID) string { return fmt.Sprintf("%s/wiki/%s/relations", base(f.spaceA.ID), e) }},
	}
}

// wireRelation mirrors the JSON the handler emits, so the assertions test the
// bytes on the wire rather than a Go struct that never left the process.
type wireRelation struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Direction   string     `json:"direction"`
	FarReadable bool       `json:"far_readable"`
	FarID       *uuid.UUID `json:"far_id"`
	FarType     *string    `json:"far_type"`
	FarTitle    *string    `json:"far_title"`
	FarStatus   *string    `json:"far_status"`
	FarSpaceID  *uuid.UUID `json:"far_space_id"`
}

func decodeRelations(t *testing.T, body []byte) []wireRelation {
	t.Helper()
	var out []wireRelation
	require.NoError(t, json.Unmarshal(body, &out), "body was: %s", string(body))
	return out
}

// requireNoIdentity asserts the D82 placeholder shape: the row is present, so
// the panel can say a link exists, but nothing about the far entity survives.
func requireNoIdentity(t *testing.T, rel wireRelation, forbidden ...string) {
	t.Helper()
	require.False(t, rel.FarReadable, "far side must not be marked readable")
	require.Nil(t, rel.FarID, "an unreadable far side must not disclose its id")
	require.Nil(t, rel.FarType, "an unreadable far side must not disclose its type")
	require.Nil(t, rel.FarTitle, "an unreadable far side must not disclose its title")
	require.Nil(t, rel.FarStatus, "an unreadable far side must not disclose its status")
	require.Nil(t, rel.FarSpaceID, "an unreadable far side must not disclose its space")
	raw, err := json.Marshal(rel)
	require.NoError(t, err)
	for _, s := range forbidden {
		require.NotContains(t, string(raw), s,
			"the serialized row must not carry the far side's identity anywhere")
	}
}

// TestRelations_FarSideInUnreadableSpaceIsRedacted is the acute defect.
//
// FAILS BEFORE THE FIX: the previous query returned COALESCE(t.title, pi.title)
// for whatever `er.to_id` matched, so far_title came back "SECRET-TITLE-IN-B"
// for a member with no access to space B at all.
func TestRelations_FarSideInUnreadableSpaceIsRedacted(t *testing.T) {
	f := newRelFixture(t)

	// The direct read is already correctly refused — which is what made the
	// relations disclosure a bypass rather than a policy difference.
	direct := f.ts.getAs(t, f.memberTok, fmt.Sprintf(
		"/api/v1/orgs/%s/spaces/%s/projects/items/%s", f.ts.OrgID, f.spaceB.ID, f.itemB))
	require.Equal(t, http.StatusNotFound, direct.StatusCode,
		"precondition: space B must be unreadable to this persona by the normal route")

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			f.linkTyped(t, m.nearID, m.nearType, f.itemB, "project_item", "blocks")

			res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
			require.Equal(t, http.StatusOK, res.StatusCode)

			rels := decodeRelations(t, res.Body)
			require.Len(t, rels, 1)
			require.Equal(t, "blocks", rels[0].Kind)
			require.Equal(t, "outgoing", rels[0].Direction)
			requireNoIdentity(t, rels[0], "SECRET-TITLE-IN-B", f.itemB.String())
		})
	}
}

// TestRelations_FarSideInAnotherOrgIsRedacted is the same defect across the
// organization boundary.
//
// FAILS BEFORE THE FIX: the join had no org predicate either, so a relation
// planted at any UUID returned that entity's title regardless of tenancy.
func TestRelations_FarSideInAnotherOrgIsRedacted(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			f.linkTyped(t, m.nearID, m.nearType, f.foreignItem, "project_item", "relates_to")

			res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
			require.Equal(t, http.StatusOK, res.StatusCode)

			rels := decodeRelations(t, res.Body)
			require.Len(t, rels, 1)
			requireNoIdentity(t, rels[0], "SECRET-TITLE-IN-OTHER-ORG", f.foreignItem.String())
		})
	}
}

// TestRelations_ReadableFarSideStillResolves is the negative control. Without
// it, "redact everything unconditionally" would pass every other test in this
// file while destroying the feature.
//
// The ticket mount's near side lives in beaconA while the far side lives in
// spaceA, so that subtest is also the cross-space positive: a relation whose
// two ends sit in two different readable spaces resolves, and far_space_id
// names the FAR side's space rather than the URL's.
func TestRelations_ReadableFarSideStillResolves(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			f.linkTyped(t, m.nearID, m.nearType, f.itemA2, "project_item", "relates_to")

			res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
			require.Equal(t, http.StatusOK, res.StatusCode)

			rels := decodeRelations(t, res.Body)
			require.Len(t, rels, 1)
			require.True(t, rels[0].FarReadable)
			require.NotNil(t, rels[0].FarTitle)
			require.Equal(t, "Readable A2", *rels[0].FarTitle)
			require.Equal(t, "open", *rels[0].FarStatus)
			require.Equal(t, f.itemA2, *rels[0].FarID)
			require.Equal(t, "project_item", *rels[0].FarType)
			require.NotNil(t, rels[0].FarSpaceID, "a readable far side carries its space, so the client can build its URL")
			require.Equal(t, f.spaceA.ID, *rels[0].FarSpaceID)
		})
	}
}

// TestRelations_SoftDeletedFarSideIsRedacted covers the row that escapes a
// space predicate by not being live. The previous join tested neither
// deleted_at nor space_id, so a soft-deleted item kept disclosing its title
// through the relations panel long after it stopped being readable anywhere
// else.
func TestRelations_SoftDeletedFarSideIsRedacted(t *testing.T) {
	f := newRelFixture(t)
	for _, m := range f.mounts() {
		f.linkTyped(t, m.nearID, m.nearType, f.itemA2, "project_item", "relates_to")
	}
	require.NoError(t, f.q.SoftDeleteProjectItemInSpace(context.Background(), generated.SoftDeleteProjectItemInSpaceParams{ItemID: f.itemA2, SpaceID: f.spaceA.ID}))

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
			require.Equal(t, http.StatusOK, res.StatusCode)

			rels := decodeRelations(t, res.Body)
			require.Len(t, rels, 1)
			requireNoIdentity(t, rels[0], "Readable A2", f.itemA2.String())
		})
	}
}

// TestRelations_PageFarSideResolvesWithTitle is the A4 pages arm, positive
// direction — and it is created through the API, because the write path has
// accepted page targets since the readable-target check landed. Only the read
// was missing.
//
// FAILS BEFORE THE FIX: readable_target had a tickets arm and a project_items
// arm and no pages arm, so a stored relation to a page — legal to create —
// rendered as an unresolvable placeholder forever.
func TestRelations_PageFarSideResolvesWithTitle(t *testing.T) {
	f := newRelFixture(t)

	created := f.ts.postAs(t, f.memberTok, f.relationsPath(f.itemA1), map[string]any{
		"to_id":   f.pageA.String(),
		"to_type": "page",
		"kind":    "relates_to",
	})
	require.Equal(t, http.StatusCreated, created.StatusCode,
		"the write path is already page-capable; body: %s", string(created.Body))

	res := f.ts.getAs(t, f.memberTok, f.relationsPath(f.itemA1))
	require.Equal(t, http.StatusOK, res.StatusCode)

	rels := decodeRelations(t, res.Body)
	require.Len(t, rels, 1)
	require.True(t, rels[0].FarReadable, "a readable page target must resolve")
	require.Equal(t, "page", *rels[0].FarType)
	require.Equal(t, "Readable Page A", *rels[0].FarTitle)
	require.Equal(t, f.pageA, *rels[0].FarID)
	require.Equal(t, f.spaceA.ID, *rels[0].FarSpaceID)
	// Pages have no status column and no workflow; the query's page arm says
	// NULL::text explicitly. Anything non-nil here would be an invented value.
	require.Nil(t, rels[0].FarStatus, "a page has no status to report")
}

// TestRelations_PageFarSideInUnreadableSpaceRendersAbsent is the pages arm,
// negative direction: the far side must be ABSENT — indistinguishable from a
// target that never existed — not an error, and not a redacted-but-typed
// placeholder that reveals a page is there.
//
// The row itself still appears, exactly as it does for items and tickets
// (D82): the panel may say "a link exists here" while carrying nothing that
// identifies the far entity. What must not exist is any daylight between
// "page you cannot read" and "id that names nothing".
func TestRelations_PageFarSideInUnreadableSpaceRendersAbsent(t *testing.T) {
	f := newRelFixture(t)
	// A page in a space the member cannot read, and an id that exists nowhere.
	// Same kind on both, so the far side is the only thing that could differ.
	f.linkTyped(t, f.itemA1, "project_item", f.pageB, "page", "relates_to")
	f.linkTyped(t, f.itemA1, "project_item", uuid.New(), "page", "relates_to")

	res := f.ts.getAs(t, f.memberTok, f.relationsPath(f.itemA1))
	require.Equal(t, http.StatusOK, res.StatusCode, "an unreadable far side is not an error")

	rels := decodeRelations(t, res.Body)
	require.Len(t, rels, 2)
	for _, rel := range rels {
		requireNoIdentity(t, rel, "SECRET-PAGE-TITLE-IN-B", f.pageB.String(), f.spaceB.ID.String())
	}

	// Byte-level: with the relation ids cleared, the unreadable-page row and
	// the never-existed row must serialize identically, so no future far-side
	// field can quietly become the oracle.
	a, b := rels[0], rels[1]
	a.ID, b.ID = uuid.Nil, uuid.Nil
	aJSON, err := json.Marshal(a)
	require.NoError(t, err)
	bJSON, err := json.Marshal(b)
	require.NoError(t, err)
	require.Equal(t, string(aJSON), string(bJSON),
		"an unreadable page and a nonexistent target must be the same absence")
}

// TestRelations_BlockedItemSeesTheRelation is the reciprocal-visibility fix
// (the T1 finding), and it is a functional gap as much as a security one.
//
// FAILS BEFORE THE FIX: the query matched `WHERE er.from_id = $1` with no
// `OR to_id = $1` and no union, and CreateEntityRelation writes exactly one row
// with no inverse. A "blocks" link was therefore invisible to the very item it
// blocked — that item's Relations panel was empty. Run per mount: a blocked
// ticket and a blocked page need the reverse union exactly as items do.
func TestRelations_BlockedItemSeesTheRelation(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			f.linkTyped(t, f.itemA2, "project_item", m.nearID, m.nearType, "blocks")

			res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
			require.Equal(t, http.StatusOK, res.StatusCode)

			rels := decodeRelations(t, res.Body)
			require.Len(t, rels, 1, "the blocked entity must see the relation that blocks it")
			require.Equal(t, "incoming", rels[0].Direction)
			require.Equal(t, "blocks", rels[0].Kind)
			require.True(t, rels[0].FarReadable)
			require.Equal(t, "Readable A2", *rels[0].FarTitle)
			require.Equal(t, f.itemA2, *rels[0].FarID)
		})
	}
}

// TestRelations_ReciprocalVisibilityIsItselfGated is the pair the D82 rule
// turns on: the same incoming relation, read by someone who may see the far
// side and by someone who may not.
//
// The row appears for both — that is the point of surfacing the reverse
// direction, since an entity needs to know it is blocked — but only one of
// them learns what is doing the blocking.
func TestRelations_ReciprocalVisibilityIsItselfGated(t *testing.T) {
	f := newRelFixture(t)
	for _, m := range f.mounts() {
		// Something in space B blocks each mount's near entity.
		f.linkTyped(t, f.itemB, "project_item", m.nearID, m.nearType, "blocks")
	}

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			t.Run("viewer who cannot read the far side gets a placeholder", func(t *testing.T) {
				res := f.ts.getAs(t, f.memberTok, m.path(m.nearID))
				require.Equal(t, http.StatusOK, res.StatusCode)

				rels := decodeRelations(t, res.Body)
				require.Len(t, rels, 1, "the blocked entity still learns that it is blocked")
				require.Equal(t, "incoming", rels[0].Direction)
				require.Equal(t, "blocks", rels[0].Kind)
				requireNoIdentity(t, rels[0], "SECRET-TITLE-IN-B", f.itemB.String())
			})

			t.Run("viewer who can read the far side gets the identity", func(t *testing.T) {
				// The org owner reads every space through the ADR-0007
				// middleware bypass, which produces a readable set with
				// nothing filtered out rather than a special case in the query.
				res := f.ts.getAs(t, f.ownerTok, m.path(m.nearID))
				require.Equal(t, http.StatusOK, res.StatusCode)

				rels := decodeRelations(t, res.Body)
				require.Len(t, rels, 1)
				require.Equal(t, "incoming", rels[0].Direction)
				require.True(t, rels[0].FarReadable)
				require.Equal(t, "SECRET-TITLE-IN-B", *rels[0].FarTitle)
				require.Equal(t, f.itemB, *rels[0].FarID)
			})
		})
	}
}

// TestRelations_CreateRefusesUnreadableTarget is the write half.
//
// FAILS BEFORE THE FIX: validateRelation checked only that the kind was known
// and that from and to differed. It never resolved the target, migration 015
// dropped the foreign keys so the database did not either, and the row was
// stored — after which the read path handed the title back.
func TestRelations_CreateRefusesUnreadableTarget(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			res := f.ts.postAs(t, f.memberTok, m.path(m.nearID), map[string]any{
				"to_id": f.itemB.String(),
				"kind":  "blocks",
			})
			require.Equal(t, http.StatusNotFound, res.StatusCode,
				"a target in an unreadable space must be refused")
			require.NotContains(t, string(res.Body), "SECRET-TITLE-IN-B")

			var count int
			require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
				`SELECT count(*) FROM entity_relations WHERE from_id = $1`, m.nearID).Scan(&count))
			require.Zero(t, count, "a refused relation must not be persisted")
		})
	}
}

// TestRelations_CreateRefusesForeignOrgTarget is the same refusal across the
// organization boundary.
func TestRelations_CreateRefusesForeignOrgTarget(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			res := f.ts.postAs(t, f.memberTok, m.path(m.nearID), map[string]any{
				"to_id": f.foreignItem.String(),
				"kind":  "relates_to",
			})
			require.Equal(t, http.StatusNotFound, res.StatusCode)

			var count int
			require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
				`SELECT count(*) FROM entity_relations WHERE from_id = $1`, m.nearID).Scan(&count))
			require.Zero(t, count)
		})
	}
}

// TestRelations_CreateAllowsReadableTarget is the negative control for the
// write refusal: "refuse everything" must not pass. Per mount, this is also
// the parity proof that each new route CREATES with its own from side: the
// stored row's from_type must match the subtree the request went through.
func TestRelations_CreateAllowsReadableTarget(t *testing.T) {
	f := newRelFixture(t)

	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			res := f.ts.postAs(t, f.memberTok, m.path(m.nearID), map[string]any{
				"to_id": f.itemA2.String(),
				"kind":  "blocks",
			})
			require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", string(res.Body))

			var fromType string
			require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
				`SELECT from_type FROM entity_relations WHERE from_id = $1`, m.nearID).Scan(&fromType))
			require.Equal(t, m.nearType, fromType,
				"the from side's type comes from the route, and must match the subtree posted to")
		})
	}
}

// TestRelations_CreateGivesNoExistenceOracle is the no-oracle proof.
//
// A distinguishable "exists but forbidden" is the same disclosure as returning
// the title, in a different shape: it turns the endpoint into a probe for
// whether an arbitrary UUID names a real entity. So the two refusals must be
// byte-identical, not merely both-4xx.
//
// The property is structural rather than a matched pair of literals in the
// handler: the repository answers with one bool, so the service has a single
// error value to return and no branch that could drift apart. This test pins
// the observable consequence.
func TestRelations_CreateGivesNoExistenceOracle(t *testing.T) {
	f := newRelFixture(t)

	// Per mount (which covers the ticket FROM-side) and per target type
	// (which covers the page target): the envelopes stay compared verbatim,
	// so a future field that differed between the refusals would fail here
	// rather than slip through a loosened status-only check.
	targets := []struct {
		name      string
		toType    string
		forbidden uuid.UUID
	}{
		{"item target", "project_item", f.itemB},
		{"page target", "page", f.pageB},
	}

	for _, m := range f.mounts() {
		for _, tgt := range targets {
			t.Run(m.name+"/"+tgt.name, func(t *testing.T) {
				// Exists, in a space this persona cannot read.
				forbidden := f.ts.postAs(t, f.memberTok, m.path(m.nearID), map[string]any{
					"to_id":   tgt.forbidden.String(),
					"to_type": tgt.toType,
					"kind":    "blocks",
				})
				// Does not exist anywhere.
				absent := f.ts.postAs(t, f.memberTok, m.path(m.nearID), map[string]any{
					"to_id":   uuid.New().String(),
					"to_type": tgt.toType,
					"kind":    "blocks",
				})

				require.Equal(t, http.StatusNotFound, forbidden.StatusCode)
				require.Equal(t, absent.StatusCode, forbidden.StatusCode,
					"an existing-but-forbidden target must not be distinguishable by status")
				require.Equal(t, withoutRequestID(t, absent.Body), withoutRequestID(t, forbidden.Body),
					"an existing-but-forbidden target must not be distinguishable by body")
			})
		}
	}
}

// withoutRequestID renders an error body with the per-request correlation id
// removed. Everything else is compared verbatim rather than field by field, so
// a future field that did differ between the two refusals would still fail this
// test instead of going unnoticed.
func withoutRequestID(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "body was: %s", string(body))
	require.NotEmpty(t, envelope.Error["request_id"], "the error envelope should carry a request id")
	delete(envelope.Error, "request_id")
	out, err := json.Marshal(envelope)
	require.NoError(t, err)
	return string(out)
}

// TestRelations_EmptyReadableSetResolvesNothing pins the degenerate case the
// SQL comment flags: `= ANY('{}')` is false for every row, and a nil array is
// NULL, which is equally non-matching in a JOIN. Both are fail-closed, but by
// accident of SQL semantics rather than by intent — so the behaviour is
// asserted rather than left resting on the trivia.
//
// The persona is an org member with no grants at all, which is the only way to
// reach an empty readable set through the real router.
func TestRelations_EmptyReadableSetResolvesNothing(t *testing.T) {
	f := newRelFixture(t)
	f.link(t, f.itemA1, f.itemA2, "relates_to")

	stranger := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")
	strangerTok := f.ts.tokenFor(t, stranger.ID, stranger.Email)

	// They cannot reach the routes at all — RequireSpaceReadable 404s first,
	// which is the correct outer behaviour and is asserted here so that a
	// regression in the guard shows up as a failure of this test rather than
	// as silently broader access.
	for _, m := range f.mounts() {
		t.Run(m.name, func(t *testing.T) {
			res := f.ts.getAs(t, strangerTok, m.path(m.nearID))
			require.Equal(t, http.StatusNotFound, res.StatusCode)
		})
	}
}
