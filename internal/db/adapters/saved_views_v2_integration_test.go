package adapters_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Filter document v2 — date ranges and per-field negation — against a real
// database.
//
// These sit beside the v1 fan-out tests and follow the same rule: every one is
// written so that DELETING the thing it tests makes it fail. A date test whose
// fixtures are all inside the window, or a negation test whose fixtures all
// have the column populated, would pass with the feature removed.

// v2Query parses a v2 document, so the tests read as the document a client
// would actually send rather than as a Go struct literal.
func v2Query(t *testing.T, body string) views.Query {
	t.Helper()
	q, err := views.ParseQuery([]byte(`{"v":2,"sort":{"field":"updated_at","dir":"desc"},"filter":{` + body + `}}`))
	require.NoError(t, err)
	return q
}

// backdate moves a ticket's created_at and updated_at.
//
// Both columns default to now(), so every fixture row starts inside any
// backward-looking window. A date test whose rows are all inside the window
// would pass with the predicate deleted, so moving them is what makes the
// assertions mean anything.
func backdate(t *testing.T, db *testutil.TestDB, id uuid.UUID, at time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(),
		`UPDATE tickets SET created_at = $2, updated_at = $2 WHERE id = $1`, id, at)
	require.NoError(t, err)
}

func backdateItem(t *testing.T, db *testutil.TestDB, id uuid.UUID, at time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(),
		`UPDATE project_items SET created_at = $2, updated_at = $2 WHERE id = $1`, id, at)
	require.NoError(t, err)
}

func setDueAt(t *testing.T, db *testutil.TestDB, id uuid.UUID, at time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(),
		`UPDATE tickets SET due_at = $2 WHERE id = $1`, id, at)
	require.NoError(t, err)
}

// orgAggStore is orgStore's counterpart for the four aggregate fan-outs.
func orgAggStore(a *adapters.SavedViewAdapter, orgID uuid.UUID) views.AggregateStore {
	return orgAggBound{a: a, orgID: orgID}
}

type orgAggBound struct {
	a     *adapters.SavedViewAdapter
	orgID uuid.UUID
}

func (o orgAggBound) CountTickets(ctx context.Context, p views.FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	return o.a.CountTickets(ctx, p)
}

func (o orgAggBound) CountProjectItems(ctx context.Context, p views.FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	return o.a.CountProjectItems(ctx, p)
}

func (o orgAggBound) BreakdownTickets(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	p.OrgID = o.orgID
	return o.a.BreakdownTickets(ctx, p)
}

func (o orgAggBound) BreakdownProjectItems(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	p.OrgID = o.orgID
	return o.a.BreakdownProjectItems(ctx, p)
}

// TestViewResults_DateRangeFiltersOnBothSidesOfTheWindow is the core date test.
//
// Three tickets, one INSIDE the window and one on each side of it. A predicate
// that were dropped entirely would return all three, and a predicate applied in
// only one direction would return two — so both bounds are proved by the same
// fixture.
func TestViewResults_DateRangeFiltersOnBothSidesOfTheWindow(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	old := insertTicket(t, db.Pool, space.ID, user.ID, 1, "old", "open", "high", nil)
	mid := insertTicket(t, db.Pool, space.ID, user.ID, 2, "mid", "open", "high", nil)
	recent := insertTicket(t, db.Pool, space.ID, user.ID, 3, "recent", "open", "high", nil)

	now := time.Now().UTC()
	backdate(t, db, old, now.AddDate(0, 0, -30))
	backdate(t, db, mid, now.AddDate(0, 0, -10))
	backdate(t, db, recent, now.AddDate(0, 0, -1))

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: now}

	// A two-sided window: 20 days ago up to 5 days ago.
	q := v2Query(t, `"modules":["beacon"],"updated_at":{"after":"-20d","before":"-5d"}`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[mid], "the ticket inside the window must be returned")
	require.False(t, got[old], "a ticket older than the window's start must be excluded")
	require.False(t, got[recent], "a ticket newer than the window's end must be excluded")

	// One-sided, the shape the "last 7 days" preset produces.
	q = v2Query(t, `"modules":["beacon"],"updated_at":{"after":"-7d"}`)
	page, err = views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	got = resultIDs(page)
	require.True(t, got[recent])
	require.False(t, got[mid])
	require.False(t, got[old])
}

