package dashboards

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// The service's own logic against fakes: what a layout write accepts, what a
// gadget resolves to for a given viewer, and that Home seeds exactly once.
// The storage half is covered against real PostgreSQL in
// internal/db/adapters/dashboards_integration_test.go.

type fakeStore struct {
	dashboards map[uuid.UUID]Dashboard
	gadgets    map[uuid.UUID][]Gadget
	defaults   map[string]uuid.UUID // ownerID+module -> dashboard id
	// seedCalls counts CreateStarter, so a test can assert seeding happened
	// once and not twice.
	seedCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		dashboards: map[uuid.UUID]Dashboard{},
		gadgets:    map[uuid.UUID][]Gadget{},
		defaults:   map[string]uuid.UUID{},
	}
}

func defKey(owner uuid.UUID, module string) string { return owner.String() + "|" + module }

func (f *fakeStore) Create(_ context.Context, d Dashboard) (Dashboard, error) {
	d.ID = uuid.New()
	f.dashboards[d.ID] = d
	if d.IsDefault {
		f.defaults[defKey(d.OwnerID, string(d.Module))] = d.ID
	}
	return d, nil
}

func (f *fakeStore) Get(_ context.Context, _, id uuid.UUID) (Dashboard, error) {
	d, ok := f.dashboards[id]
	if !ok {
		return Dashboard{}, ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) Update(_ context.Context, d Dashboard) (Dashboard, error) {
	if _, ok := f.dashboards[d.ID]; !ok {
		return Dashboard{}, ErrNotFound
	}
	f.dashboards[d.ID] = d
	return d, nil
}

func (f *fakeStore) SoftDelete(_ context.Context, _, id uuid.UUID) (int64, error) {
	if _, ok := f.dashboards[id]; !ok {
		return 0, nil
	}
	delete(f.dashboards, id)
	return 1, nil
}

func (f *fakeStore) ListForViewer(_ context.Context, _, viewerID uuid.UUID, teams []uuid.UUID, module string) ([]Dashboard, error) {
	act := views.Actor{UserID: viewerID, EffectiveTeamIDs: teams}
	out := []Dashboard{}
	for _, d := range f.dashboards {
		if module != "" && string(d.Module) != module {
			continue
		}
		if d.CanSee(act) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeStore) ListGadgets(_ context.Context, dashboardID uuid.UUID) ([]Gadget, error) {
	return f.gadgets[dashboardID], nil
}

func (f *fakeStore) ReplaceGadgets(_ context.Context, dashboardID uuid.UUID, gs []Gadget) ([]Gadget, error) {
	out := make([]Gadget, 0, len(gs))
	for _, g := range gs {
		g.ID = uuid.New()
		g.DashboardID = dashboardID
		out = append(out, g)
	}
	f.gadgets[dashboardID] = out
	return out, nil
}

func (f *fakeStore) DefaultFor(_ context.Context, _, ownerID uuid.UUID, module string) (Dashboard, error) {
	id, ok := f.defaults[defKey(ownerID, module)]
	if !ok {
		return Dashboard{}, ErrNotFound
	}
	return f.dashboards[id], nil
}

func (f *fakeStore) CreateStarter(_ context.Context, d Dashboard, gs []Gadget) (bool, error) {
	f.seedCalls++
	key := defKey(d.OwnerID, string(d.Module))
	if _, exists := f.defaults[key]; exists {
		return false, nil // the ON CONFLICT DO NOTHING branch
	}
	d.ID = uuid.New()
	f.dashboards[d.ID] = d
	f.defaults[key] = d.ID
	f.gadgets[d.ID] = gs
	return true, nil
}

type fakeViews struct {
	byID map[uuid.UUID]views.View
	// calls counts ByIDs, so a test can prove a dashboard resolves its views in
	// ONE lookup regardless of gadget count (spec §2.5 case 23).
	calls int
}

func (f *fakeViews) ByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]views.View, error) {
	f.calls++
	out := []views.View{}
	for _, id := range ids {
		if v, ok := f.byID[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func orgView(t *testing.T, owner uuid.UUID, name string) views.View {
	t.Helper()
	q, err := views.ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	return views.View{ID: uuid.New(), OwnerID: owner, Name: name, Query: q, Visibility: views.VisibilityOrg}
}

func harness(t *testing.T) (*Service, *fakeStore, *fakeViews, views.Actor, uuid.UUID) {
	t.Helper()
	store, vw := newFakeStore(), &fakeViews{byID: map[uuid.UUID]views.View{}}
	me := uuid.New()
	return NewService(store, vw), store, vw, views.Actor{UserID: me}, uuid.New()
}

func mustCreate(t *testing.T, s *Service, orgID uuid.UUID, a views.Actor, module Module) Dashboard {
	t.Helper()
	d, err := s.Create(context.Background(), orgID, a, Draft{Name: "Board health", Module: module})
	require.NoError(t, err)
	return d
}

// ── Draft validation ────────────────────────────────────────────────────────

func TestCreate_RequiresANameAndAKnownModule(t *testing.T) {
	s, _, _, me, org := harness(t)
	ctx := context.Background()

	_, err := s.Create(ctx, org, me, Draft{Name: "   "})
	require.ErrorIs(t, err, ErrNameRequired, "whitespace is not a name")

	_, err = s.Create(ctx, org, me, Draft{Name: "x", Module: "codex"})
	require.ErrorIs(t, err, ErrModuleInvalid,
		"there is no Codex dashboard module — a saved view cannot query pages")
}

func TestCreate_DefaultsToAPrivateHomeDashboard(t *testing.T) {
	s, _, _, me, org := harness(t)

	d, err := s.Create(context.Background(), org, me, Draft{Name: "Mine"})
	require.NoError(t, err)
	require.Equal(t, ModuleHome, d.Module)
	require.Equal(t, views.VisibilityPrivate, d.Visibility)
	require.False(t, d.IsDefault)
	require.True(t, d.IsValid())
}

// The tri-state on is_default. A PATCH that omits it must leave the flag
// alone; a bare bool would clear somebody's default on every unrelated edit,
// which is the absent-versus-null collapse that wiped every item's due_at.
func TestUpdate_OmittingIsDefaultLeavesItAlone(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()

	yes := true
	d, err := s.Create(ctx, org, me, Draft{Name: "Mine", IsDefault: &yes})
	require.NoError(t, err)
	require.True(t, d.IsDefault)

	updated, err := s.Update(ctx, org, d.ID, me, Draft{Name: "Renamed"})
	require.NoError(t, err)
	require.True(t, updated.IsDefault, "a rename must not stand a dashboard down from default")
	require.True(t, store.dashboards[d.ID].IsDefault)

	no := false
	updated, err = s.Update(ctx, org, d.ID, me, Draft{Name: "Renamed", IsDefault: &no})
	require.NoError(t, err)
	require.False(t, updated.IsDefault, "sending it explicitly does change it")
}

// ── Audience ────────────────────────────────────────────────────────────────

func TestGetAndUpdate_APrivateDashboardIs404ToEveryoneElse(t *testing.T) {
	s, _, _, me, org := harness(t)
	ctx := context.Background()
	d := mustCreate(t, s, org, me, ModuleHome)

	stranger := views.Actor{UserID: uuid.New()}
	_, err := s.Get(ctx, org, d.ID, stranger)
	require.ErrorIs(t, err, ErrNotFound)

	// 404 rather than 403 on a write too: a 403 would confirm that somebody
	// else's private dashboard exists.
	_, err = s.Update(ctx, org, d.ID, stranger, Draft{Name: "Theirs"})
	require.ErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrNotOwner)
}

// Seeing is not editing. Somebody who reaches a shared dashboard gets 403 on a
// write — the audience decides who may look, ownership decides who may change.
func TestUpdate_ASharedDashboardIs403ToANonOwner(t *testing.T) {
	s, _, _, me, org := harness(t)
	ctx := context.Background()

	d, err := s.Create(ctx, org, me, Draft{Name: "Team board", Visibility: views.VisibilityOrg})
	require.NoError(t, err)

	other := views.Actor{UserID: uuid.New()}
	_, err = s.Get(ctx, org, d.ID, other)
	require.NoError(t, err, "an org-audience dashboard is readable")

	_, err = s.Update(ctx, org, d.ID, other, Draft{Name: "Hijacked"})
	require.ErrorIs(t, err, ErrNotOwner)

	_, err = s.SetGadgets(ctx, org, d.ID, other, nil)
	require.ErrorIs(t, err, ErrNotOwner, "arranging somebody else's dashboard is changing their work")
}

// THE TEAM INHERITS ALONGSIDE THE VISIBILITY, AND STOPS INHERITING THE MOMENT
// THE CALLER CHANGES THE AUDIENCE.
//
// Update inherited `existing.Visibility` and `existing.Module` but never
// `existing.VisibilityTeamID`, so for the one visibility that carries a payload
// the inheritance could not succeed: the merged draft was "team" with no team,
// which the shared views.Audience.Normalise refuses. A caller who PATCHed a
// team-shared dashboard with only a new name was answered 422 "a team-visible
// dashboard must name a team" — about a field they had not sent.
//
// This is known-issues #26, and the third instance of the same half-merge
// shape; #91 closed the saved-views instance (#25) with this exact pattern.
// Both models delegate to one Audience rule, so they inherit its defects too.
//
// WHAT EACH CASE ACTUALLY PINS, mutation-tested rather than assumed:
//
//   - Remove the team inheritance and the first two subtests fail with the
//     reported 422. They are the regression test.
//   - Remove the `d.Visibility == existing.Visibility` guard and NOTHING here
//     fails, because Normalise independently nils the team id for a private or
//     org audience. The last two subtests therefore pin Normalise's cleanup,
//     not the guard.
//
// The guard is kept anyway, and deliberately: it means this merge never
// fabricates a pair the caller did not state, rather than relying on a
// downstream function to tidy one away. That is a weaker claim than "each half
// is load-bearing", and it is the true one.
func TestDashboardUpdate_TheTeamInheritsWithTheVisibility(t *testing.T) {
	ctx := context.Background()
	team, otherTeam := uuid.New(), uuid.New()

	// A team-shared dashboard owned by an actor who belongs to both teams, so
	// Normalise's membership check never masks what is under test.
	teamDashboard := func(t *testing.T) (*Service, *fakeStore, views.Actor, uuid.UUID, Dashboard) {
		t.Helper()
		s, store, _, me, org := harness(t)
		me.EffectiveTeamIDs = []uuid.UUID{team, otherTeam}
		d, err := s.Create(ctx, org, me, Draft{
			Name:             "Team board",
			Visibility:       views.VisibilityTeam,
			VisibilityTeamID: &team,
		})
		require.NoError(t, err)
		require.Equal(t, &team, d.VisibilityTeamID)
		return s, store, me, org, d
	}

	t.Run("a rename that names no audience keeps the team share", func(t *testing.T) {
		s, store, me, org, d := teamDashboard(t)

		updated, err := s.Update(ctx, org, d.ID, me, Draft{Name: "Renamed"})
		require.NoError(t, err, "a rename must not have to restate the audience")
		require.Equal(t, "Renamed", updated.Name)
		require.Equal(t, views.VisibilityTeam, updated.Visibility)
		require.Equal(t, &team, updated.VisibilityTeamID,
			"the team has to inherit with the visibility, or the merge is half done")
		require.Equal(t, &team, store.dashboards[d.ID].VisibilityTeamID,
			"and it has to be what was written, not only what was returned")
	})

	t.Run("restating the same audience inherits the team too", func(t *testing.T) {
		s, _, me, org, d := teamDashboard(t)

		updated, err := s.Update(ctx, org, d.ID, me, Draft{
			Name: "Team board", Visibility: views.VisibilityTeam,
		})
		require.NoError(t, err, "the audience is unchanged, so the team is unchanged")
		require.Equal(t, &team, updated.VisibilityTeamID)
	})

	t.Run("moving to org clears the team rather than inheriting it", func(t *testing.T) {
		s, _, me, org, d := teamDashboard(t)

		updated, err := s.Update(ctx, org, d.ID, me, Draft{
			Name: "Team board", Visibility: views.VisibilityOrg,
		})
		require.NoError(t, err)
		require.Equal(t, views.VisibilityOrg, updated.Visibility)
		require.Nil(t, updated.VisibilityTeamID,
			"a widened dashboard carrying its old team id is a lie the next reader has to interpret")
	})

	t.Run("an explicit team wins over the inherited one", func(t *testing.T) {
		s, _, me, org, d := teamDashboard(t)

		updated, err := s.Update(ctx, org, d.ID, me, Draft{
			Name: "Team board", Visibility: views.VisibilityTeam, VisibilityTeamID: &otherTeam,
		})
		require.NoError(t, err)
		require.Equal(t, &otherTeam, updated.VisibilityTeamID,
			"inheritance fills a gap; it never overrides what the caller sent")
	})
}

// Moving a dashboard TO a team audience still has to name the team. Nothing is
// inherited here because there is nothing to inherit: the row's own audience is
// private, so the caller is stating a new pair and has to state all of it.
//
// Asserted separately so the inheritance above cannot quietly swallow it — a
// fix that inherited unconditionally would widen a hole rather than close one.
func TestDashboardUpdate_MovingToATeamAudienceStillNamesTheTeam(t *testing.T) {
	ctx := context.Background()
	s, _, _, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome) // private, no team

	_, err := s.Update(ctx, org, d.ID, me, Draft{
		Name: "Now shared", Visibility: views.VisibilityTeam,
	})
	require.ErrorIs(t, err, views.ErrTeamRequired,
		"a move to a team audience states a new pair and has to state all of it")
}

func TestList_FiltersByModuleAndRefusesAnUnknownOne(t *testing.T) {
	s, _, _, me, org := harness(t)
	ctx := context.Background()
	mustCreate(t, s, org, me, ModuleHome)
	mustCreate(t, s, org, me, ModuleVector)

	all, err := s.List(ctx, org, me, "")
	require.NoError(t, err)
	require.Len(t, all, 2)

	vector, err := s.List(ctx, org, me, ModuleVector)
	require.NoError(t, err)
	require.Len(t, vector, 1)
	require.Equal(t, ModuleVector, vector[0].Module)

	_, err = s.List(ctx, org, me, "codex")
	require.ErrorIs(t, err, ErrModuleInvalid)
}

// ── Layout writes ───────────────────────────────────────────────────────────

func TestSetGadgets_NumbersPositionsFromTheArrayOrder(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()
	d := mustCreate(t, s, org, me, ModuleHome)

	detail, err := s.SetGadgets(ctx, org, d.ID, me, []GadgetDraft{
		{Key: GadgetNote, Config: []byte(`{"body":"first"}`)},
		{Key: GadgetMyWork},
		{Key: GadgetRecentWork},
	})
	require.NoError(t, err)
	require.Len(t, detail.Gadgets, 3)
	for i, g := range detail.Gadgets {
		require.Equal(t, int32(i), g.Position,
			"positions are dense and server-assigned, so gaps and duplicates are structurally impossible")
	}
	require.Len(t, store.gadgets[d.ID], 3)
}

func TestSetGadgets_ReplacesTheWholeCollection(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()
	d := mustCreate(t, s, org, me, ModuleHome)

	_, err := s.SetGadgets(ctx, org, d.ID, me, []GadgetDraft{{Key: GadgetMyWork}, {Key: GadgetRecentWork}})
	require.NoError(t, err)
	_, err = s.SetGadgets(ctx, org, d.ID, me, []GadgetDraft{{Key: GadgetRecentWork}})
	require.NoError(t, err)

	require.Len(t, store.gadgets[d.ID], 1, "a layout write is a replacement, never a merge")
	require.Equal(t, string(GadgetRecentWork), store.gadgets[d.ID][0].Key)

	// An empty collection is a legal layout: clearing a dashboard is
	// something a person may do.
	_, err = s.SetGadgets(ctx, org, d.ID, me, nil)
	require.NoError(t, err)
	require.Empty(t, store.gadgets[d.ID])
}

func TestSetGadgets_DefaultsTheSpanFromTheRegistry(t *testing.T) {
	s, _, _, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)

	detail, err := s.SetGadgets(context.Background(), org, d.ID, me, []GadgetDraft{{Key: GadgetMyWork}})
	require.NoError(t, err)
	want, _ := Lookup(GadgetMyWork)
	require.Equal(t, want.DefaultSpan, detail.Gadgets[0].ColSpan)
}

