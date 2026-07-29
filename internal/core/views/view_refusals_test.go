package views

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// WHAT THE SAVED-VIEW SERVICE REFUSES, AND HOW IT REFUSES IT.
//
// Two families here, and the second is the one that matters.
//
// The bounds — name required, name and description capped — are ordinary
// validation, except that they were answering 500 until P5 added
// views.ValidationError. They are asserted at the service rather than only
// through HTTP so a future refactor cannot quietly turn one back into a
// wrapped fmt.Errorf.
//
// The DISTINCTION between ErrNotFound and ErrNotOwner is the security-relevant
// one. A view the caller cannot even see must be indistinguishable from one
// that does not exist: answering 403 there tells anybody who asks whether user
// X owns a view with id Y. Only somebody who can SEE the row is told they do
// not own it. Both arms are asserted, in both directions, because a single
// arm's test passes with the other deleted.

// spaceScopedQuery names one space, so markValidity has something to check.
func spaceScopedQuery(t *testing.T, spaceID uuid.UUID) Query {
	t.Helper()
	q, err := ParseQuery([]byte(
		`{"v":1,"filter":{"modules":["beacon"],"space_ids":["` + spaceID.String() + `"]},` +
			`"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	return q
}

// A view's name is required and both text fields are bounded, and each refusal
// carries the typed error so the API answers 422 rather than 500.
func TestViewDraft_BoundsAreTypedValidationErrors(t *testing.T) {
	ctx := context.Background()
	s, a, org := svcFor(&failingStore{})

	blank := draftFor()
	blank.Name = "   "
	_, err := s.Create(ctx, org, a, blank)
	require.ErrorIs(t, err, ErrNameRequired, "whitespace is not a name")

	long := draftFor()
	long.Name = strings.Repeat("v", MaxNameLen+1)
	_, err = s.Create(ctx, org, a, long)
	var invalid ValidationError
	require.ErrorAs(t, err, &invalid,
		"a bound the caller can fix must be typed, or the handler answers 500")
	require.Contains(t, err.Error(), "at most")

	verbose := draftFor()
	verbose.Description = strings.Repeat("d", MaxDescLen+1)
	_, err = s.Create(ctx, org, a, verbose)
	require.ErrorAs(t, err, &invalid)
	require.Contains(t, err.Error(), "description")

	// The same three run on Update, which validates the merged draft rather
	// than trusting that Create already did.
	_, err = s.Update(ctx, org, uuid.New(), a, blank)
	require.ErrorIs(t, err, ErrNameRequired)
	_, err = s.Update(ctx, org, uuid.New(), a, long)
	require.ErrorAs(t, err, &invalid)
}

// An Update that omits visibility inherits the row's own, rather than
// defaulting to private. A PATCH that silently un-shared an org-wide view would
// remove other people's access as a side effect of a rename.
func TestViewUpdate_OmittedVisibilityInheritsTheExistingOne(t *testing.T) {
	me := uuid.New()
	store := &failingStore{}
	store.view = ownedView(me)
	store.view.Visibility = VisibilityOrg
	s := NewService(store, failingResults{}, failingAggregates{})

	d := draftFor()
	d.Visibility = ""
	updated, err := s.Update(context.Background(), uuid.New(), uuid.New(),
		Actor{UserID: me}, d)
	require.NoError(t, err)
	require.Equal(t, VisibilityOrg, updated.Visibility, "a rename must not un-share the view")
}

// THE INHERITANCE IS HALF DONE, AND THIS PINS THE GAP RATHER THAN THE INTENT.
//
// Update inherits `existing.Visibility` when the request omits it, but never
// inherits `existing.VisibilityTeamID`. For the one visibility that carries a
// payload, the inheritance therefore cannot succeed: the merged draft is
// "team" with no team, which Normalise refuses.
//
// So a caller who PATCHes a team-shared view with only a new name is answered
// 422 "a team-visible view must name a team" — a message about a field they
// did not send, describing state they did not create. It is not a disclosure
// and it is not data loss; it is P4 behaviour outside this phase's scope, and
// the fix direction (inherit the team alongside the visibility) is a decision
// about PATCH semantics rather than an obvious repair. Recorded in
// docs/known-issues.md and reported rather than changed here.
//
// This test asserts what the code DOES. If somebody fixes it, this fails, and
// the fix is to invert it — which is the point of pinning it.
func TestViewUpdate_OmittingTheTeamOnATeamViewIsRefused(t *testing.T) {
	me, team := uuid.New(), uuid.New()
	store := &failingStore{}
	store.view = ownedView(me)
	store.view.Visibility = VisibilityTeam
	store.view.VisibilityTeamID = &team
	s := NewService(store, failingResults{}, failingAggregates{})

	d := draftFor()
	d.Visibility, d.VisibilityTeamID = "", nil
	_, err := s.Update(context.Background(), uuid.New(), uuid.New(),
		Actor{UserID: me, EffectiveTeamIDs: []uuid.UUID{team}}, d)
	require.Error(t, err, "the inherited visibility has no inherited team to go with it")
	require.Contains(t, err.Error(), "must name a team")

	// Naming the team explicitly works, which is what makes the above a gap in
	// the inheritance rather than a rule about team views.
	d.VisibilityTeamID = &team
	updated, err := s.Update(context.Background(), uuid.New(), uuid.New(),
		Actor{UserID: me, EffectiveTeamIDs: []uuid.UUID{team}}, d)
	require.NoError(t, err)
	require.Equal(t, VisibilityTeam, updated.Visibility)
	require.Equal(t, &team, updated.VisibilityTeamID)
}

// A non-owner who can SEE the view is told they do not own it. A non-owner who
// CANNOT see it is told it does not exist — the two answers are different on
// purpose, and each arm is asserted separately.
func TestViewWrites_NotFoundAndNotOwnerAreDistinguished(t *testing.T) {
	ctx := context.Background()
	owner, stranger := uuid.New(), uuid.New()

	t.Run("a private view somebody else owns does not exist", func(t *testing.T) {
		store := &failingStore{deleted: 1}
		store.view = ownedView(owner) // VisibilityPrivate
		s := NewService(store, failingResults{}, failingAggregates{})
		a := Actor{UserID: stranger}

		_, err := s.Update(ctx, uuid.New(), uuid.New(), a, draftFor())
		require.ErrorIs(t, err, ErrNotFound,
			"403 here would answer \"does that person own a view with this id\"")
		require.ErrorIs(t, s.Delete(ctx, uuid.New(), uuid.New(), a), ErrNotFound)
		_, err = s.Get(ctx, uuid.New(), uuid.New(), a)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("an org-wide view somebody else owns is visible but not editable", func(t *testing.T) {
		store := &failingStore{deleted: 1}
		store.view = ownedView(owner)
		store.view.Visibility = VisibilityOrg
		s := NewService(store, failingResults{}, failingAggregates{})
		a := Actor{UserID: stranger}

		_, err := s.Update(ctx, uuid.New(), uuid.New(), a, draftFor())
		require.ErrorIs(t, err, ErrNotOwner, "they can see it, so hiding it would be a lie")
		require.ErrorIs(t, s.Delete(ctx, uuid.New(), uuid.New(), a), ErrNotOwner)

		// And they can read it, which is what makes the two answers different.
		got, err := s.Get(ctx, uuid.New(), uuid.New(), a)
		require.NoError(t, err)
		require.Equal(t, store.view.ID, got.ID)
	})

	t.Run("an org admin bypasses the owner check on both", func(t *testing.T) {
		store := &failingStore{deleted: 1}
		store.view = ownedView(owner)
		s := NewService(store, failingResults{}, failingAggregates{})
		admin := Actor{UserID: stranger, IsOrgAdmin: true}

		_, err := s.Update(ctx, uuid.New(), uuid.New(), admin, draftFor())
		require.NoError(t, err)
		require.NoError(t, s.Delete(ctx, uuid.New(), uuid.New(), admin))
	})
}

// Results refuses a view the caller cannot see BEFORE it resolves anything.
// Running the fan-out first and filtering after would put another person's
// filter document to work against this viewer's access.
func TestViewResults_RefusesAViewTheCallerCannotSee(t *testing.T) {
	store := &failingStore{}
	store.view = ownedView(uuid.New())
	s := NewService(store, failingResults{failTickets: true}, failingAggregates{})

	_, err := s.Results(context.Background(), uuid.New(), uuid.New(),
		Actor{UserID: uuid.New()}, Viewer{}, "", 0)
	require.ErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, errStore,
		"the refusal happens before the fan-out — a store error here means it ran anyway")
}

// The scope check runs on every path that hands a view out, not only on the
// list: Get and ByIDs mark validity too, and a failure there fails the request
// rather than reporting every view as fine.
func TestViewValidity_FailsGetAndByIDsToo(t *testing.T) {
	ctx := context.Background()
	me := uuid.New()
	space := uuid.New()

	store := &failingStore{failLiveSpaces: true}
	store.view = View{ID: uuid.New(), OwnerID: me, Name: "Scoped",
		Query: spaceScopedQuery(t, space), Visibility: VisibilityPrivate}
	s := NewService(store, failingResults{}, failingAggregates{})

	_, err := s.Get(ctx, uuid.New(), uuid.New(), Actor{UserID: me})
	require.ErrorIs(t, err, errStore)

	_, err = s.ByIDs(ctx, uuid.New(), []uuid.UUID{uuid.New()})
	require.ErrorIs(t, err, errStore)
}

// ByIDs asks nothing when it is asked for nothing — a dashboard whose gadgets
// name no view must not cost a query.
func TestViewByIDs_AnEmptyRequestCostsNoQuery(t *testing.T) {
	store := &failingStore{failGetMany: true}
	store.view = ownedView(uuid.New())
	s := NewService(store, failingResults{}, failingAggregates{})

	rows, err := s.ByIDs(context.Background(), uuid.New(), nil)
	require.NoError(t, err, "the store must not have been consulted at all")
	require.Empty(t, rows)
}

// ADR-0009 case C1, both halves. A view whose team was deleted reports the team
// as the reason; a view every one of whose spaces is gone reports the scope.
// Some spaces gone is a NARROWER view, not a broken one — reporting that as
// invalid would cry wolf on an ordinary space deletion.
func TestViewValidity_DegradationReasonsAreDistinct(t *testing.T) {
	ctx := context.Background()
	me := uuid.New()
	gone, alive := uuid.New(), uuid.New()

	orphanTeam := &View{ID: uuid.New(), OwnerID: me, Visibility: VisibilityTeam}
	allGone := &View{ID: uuid.New(), OwnerID: me, Visibility: VisibilityPrivate,
		Query: spaceScopedQuery(t, gone)}

	partial, err := ParseQuery([]byte(
		`{"v":1,"filter":{"modules":["beacon"],"space_ids":["` + gone.String() + `","` +
			alive.String() + `"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	someGone := &View{ID: uuid.New(), OwnerID: me, Visibility: VisibilityPrivate, Query: partial}

	s := NewService(&liveSpacesStore{live: []uuid.UUID{alive}}, failingResults{}, failingAggregates{})
	require.NoError(t, s.markValidity(ctx, uuid.New(), []*View{orphanTeam, allGone, someGone}))

	require.Contains(t, orphanTeam.InvalidReason, "team")
	require.Contains(t, allGone.InvalidReason, "space")
	require.Empty(t, someGone.InvalidReason,
		"a view that lost one of two spaces still returns rows — calling it invalid cries wolf")
}

// liveSpacesStore answers LiveSpaceIDs with a fixed set and panics on anything
// else, so a test that accidentally reaches another call fails loudly.
type liveSpacesStore struct {
	failingStore
	live []uuid.UUID
}

func (l *liveSpacesStore) LiveSpaceIDs(context.Context, uuid.UUID, []uuid.UUID) ([]uuid.UUID, error) {
	return l.live, nil
}