// TestViewResults_DateBoundsAreHalfOpen pins the inclusivity, which is the part
// a reader cannot infer and the part two adjacent ranges depend on.
//
// `after` is inclusive and `before` is exclusive, so a row sitting exactly on a
// boundary belongs to precisely one of two abutting windows. If both bounds
// were inclusive it would belong to both, and two gadgets splitting a timeline
// would report a total larger than the list they split.
func TestViewResults_DateBoundsAreHalfOpen(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	edge := insertTicket(t, db.Pool, space.ID, user.ID, 1, "on the boundary", "open", "high", nil)
	boundary := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	backdate(t, db, edge, boundary)

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: time.Now().UTC()}
	iso := boundary.Format(time.RFC3339)

	// after is INCLUSIVE: the row on the boundary is in.
	q := v2Query(t, `"modules":["beacon"],"updated_at":{"after":"`+iso+`"}`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	require.True(t, resultIDs(page)[edge], "after is inclusive, so the boundary row is inside the range")

	// before is EXCLUSIVE: the same row is out.
	q = v2Query(t, `"modules":["beacon"],"updated_at":{"before":"`+iso+`"}`)
	page, err = views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	require.False(t, resultIDs(page)[edge], "before is exclusive, so the boundary row is outside the range")
}

// TestViewResults_NullDatesMatchNoRange states the reading of an ABSENT date.
//
// due_at and resolved_at are nullable. A row with no due date is not "due
// before X" and not "due after X" either — an absent fact is neither early nor
// late — so it matches no due_at range in either direction. The row must still
// be reachable by the same view without the date filter, or the test cannot
// tell "correctly excluded" from "not there at all".
func TestViewResults_NullDatesMatchNoRange(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	noDue := insertTicket(t, db.Pool, space.ID, user.ID, 1, "no due date", "open", "high", nil)
	withDue := insertTicket(t, db.Pool, space.ID, user.ID, 2, "has a due date", "open", "high", nil)
	setDueAt(t, db, withDue, time.Now().UTC().AddDate(0, 0, 3))

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: time.Now().UTC()}

	for _, body := range []string{
		`"modules":["beacon"],"due_at":{"after":"-999d"}`,
		`"modules":["beacon"],"due_at":{"before":"+999d"}`,
	} {
		page, err := views.Resolve(ctx, orgStore(a, org.ID), v2Query(t, body), viewer, "", 50)
		require.NoError(t, err)
		got := resultIDs(page)
		require.True(t, got[withDue], "the dated row must be inside this deliberately huge range: %s", body)
		require.False(t, got[noDue], "a row with no due date matches no due_at range: %s", body)
	}

	// And it is still reachable without the date filter.
	page, err := views.Resolve(ctx, orgStore(a, org.ID), beaconQuery(t, `"modules":["beacon"]`), viewer, "", 50)
	require.NoError(t, err)
	require.True(t, resultIDs(page)[noDue], "the undated row exists and is readable — the exclusion above was the filter")
}

// TestViewResults_NegationKeepsNullRows is the three-valued-logic regression
// test, and the fixture shape IS the test.
//
// `assignee_id = ANY(...)` is NULL rather than false for an unassigned row, and
// `NULL <> true` is NULL, so a negation written without COALESCE silently drops
// every unassigned row from "not assigned to Alice" — the set they plainly
// belong to. A fixture in which every row has an assignee passes either way.
//
// Fails-before: remove the COALESCE from the assignee predicate in
// saved_views.sql and the unassigned assertion fails.
func TestViewResults_NegationKeepsNullRows(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	alice := testutil.CreateTestUser(t, db.Pool, org.ID)
	bob := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, alice.ID, "beacon")

	hers := insertTicket(t, db.Pool, space.ID, alice.ID, 1, "alice's", "open", "high", &alice.ID)
	his := insertTicket(t, db.Pool, space.ID, alice.ID, 2, "bob's", "open", "high", &bob.ID)
	nobodys := insertTicket(t, db.Pool, space.ID, alice.ID, 3, "unassigned", "open", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: alice.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: time.Now().UTC()}

	q := v2Query(t, `"modules":["beacon"],"assignees":["`+alice.ID.String()+`"],"not":{"assignees":true}`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.False(t, got[hers], "the excluded assignee's ticket must not be returned")
	require.True(t, got[his], "another assignee's ticket is not the excluded one")
	require.True(t, got[nobodys],
		"an UNASSIGNED ticket is not assigned to the excluded person, so it belongs in the result — "+
			"if this fails, the negated predicate is losing NULL rows to three-valued logic")
}