func TestSetGadgets_RefusesWhatTheRegistryDoesNotAllow(t *testing.T) {
	s, _, vw, me, org := harness(t)
	ctx := context.Background()
	d := mustCreate(t, s, org, me, ModuleHome)
	v := orgView(t, me.UserID, "Open bugs")
	vw.byID[v.ID] = v

	cases := []struct {
		name  string
		draft GadgetDraft
		want  error
	}{
		{"unknown key", GadgetDraft{Key: "burndown"}, ErrUnknownGadget},
		{"span outside the CHECK", GadgetDraft{Key: GadgetMyWork, ColSpan: 3}, ErrSpanInvalid},
		{"a view-backed gadget with no view", GadgetDraft{Key: GadgetViewResults}, ErrViewRequired},
		{"a view on a gadget that takes none", GadgetDraft{Key: GadgetNote, SavedViewID: &v.ID}, ErrViewNotAllowed},
		{"a view nobody can see", GadgetDraft{Key: GadgetViewResults, SavedViewID: ptrOf(uuid.New())}, ErrViewNotVisible},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.SetGadgets(ctx, org, d.ID, me, []GadgetDraft{c.draft})
			require.ErrorIs(t, err, c.want)
		})
	}
}

// A private view belonging to somebody else must be refused with the SAME
// message a non-existent one gets, or the endpoint answers "does user X have a
// view with id Y" to anyone who asks.
func TestSetGadgets_APrivateViewOfAnotherPersonIsIndistinguishableFromNone(t *testing.T) {
	s, _, vw, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)

	theirs := orgView(t, uuid.New(), "Theirs")
	theirs.Visibility = views.VisibilityPrivate
	vw.byID[theirs.ID] = theirs

	_, err := s.SetGadgets(context.Background(), org, d.ID, me,
		[]GadgetDraft{{Key: GadgetViewResults, SavedViewID: &theirs.ID}})
	require.ErrorIs(t, err, ErrViewNotVisible)
}

