package dashboards

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// A store failure must SURFACE. The alternative — a zero value and a nil error
// — is what turns a database outage into a dashboard that quietly loses its
// gadgets, and the person looking at it has no way to tell the two apart.

var errDashStore = errors.New("dashboard store is down")

type brokenStore struct {
	failCreate, failGet, failUpdate, failDelete bool
	failList, failGadgets, failReplace          bool
	failDefault, failStarter                    bool
	dashboard                                   Dashboard
	deleted                                     int64
}

func (b *brokenStore) Create(context.Context, Dashboard) (Dashboard, error) {
	if b.failCreate {
		return Dashboard{}, errDashStore
	}
	return b.dashboard, nil
}

func (b *brokenStore) Get(context.Context, uuid.UUID, uuid.UUID) (Dashboard, error) {
	if b.failGet {
		return Dashboard{}, errDashStore
	}
	return b.dashboard, nil
}

func (b *brokenStore) Update(context.Context, Dashboard) (Dashboard, error) {
	if b.failUpdate {
		return Dashboard{}, errDashStore
	}
	return b.dashboard, nil
}

func (b *brokenStore) SoftDelete(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	if b.failDelete {
		return 0, errDashStore
	}
	return b.deleted, nil
}

func (b *brokenStore) ListForViewer(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, string) ([]Dashboard, error) {
	if b.failList {
		return nil, errDashStore
	}
	return []Dashboard{b.dashboard}, nil
}

func (b *brokenStore) ListGadgets(context.Context, uuid.UUID) ([]Gadget, error) {
	if b.failGadgets {
		return nil, errDashStore
	}
	return nil, nil
}

func (b *brokenStore) ReplaceGadgets(_ context.Context, _ uuid.UUID, g []Gadget) ([]Gadget, error) {
	if b.failReplace {
		return nil, errDashStore
	}
	return g, nil
}

func (b *brokenStore) DefaultFor(context.Context, uuid.UUID, uuid.UUID, string) (Dashboard, error) {
	if b.failDefault {
		return Dashboard{}, errDashStore
	}
	return b.dashboard, nil
}

func (b *brokenStore) CreateStarter(context.Context, Dashboard, []Gadget) (bool, error) {
	if b.failStarter {
		return false, errDashStore
	}
	return true, nil
}

type brokenViews struct{ fail bool }

func (v brokenViews) ByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]views.View, error) {
	if v.fail {
		return nil, errDashStore
	}
	return nil, nil
}

func brokenSvc(store *brokenStore, vw brokenViews) (*Service, views.Actor, uuid.UUID) {
	me := uuid.New()
	store.dashboard = Dashboard{
		ID: uuid.New(), OwnerID: me, Name: "Mine",
		Module: ModuleHome, Visibility: views.VisibilityPrivate,
	}
	return NewService(store, vw), views.Actor{UserID: me}, uuid.New()
}

func TestDashboardService_StoreFailuresSurface(t *testing.T) {
	ctx := context.Background()
	d := Draft{Name: "Mine", Module: ModuleHome, Visibility: views.VisibilityPrivate}

	t.Run("create", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failCreate: true}, brokenViews{})
		_, err := s.Create(ctx, org, a, d)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("get", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failGet: true}, brokenViews{})
		_, err := s.Get(ctx, org, uuid.New(), a)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("the gadget read", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failGadgets: true}, brokenViews{})
		_, err := s.Get(ctx, org, uuid.New(), a)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("update", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failUpdate: true}, brokenViews{})
		_, err := s.Update(ctx, org, uuid.New(), a, d)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("delete", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failDelete: true}, brokenViews{})
		require.ErrorIs(t, s.Delete(ctx, org, uuid.New(), a), errDashStore)
	})

	t.Run("list", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failList: true}, brokenViews{})
		_, err := s.List(ctx, org, a, "")
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("the layout write", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failReplace: true}, brokenViews{})
		_, err := s.SetGadgets(ctx, org, uuid.New(), a, []GadgetDraft{{Key: GadgetMyWork}})
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("Home's default lookup", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{failDefault: true}, brokenViews{})
		_, err := s.ResolveHome(ctx, org, a)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("the view lookup on a read", func(t *testing.T) {
		store := &brokenStore{}
		_, a, org := brokenSvc(store, brokenViews{fail: true})
		id := uuid.New()
		store.dashboard.ID = id
		// A dashboard with a gadget that names a view, so the lookup runs.
		store.failGadgets = false
		gadgetStore := &gadgetReturningStore{brokenStore: store, viewID: uuid.New()}
		s := NewService(gadgetStore, brokenViews{fail: true})
		_, err := s.Get(ctx, org, id, a)
		require.ErrorIs(t, err, errDashStore)
	})

	t.Run("the view lookup on a write", func(t *testing.T) {
		s, a, org := brokenSvc(&brokenStore{}, brokenViews{fail: true})
		viewID := uuid.New()
		_, err := s.SetGadgets(ctx, org, uuid.New(), a,
			[]GadgetDraft{{Key: GadgetViewResults, SavedViewID: &viewID}})
		require.ErrorIs(t, err, errDashStore)
	})
}

// gadgetReturningStore is a brokenStore that returns one view-backed gadget,
// so the batch view lookup is actually reached.
type gadgetReturningStore struct {
	*brokenStore
	viewID uuid.UUID
}

func (g *gadgetReturningStore) ListGadgets(context.Context, uuid.UUID) ([]Gadget, error) {
	return []Gadget{{
		ID: uuid.New(), Key: string(GadgetViewResults), Position: 0, ColSpan: 2,
		SavedViewID: &g.viewID,
	}}, nil
}

// Deleting a dashboard that vanished between the read and the write is
// ErrNotFound rather than a silent success — reporting "deleted" for a row
// nobody deleted is a lie about what happened.
func TestDashboardService_DeleteReportsNotFoundWhenNothingChanged(t *testing.T) {
	s, a, org := brokenSvc(&brokenStore{deleted: 0}, brokenViews{})
	require.ErrorIs(t, s.Delete(context.Background(), org, uuid.New(), a), ErrNotFound)
}

// A starter insert that fails must fail the request. Serving an empty Home
// would look like a dashboard somebody had cleared themselves.
func TestDashboardService_AFailedSeedFailsTheRequest(t *testing.T) {
	// DefaultFor must MISS first, so the seed is attempted — a store that
	// merely fails the seed never reaches it.
	_, a, org := brokenSvc(&brokenStore{}, brokenViews{})
	s := NewService(&missingDefaultStore{failStarter: true}, brokenViews{})
	_, err := s.ResolveHome(context.Background(), org, a)
	require.ErrorIs(t, err, errDashStore)
}

// missingDefaultStore has no default dashboard, so ResolveHome reaches the
// seeding branch.
type missingDefaultStore struct {
	brokenStore
	failStarter bool
}

func (m *missingDefaultStore) DefaultFor(context.Context, uuid.UUID, uuid.UUID, string) (Dashboard, error) {
	return Dashboard{}, ErrNotFound
}

func (m *missingDefaultStore) CreateStarter(context.Context, Dashboard, []Gadget) (bool, error) {
	if m.failStarter {
		return false, errDashStore
	}
	return true, nil
}