// TestViewResults_NegationInvertsEachMembershipField checks the flip itself, on
// every negatable field with a NOT NULL column.
//
// It exists because of a mutation that survived without it: neutralising the
// `<> @not_statuses` flip left the whole suite green. The count-versus-list
// parity test uses a negated status filter, but it compares two queries that
// BOTH lost the negation, so they went on agreeing with each other while
// agreeing about the wrong set. Only a test that names the expected rows can
// see that.
//
// Fails-before: replace any `<> @not_*` with plain inclusion and the matching
// case here fails.
func TestViewResults_NegationInvertsEachMembershipField(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	open := insertTicket(t, db.Pool, space.ID, user.ID, 1, "open one", "open", "high", nil)
	closed := insertTicket(t, db.Pool, space.ID, user.ID, 2, "closed one", "closed", "low", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: time.Now().UTC()}

	cases := []struct {
		name           string
		body           string
		wantIn, wantNo uuid.UUID
	}{
		{
			"statuses",
			`"modules":["beacon"],"statuses":["closed"],"not":{"statuses":true}`,
			open, closed,
		},
		{
			"priorities",
			`"modules":["beacon"],"priorities":["low"],"not":{"priorities":true}`,
			open, closed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, err := views.Resolve(ctx, orgStore(a, org.ID), v2Query(t, c.body), viewer, "", 50)
			require.NoError(t, err)
			got := resultIDs(page)
			require.True(t, got[c.wantIn], "the row NOT named by the filter must be returned")
			require.False(t, got[c.wantNo], "the row named by a negated filter must be excluded")

			// The same document without the negation must return the mirror
			// image. Without this half, a predicate that always returned the
			// same single row would satisfy the assertions above.
			inclusive := v2Query(t, withoutNegation(c.body))
			page, err = views.Resolve(ctx, orgStore(a, org.ID), inclusive, viewer, "", 50)
			require.NoError(t, err)
			got = resultIDs(page)
			require.True(t, got[c.wantNo], "without the negation the named row is the one returned")
			require.False(t, got[c.wantIn], "and the other one is not")
		})
	}
}

// withoutNegation turns a negated filter body into its inclusive twin, so both
// directions come from ONE fixture and cannot drift apart.
func withoutNegation(body string) string {
	for _, f := range []string{"statuses", "priorities", "space_ids", "assignees", "kinds", "sprint_ids"} {
		body = strings.ReplaceAll(body, `,"not":{"`+f+`":true}`, "")
	}
	return body
}

// TestViewResults_NegatedMeTokenDivergesPerViewer joins the two viewer-relative
// mechanisms.
//
// The `me` token resolves per viewer; negation flips the sense. Together they
// must mean "not mine" for EACH reader independently, so one stored document
// returns different rows to two people — and each person's own ticket is the
// one missing from their own result.
func TestViewResults_NegatedMeTokenDivergesPerViewer(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	alice := testutil.CreateTestUser(t, db.Pool, org.ID)
	bob := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, alice.ID, "beacon")

	hers := insertTicket(t, db.Pool, space.ID, alice.ID, 1, "alice's", "open", "high", &alice.ID)
	his := insertTicket(t, db.Pool, space.ID, alice.ID, 2, "bob's", "open", "high", &bob.ID)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := v2Query(t, `"modules":["beacon"],"assignees":["me"],"not":{"assignees":true}`)
	now := time.Now().UTC()

	page, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: alice.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: now}, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.False(t, got[hers], "alice's own ticket is the one \"not mine\" excludes for alice")
	require.True(t, got[his])

	page, err = views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: bob.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: now}, "", 50)
	require.NoError(t, err)
	got = resultIDs(page)
	require.True(t, got[hers], "the SAME document returns alice's ticket to bob")
	require.False(t, got[his], "and excludes bob's own")
}