func TestSetGadgets_RefusesMoreThanTheCap(t *testing.T) {
	s, _, _, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)

	drafts := make([]GadgetDraft, MaxGadgets+1)
	for i := range drafts {
		drafts[i] = GadgetDraft{Key: GadgetMyWork}
	}
	_, err := s.SetGadgets(context.Background(), org, d.ID, me, drafts)
	require.ErrorIs(t, err, ErrTooManyGadgets)
}

// The Vector-only breakdown rule reaches the write path as well as the read
// path, so a gadget that could never render is refused when it is saved rather
// than when somebody opens it.
func TestSetGadgets_RefusesAKindBreakdownOverABeaconInclusiveView(t *testing.T) {
	s, _, vw, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)

	crossModule := orgView(t, me.UserID, "Everything")
	q, err := views.ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon","vector"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	crossModule.Query = q
	vw.byID[crossModule.ID] = crossModule

	_, err = s.SetGadgets(context.Background(), org, d.ID, me, []GadgetDraft{{
		Key: GadgetBreakdown, SavedViewID: &crossModule.ID, Config: []byte(`{"group_by":"kind"}`),
	}})
	require.ErrorIs(t, err, views.ErrGroupFieldModule)
}

// ── Gadget resolution ───────────────────────────────────────────────────────

