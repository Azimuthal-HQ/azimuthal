package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// CustomFieldDefAdapter implements customfields.DefRepository.
type CustomFieldDefAdapter struct {
	q *generated.Queries
}

// NewCustomFieldDefAdapter creates a CustomFieldDefAdapter.
func NewCustomFieldDefAdapter(q *generated.Queries) *CustomFieldDefAdapter {
	return &CustomFieldDefAdapter{q: q}
}

// ListByOrg implements the repository interface.
func (a *CustomFieldDefAdapter) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*customfields.FieldDef, error) {
	rows, err := a.q.ListCustomFieldDefsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter list by org: %w", err)
	}
	return dbFieldDefsToDefs(rows), nil
}

// ListActiveByOrg implements the repository interface.
func (a *CustomFieldDefAdapter) ListActiveByOrg(ctx context.Context, orgID uuid.UUID) ([]*customfields.FieldDef, error) {
	rows, err := a.q.ListActiveCustomFieldDefsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter list active by org: %w", err)
	}
	return dbFieldDefsToDefs(rows), nil
}

// GetByID implements the repository interface.
func (a *CustomFieldDefAdapter) GetByID(ctx context.Context, id uuid.UUID) (*customfields.FieldDef, error) {
	row, err := a.q.GetCustomFieldDefByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter get by id: %w", err)
	}
	return dbFieldDefToDef(row), nil
}

// GetByOrgSlug implements the repository interface.
func (a *CustomFieldDefAdapter) GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (*customfields.FieldDef, error) {
	row, err := a.q.GetCustomFieldDefByOrgSlug(ctx, generated.GetCustomFieldDefByOrgSlugParams{OrgID: orgID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter get by slug: %w", err)
	}
	return dbFieldDefToDef(row), nil
}

// Create implements the repository interface.
func (a *CustomFieldDefAdapter) Create(ctx context.Context, d *customfields.FieldDef) error {
	opts, err := marshalOptions(d.Options)
	if err != nil {
		return err
	}
	_, err = a.q.CreateCustomFieldDef(ctx, generated.CreateCustomFieldDefParams{
		ID:        d.ID,
		OrgID:     d.OrgID,
		Slug:      d.Slug,
		Name:      d.Name,
		FieldType: d.Type,
		Options:   opts,
		Position:  int32(d.Position), //nolint:gosec // small positive ordinal
	})
	if err != nil {
		return fmt.Errorf("custom field def adapter create: %w", err)
	}
	return nil
}

// Update implements the repository interface.
func (a *CustomFieldDefAdapter) Update(ctx context.Context, id uuid.UUID, name string, options []string) (*customfields.FieldDef, error) {
	opts, err := marshalOptions(options)
	if err != nil {
		return nil, err
	}
	row, err := a.q.UpdateCustomFieldDef(ctx, generated.UpdateCustomFieldDefParams{ID: id, Name: name, Options: opts})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter update: %w", err)
	}
	return dbFieldDefToDef(row), nil
}

// SetArchived implements the repository interface.
func (a *CustomFieldDefAdapter) SetArchived(ctx context.Context, id uuid.UUID, archived bool) (*customfields.FieldDef, error) {
	var at pgtype.Timestamptz
	if archived {
		at = pgTimestamp(time.Now().UTC())
	}
	row, err := a.q.SetCustomFieldDefArchived(ctx, generated.SetCustomFieldDefArchivedParams{ID: id, ArchivedAt: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field def adapter set archived: %w", err)
	}
	return dbFieldDefToDef(row), nil
}

// Delete implements the repository interface.
func (a *CustomFieldDefAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteCustomFieldDef(ctx, id); err != nil {
		return fmt.Errorf("custom field def adapter delete: %w", err)
	}
	return nil
}

// NextPosition implements the repository interface.
func (a *CustomFieldDefAdapter) NextPosition(ctx context.Context, orgID uuid.UUID) (int, error) {
	maxPos, err := a.q.MaxCustomFieldDefPosition(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("custom field def adapter next position: %w", err)
	}
	return int(maxPos) + 1, nil
}

// CustomFieldValueAdapter implements customfields.ValueRepository.
type CustomFieldValueAdapter struct {
	q *generated.Queries
}

// NewCustomFieldValueAdapter creates a CustomFieldValueAdapter.
func NewCustomFieldValueAdapter(q *generated.Queries) *CustomFieldValueAdapter {
	return &CustomFieldValueAdapter{q: q}
}

// ListByItem implements the repository interface.
func (a *CustomFieldValueAdapter) ListByItem(ctx context.Context, itemID uuid.UUID) ([]customfields.StoredValue, error) {
	rows, err := a.q.ListItemFieldValues(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("custom field value adapter list by item: %w", err)
	}
	out := make([]customfields.StoredValue, len(rows))
	for i, r := range rows {
		out[i] = customfields.StoredValue{FieldSlug: r.FieldSlug, Value: r.Value}
	}
	return out, nil
}

// Upsert implements the repository interface.
func (a *CustomFieldValueAdapter) Upsert(ctx context.Context, itemID uuid.UUID, slug, value string) error {
	_, err := a.q.UpsertItemFieldValue(ctx, generated.UpsertItemFieldValueParams{
		ID:        uuid.New(),
		ItemID:    itemID,
		FieldSlug: slug,
		Value:     value,
	})
	if err != nil {
		return fmt.Errorf("custom field value adapter upsert: %w", err)
	}
	return nil
}

// Delete implements the repository interface.
func (a *CustomFieldValueAdapter) Delete(ctx context.Context, itemID uuid.UUID, slug string) error {
	if err := a.q.DeleteItemFieldValue(ctx, generated.DeleteItemFieldValueParams{ItemID: itemID, FieldSlug: slug}); err != nil {
		return fmt.Errorf("custom field value adapter delete: %w", err)
	}
	return nil
}

func marshalOptions(options []string) ([]byte, error) {
	if options == nil {
		options = []string{}
	}
	b, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("marshalling options: %w", err)
	}
	return b, nil
}

func unmarshalOptions(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func dbFieldDefToDef(d generated.CustomFieldDef) *customfields.FieldDef {
	return &customfields.FieldDef{
		ID:         d.ID,
		OrgID:      d.OrgID,
		Slug:       d.Slug,
		Name:       d.Name,
		Type:       d.FieldType,
		Options:    unmarshalOptions(d.Options),
		Position:   int(d.Position),
		ArchivedAt: goTimePtr(d.ArchivedAt),
		CreatedAt:  goTime(d.CreatedAt),
		UpdatedAt:  goTime(d.UpdatedAt),
	}
}

func dbFieldDefsToDefs(rows []generated.CustomFieldDef) []*customfields.FieldDef {
	out := make([]*customfields.FieldDef, len(rows))
	for i, row := range rows {
		out[i] = dbFieldDefToDef(row)
	}
	return out
}
