// Package customfields manages org-scoped custom field definitions, the
// per-space/per-entity-type scopes that attach them to forms, and the values
// entities hold under them. Values are polymorphic over tickets, project items
// and pages (migration 053, following 015's technique). Field slugs are
// immutable; values are stored by slug so they survive a definition's archival
// or deletion and can be surfaced read-only as "legacy" fields (zero silent
// data loss — migration 033's comment, this package, reconciliation D48).
//
// Requiredness deliberately lives on the scope row, never the definition:
// definitions are org-wide, and a required flag there would make the field
// required on every form in every space at once. A field is required for one
// (space, entity type) attachment at a time, and the requirement is enforced
// at the write, never retroactively — rows written before the flag stay
// readable and simply surface as incomplete.
package customfields

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
)

// Field types supported in this phase. No formula/computed/cascading fields.
const (
	TypeText         = "text"
	TypeNumber       = "number"
	TypeDate         = "date"
	TypeSingleSelect = "single_select"
)

var validTypes = map[string]bool{
	TypeText: true, TypeNumber: true, TypeDate: true, TypeSingleSelect: true,
}

// Entity type constants for polymorphic field values. They mirror the
// entity_field_values entity_type CHECK from migration 053 — the same
// three-value vocabulary comments and relations use (migration 015), even
// though no page field surface exists yet: one vocabulary, not two.
const (
	EntityTypeTicket      = "ticket"
	EntityTypeProjectItem = "project_item"
	EntityTypePage        = "page"
)

// ValidEntityTypes is the CHECK vocabulary. A value outside it reaches the
// database only to be rejected by the constraint, surfacing as a 500 rather
// than a 400 — so the service refuses it first.
var ValidEntityTypes = map[string]bool{
	EntityTypeTicket: true, EntityTypeProjectItem: true, EntityTypePage: true,
}

// scopeHostSpaceType maps an entity type to the space type that hosts its
// forms. It doubles as the set of entity types a field can be ATTACHED to:
// pages are in the value vocabulary (a page value, if one ever exists, renders
// and survives like any other) but have no field surface, so attaching a field
// to them would create scope rows nothing reads — a promise, not a feature.
var scopeHostSpaceType = map[string]string{
	EntityTypeProjectItem: "vector",
	EntityTypeTicket:      "beacon",
}

// Sentinel errors.
var (
	ErrNameRequired    = errors.New("name is required")
	ErrInvalidName     = errors.New("name must contain at least one letter or digit")
	ErrInvalidType     = errors.New("field type must be text, number, date, or single_select")
	ErrOptionsRequired = errors.New("a single-select field needs at least one option")
	ErrNotFound        = errors.New("custom field not found")
	ErrDuplicate       = errors.New("a custom field with this name already exists")
	ErrUndefinedField  = errors.New("no active custom field with this slug")
	ErrInvalidValue    = errors.New("value does not match the field type")

	// ErrInvalidEntityType refuses a value outside the migration 053 CHECK
	// vocabulary before it reaches the database as a constraint violation.
	ErrInvalidEntityType = errors.New("entity type must be ticket, project_item, or page")

	// ErrUnscopableEntityType refuses attaching a field to an entity type with
	// no field surface. Only the ATTACH path narrows to two of the three
	// vocabulary values; storage and rendering keep all three.
	ErrUnscopableEntityType = errors.New("fields can be attached to ticket or project_item forms only")

	// ErrScopeSpaceMismatch refuses an attachment whose entity type has no
	// forms in the named space — a ticket scope on a Vector space would be a
	// row nothing reads.
	ErrScopeSpaceMismatch = errors.New("this entity type has no forms in this space")

	// ErrSpaceNotFound covers a scope space that does not exist, is deleted,
	// or belongs to another organisation — one answer for all three, because
	// a distinguishable "exists but not yours" is an existence oracle.
	ErrSpaceNotFound = errors.New("space not found")

	// ErrScopeNotFound is a missing attachment row.
	ErrScopeNotFound = errors.New("this custom field is not attached to this space")

	// ErrOrderMismatch refuses a form reorder that is not a permutation of the
	// form: every attached field named exactly once, nothing else. Partial
	// orders are refused rather than merged because the caller is always
	// looking at the whole form — a request that disagrees with it is stale or
	// wrong, and half-applying it would leave an order nobody asked for.
	ErrOrderMismatch = errors.New("the new order must name every field attached to this form, each exactly once")

	// ErrFieldNotInScope refuses a value write for a field that is defined in
	// the org but not attached to this (space, entity type) form. Unscoped
	// fields are read-only here, exactly like legacy fields: their stored
	// values still render, they just cannot be written through this form.
	ErrFieldNotInScope = errors.New("this custom field is not attached to this space")

	// ErrEntityNotFound is a value write whose statement matched no entity —
	// outside the space, soft-deleted, wrong type, or never existed. One
	// error for all four; handlers answer it exactly as they answer an
	// unknown entity id.
	ErrEntityNotFound = errors.New("entity not found")

	// ErrValueRequired refuses clearing (or writing empty to) a field that a
	// scope marks required. Wrapped with the field's slug so the surface that
	// carries the form can render the refusal against the field itself.
	ErrValueRequired = errors.New("this field is required here")

	// ErrSlugHeldByLegacyValues is returned when a new definition's slug still
	// has values stored under it by a definition that was deleted. Values are
	// keyed by slug and outlive their definitions on purpose (migration 033),
	// so a new field with the same derived slug would adopt every one of them —
	// silently, and under a type they were never validated against.
	ErrSlugHeldByLegacyValues = errors.New("this name's slug still holds values from a deleted custom field")
)