// ADR-0009's four degradation rules and the ordinary case, in one pass.
func TestGet_ResolvesEveryGadgetState(t *testing.T) {
	s, store, vw, me, org := harness(t)
	ctx := context.Background()
	d := mustCreate(t, s, org, me, ModuleHome)

	visible := orgView(t, me.UserID, "Everything open")
	vw.byID[visible.ID] = visible

	theirs := orgView(t, uuid.New(), "Not yours")
	theirs.Visibility = views.VisibilityPrivate
	vw.byID[theirs.ID] = theirs

	degraded := orgView(t, me.UserID, "Scope gone")
	degraded.InvalidReason = "every space this view is scoped to has been deleted"
	vw.byID[degraded.ID] = degraded

	deleted := uuid.New() // not in the lookup at all

	// Written straight to the fake store: a layout write refuses most of
	// these, and the point is what happens to rows that already exist.
	store.gadgets[d.ID] = []Gadget{
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 0, ColSpan: 2, SavedViewID: &visible.ID},
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 1, ColSpan: 2, SavedViewID: &theirs.ID},
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 2, ColSpan: 2, SavedViewID: &degraded.ID},
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 3, ColSpan: 2, SavedViewID: &deleted},
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 4, ColSpan: 2},
		{ID: uuid.New(), Key: "burndown", Position: 5, ColSpan: 1},
		{ID: uuid.New(), Key: string(GadgetMyWork), Position: 6, ColSpan: 2},
	}

	detail, err := s.Get(ctx, org, d.ID, me)
	require.NoError(t, err, "a dashboard ALWAYS loads, whatever its gadgets are in")
	require.Len(t, detail.Gadgets, 7)

	require.Equal(t, StateReady, detail.Gadgets[0].State)
	require.Equal(t, "Everything open", detail.Gadgets[0].Title, "an untitled gadget takes the view's name")
	require.NotNil(t, detail.Gadgets[0].Query)

	require.Equal(t, StateViewUnreadable, detail.Gadgets[1].State, "C2: not available to you")
	require.Nil(t, detail.Gadgets[1].Query, "a tile the viewer may not see must not be handed its query")
	require.Empty(t, detail.Gadgets[1].ViewName, "nor the private view's NAME")

	require.Equal(t, StateScopeUnavailable, detail.Gadgets[2].State, "C1 reaching a gadget")
	require.NotEmpty(t, detail.Gadgets[2].InvalidReason)
	require.Nil(t, detail.Gadgets[2].Query)

	require.Equal(t, StateViewRequired, detail.Gadgets[3].State, "a soft-deleted view leaves a recoverable tile")
	require.Equal(t, StateViewRequired, detail.Gadgets[4].State, "so does a null saved_view_id")

	require.Equal(t, StateUnknownGadget, detail.Gadgets[5].State, "C5: a placeholder, never a crash")
	require.Equal(t, "burndown", detail.Gadgets[5].GadgetKeyString())
	require.Nil(t, detail.Gadgets[5].Query)

	require.Equal(t, StateReady, detail.Gadgets[6].State)
	require.NotNil(t, detail.Gadgets[6].Query, "a registry-supplied query needs no saved view")
	require.Equal(t, []string{views.AssigneeMe}, detail.Gadgets[6].Query.Filter.Assignees)
}

