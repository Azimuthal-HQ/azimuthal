// Package customfields manages org-scoped custom field definitions over Vector
// project items and their per-item values. Field slugs are immutable; values
// are stored by slug so they survive a definition's archival or deletion and can
// be surfaced read-only as "legacy" fields (zero silent data loss).
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

// StoredValue is a raw per-item value keyed by field slug.
type StoredValue struct {
	FieldSlug string `json:"field_slug"`
	Value     string `json:"value"`
}

// RenderedField is a field as shown on an item: an active definition with its
// current value, or a legacy value whose definition is gone (read-only).
type RenderedField struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Type    string   `json:"field_type"`
	Options []string `json:"options"`
	Value   string   `json:"value"`
	// Legacy marks a value with no active definition — surfaced read-only so no
	// data is silently dropped.
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

// ValueRepository is the data-access contract for per-item values.
type ValueRepository interface {
	// ListByItemInSpace returns an item's stored values, reconciled against the
	// space the request named. The route that reaches this proved the caller may
	// read {spaceID} and proved nothing whatever about {itemID}, so reading by
	// item id alone surfaced every item's custom-field values in the
	// installation, across organizations included. An item in another space
	// returns no values, exactly as an unknown item does.
	ListByItemInSpace(ctx context.Context, spaceID, itemID uuid.UUID) ([]StoredValue, error)
	// Upsert and Delete write by bare item id: item_field_values has no space
	// column and the upsert conflicts on (item_id, field_slug). The space
	// reconciliation for the write path therefore happens before the call —
	// SetItemField resolves the item through the space first, and refuses with
	// the item's own 404 when it belongs to another one.
	Upsert(ctx context.Context, itemID uuid.UUID, slug, value string) error
	Delete(ctx context.Context, itemID uuid.UUID, slug string) error
	// CountByOrgSlug counts the org's live items holding a value under slug.
	// It is what makes an orphaned legacy value visible to a definition that
	// does not exist yet.
	CountByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (int, error)
}

// Service holds custom-field business logic.
type Service struct {
	defs   DefRepository
	values ValueRepository
}

// NewService creates a custom-fields Service.
func NewService(defs DefRepository, values ValueRepository) *Service {
	return &Service{defs: defs, values: values}
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
// and is immutable.
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
// values but removes the field from item forms (they become legacy read-only).
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

// DeleteDef removes a definition. Stored values are intentionally left behind
// and surfaced read-only as legacy fields — no silent data loss.
func (s *Service) DeleteDef(ctx context.Context, orgID, id uuid.UUID) error {
	if _, err := s.getOwned(ctx, orgID, id); err != nil {
		return err
	}
	if err := s.defs.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting custom field def: %w", err)
	}
	return nil
}

// RenderForItem composes the fields shown on an item: every active definition
// (with its current value), followed by any stored value whose definition is no
// longer active — the legacy, read-only fields.
//
// spaceID is the space the request named, and the values are read through it.
// The route proved that space readable; it proved nothing about itemID, so an
// item id on its own was enough to read another space's — or another org's —
// custom-field values. An item outside the space renders the org's active
// definitions with empty values and no legacy fields, which is what an item
// that has never been given a value renders.
func (s *Service) RenderForItem(ctx context.Context, orgID, spaceID, itemID uuid.UUID) ([]RenderedField, error) {
	defs, err := s.defs.ListActiveByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing active defs: %w", err)
	}
	stored, err := s.values.ListByItemInSpace(ctx, spaceID, itemID)
	if err != nil {
		return nil, fmt.Errorf("listing item values: %w", err)
	}
	valueBySlug := make(map[string]string, len(stored))
	for _, v := range stored {
		valueBySlug[v.FieldSlug] = v.Value
	}

	out := make([]RenderedField, 0, len(defs)+len(stored))
	activeSlugs := make(map[string]bool, len(defs))
	for _, d := range defs {
		activeSlugs[d.Slug] = true
		out = append(out, RenderedField{
			Slug: d.Slug, Name: d.Name, Type: d.Type, Options: d.Options,
			Value: valueBySlug[d.Slug], Legacy: false,
		})
	}
	for _, v := range stored {
		if !activeSlugs[v.FieldSlug] {
			out = append(out, RenderedField{
				Slug: v.FieldSlug, Name: v.FieldSlug, Type: TypeText,
				Options: []string{}, Value: v.Value, Legacy: true,
			})
		}
	}
	return out, nil
}

// SetValue writes an item's value for an active field, validating it against the
// field type. An empty value clears the field. Values for undefined/archived
// (legacy) fields cannot be written — they are read-only (ErrUndefinedField).
//
// itemID arrives without a space deliberately, and the caller owes it one: the
// write is keyed on (item_id, field_slug) and there is no space column to test
// against, so this cannot reconcile the item itself. Every caller must resolve
// the item through the request's space before calling — SetItemField does, via
// ItemService.GetItemInSpace, which is the same read its permission gate needs.
func (s *Service) SetValue(ctx context.Context, orgID, itemID uuid.UUID, slug, value string) error {
	def, err := s.defs.GetByOrgSlug(ctx, orgID, slug)
	if errors.Is(err, ErrNotFound) {
		return ErrUndefinedField
	}
	if err != nil {
		return fmt.Errorf("resolving custom field: %w", err)
	}
	if def.ArchivedAt != nil {
		return ErrUndefinedField
	}

	value = strings.TrimSpace(value)
	if value == "" {
		if err := s.values.Delete(ctx, itemID, slug); err != nil {
			return fmt.Errorf("clearing custom field value: %w", err)
		}
		return nil
	}
	if err := validateValue(def, value); err != nil {
		return err
	}
	if err := s.values.Upsert(ctx, itemID, slug, value); err != nil {
		return fmt.Errorf("saving custom field value: %w", err)
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
