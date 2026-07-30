package search

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeStore records which module branches were asked for and replays canned
// rows. It deliberately does NOT imitate PostgreSQL's filtering: everything
// about whether the access predicate is correct is an integration-test
// question, because a double that answers from Go cannot be wrong in the same
// way the SQL can. What it CAN prove is what this package owns — which
// branches ran, how the halves merged, what the cursor said, and what the
// response was allowed to disclose.
type fakeStore struct {
	parsed    string
	parseErr  error
	tagID     uuid.UUID
	tagErr    error
	pages     []Result
	tickets   []Result
	items     []Result
	calls     []Module
	lastParam FanoutParams
}

func (f *fakeStore) ParsedQuery(_ context.Context, text string) (string, error) {
	if f.parseErr != nil {
		return "", f.parseErr
	}
	if f.parsed != "" {
		return f.parsed, nil
	}
	return text, nil // stand-in: non-empty in, non-empty out
}

func (f *fakeStore) ResolveTagSlug(context.Context, uuid.UUID, string) (uuid.UUID, error) {
	return f.tagID, f.tagErr
}

func (f *fakeStore) SearchPages(_ context.Context, p FanoutParams) ([]Result, error) {
	f.calls = append(f.calls, ModuleCodex)
	f.lastParam = p
	return f.pages, nil
}

func (f *fakeStore) SearchTickets(_ context.Context, p FanoutParams) ([]Result, error) {
	f.calls = append(f.calls, ModuleBeacon)
	f.lastParam = p
	return f.tickets, nil
}

func (f *fakeStore) SearchProjectItems(_ context.Context, p FanoutParams) ([]Result, error) {
	f.calls = append(f.calls, ModuleVector)
	f.lastParam = p
	return f.items, nil
}

func readableReq(spaces ...uuid.UUID) Request {
	return Request{OrgID: uuid.New(), Raw: "widget", ReadableSpaceIDs: spaces}
}

func hit(m Module, key string, space uuid.UUID) Result {
	sp := space
	return Result{Module: m, ID: uuid.New(), Title: "t", SortKey: key, SpaceID: &sp,
		SpaceKey: "SP", SpaceName: "Space Name"}
}

// TestSearch_ShareOnlyHitDisclosesNoContainer is the matrix-case-16 guard.
//
// A share-only hit reached the viewer through a share on the ENTITY. The viewer
// cannot enter its space, so the response must not name the space — not its key,
// not its name, not even its id. Spec §7 asks for results "tagged with module
// and owning team"; where that wording and the enforced matrix case disagree,
// the matrix case wins, and a share-only hit is rendered the way /shared already
// renders one.
//
// Fails-before: relax redactSharedContainers to leave the fields in place (or to
// key off "was it in the shared id list" rather than "is its space readable")
// and this fails on the share-only row.
//
// The positive half is in the same test on purpose. A test that only asserted
// absence would pass with the whole fan-out returning nothing.
func TestSearch_ShareOnlyHitDisclosesNoContainer(t *testing.T) {
	readable, hidden := uuid.New(), uuid.New()
	mine := hit(ModuleCodex, "0000900000", readable)
	shared := hit(ModuleCodex, "0000800000", hidden)

	st := &fakeStore{pages: []Result{mine, shared}}
	req := readableReq(readable)
	req.SharedPageIDs = []uuid.UUID{shared.ID}

	got, err := NewService(st).Search(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, got.Results, 2, "both hits are returned — only the disclosure differs")

	byID := map[uuid.UUID]Result{}
	for _, r := range got.Results {
		byID[r.ID] = r
	}

	// The readable one keeps its container. Without this the test would pass
	// against a serializer that stripped every row.
	own := byID[mine.ID]
	require.Equal(t, OriginSpace, own.Origin)
	require.NotNil(t, own.SpaceID)
	require.Equal(t, "SP", own.SpaceKey)
	require.Equal(t, "Space Name", own.SpaceName)

	// The share-only one discloses nothing about where it lives.
	via := byID[shared.ID]
	require.Equal(t, OriginShare, via.Origin)
	require.Nil(t, via.SpaceID, "a share-only hit must not carry its space id")
	require.Empty(t, via.SpaceKey, "a share-only hit must not carry its space key")
	require.Empty(t, via.SpaceName, "a share-only hit must not name the space")
}

// TestSearch_SharedEntityInAReadableSpaceKeepsItsContainer pins the rule to the
// SPACE rather than to share membership. A page can be both directly shared and
// sitting in a space the viewer can read; the container is already theirs to
// see, and blanking it would be a needless regression in the common case.
func TestSearch_SharedEntityInAReadableSpaceKeepsItsContainer(t *testing.T) {
	readable := uuid.New()
	both := hit(ModuleCodex, "0000900000", readable)

	st := &fakeStore{pages: []Result{both}}
	req := readableReq(readable)
	req.SharedPageIDs = []uuid.UUID{both.ID}

	got, err := NewService(st).Search(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, got.Results, 1)
	require.Equal(t, OriginSpace, got.Results[0].Origin)
	require.Equal(t, "SP", got.Results[0].SpaceKey)
}