// One view lookup for a whole dashboard, whatever its gadget count. N gadgets
// each resolving their own view is exactly the per-item shape spec §2.5 case
// 23 forbids and TestMatrixAPI23 traces.
//
// Fails-before: move the ByIDs call inside the per-gadget loop and calls
// becomes 8.
func TestGet_ResolvesEveryViewInOneLookup(t *testing.T) {
	s, store, vw, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)

	rows := []Gadget{}
	for i := range 8 {
		v := orgView(t, me.UserID, "View")
		vw.byID[v.ID] = v
		rows = append(rows, Gadget{ID: uuid.New(), Key: string(GadgetViewCount), Position: int32(i), ColSpan: 1, SavedViewID: &v.ID})
	}
	store.gadgets[d.ID] = rows

	vw.calls = 0
	_, err := s.Get(context.Background(), org, d.ID, me)
	require.NoError(t, err)
	require.Equal(t, 1, vw.calls, "eight gadgets must cost one view lookup, not eight")
}

// A config title beats the view name; the gadget kind's own name is the last
// resort. Resolved server-side so renaming a view renames every untitled
// gadget that shows it.
func TestGet_TitleFallsBackThroughConfigThenViewThenKind(t *testing.T) {
	s, store, vw, me, org := harness(t)
	d := mustCreate(t, s, org, me, ModuleHome)
	v := orgView(t, me.UserID, "Open bugs")
	vw.byID[v.ID] = v

	store.gadgets[d.ID] = []Gadget{
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 0, ColSpan: 2, SavedViewID: &v.ID,
			Config: Config{Title: "What I care about"}},
		{ID: uuid.New(), Key: string(GadgetViewResults), Position: 1, ColSpan: 2, SavedViewID: &v.ID},
		{ID: uuid.New(), Key: string(GadgetNote), Position: 2, ColSpan: 4},
	}

	detail, err := s.Get(context.Background(), org, d.ID, me)
	require.NoError(t, err)
	require.Equal(t, "What I care about", detail.Gadgets[0].Title)
	require.Equal(t, "Open bugs", detail.Gadgets[1].Title)
	require.Equal(t, "Note", detail.Gadgets[2].Title)
}

