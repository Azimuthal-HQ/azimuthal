package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/dashboards"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// DashboardAdapter implements dashboards.Store over the dashboards and
// dashboard_gadgets tables (migration 042).
//
// It holds the pool as well as the queries because two of its writes are
// several statements that must land together: replacing a layout, and seeding
// a starter dashboard with its gadgets. Both are stated at their call sites.
type DashboardAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewDashboardAdapter creates a DashboardAdapter backed by the given pool.
func NewDashboardAdapter(pool *pgxpool.Pool) *DashboardAdapter {
	return &DashboardAdapter{pool: pool, q: generated.New(pool)}
}

// Compile-time proof the adapter satisfies the seam.
var _ dashboards.Store = (*DashboardAdapter)(nil)

func dashboardFromRow(r generated.Dashboard, ownerName string, teamName *string) dashboards.Dashboard {
	return dashboards.Dashboard{
		ID:               r.ID,
		OrgID:            r.OrgID,
		OwnerID:          r.OwnerID,
		Name:             r.Name,
		Description:      r.Description,
		Module:           dashboards.Module(r.Module),
		IsDefault:        r.IsDefault,
		IsSeeded:         r.IsSeeded,
		Visibility:       views.Visibility(r.Visibility),
		VisibilityTeamID: goUUIDPtr(r.VisibilityTeamID),
		CreatedAt:        goTime(r.CreatedAt),
		UpdatedAt:        goTime(r.UpdatedAt),
		OwnerName:        ownerName,
		TeamName:         derefStr(teamName),
	}
}

// gadgetFromRow parses a stored gadget.
//
// A config document this build cannot parse does NOT fail the read. That is
// the opposite of the rule for a saved view's filter document, and
// deliberately so: an unreadable filter would silently widen what a view
// matches, whereas an unreadable config at worst loses a title or a row limit
// on one tile. ADR-0009's C5 requires the dashboard to load whatever a gadget
// row holds, so a config that no longer parses degrades to the zero value and
// the tile renders with its defaults rather than taking the page down.
//
// The key is carried through verbatim either way — that is what lets the
// service report an unknown gadget as a placeholder rather than as an error.
func gadgetFromRow(r generated.DashboardGadget) dashboards.Gadget {
	g := dashboards.Gadget{
		ID:          r.ID,
		DashboardID: r.DashboardID,
		Key:         r.GadgetKey,
		Position:    r.Position,
		ColSpan:     int32(r.ColSpan),
		SavedViewID: goUUIDPtr(r.SavedViewID),
	}
	if def, known := dashboards.Lookup(dashboards.GadgetKey(r.GadgetKey)); known {
		if cfg, err := dashboards.ParseConfig(def, r.Config); err == nil {
			g.Config = cfg
		}
	}
	return g
}

// Create inserts a dashboard.
func (a *DashboardAdapter) Create(ctx context.Context, d dashboards.Dashboard) (dashboards.Dashboard, error) {
	if d.IsDefault {
		// Promoting on create needs the same demotion an update does, and for
		// the same reason: dashboards_one_default is not deferrable, so the
		// old default has to stand down first.
		return a.createDefault(ctx, d)
	}
	row, err := a.q.CreateDashboard(ctx, createParams(d))
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter create: %w", err)
	}
	return dashboardFromRow(row, "", nil), nil
}

func createParams(d dashboards.Dashboard) generated.CreateDashboardParams {
	return generated.CreateDashboardParams{
		OrgID:            d.OrgID,
		OwnerID:          d.OwnerID,
		Name:             d.Name,
		Description:      d.Description,
		Module:           string(d.Module),
		IsDefault:        d.IsDefault,
		IsSeeded:         d.IsSeeded,
		Visibility:       string(d.Visibility),
		VisibilityTeamID: pgUUID(d.VisibilityTeamID),
	}
}

// createDefault inserts a dashboard that claims the default slot, standing the
// previous holder down in the same transaction.
func (a *DashboardAdapter) createDefault(ctx context.Context, d dashboards.Dashboard) (dashboards.Dashboard, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if _, err := qtx.ClearDefaultDashboard(ctx, generated.ClearDefaultDashboardParams{
		OrgID: d.OrgID, OwnerID: d.OwnerID, Module: string(d.Module), KeepID: uuid.Nil,
	}); err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter clear default: %w", err)
	}
	row, err := qtx.CreateDashboard(ctx, createParams(d))
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter create: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter create commit: %w", err)
	}
	return dashboardFromRow(row, "", nil), nil
}