// TestSearch_MergeOrdersByRankThenID proves the three halves interleave by the
// same comparison the SQL used. Each half arrives already sorted; a merge that
// concatenated them, or compared the id as bytes rather than as its string form,
// would produce a different order here.
func TestSearch_MergeOrdersByRankThenID(t *testing.T) {
	sp := uuid.New()
	st := &fakeStore{
		pages:   []Result{hit(ModuleCodex, "0000900000", sp), hit(ModuleCodex, "0000100000", sp)},
		tickets: []Result{hit(ModuleBeacon, "0000800000", sp)},
		items:   []Result{hit(ModuleVector, "0000950000", sp), hit(ModuleVector, "0000500000", sp)},
	}
	got, err := NewService(st).Search(context.Background(), readableReq(sp))
	require.NoError(t, err)

	keys := make([]string, 0, len(got.Results))
	for _, r := range got.Results {
		keys = append(keys, r.SortKey)
	}
	require.Equal(t,
		[]string{"0000950000", "0000900000", "0000800000", "0000500000", "0000100000"},
		keys, "merged strictly by descending sort key across modules")
}

// TestSearch_TypeFilterSkipsWholeBranches proves a narrowed query does not issue
// the other modules' queries at all. Post-filtering would return the same rows
// while paying for every branch — and the branch never issued is the one that
// cannot leak.
func TestSearch_TypeFilterSkipsWholeBranches(t *testing.T) {
	sp := uuid.New()
	for _, tc := range []struct {
		raw  string
		want []Module
	}{
		{"widget", []Module{ModuleCodex, ModuleBeacon, ModuleVector}},
		{"type:beacon widget", []Module{ModuleBeacon}},
		{"type:page type:item widget", []Module{ModuleCodex, ModuleVector}},
		{"tag:runbooks widget", []Module{ModuleCodex}},
	} {
		st := &fakeStore{}
		req := readableReq(sp)
		req.Raw = tc.raw
		_, err := NewService(st).Search(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, tc.want, st.calls, "branches issued for %q", tc.raw)
	}
}

// TestSearch_DistinctEmptyStates covers the three ways a search comes back with
// nothing. Collapsing them is the S2-of-#64 lesson, and it has a sharper edge
// here: an empty tsquery matches nothing, so if it were reported as an ordinary
// empty page then every "the unreadable row does not appear" assertion would
// pass vacuously for stopword-only input, with the access filter deleted.
func TestSearch_DistinctEmptyStates(t *testing.T) {
	sp := uuid.New()

	t.Run("no readable scope short-circuits before the query is parsed", func(t *testing.T) {
		st := &fakeStore{}
		got, err := NewService(st).Search(context.Background(), Request{OrgID: uuid.New(), Raw: "widget"})
		require.NoError(t, err)
		require.Equal(t, StateNoReadableScope, got.State)
		require.Empty(t, st.calls, "the fan-out must not run with an empty access set")
	})

	t.Run("empty tsquery is its own state", func(t *testing.T) {
		st := &fakeStore{parsed: "  "}
		req := readableReq(sp)
		req.Raw = "the of a"
		got, err := NewService(st).Search(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, StateNoSearchableTerms, got.State)
		require.Empty(t, st.calls, "nothing could match, so nothing is queried")
	})

	t.Run("ran and matched nothing is StateOK", func(t *testing.T) {
		st := &fakeStore{}
		got, err := NewService(st).Search(context.Background(), readableReq(sp))
		require.NoError(t, err)
		require.Equal(t, StateOK, got.State)
		require.Empty(t, got.Results)
		require.Len(t, st.calls, 3)
	})

	t.Run("an unused tag is StateOK with no results, not an error", func(t *testing.T) {
		st := &fakeStore{tagErr: ErrTagNotFound}
		req := readableReq(sp)
		req.Raw = "tag:nobodyusesthis widget"
		got, err := NewService(st).Search(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, StateOK, got.State)
		require.Empty(t, got.Results)
		require.Empty(t, st.calls)
	})
}

// TestSearch_ShareOnlyScopeStillSearches guards the access short-circuit against
// being written as "readable spaces only". A viewer who can read no space but
// holds one share must still get a search.
func TestSearch_ShareOnlyScopeStillSearches(t *testing.T) {
	hidden := uuid.New()
	shared := hit(ModuleCodex, "0000900000", hidden)
	st := &fakeStore{pages: []Result{shared}}

	got, err := NewService(st).Search(context.Background(), Request{
		OrgID: uuid.New(), Raw: "widget", SharedPageIDs: []uuid.UUID{shared.ID},
	})
	require.NoError(t, err)
	require.Equal(t, StateOK, got.State)
	require.Len(t, got.Results, 1)
	require.Equal(t, OriginShare, got.Results[0].Origin)

	// And a subtree share alone is scope too.
	st2 := &fakeStore{pages: []Result{shared}}
	got2, err := NewService(st2).Search(context.Background(), Request{
		OrgID: uuid.New(), Raw: "widget",
		SubtreeSpaceIDs: []uuid.UUID{hidden}, SubtreePatterns: []string{"a.%"},
	})
	require.NoError(t, err)
	require.Equal(t, StateOK, got2.State)
}