// ── Home seeding ────────────────────────────────────────────────────────────

func TestResolveHome_SeedsTheStarterOnAFirstVisit(t *testing.T) {
	s, _, _, me, org := harness(t)

	detail, err := s.ResolveHome(context.Background(), org, me)
	require.NoError(t, err)
	require.Equal(t, StarterName, detail.Name)
	require.True(t, detail.IsDefault)
	require.True(t, detail.IsSeeded)
	require.Equal(t, views.VisibilityPrivate, detail.Visibility)
	require.Len(t, detail.Gadgets, 3, "my work, recently updated, and a getting-started note")
	require.Equal(t, string(GadgetMyWork), detail.Gadgets[0].GadgetKeyString())
	require.Equal(t, string(GadgetRecentWork), detail.Gadgets[1].GadgetKeyString())
	require.Equal(t, string(GadgetNote), detail.Gadgets[2].GadgetKeyString())
	require.NotEmpty(t, detail.Gadgets[2].Body(), "the note ships with its text, so it is editable rather than special-cased")
}

// THE P5 DoD LINE: seeding runs exactly once. The spec's own test changes a
// user's primary team and asserts the dashboard is unchanged; the equivalent
// here is that nothing about the person re-seeds, and that a customised
// starter survives.
//
// Fails-before: drop the DefaultFor check at the top of ResolveHome and the
// second visit re-seeds, restoring the three starter tiles over the person's
// own layout.
func TestResolveHome_NeverReSeedsAndNeverOverwritesCustomisation(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()

	first, err := s.ResolveHome(ctx, org, me)
	require.NoError(t, err)
	require.Equal(t, 1, store.seedCalls)

	// The person makes it theirs.
	_, err = s.SetGadgets(ctx, org, first.ID, me, []GadgetDraft{{Key: GadgetRecentWork}})
	require.NoError(t, err)

	// Everything about them changes except the dashboard.
	me.EffectiveTeamIDs = []uuid.UUID{uuid.New(), uuid.New()}

	second, err := s.ResolveHome(ctx, org, me)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "the same dashboard, not a new one")
	require.Equal(t, 1, store.seedCalls, "seeding runs exactly once")
	require.Len(t, second.Gadgets, 1, "re-seeding would have destroyed the customisation")
	require.Equal(t, string(GadgetRecentWork), second.Gadgets[0].GadgetKeyString())
}