// FieldDef is a custom field definition.
type FieldDef struct {
	ID         uuid.UUID  `json:"id"`
	OrgID      uuid.UUID  `json:"org_id"`
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Type       string     `json:"field_type"`
	Options    []string   `json:"options"`
	Position   int        `json:"position"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// FieldScope is one attachment of a field to a (space, entity type) form,
// carrying the per-attachment required flag and form position. Its identity
// is the triple, not a surrogate id — the row dies with its definition
// (CASCADE), unlike values, which survive theirs.
type FieldScope struct {
	FieldID    uuid.UUID `json:"field_id"`
	SpaceID    uuid.UUID `json:"space_id"`
	EntityType string    `json:"entity_type"`
	Required   bool      `json:"required"`
	Position   int       `json:"position"`
}

// StoredValue is a raw per-entity value keyed by field slug.
type StoredValue struct {
	FieldSlug string `json:"field_slug"`
	Value     string `json:"value"`
}

// RenderedField is a field as shown on an entity: a scoped active definition
// with its current value, or a stored value with no writable field on this
// form (read-only).
type RenderedField struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Type    string   `json:"field_type"`
	Options []string `json:"options"`
	Value   string   `json:"value"`
	// Required mirrors the scope row for this form. It is a write-side rule
	// surfaced for the form control; a pre-existing entity missing the value
	// still renders (with Value empty) — never an error, never a default.
	Required bool `json:"required"`
	// Legacy marks a value that cannot be written through this form: its
	// definition is archived or deleted, or is not attached to this
	// (space, entity type). Surfaced read-only so no data is silently dropped.
	Legacy bool `json:"legacy"`
}

// DefRepository is the data-access contract for field definitions.
type DefRepository interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*FieldDef, error)
	ListActiveByOrg(ctx context.Context, orgID uuid.UUID) ([]*FieldDef, error)
	GetByID(ctx context.Context, id uuid.UUID) (*FieldDef, error)
	GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (*FieldDef, error)
	Create(ctx context.Context, d *FieldDef) error
	Update(ctx context.Context, id uuid.UUID, name string, options []string) (*FieldDef, error)
	SetArchived(ctx context.Context, id uuid.UUID, archived bool) (*FieldDef, error)
	Delete(ctx context.Context, id uuid.UUID) error
	NextPosition(ctx context.Context, orgID uuid.UUID) (int, error)
}

// ValueRepository is the data-access contract for per-entity values.
//
// Every method that touches an entity's values carries the space the request
// named, and the SPACE PREDICATE IS IN THE STATEMENT — there is deliberately
// no method that writes by bare entity id. The predecessor of this interface
// documented that "the caller owes the write a space reconciliation"; that
// unenforced calling convention was the entire write-path authorization, and
// it held only because the one handler calling it happened to resolve the
// entity first. Now an upsert or delete addressed at an entity outside the
// space affects zero rows no matter who calls it.
type ValueRepository interface {
	// ListForEntityInSpace returns an entity's stored values, reconciled
	// against the space in the statement. An entity in another space returns
	// no values, exactly as an unknown entity does.
	ListForEntityInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID) ([]StoredValue, error)
	// UpsertInSpace writes one value. ok=false reports that the statement
	// matched no entity — outside the space, soft-deleted, wrong type, or
	// nonexistent, indistinguishably — and nothing was written.
	UpsertInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug, value string) (ok bool, err error)
	// DeleteInSpace clears one value, with the same in-statement
	// reconciliation. Deleting an absent value is not an error (clearing is
	// idempotent), and neither is a delete refused by the predicate — both
	// affect zero rows and disclose nothing.
	DeleteInSpace(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug string) error
	// CountByOrgSlug counts the org's live entities holding a value under
	// slug, across all entity types. It is what makes an orphaned legacy
	// value visible to a definition that does not exist yet.
	CountByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (int, error)
}

// ScopeRepository is the data-access contract for field scopes.
type ScopeRepository interface {
	ListByField(ctx context.Context, fieldID uuid.UUID) ([]FieldScope, error)
	// ListForSpaceEntity returns the scope rows governing one form, in form
	// order (position, then attach time).
	ListForSpaceEntity(ctx context.Context, spaceID uuid.UUID, entityType string) ([]FieldScope, error)
	// Get returns the attachment row for the triple, or ErrScopeNotFound.
	Get(ctx context.Context, fieldID, spaceID uuid.UUID, entityType string) (*FieldScope, error)
	// Upsert attaches (or re-flags) a field. The org predicate is in the
	// statement: a space outside orgID — or soft-deleted — matches nothing
	// and returns ErrSpaceNotFound. Position is kept on re-attach.
	Upsert(ctx context.Context, orgID uuid.UUID, scope *FieldScope) (*FieldScope, error)
	// Delete detaches a field; found=false reports there was no row.
	Delete(ctx context.Context, fieldID, spaceID uuid.UUID, entityType string) (found bool, err error)
	// Reorder assigns each listed field its 1-based position on one
	// (space, entity type) form, in list order, in a single statement. The
	// org predicate is in the statement, exactly as Upsert's is; a space
	// outside orgID matches zero rows and nothing is written. It touches
	// position only — never required.
	Reorder(ctx context.Context, orgID, spaceID uuid.UUID, entityType string, fieldIDs []uuid.UUID) (int64, error)
	// SpaceOrgType resolves a live space's org and type, or ErrSpaceNotFound.
	// It backs the attach-time validation that a scope's entity type has
	// forms in the named space at all.
	SpaceOrgType(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, string, error)
}

// Service holds custom-field business logic.
type Service struct {
	defs   DefRepository
	values ValueRepository
	scopes ScopeRepository
}

// NewService creates a custom-fields Service.
func NewService(defs DefRepository, values ValueRepository, scopes ScopeRepository) *Service {
	return &Service{defs: defs, values: values, scopes: scopes}
}

// ListDefs returns all definitions (active and archived) for an org.
func (s *Service) ListDefs(ctx context.Context, orgID uuid.UUID) ([]*FieldDef, error) {
	out, err := s.defs.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing custom field defs: %w", err)
	}
	return out, nil
}

// CreateDef defines a new custom field. The slug is derived once from the name
// and is immutable. A new definition starts with no scopes — it appears on no
// form until an admin attaches it to the spaces that want it. (Existing
// definitions were attached to every Vector space by migration 053's backfill,
// preserving the pre-scopes behaviour for them.)
func (s *Service) CreateDef(ctx context.Context, orgID uuid.UUID, name, fieldType string, options []string) (*FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if !validTypes[fieldType] {
		return nil, ErrInvalidType
	}
	options, err := normalizeOptions(fieldType, options)
	if err != nil {
		return nil, err
	}
	slug := itemtypes.Slugify(name)
	if slug == "" {
		return nil, ErrInvalidName
	}
	if err := s.slugIsFree(ctx, orgID, slug); err != nil {
		return nil, err
	}

	pos, err := s.defs.NextPosition(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("computing custom field position: %w", err)
	}
	d := &FieldDef{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: name, Type: fieldType, Options: options, Position: pos}
	if err := s.defs.Create(ctx, d); err != nil {
		return nil, fmt.Errorf("creating custom field def: %w", err)
	}
	return d, nil
}

// slugIsFree reports whether a new definition may take this slug, and refuses
// with a message that names what is in the way.
//
// Two things can hold a slug, and only one of them is a definition.
//
// S12. Deleting a definition deliberately leaves its values behind as
// read-only legacy fields (migration 033, zero silent data loss), and they are
// keyed by slug. A new field whose name derives to the same slug would adopt
// every one of them: values entered under a different field's meaning, and
// possibly a different type, appearing as this field's values — already
// populated, and never subjected to the validation this field's type implies.
// Nothing anywhere would report it.
func (s *Service) slugIsFree(ctx context.Context, orgID uuid.UUID, slug string) error {
	if _, err := s.defs.GetByOrgSlug(ctx, orgID, slug); err == nil {
		return ErrDuplicate
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("checking custom field slug: %w", err)
	}

	orphans, err := s.values.CountByOrgSlug(ctx, orgID, slug)
	if err != nil {
		return fmt.Errorf("checking legacy values for custom field slug: %w", err)
	}
	if orphans > 0 {
		return fmt.Errorf("%w: %d item(s) still hold values under %q — "+
			"choose a different name, or clear those legacy values first",
			ErrSlugHeldByLegacyValues, orphans, slug)
	}
	return nil
}

// UpdateDef renames a field and/or replaces its select options. The slug and
// type are immutable.
func (s *Service) UpdateDef(ctx context.Context, orgID, id uuid.UUID, name string, options []string) (*FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	def, err := s.getOwned(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	options, err = normalizeOptions(def.Type, options)
	if err != nil {
		return nil, err
	}
	updated, err := s.defs.Update(ctx, id, name, options)
	if err != nil {
		return nil, fmt.Errorf("updating custom field def: %w", err)
	}
	return updated, nil
}

// SetDefArchived archives or unarchives a field. Archiving keeps existing
// values AND scope rows but removes the field from entity forms (values become
// legacy read-only); unarchiving restores it to exactly the forms its scopes
// name.
func (s *Service) SetDefArchived(ctx context.Context, orgID, id uuid.UUID, archived bool) (*FieldDef, error) {
	if _, err := s.getOwned(ctx, orgID, id); err != nil {
		return nil, err
	}
	d, err := s.defs.SetArchived(ctx, id, archived)
	if err != nil {
		return nil, fmt.Errorf("archiving custom field def: %w", err)
	}
	return d, nil
}

// DeleteDef removes a definition. Its scope rows die with it (CASCADE — an
// attachment of nothing governs nothing); its stored values are intentionally
// left behind and surfaced read-only as legacy fields — no silent data loss.
func (s *Service) DeleteDef(ctx context.Context, orgID, id uuid.UUID) error {
	if _, err := s.getOwned(ctx, orgID, id); err != nil {
		return err
	}
	if err := s.defs.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting custom field def: %w", err)
	}
	return nil
}

// ListScopes returns every attachment of one field, for the admin surface.
func (s *Service) ListScopes(ctx context.Context, orgID, fieldID uuid.UUID) ([]FieldScope, error) {
	if _, err := s.getOwned(ctx, orgID, fieldID); err != nil {
		return nil, err
	}
	out, err := s.scopes.ListByField(ctx, fieldID)
	if err != nil {
		return nil, fmt.Errorf("listing custom field scopes: %w", err)
	}
	return out, nil
}

// resolveFormSpace validates that (spaceID, entityType) names a form this org
// can have: the entity type is attachable at all, the space is the org's own,
// and the space's module hosts this entity type's forms. Shared by SetScope
// and the form-order surface, so the two cannot drift on what "a form" means.
func (s *Service) resolveFormSpace(ctx context.Context, orgID, spaceID uuid.UUID, entityType string) error {
	hostType, ok := scopeHostSpaceType[entityType]
	if !ok {
		if ValidEntityTypes[entityType] {
			return ErrUnscopableEntityType
		}
		return ErrInvalidEntityType
	}
	spaceOrg, spaceType, err := s.scopes.SpaceOrgType(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("resolving scope space: %w", err)
	}
	// Another org's space answers exactly as a nonexistent one — the org
	// check happens before the type check so a mismatch message cannot become
	// a probe for what kind of space a foreign id is.
	if spaceOrg != orgID {
		return fmt.Errorf("resolving scope space: %w", ErrSpaceNotFound)
	}
	if spaceType != hostType {
		return fmt.Errorf("%w: %s forms live in %s spaces and this space is %s",
			ErrScopeSpaceMismatch, entityType, hostType, spaceType)
	}
	return nil
}

// SetScope attaches a field to a (space, entity type) form, or updates the
// attachment's required flag. Position is taken from the definition on first
// attach — the same rule migration 053's backfill applied — and kept on
// re-attach, so toggling required does not reshuffle the form.
func (s *Service) SetScope(ctx context.Context, orgID, fieldID, spaceID uuid.UUID, entityType string, required bool) (*FieldScope, error) {
	def, err := s.getOwned(ctx, orgID, fieldID)
	if err != nil {
		return nil, err
	}
	if err := s.resolveFormSpace(ctx, orgID, spaceID, entityType); err != nil {
		return nil, err
	}

	scope := &FieldScope{
		FieldID:    def.ID,
		SpaceID:    spaceID,
		EntityType: entityType,
		Required:   required,
		Position:   def.Position,
	}
	saved, err := s.scopes.Upsert(ctx, orgID, scope)
	if err != nil {
		return nil, fmt.Errorf("saving custom field scope: %w", err)
	}
	return saved, nil
}

// ListFormScopes returns the scope rows governing one form — this space, this
// entity type — in form order. It is the read the admin ordering surface edits
// against; entity pages keep consuming the composed RenderForEntity, which
// carries no space ids beyond the URL's own.
func (s *Service) ListFormScopes(ctx context.Context, orgID, spaceID uuid.UUID, entityType string) ([]FieldScope, error) {
	if err := s.resolveFormSpace(ctx, orgID, spaceID, entityType); err != nil {
		return nil, err
	}
	out, err := s.scopes.ListForSpaceEntity(ctx, spaceID, entityType)
	if err != nil {
		return nil, fmt.Errorf("listing form scopes: %w", err)
	}
	return out, nil
}

// ReorderForm rewrites one form's field order to exactly fieldIDs, first to
// last. The request must be a permutation of the form — every attached field
// named exactly once — or it is refused whole (ErrOrderMismatch): the caller
// is always looking at the entire form, so a request that disagrees with it
// is stale, and half-applying it would leave an order nobody asked for.
//
// Ordering is the one scope property this writes. The required flag is
// untouched by construction — the statement never mentions it — which is the
// converse of the upsert's pin that toggling required keeps position.
func (s *Service) ReorderForm(ctx context.Context, orgID, spaceID uuid.UUID, entityType string, fieldIDs []uuid.UUID) ([]FieldScope, error) {
	if err := s.resolveFormSpace(ctx, orgID, spaceID, entityType); err != nil {
		return nil, err
	}
	current, err := s.scopes.ListForSpaceEntity(ctx, spaceID, entityType)
	if err != nil {
		return nil, fmt.Errorf("listing form scopes: %w", err)
	}
	if len(fieldIDs) != len(current) {
		return nil, ErrOrderMismatch
	}
	attached := make(map[uuid.UUID]bool, len(current))
	for _, sc := range current {
		attached[sc.FieldID] = true
	}
	for _, id := range fieldIDs {
		// !attached covers both an id never on this form and a duplicate (the
		// first occurrence consumes the entry) — with the length check above,
		// either means the request is not a permutation of the form.
		if !attached[id] {
			return nil, ErrOrderMismatch
		}
		delete(attached, id)
	}

	if _, err := s.scopes.Reorder(ctx, orgID, spaceID, entityType, fieldIDs); err != nil {
		return nil, fmt.Errorf("reordering form scopes: %w", err)
	}
	// Re-list rather than fabricate: the response is what the statement left
	// behind, including any row a concurrent detach removed mid-flight.
	out, err := s.scopes.ListForSpaceEntity(ctx, spaceID, entityType)
	if err != nil {
		return nil, fmt.Errorf("listing form scopes: %w", err)
	}
	return out, nil
}

// RemoveScope detaches a field from a (space, entity type) form. Values
// entities hold under it are untouched — they surface read-only as legacy
// until the field is re-attached.
func (s *Service) RemoveScope(ctx context.Context, orgID, fieldID, spaceID uuid.UUID, entityType string) error {
	if _, err := s.getOwned(ctx, orgID, fieldID); err != nil {
		return err
	}
	found, err := s.scopes.Delete(ctx, fieldID, spaceID, entityType)
	if err != nil {
		return fmt.Errorf("removing custom field scope: %w", err)
	}
	if !found {
		return ErrScopeNotFound
	}
	return nil
}

// RenderForEntity composes the fields shown on an entity: every active
// definition attached to this (space, entity type) form, in form order, with
// its current value and required flag — followed by any stored value that
// cannot be written through this form (definition archived, deleted, or not
// attached here), read-only.
//
// A required field with no stored value renders with an empty value and
// Required true. That is the never-retroactively rule on the read side: rows
// that predate the flag are incomplete, not erroneous.
//
// spaceID is the space the request named, and the values are read through it
// in the statement. An entity outside the space contributes no values, which
// is what an entity that has never been given a value contributes.
func (s *Service) RenderForEntity(ctx context.Context, orgID, spaceID uuid.UUID, entityType string, entityID uuid.UUID) ([]RenderedField, error) {
	if !ValidEntityTypes[entityType] {
		return nil, ErrInvalidEntityType
	}
	defs, err := s.defs.ListActiveByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing active defs: %w", err)
	}
	scopes, err := s.scopes.ListForSpaceEntity(ctx, spaceID, entityType)
	if err != nil {
		return nil, fmt.Errorf("listing field scopes: %w", err)
	}
	stored, err := s.values.ListForEntityInSpace(ctx, spaceID, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("listing entity values: %w", err)
	}
	return composeRendered(defs, scopes, stored), nil
}

// composeRendered merges one form's scope rows, the org's active definitions
// and one entity's stored values into the rendered field list: scoped active
// definitions first, in form order, then every stored value the form cannot
// write, read-only.
func composeRendered(defs []*FieldDef, scopes []FieldScope, stored []StoredValue) []RenderedField {
	defByID := make(map[uuid.UUID]*FieldDef, len(defs))
	defBySlug := make(map[string]*FieldDef, len(defs))
	for _, d := range defs {
		defByID[d.ID] = d
		defBySlug[d.Slug] = d
	}
	valueBySlug := make(map[string]string, len(stored))
	for _, v := range stored {
		valueBySlug[v.FieldSlug] = v.Value
	}

	out := make([]RenderedField, 0, len(scopes)+len(stored))
	onForm := make(map[string]bool, len(scopes))
	for _, sc := range scopes {
		d, ok := defByID[sc.FieldID]
		if !ok {
			continue // archived definition: its value, if any, renders legacy below
		}
		onForm[d.Slug] = true
		out = append(out, RenderedField{
			Slug: d.Slug, Name: d.Name, Type: d.Type, Options: d.Options,
			Value: valueBySlug[d.Slug], Required: sc.Required, Legacy: false,
		})
	}
	for _, v := range stored {
		if onForm[v.FieldSlug] {
			continue
		}
		// Read-only value. If an active definition still exists (field defined
		// but not attached here), render its real name and type; otherwise the
		// slug stands in, as it always has for deleted definitions.
		name, fieldType, options := v.FieldSlug, TypeText, []string{}
		if d, ok := defBySlug[v.FieldSlug]; ok {
			name, fieldType, options = d.Name, d.Type, d.Options
		}
		out = append(out, RenderedField{
			Slug: v.FieldSlug, Name: name, Type: fieldType,
			Options: options, Value: v.Value, Legacy: true,
		})
	}
	return out
}

// SetValue writes an entity's value for a field attached to this
// (space, entity type) form, validating it against the field type. An empty
// value clears the field — unless the scope marks it required, in which case
// the clear is refused with an error naming the field: required is enforced at
// the write, and a required field cannot be written back to absent. Values for
// undefined, archived, or unattached fields cannot be written — they are
// read-only (ErrUndefinedField / ErrFieldNotInScope).
//
// The space predicate travels INTO the statement (UpsertEntityFieldValue /
// DeleteEntityFieldValue): an entity outside spaceID matches zero rows and
// returns ErrEntityNotFound, which callers answer exactly as an unknown id.
// Handlers still resolve the entity first for their permission checks; the
// statement no longer trusts them to have done so.
func (s *Service) SetValue(ctx context.Context, orgID, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug, value string) error {
	if !ValidEntityTypes[entityType] {
		return ErrInvalidEntityType
	}
	def, scope, err := s.writableField(ctx, orgID, spaceID, entityType, slug)
	if err != nil {
		return err
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return s.clearValue(ctx, spaceID, entityType, entityID, slug, scope)
	}
	if err := validateValue(def, value); err != nil {
		return err
	}
	ok, err := s.values.UpsertInSpace(ctx, spaceID, entityType, entityID, slug, value)
	if err != nil {
		return fmt.Errorf("saving custom field value: %w", err)
	}
	if !ok {
		return ErrEntityNotFound
	}
	return nil
}

// writableField resolves the definition and attachment a value write goes
// through: the field must exist, be active, and be attached to this
// (space, entity type) form — anything else is read-only here.
func (s *Service) writableField(ctx context.Context, orgID, spaceID uuid.UUID, entityType, slug string) (*FieldDef, *FieldScope, error) {
	def, err := s.defs.GetByOrgSlug(ctx, orgID, slug)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, ErrUndefinedField
	}
	if err != nil {
		return nil, nil, fmt.Errorf("resolving custom field: %w", err)
	}
	if def.ArchivedAt != nil {
		return nil, nil, ErrUndefinedField
	}
	scope, err := s.scopes.Get(ctx, def.ID, spaceID, entityType)
	if errors.Is(err, ErrScopeNotFound) {
		return nil, nil, ErrFieldNotInScope
	}
	if err != nil {
		return nil, nil, fmt.Errorf("resolving custom field scope: %w", err)
	}
	return def, scope, nil
}

// clearValue deletes a stored value — unless the attachment marks the field
// required, in which case the write that would leave it absent is refused,
// naming the field.
func (s *Service) clearValue(ctx context.Context, spaceID uuid.UUID, entityType string, entityID uuid.UUID, slug string, scope *FieldScope) error {
	if scope.Required {
		return fmt.Errorf("%w: %q must have a value on this form — detach it or clear its required flag first", ErrValueRequired, slug)
	}
	if err := s.values.DeleteInSpace(ctx, spaceID, entityType, entityID, slug); err != nil {
		return fmt.Errorf("clearing custom field value: %w", err)
	}
	return nil
}

func (s *Service) getOwned(ctx context.Context, orgID, id uuid.UUID) (*FieldDef, error) {
	d, err := s.defs.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting custom field: %w", err)
	}
	if d.OrgID != orgID {
		return nil, ErrNotFound
	}
	return d, nil
}

// normalizeOptions cleans and validates a field's options for its type: only
// single_select carries options and it requires at least one; other types have
// none.
func normalizeOptions(fieldType string, options []string) ([]string, error) {
	options = cleanOptions(options)
	if fieldType != TypeSingleSelect {
		return []string{}, nil
	}
	if len(options) == 0 {
		return nil, ErrOptionsRequired
	}
	return options, nil
}

func validateValue(def *FieldDef, value string) error {
	switch def.Type {
	case TypeNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("%w: expected a number", ErrInvalidValue)
		}
	case TypeDate:
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return fmt.Errorf("%w: expected a date (YYYY-MM-DD)", ErrInvalidValue)
		}
	case TypeSingleSelect:
		for _, opt := range def.Options {
			if opt == value {
				return nil
			}
		}
		return fmt.Errorf("%w: not one of the allowed options", ErrInvalidValue)
	case TypeText:
		// any string is valid
	}
	return nil
}

func cleanOptions(options []string) []string {
	out := make([]string, 0, len(options))
	seen := map[string]bool{}
	for _, o := range options {
		o = strings.TrimSpace(o)
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	return out
}