// TestViewResults_NegatedSpaceCannotReachAnUnreadableSpace is the access test
// for negation.
//
// Negating space_ids inverts a membership term that sits as a sibling AND of
// the access union, so it can only narrow within what the viewer may already
// read. That is an argument about operator precedence in the SQL, and a
// misplaced parenthesis would break it silently — the existing leak tests
// exercise array WIDENING and would not catch it.
func TestViewResults_NegatedSpaceCannotReachAnUnreadableSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	other := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")

	inOpen := insertTicket(t, db.Pool, open.ID, user.ID, 1, "open one", "open", "high", nil)
	inOther := insertTicket(t, db.Pool, other.ID, user.ID, 1, "other one", "open", "high", nil)
	secret := insertTicket(t, db.Pool, hidden.ID, user.ID, 1, "secret", "open", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{
		UserID:           user.ID,
		ReadableSpaceIDs: []uuid.UUID{open.ID, other.ID},
		At:               time.Now().UTC(),
	}

	// "Every space except `open`" — which must mean every space the viewer can
	// read except open, and emphatically not the hidden one.
	q := v2Query(t, `"modules":["beacon"],"space_ids":["`+open.ID.String()+`"],"not":{"space_ids":true}`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.False(t, got[inOpen], "the excluded space's ticket must not appear")
	require.True(t, got[inOther], "a readable space that was not excluded must still appear")
	require.False(t, got[secret],
		"negating a space filter must never reach a space the viewer cannot read — "+
			"the access union is a sibling AND, not a term the negation can invert")
}

// TestViewResults_HiddenSpaceDoesNotLeakThroughADateFilter re-runs the leak
// test through the new predicate.
//
// A new WHERE term added in the wrong place — inside the access OR-group rather
// than beside it — widens read access rather than narrowing results, and every
// existing test still passes because they do not use the new term.
func TestViewResults_HiddenSpaceDoesNotLeakThroughADateFilter(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")

	visible := insertTicket(t, db.Pool, open.ID, user.ID, 1, "visible", "open", "high", nil)
	secret := insertTicket(t, db.Pool, hidden.ID, user.ID, 1, "secret", "open", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := v2Query(t, `"modules":["beacon"],"updated_at":{"after":"-30d"}`)

	outsider := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID}, At: time.Now().UTC()}
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, outsider, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[visible], "the readable ticket is inside the window")
	require.False(t, got[secret], "a date filter must not widen what the viewer can read")

	insider := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID, hidden.ID}, At: time.Now().UTC()}
	page, err = views.Resolve(ctx, orgStore(a, org.ID), q, insider, "", 50)
	require.NoError(t, err)
	require.True(t, resultIDs(page)[secret],
		"the same view shows it to someone who can read the space, or the first half proves nothing")
}

// TestViewAggregate_CountAgreesWithTheListItCounts is the parity test the six
// duplicated predicate blocks need.
//
// The count and breakdown gadget queries are separate SQL from the list
// fan-outs. A v2 term added to one and forgotten in the other compiles, runs,
// and reports a number for a query nobody ran — a gadget quietly disagreeing
// with the list beneath it. This exercises EVERY v2 field at once, across both
// modules, so a single omission anywhere in the six shows up here.
func TestViewAggregate_CountAgreesWithTheListItCounts(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	beacon := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	vector := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	now := time.Now().UTC()
	for i := int32(1); i <= 6; i++ {
		id := insertTicket(t, db.Pool, beacon.ID, user.ID, i, "t", statusFor(i), "high", assigneeFor(i, user.ID))
		backdate(t, db, id, now.AddDate(0, 0, -int(i)*3))
	}
	for i := int32(1); i <= 6; i++ {
		id := insertItem(t, db.Pool, org.ID, vector.ID, user.ID, i, "v", statusFor(i), "high", "task", assigneeFor(i, user.ID))
		backdateItem(t, db, id, now.AddDate(0, 0, -int(i)*3))
	}

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{
		UserID:           user.ID,
		ReadableSpaceIDs: []uuid.UUID{beacon.ID, vector.ID},
		At:               now,
	}

	// Every v2 capability at once, across both modules.
	q := v2Query(t, `"modules":["beacon","vector"],`+
		`"statuses":["closed"],"not":{"statuses":true},`+
		`"updated_at":{"after":"-20d","before":"-2d"}`)

	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 200)
	require.NoError(t, err)

	res, err := views.Aggregate(ctx, orgAggStore(a, org.ID), q, viewer, views.GroupStatus)
	require.NoError(t, err)

	require.Equal(t, int64(len(page.Results)), res.Total,
		"the count gadget and the list disagree — one of the six fan-out predicate blocks "+
			"is missing a v2 term the others have")
	require.NotZero(t, res.Total, "the fixture must actually match something, or this proves nothing")

	// The breakdown buckets must sum to the same number, which reaches the
	// remaining two of the six queries.
	var summed int64
	for _, b := range res.Buckets {
		summed += b.Count
	}
	require.Equal(t, res.Total, summed, "the breakdown fan-outs filter differently from the count fan-outs")
}