// A person who already made their own default Home dashboard is served that
// one and never seeded at all.
func TestResolveHome_ServesAnExistingDefaultWithoutSeeding(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()

	yes := true
	mine, err := s.Create(ctx, org, me, Draft{Name: "My own", Module: ModuleHome, IsDefault: &yes})
	require.NoError(t, err)

	detail, err := s.ResolveHome(ctx, org, me)
	require.NoError(t, err)
	require.Equal(t, mine.ID, detail.ID)
	require.Zero(t, store.seedCalls)
}

// Seeding is per person. One user's starter must not satisfy another's first
// visit — the unique index is on (owner_id, module), not on module alone.
func TestResolveHome_SeedsPerPerson(t *testing.T) {
	s, store, _, me, org := harness(t)
	ctx := context.Background()

	a, err := s.ResolveHome(ctx, org, me)
	require.NoError(t, err)
	b, err := s.ResolveHome(ctx, org, views.Actor{UserID: uuid.New()})
	require.NoError(t, err)

	require.NotEqual(t, a.ID, b.ID)
	require.Equal(t, 2, store.seedCalls)
}

func ptrOf(id uuid.UUID) *uuid.UUID { return &id }

// Small readers used only by the assertions above, so a test does not reach
// into the struct's field names in six places.

// GadgetKeyString returns the stored key.
func (r ResolvedGadget) GadgetKeyString() string { return r.Key }

// Body returns the note body from the resolved config.
func (r ResolvedGadget) Body() string { return r.Config.Body }