// Get returns one dashboard by id, or dashboards.ErrNotFound.
func (a *DashboardAdapter) Get(ctx context.Context, orgID, id uuid.UUID) (dashboards.Dashboard, error) {
	row, err := a.q.GetDashboard(ctx, generated.GetDashboardParams{ID: id, OrgID: orgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboards.Dashboard{}, dashboards.ErrNotFound
		}
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter get: %w", err)
	}
	return dashboardFromRow(generated.Dashboard{
		ID: row.ID, OrgID: row.OrgID, OwnerID: row.OwnerID, Name: row.Name,
		Description: row.Description, Module: row.Module, IsDefault: row.IsDefault,
		IsSeeded: row.IsSeeded, Visibility: row.Visibility,
		VisibilityTeamID: row.VisibilityTeamID, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, DeletedAt: row.DeletedAt,
	}, row.OwnerName, row.TeamName), nil
}

// Update writes the whole mutable surface back.
//
// When the row claims the default slot the write is a transaction: the
// previous holder is stood down first, because dashboards_one_default is a
// plain partial unique index rather than a deferrable constraint. Migration
// 042 says why it is not deferrable — layout writes are delete-then-insert and
// never pass through a colliding state — so this one caller pays for it
// explicitly rather than every reader paying for a deferral it does not need.
func (a *DashboardAdapter) Update(ctx context.Context, d dashboards.Dashboard) (dashboards.Dashboard, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if d.IsDefault {
		if _, err := qtx.ClearDefaultDashboard(ctx, generated.ClearDefaultDashboardParams{
			OrgID: d.OrgID, OwnerID: d.OwnerID, Module: string(d.Module), KeepID: d.ID,
		}); err != nil {
			return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter clear default: %w", err)
		}
	}
	row, err := qtx.UpdateDashboard(ctx, generated.UpdateDashboardParams{
		ID:               d.ID,
		OrgID:            d.OrgID,
		Name:             d.Name,
		Description:      d.Description,
		Module:           string(d.Module),
		IsDefault:        d.IsDefault,
		Visibility:       string(d.Visibility),
		VisibilityTeamID: pgUUID(d.VisibilityTeamID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboards.Dashboard{}, dashboards.ErrNotFound
		}
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter update commit: %w", err)
	}
	return dashboardFromRow(row, d.OwnerName, nil), nil
}

// SoftDelete marks a dashboard deleted and reports how many rows changed.
func (a *DashboardAdapter) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error) {
	n, err := a.q.SoftDeleteDashboard(ctx, generated.SoftDeleteDashboardParams{ID: id, OrgID: orgID})
	if err != nil {
		return 0, fmt.Errorf("dashboard adapter delete: %w", err)
	}
	return n, nil
}

// ListForViewer returns every dashboard whose definition reaches the caller.
func (a *DashboardAdapter) ListForViewer(
	ctx context.Context, orgID, viewerID uuid.UUID, teamIDs []uuid.UUID, module string,
) ([]dashboards.Dashboard, error) {
	rows, err := a.q.ListDashboardsForViewer(ctx, generated.ListDashboardsForViewerParams{
		OrgID:            orgID,
		ViewerID:         viewerID,
		EffectiveTeamIds: nonNilUUIDs(teamIDs),
		Module:           module,
	})
	if err != nil {
		return nil, fmt.Errorf("dashboard adapter list: %w", err)
	}
	out := make([]dashboards.Dashboard, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboardFromRow(generated.Dashboard{
			ID: r.ID, OrgID: r.OrgID, OwnerID: r.OwnerID, Name: r.Name,
			Description: r.Description, Module: r.Module, IsDefault: r.IsDefault,
			IsSeeded: r.IsSeeded, Visibility: r.Visibility,
			VisibilityTeamID: r.VisibilityTeamID, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, DeletedAt: r.DeletedAt,
		}, r.OwnerName, r.TeamName))
	}
	return out, nil
}

// DefaultFor returns the caller's default dashboard for a module.
func (a *DashboardAdapter) DefaultFor(ctx context.Context, orgID, ownerID uuid.UUID, module string) (dashboards.Dashboard, error) {
	row, err := a.q.GetDefaultDashboard(ctx, generated.GetDefaultDashboardParams{
		OrgID: orgID, OwnerID: ownerID, Module: module,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboards.Dashboard{}, dashboards.ErrNotFound
		}
		return dashboards.Dashboard{}, fmt.Errorf("dashboard adapter default: %w", err)
	}
	return dashboardFromRow(generated.Dashboard{
		ID: row.ID, OrgID: row.OrgID, OwnerID: row.OwnerID, Name: row.Name,
		Description: row.Description, Module: row.Module, IsDefault: row.IsDefault,
		IsSeeded: row.IsSeeded, Visibility: row.Visibility,
		VisibilityTeamID: row.VisibilityTeamID, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, DeletedAt: row.DeletedAt,
	}, row.OwnerName, row.TeamName), nil
}

