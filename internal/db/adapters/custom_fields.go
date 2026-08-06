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

// ListForEntityInSpace implements the repository interface. The values are
// reconciled against the space in the statement, per entity type: the route
// that reaches this proved {spaceID} readable while proving nothing about the
// entity id. An entity in another space yields no rows, which is what an
// entity with no values yields too — the two are indistinguishable on purpose.
func (a *CustomFieldValueAdapter) ListForEntityInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID) ([]customfields.StoredValue, error) {
	rows, err := a.q.ListEntityFieldValues(ctx, generated.ListEntityFieldValuesParams{
		EntityType: entityType,
		EntityID:   entityID,
		SpaceID:    spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("custom field value adapter list for entity: %w", err)
	}
	out := make([]customfields.StoredValue, len(rows))
	for i, r := range rows {
		out[i] = customfields.StoredValue{FieldSlug: r.FieldSlug, Value: r.Value}
	}
	return out, nil
}

// UpsertInSpace implements the repository interface. The statement inserts
// only when the entity resolves inside spaceID (live, right type); when it
// does not, zero rows are proposed, the ON CONFLICT never fires, and
// pgx.ErrNoRows on the RETURNING reports ok=false with nothing written.
func (a *CustomFieldValueAdapter) UpsertInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug, value string) (bool, error) {
	_, err := a.q.UpsertEntityFieldValue(ctx, generated.UpsertEntityFieldValueParams{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   entityID,
		FieldSlug:  slug,
		Value:      value,
		SpaceID:    spaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("custom field value adapter upsert: %w", err)
	}
	return true, nil
}

// DeleteInSpace implements the repository interface. Zero rows affected is not
// an error: clearing an absent value is idempotent, and a delete refused by
// the space predicate must answer exactly as one that found nothing.
func (a *CustomFieldValueAdapter) DeleteInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug string) error {
	_, err := a.q.DeleteEntityFieldValue(ctx, generated.DeleteEntityFieldValueParams{
		EntityType: entityType,
		EntityID:   entityID,
		FieldSlug:  slug,
		SpaceID:    spaceID,
	})
	if err != nil {
		return fmt.Errorf("custom field value adapter delete: %w", err)
	}
	return nil
}

// CountByOrgSlug implements the repository interface. entity_field_values has
// no org column of its own: items carry org_id directly (migration 031),
// tickets and pages reach it through their space.
func (a *CustomFieldValueAdapter) CountByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (int, error) {
	n, err := a.q.CountEntityFieldValuesByOrgSlug(ctx, generated.CountEntityFieldValuesByOrgSlugParams{
		OrgID:     orgID,
		FieldSlug: slug,
	})
	if err != nil {
		return 0, fmt.Errorf("custom field value adapter count by org slug: %w", err)
	}
	return int(n), nil
}

// CustomFieldScopeAdapter implements customfields.ScopeRepository.
type CustomFieldScopeAdapter struct {
	q *generated.Queries
}

// NewCustomFieldScopeAdapter creates a CustomFieldScopeAdapter.
func NewCustomFieldScopeAdapter(q *generated.Queries) *CustomFieldScopeAdapter {
	return &CustomFieldScopeAdapter{q: q}
}

// ListByField implements the repository interface.
func (a *CustomFieldScopeAdapter) ListByField(ctx context.Context, fieldID uuid.UUID) ([]customfields.FieldScope, error) {
	rows, err := a.q.ListCustomFieldScopesByField(ctx, fieldID)
	if err != nil {
		return nil, fmt.Errorf("custom field scope adapter list by field: %w", err)
	}
	return dbScopesToScopes(rows), nil
}

// ListForSpaceEntity implements the repository interface.
func (a *CustomFieldScopeAdapter) ListForSpaceEntity(ctx context.Context, spaceID uuid.UUID, entityType string) ([]customfields.FieldScope, error) {
	rows, err := a.q.ListCustomFieldScopesForSpaceEntity(ctx, generated.ListCustomFieldScopesForSpaceEntityParams{
		SpaceID:    spaceID,
		EntityType: entityType,
	})
	if err != nil {
		return nil, fmt.Errorf("custom field scope adapter list for space: %w", err)
	}
	return dbScopesToScopes(rows), nil
}

// Get implements the repository interface.
func (a *CustomFieldScopeAdapter) Get(ctx context.Context, fieldID, spaceID uuid.UUID, entityType string) (*customfields.FieldScope, error) {
	row, err := a.q.GetCustomFieldScope(ctx, generated.GetCustomFieldScopeParams{
		FieldID:    fieldID,
		SpaceID:    spaceID,
		EntityType: entityType,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrScopeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field scope adapter get: %w", err)
	}
	s := dbScopeToScope(row)
	return &s, nil
}

// Upsert implements the repository interface. The org predicate is in the
// statement — a space outside orgID (or soft-deleted) proposes zero rows, and
// pgx.ErrNoRows on the RETURNING maps to ErrSpaceNotFound with nothing
// written.
func (a *CustomFieldScopeAdapter) Upsert(ctx context.Context, orgID uuid.UUID, scope *customfields.FieldScope) (*customfields.FieldScope, error) {
	row, err := a.q.UpsertCustomFieldScope(ctx, generated.UpsertCustomFieldScopeParams{
		ID:         uuid.New(),
		FieldID:    scope.FieldID,
		SpaceID:    scope.SpaceID,
		EntityType: scope.EntityType,
		Required:   scope.Required,
		Position:   int32(scope.Position), //nolint:gosec // small positive ordinal
		OrgID:      orgID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, customfields.ErrSpaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("custom field scope adapter upsert: %w", err)
	}
	s := dbScopeToScope(row)
	return &s, nil
}

// Delete implements the repository interface.
func (a *CustomFieldScopeAdapter) Delete(ctx context.Context, fieldID, spaceID uuid.UUID, entityType string) (bool, error) {
	n, err := a.q.DeleteCustomFieldScope(ctx, generated.DeleteCustomFieldScopeParams{
		FieldID:    fieldID,
		SpaceID:    spaceID,
		EntityType: entityType,
	})
	if err != nil {
		return false, fmt.Errorf("custom field scope adapter delete: %w", err)
	}
	return n > 0, nil
}

// SpaceOrgType implements the repository interface. GetSpaceByID already
// excludes soft-deleted spaces, so a deleted space answers exactly as a
// missing one.
func (a *CustomFieldScopeAdapter) SpaceOrgType(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, string, error) {
	row, err := a.q.GetSpaceByID(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", customfields.ErrSpaceNotFound
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("custom field scope adapter space lookup: %w", err)
	}
	return row.OrgID, row.Type, nil
}

func dbScopeToScope(r generated.CustomFieldScope) customfields.FieldScope {
	return customfields.FieldScope{
		FieldID:    r.FieldID,
		SpaceID:    r.SpaceID,
		EntityType: r.EntityType,
		Required:   r.Required,
		Position:   int(r.Position),
	}
}

func dbScopesToScopes(rows []generated.CustomFieldScope) []customfields.FieldScope {
	out := make([]customfields.FieldScope, len(rows))
	for i, r := range rows {
		out[i] = dbScopeToScope(r)
	}
	return out
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