// TestViewResults_RelativeTokensResolveAgainstOneInstant is the single-now
// test.
//
// Two evaluations built from ONE Viewer must land on the same boundary. If the
// clock were read per evaluation instead, a row written between them could
// appear in one and not the other — a difference of one, occasionally, which is
// exactly the kind of disagreement nobody reports and nobody can reproduce.
//
// Pinning At to a fixed instant makes the property checkable at all: with a
// per-call time.Now() the two results would differ only by microseconds of
// luck, so the test asserts the mechanism rather than hoping to catch the race.
func TestViewResults_RelativeTokensResolveAgainstOneInstant(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	// A row sitting EXACTLY on the -7d boundary relative to the pinned instant.
	// Any drift in the resolved instant moves it across the edge.
	pinned := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	edge := insertTicket(t, db.Pool, space.ID, user.ID, 1, "exactly seven days old", "open", "high", nil)
	backdate(t, db, edge, pinned.AddDate(0, 0, -7))

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: pinned}
	q := v2Query(t, `"modules":["beacon"],"updated_at":{"after":"-7d"}`)

	// Two evaluations, one Viewer — the shape two gadgets on one dashboard take.
	first, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	second, err := views.Aggregate(ctx, orgAggStore(a, org.ID), q, viewer, views.GroupStatus)
	require.NoError(t, err)

	require.True(t, resultIDs(first)[edge], "after is inclusive, so the row exactly on the boundary is in")
	require.Equal(t, int64(len(first.Results)), second.Total,
		"two evaluations from one Viewer resolved \"-7d\" to different instants")

	// And a viewer whose instant is one day later no longer sees it, which
	// proves the boundary actually moves with At rather than being ignored.
	later := viewer
	later.At = pinned.AddDate(0, 0, 1)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, later, "", 50)
	require.NoError(t, err)
	require.False(t, resultIDs(page)[edge],
		"the window is anchored to Viewer.At; if this passes unchanged the token is not being resolved at all")
}

// TestViewResults_RelativeTokenWithoutAnInstantIsRefused pins the fail-closed
// behaviour.
//
// A caller that forgets Viewer.At would otherwise resolve "-7d" against the
// zero time and match everything — a SILENTLY WIDER result set, which is the
// failure mode this package refuses everywhere else. An error is the only safe
// answer.
func TestViewResults_RelativeTokenWithoutAnInstantIsRefused(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	insertTicket(t, db.Pool, space.ID, user.ID, 1, "anything", "open", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	noClock := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}} // At deliberately unset

	_, err := views.Resolve(ctx, orgStore(a, org.ID),
		v2Query(t, `"modules":["beacon"],"updated_at":{"after":"-7d"}`), noClock, "", 50)
	require.Error(t, err, "a relative bound with no evaluation instant must fail rather than match everything")

	// An ABSOLUTE bound needs no clock and must still work, so the guard is on
	// the relative token rather than on date filtering as such.
	_, err = views.Resolve(ctx, orgStore(a, org.ID),
		v2Query(t, `"modules":["beacon"],"updated_at":{"after":"2020-01-01T00:00:00Z"}`), noClock, "", 50)
	require.NoError(t, err, "an absolute bound resolves without a clock")
}

// TestViewResults_V1DocumentsEvaluateUnchanged is the compatibility test.
//
// A stored v1 document must go on meaning exactly what it meant. Evaluation is
// version-free by construction — an absent bound and a false flag are already
// no-ops in the SQL — and this is the assertion that keeps it so.
func TestViewResults_V1DocumentsEvaluateUnchanged(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	open := insertTicket(t, db.Pool, space.ID, user.ID, 1, "open one", "open", "high", nil)
	closed := insertTicket(t, db.Pool, space.ID, user.ID, 2, "closed one", "closed", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}

	// A v1 document, evaluated by a v2 build, with NO evaluation instant set —
	// because a v1 document can never need one.
	q := beaconQuery(t, `"modules":["beacon"],"statuses":["open"]`)
	require.Equal(t, 1, q.V, "the document is still v1 after parsing")

	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[open])
	require.False(t, got[closed], "the v1 status filter still filters")
}

func statusFor(i int32) string {
	if i%2 == 0 {
		return "closed"
	}
	return "open"
}

func assigneeFor(i int32, id uuid.UUID) *uuid.UUID {
	if i%3 == 0 {
		return nil
	}
	return &id
}