// TestSearch_CursorPagesWithoutGapOrRepeat walks two pages and asserts the join
// is seamless. The cursor is minted from the LAST RETURNED row, never from the
// limit+1 probe row — minting from the probe skips exactly one result per page,
// and the gap is invisible unless the pages are compared.
func TestSearch_CursorPagesWithoutGapOrRepeat(t *testing.T) {
	sp := uuid.New()
	all := []Result{
		hit(ModuleCodex, "0000900000", sp),
		hit(ModuleCodex, "0000800000", sp),
		hit(ModuleCodex, "0000700000", sp),
	}
	st := &fakeStore{pages: all}
	req := readableReq(sp)
	req.Limit = 2

	first, err := NewService(st).Search(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, first.Results, 2)
	require.NotEmpty(t, first.NextCursor, "a truncated page must issue a cursor")

	// The cursor names the last row actually returned.
	pos, err := decodeCursor(first.NextCursor)
	require.NoError(t, err)
	require.Equal(t, first.Results[1].SortKey, pos.Key)
	require.Equal(t, first.Results[1].ID, pos.ID)

	// Second page: the store would have applied the cursor, so hand back the tail.
	st.pages = all[2:]
	req.Cursor = first.NextCursor
	second, err := NewService(st).Search(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, second.Results, 1)
	require.Empty(t, second.NextCursor, "a short page issues no cursor")
	require.Equal(t, all[2].ID, second.Results[0].ID)

	// The cursor reached the store rather than being silently dropped.
	require.Equal(t, pos.Key, st.lastParam.CursorKey)
	require.Equal(t, pos.ID, st.lastParam.CursorID)
}

// TestSearch_FanoutAsksForOneMoreThanThePage pins the limit+1 probe. Asking for
// exactly the page size makes "there are more" unknowable, and the surface then
// shows a truncated list indistinguishable from a complete one.
func TestSearch_FanoutAsksForOneMoreThanThePage(t *testing.T) {
	sp := uuid.New()
	st := &fakeStore{}
	req := readableReq(sp)
	req.Limit = 7
	_, err := NewService(st).Search(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, int32(8), st.lastParam.Limit)
}

// TestSearch_LimitIsClamped stops a caller asking for an unbounded page.
func TestSearch_LimitIsClamped(t *testing.T) {
	sp := uuid.New()
	for _, tc := range []struct{ in, want int32 }{
		{0, DefaultPageSize + 1}, {-5, DefaultPageSize + 1},
		{10, 11}, {MaxPageSize, MaxPageSize + 1}, {9999, MaxPageSize + 1},
	} {
		st := &fakeStore{}
		req := readableReq(sp)
		req.Limit = int(tc.in)
		_, err := NewService(st).Search(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, tc.want, st.lastParam.Limit, "limit %d", tc.in)
	}
}

// TestSearch_BadCursorIsRejected proves a cursor this build never issued is an
// error rather than a silently ignored empty position — which would quietly
// serve page one forever.
func TestSearch_BadCursorIsRejected(t *testing.T) {
	sp := uuid.New()
	bads := []string{
		"!!!not-base64!!!", // not base64url at all
		base64.RawURLEncoding.EncodeToString([]byte("no-separator-here")),
		base64.RawURLEncoding.EncodeToString([]byte("0000900000\x00not-a-uuid")),
	}
	for _, bad := range bads {
		req := readableReq(sp)
		req.Cursor = bad
		_, err := NewService(&fakeStore{}).Search(context.Background(), req)
		require.ErrorIs(t, err, ErrBadCursor, "cursor %q", bad)
	}

	// A cursor this build DID issue round-trips, so the test above is rejecting
	// malformed input rather than rejecting everything.
	good := encodeCursor(cursorPos{Key: "0000900000", ID: uuid.New()})
	req := readableReq(sp)
	req.Cursor = good
	_, err := NewService(&fakeStore{}).Search(context.Background(), req)
	require.NoError(t, err)
}

// TestSearch_ParseErrorPropagates keeps a database failure from being reported
// as "no searchable terms", which would read to a user as their own fault.
func TestSearch_ParseErrorPropagates(t *testing.T) {
	sp := uuid.New()
	st := &fakeStore{parseErr: errors.New("connection reset")}
	_, err := NewService(st).Search(context.Background(), readableReq(sp))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrBadCursor)
}
