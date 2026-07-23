package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ItemTypeAdapter implements itemtypes.Repository using the item_types table.
type ItemTypeAdapter struct {
	q *generated.Queries
}

// NewItemTypeAdapter creates an ItemTypeAdapter.
func NewItemTypeAdapter(q *generated.Queries) *ItemTypeAdapter {
	return &ItemTypeAdapter{q: q}
}

func (a *ItemTypeAdapter) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*itemtypes.ItemType, error) {
	rows, err := a.q.ListItemTypesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("item type adapter list by org: %w", err)
	}
	return dbItemTypesToTypes(rows), nil
}

func (a *ItemTypeAdapter) ListActiveByOrg(ctx context.Context, orgID uuid.UUID) ([]*itemtypes.ItemType, error) {
	rows, err := a.q.ListActiveItemTypesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("item type adapter list active by org: %w", err)
	}
	return dbItemTypesToTypes(rows), nil
}

func (a *ItemTypeAdapter) GetByID(ctx context.Context, id uuid.UUID) (*itemtypes.ItemType, error) {
	row, err := a.q.GetItemTypeByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, itemtypes.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item type adapter get by id: %w", err)
	}
	return dbItemTypeToType(row), nil
}

func (a *ItemTypeAdapter) GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (*itemtypes.ItemType, error) {
	row, err := a.q.GetItemTypeByOrgSlug(ctx, generated.GetItemTypeByOrgSlugParams{OrgID: orgID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, itemtypes.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item type adapter get by slug: %w", err)
	}
	return dbItemTypeToType(row), nil
}

func (a *ItemTypeAdapter) Create(ctx context.Context, t *itemtypes.ItemType) error {
	_, err := a.q.CreateItemType(ctx, generated.CreateItemTypeParams{
		ID:       t.ID,
		OrgID:    t.OrgID,
		Slug:     t.Slug,
		Name:     t.Name,
		Position: int32(t.Position), //nolint:gosec // small positive ordinal
	})
	if err != nil {
		return fmt.Errorf("item type adapter create: %w", err)
	}
	return nil
}

func (a *ItemTypeAdapter) Rename(ctx context.Context, id uuid.UUID, name string) (*itemtypes.ItemType, error) {
	row, err := a.q.RenameItemType(ctx, generated.RenameItemTypeParams{ID: id, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, itemtypes.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item type adapter rename: %w", err)
	}
	return dbItemTypeToType(row), nil
}

func (a *ItemTypeAdapter) SetArchived(ctx context.Context, id uuid.UUID, archived bool) (*itemtypes.ItemType, error) {
	var at pgtype.Timestamptz
	if archived {
		at = pgTimestamp(time.Now().UTC())
	}
	row, err := a.q.SetItemTypeArchived(ctx, generated.SetItemTypeArchivedParams{ID: id, ArchivedAt: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, itemtypes.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item type adapter set archived: %w", err)
	}
	return dbItemTypeToType(row), nil
}

func (a *ItemTypeAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteItemType(ctx, id); err != nil {
		return fmt.Errorf("item type adapter delete: %w", err)
	}
	return nil
}

func (a *ItemTypeAdapter) CountItemsOfType(ctx context.Context, orgID uuid.UUID, slug string) (int, error) {
	n, err := a.q.CountItemsOfType(ctx, generated.CountItemsOfTypeParams{OrgID: orgID, Kind: slug})
	if err != nil {
		return 0, fmt.Errorf("item type adapter count items: %w", err)
	}
	return int(n), nil
}

func (a *ItemTypeAdapter) NextPosition(ctx context.Context, orgID uuid.UUID) (int, error) {
	max, err := a.q.MaxItemTypePosition(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("item type adapter next position: %w", err)
	}
	return int(max) + 1, nil
}

func (a *ItemTypeAdapter) SeedDefaults(ctx context.Context, orgID uuid.UUID) error {
	if err := a.q.SeedDefaultItemTypes(ctx, orgID); err != nil {
		return fmt.Errorf("item type adapter seed defaults: %w", err)
	}
	return nil
}

func dbItemTypeToType(t generated.ItemType) *itemtypes.ItemType {
	return &itemtypes.ItemType{
		ID:         t.ID,
		OrgID:      t.OrgID,
		Slug:       t.Slug,
		Name:       t.Name,
		Position:   int(t.Position),
		ArchivedAt: goTimePtr(t.ArchivedAt),
		CreatedAt:  goTime(t.CreatedAt),
		UpdatedAt:  goTime(t.UpdatedAt),
	}
}

func dbItemTypesToTypes(rows []generated.ItemType) []*itemtypes.ItemType {
	out := make([]*itemtypes.ItemType, len(rows))
	for i, row := range rows {
		out[i] = dbItemTypeToType(row)
	}
	return out
}