// TestViewResults_VectorHalfAppliesEveryV2Filter is the test whose absence let a
// real defect ship in this change's first draft.
//
// Every other v2 test here is Beacon-only, and the one cross-module test
// compared a count against a list — two queries that had BOTH lost the same
// parameters, so they agreed with each other about the wrong set of rows. The
// project_items adapters were silently dropping the four shared negation flags
// and all eight date bounds, and nothing said so.
//
// So this exercises the VECTOR half directly, on its own, for each v2
// capability. Fails-before: remove any of those twelve fields from
// ListViewProjectItemsParams in the adapter and one of these assertions fails.
func TestViewResults_VectorHalfAppliesEveryV2Filter(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	now := time.Now().UTC()
	old := insertItem(t, db.Pool, org.ID, space.ID, user.ID, 1, "old bug", "open", "high", "bug", nil)
	recentBug := insertItem(t, db.Pool, org.ID, space.ID, user.ID, 2, "recent bug", "open", "high", "bug", nil)
	recentTask := insertItem(t, db.Pool, org.ID, space.ID, user.ID, 3, "recent task", "closed", "low", "task", nil)
	backdateItem(t, db, old, now.AddDate(0, 0, -30))
	backdateItem(t, db, recentBug, now.AddDate(0, 0, -1))
	backdateItem(t, db, recentTask, now.AddDate(0, 0, -1))

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: now}

	cases := []struct {
		name           string
		body           string
		wantIn, wantNo uuid.UUID
	}{
		{
			"updated_at range",
			`"modules":["vector"],"updated_at":{"after":"-7d"}`,
			recentBug, old,
		},
		{
			"created_at range",
			`"modules":["vector"],"created_at":{"after":"-7d"}`,
			recentBug, old,
		},
		{
			"negated statuses",
			`"modules":["vector"],"statuses":["closed"],"not":{"statuses":true}`,
			recentBug, recentTask,
		},
		{
			"negated priorities",
			`"modules":["vector"],"priorities":["low"],"not":{"priorities":true}`,
			recentBug, recentTask,
		},
		{
			"negated kinds",
			`"modules":["vector"],"kinds":["task"],"not":{"kinds":true}`,
			recentBug, recentTask,
		},
		{
			"negated space_ids still bounded by access",
			`"modules":["vector"],"space_ids":["` + uuid.New().String() + `"],"not":{"space_ids":true}`,
			recentBug, uuid.Nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, err := views.Resolve(ctx, orgStore(a, org.ID), v2Query(t, c.body), viewer, "", 50)
			require.NoError(t, err)
			got := resultIDs(page)
			require.True(t, got[c.wantIn], "the matching item must be returned")
			if c.wantNo != uuid.Nil {
				require.False(t, got[c.wantNo], "the excluded item must not be returned")
			}
		})
	}
}

// TestViewAggregate_VectorCountAgreesWithTheVectorList closes the other half of
// the same gap: the Vector COUNT and BREAKDOWN adapters, on their own.
//
// The existing cross-module parity test could not see this, because a count and
// a list that have both lost the same parameters agree with each other
// perfectly. Comparing the Vector count against the Vector list only works as a
// check once the list itself is known correct, which the test above establishes.
func TestViewAggregate_VectorCountAgreesWithTheVectorList(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	now := time.Now().UTC()
	for i := int32(1); i <= 6; i++ {
		id := insertItem(t, db.Pool, org.ID, space.ID, user.ID, i, "v", statusFor(i), "high", "task", nil)
		backdateItem(t, db, id, now.AddDate(0, 0, -int(i)*3))
	}

	a := adapters.NewSavedViewAdapter(db.Pool)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}, At: now}
	q := v2Query(t, `"modules":["vector"],"statuses":["closed"],"not":{"statuses":true},`+
		`"updated_at":{"after":"-20d","before":"-2d"}`)

	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, "", 200)
	require.NoError(t, err)
	res, err := views.Aggregate(ctx, orgAggStore(a, org.ID), q, viewer, views.GroupStatus)
	require.NoError(t, err)

	require.NotZero(t, len(page.Results), "the fixture must match something, or this proves nothing")
	require.Equal(t, int64(len(page.Results)), res.Total,
		"the Vector count fan-out filters differently from the Vector list fan-out")
	var summed int64
	for _, b := range res.Buckets {
		summed += b.Count
	}
	require.Equal(t, res.Total, summed, "the Vector breakdown fan-out filters differently again")
}