// ListGadgets returns one dashboard's gadgets in display order.
func (a *DashboardAdapter) ListGadgets(ctx context.Context, dashboardID uuid.UUID) ([]dashboards.Gadget, error) {
	rows, err := a.q.ListDashboardGadgets(ctx, dashboardID)
	if err != nil {
		return nil, fmt.Errorf("dashboard adapter list gadgets: %w", err)
	}
	out := make([]dashboards.Gadget, 0, len(rows))
	for _, r := range rows {
		out = append(out, gadgetFromRow(r))
	}
	return out, nil
}

// ReplaceGadgets writes a whole layout in ONE transaction.
//
// Delete-then-insert rather than a diff. Spec §6 requires the collection to
// save as a whole "to avoid partial states", and a diff would reintroduce
// exactly the states it forbids: a reorder that half-applied would leave two
// tiles claiming one slot, and dashboard_gadgets_position_key is a plain
// unique index precisely because this shape never produces that.
//
// Splitting it into separate Execs would make each its own implicit
// transaction, and a failure between them would commit somebody an empty
// dashboard.
func (a *DashboardAdapter) ReplaceGadgets(
	ctx context.Context, dashboardID uuid.UUID, gadgets []dashboards.Gadget,
) ([]dashboards.Gadget, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard adapter replace gadgets: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if _, err := qtx.DeleteDashboardGadgets(ctx, dashboardID); err != nil {
		return nil, fmt.Errorf("dashboard adapter clear gadgets: %w", err)
	}
	out := make([]dashboards.Gadget, 0, len(gadgets))
	for _, g := range gadgets {
		row, err := insertGadget(ctx, qtx, dashboardID, g)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("dashboard adapter replace gadgets commit: %w", err)
	}
	return out, nil
}

func insertGadget(ctx context.Context, q *generated.Queries, dashboardID uuid.UUID, g dashboards.Gadget) (dashboards.Gadget, error) {
	// The config is re-serialised from this build's own struct rather than
	// passed through, so a document in the table is by construction one this
	// build produced — the same rule views.Query.Encode enforces. A gadget
	// whose key is unknown cannot be written at all (the service refuses it
	// first), so Lookup always succeeds here.
	def, known := dashboards.Lookup(dashboards.GadgetKey(g.Key))
	if !known {
		return dashboards.Gadget{}, fmt.Errorf("%w %q", dashboards.ErrUnknownGadget, g.Key)
	}
	raw, err := g.Config.Encode(def)
	if err != nil {
		return dashboards.Gadget{}, fmt.Errorf("encoding the gadget's configuration: %w", err)
	}
	row, err := q.CreateDashboardGadget(ctx, generated.CreateDashboardGadgetParams{
		DashboardID: dashboardID,
		GadgetKey:   g.Key,
		Position:    g.Position,
		//nolint:gosec // col_span is validated against {1,2,4} before it reaches here
		ColSpan:     int16(g.ColSpan),
		SavedViewID: pgUUID(g.SavedViewID),
		Config:      raw,
	})
	if err != nil {
		return dashboards.Gadget{}, fmt.Errorf("dashboard adapter create gadget: %w", err)
	}
	return gadgetFromRow(row), nil
}

// CreateStarter seeds a Home dashboard and its gadgets in one transaction.
//
// The dashboard insert is ON CONFLICT DO NOTHING against
// dashboards_one_default, so a person who already has a default gets nothing —
// which is what makes lazy seeding idempotent under concurrency rather than
// under a check. When the insert does nothing there is nothing to attach
// gadgets to, and the transaction ends having written nothing at all.
func (a *DashboardAdapter) CreateStarter(ctx context.Context, d dashboards.Dashboard, gadgets []dashboards.Gadget) (bool, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("dashboard adapter seed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	row, err := qtx.CreateStarterDashboard(ctx, generated.CreateStarterDashboardParams{
		OrgID: d.OrgID, OwnerID: d.OwnerID, Name: d.Name, Module: string(d.Module),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Somebody already holds the default slot. Not an error: the
			// caller re-reads and serves whatever is there.
			return false, nil
		}
		return false, fmt.Errorf("dashboard adapter seed: %w", err)
	}
	for _, g := range gadgets {
		if _, err := insertGadget(ctx, qtx, row.ID, g); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("dashboard adapter seed commit: %w", err)
	}
	return true, nil
}
