package customfields

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type stubDefRepo struct {
	byID   map[uuid.UUID]*FieldDef
	bySlug map[string]*FieldDef
	pos    int
}

func newStubDefRepo() *stubDefRepo {
	return &stubDefRepo{byID: map[uuid.UUID]*FieldDef{}, bySlug: map[string]*FieldDef{}}
}

func dkey(o uuid.UUID, s string) string { return o.String() + "|" + s }

func (r *stubDefRepo) ListByOrg(_ context.Context, orgID uuid.UUID) ([]*FieldDef, error) {
	var out []*FieldDef
	for _, d := range r.byID {
		if d.OrgID == orgID {
			out = append(out, d)
		}
	}
	return out, nil
}
func (r *stubDefRepo) ListActiveByOrg(_ context.Context, orgID uuid.UUID) ([]*FieldDef, error) {
	var out []*FieldDef
	for _, d := range r.byID {
		if d.OrgID == orgID && d.ArchivedAt == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
func (r *stubDefRepo) GetByID(_ context.Context, id uuid.UUID) (*FieldDef, error) {
	if d, ok := r.byID[id]; ok {
		return d, nil
	}
	return nil, ErrNotFound
}
func (r *stubDefRepo) GetByOrgSlug(_ context.Context, orgID uuid.UUID, slug string) (*FieldDef, error) {
	if d, ok := r.bySlug[dkey(orgID, slug)]; ok {
		return d, nil
	}
	return nil, ErrNotFound
}
func (r *stubDefRepo) Create(_ context.Context, d *FieldDef) error {
	r.byID[d.ID] = d
	r.bySlug[dkey(d.OrgID, d.Slug)] = d
	return nil
}
func (r *stubDefRepo) Update(_ context.Context, id uuid.UUID, name string, options []string) (*FieldDef, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	d.Name = name
	d.Options = options
	return d, nil
}
func (r *stubDefRepo) SetArchived(_ context.Context, id uuid.UUID, archived bool) (*FieldDef, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	if archived {
		now := d.CreatedAt
		d.ArchivedAt = &now
	} else {
		d.ArchivedAt = nil
	}
	return d, nil
}
func (r *stubDefRepo) Delete(_ context.Context, id uuid.UUID) error {
	d, ok := r.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(r.byID, id)
	delete(r.bySlug, dkey(d.OrgID, d.Slug))
	return nil
}
func (r *stubDefRepo) NextPosition(_ context.Context, _ uuid.UUID) (int, error) {
	r.pos++
	return r.pos, nil
}

type stubValueRepo struct {
	values map[string]string // itemID|slug → value
}

func newStubValueRepo() *stubValueRepo  { return &stubValueRepo{values: map[string]string{}} }
func vkey(i uuid.UUID, s string) string { return i.String() + "|" + s }

// ListByItemInSpace ignores the space: the stub is keyed by item alone, and
// modelling the reconciliation here would be modelling it in the one place it
// is not implemented. The real join is proved against a real database in
// internal/db/adapters; what this stub models faithfully is the composition
// RenderForItem performs over whatever values come back.
func (r *stubValueRepo) ListByItemInSpace(_ context.Context, _, itemID uuid.UUID) ([]StoredValue, error) {
	var out []StoredValue
	for k, v := range r.values {
		if len(k) > 37 && k[:36] == itemID.String() {
			out = append(out, StoredValue{FieldSlug: k[37:], Value: v})
		}
	}
	return out, nil
}
func (r *stubValueRepo) Upsert(_ context.Context, itemID uuid.UUID, slug, value string) error {
	r.values[vkey(itemID, slug)] = value
	return nil
}
func (r *stubValueRepo) Delete(_ context.Context, itemID uuid.UUID, slug string) error {
	delete(r.values, vkey(itemID, slug))
	return nil
}

// CountByOrgSlug counts every stored value under slug. The stub holds one org,
// so org scoping is the real repository's job (proved against a real database
// in internal/db/adapters); what this models faithfully is the fact the guard
// turns on — that values survive their definitions and are found by slug.
func (r *stubValueRepo) CountByOrgSlug(_ context.Context, _ uuid.UUID, slug string) (int, error) {
	n := 0
	for k := range r.values {
		if len(k) > 37 && k[37:] == slug {
			n++
		}
	}
	return n, nil
}

func TestCreateDef_Validation(t *testing.T) {
	svc := NewService(newStubDefRepo(), newStubValueRepo())
	org := uuid.New()

	d, err := svc.CreateDef(context.Background(), org, "Story Points", TypeNumber, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	if d.Slug != "story_points" || d.Type != TypeNumber {
		t.Errorf("unexpected def: %+v", d)
	}

	// Duplicate name/slug → ErrDuplicate.
	if _, err := svc.CreateDef(context.Background(), org, "story points", TypeText, nil); !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
	// Invalid type.
	if _, err := svc.CreateDef(context.Background(), org, "X", "formula", nil); !errors.Is(err, ErrInvalidType) {
		t.Errorf("expected ErrInvalidType, got %v", err)
	}
	// single_select needs options.
	if _, err := svc.CreateDef(context.Background(), org, "Team", TypeSingleSelect, nil); !errors.Is(err, ErrOptionsRequired) {
		t.Errorf("expected ErrOptionsRequired, got %v", err)
	}
	// Empty name.
	if _, err := svc.CreateDef(context.Background(), org, "  ", TypeText, nil); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestSetValue_TypeValidation(t *testing.T) {
	defs, vals := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, vals)
	org, item := uuid.New(), uuid.New()

	num, _ := svc.CreateDef(context.Background(), org, "Points", TypeNumber, nil)
	sel, _ := svc.CreateDef(context.Background(), org, "Tier", TypeSingleSelect, []string{"gold", "silver"})
	dt, _ := svc.CreateDef(context.Background(), org, "Target", TypeDate, nil)

	// Number: rejects non-numeric, accepts numeric.
	if err := svc.SetValue(context.Background(), org, item, num.Slug, "abc"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("number reject: got %v", err)
	}
	if err := svc.SetValue(context.Background(), org, item, num.Slug, "13"); err != nil {
		t.Errorf("number accept: %v", err)
	}
	// Select: rejects out-of-set, accepts in-set.
	if err := svc.SetValue(context.Background(), org, item, sel.Slug, "bronze"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("select reject: got %v", err)
	}
	if err := svc.SetValue(context.Background(), org, item, sel.Slug, "gold"); err != nil {
		t.Errorf("select accept: %v", err)
	}
	// Date: rejects bad format.
	if err := svc.SetValue(context.Background(), org, item, dt.Slug, "not-a-date"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("date reject: got %v", err)
	}
	if err := svc.SetValue(context.Background(), org, item, dt.Slug, "2026-07-23"); err != nil {
		t.Errorf("date accept: %v", err)
	}

	// Undefined field → ErrUndefinedField.
	if err := svc.SetValue(context.Background(), org, item, "nonexistent", "x"); !errors.Is(err, ErrUndefinedField) {
		t.Errorf("undefined field: got %v", err)
	}

	// Empty value clears.
	if err := svc.SetValue(context.Background(), org, item, num.Slug, ""); err != nil {
		t.Errorf("clear: %v", err)
	}
	if _, ok := vals.values[vkey(item, num.Slug)]; ok {
		t.Error("empty value must clear the stored value")
	}
}

// TestLegacyValuesSurvive is the zero-data-loss guarantee: a value whose
// definition is archived or deleted remains stored, renders read-only as
// legacy, and cannot be overwritten through the write path.
func TestLegacyValuesSurvive(t *testing.T) {
	defs, vals := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, vals)
	org, space, item := uuid.New(), uuid.New(), uuid.New()

	def, _ := svc.CreateDef(context.Background(), org, "Squad", TypeText, nil)
	if err := svc.SetValue(context.Background(), org, item, def.Slug, "Falcon"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Archive the definition.
	if _, err := svc.SetDefArchived(context.Background(), org, def.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	rendered, err := svc.RenderForItem(context.Background(), org, space, item)
	if err != nil {
		t.Fatalf("RenderForItem: %v", err)
	}
	var legacy *RenderedField
	for i := range rendered {
		if rendered[i].Slug == def.Slug {
			legacy = &rendered[i]
		}
	}
	if legacy == nil {
		t.Fatal("archived field's value must still render (legacy), not vanish")
	}
	if !legacy.Legacy || legacy.Value != "Falcon" {
		t.Errorf("expected legacy read-only value 'Falcon', got %+v", legacy)
	}

	// Writes to the now-legacy field are refused.
	if err := svc.SetValue(context.Background(), org, item, def.Slug, "Hawk"); !errors.Is(err, ErrUndefinedField) {
		t.Errorf("legacy field must be read-only, got %v", err)
	}

	// Deleting the definition entirely still preserves the value.
	if _, err := svc.SetDefArchived(context.Background(), org, def.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if err := svc.DeleteDef(context.Background(), org, def.ID); err != nil {
		t.Fatalf("delete def: %v", err)
	}
	rendered, _ = svc.RenderForItem(context.Background(), org, space, item)
	found := false
	for _, f := range rendered {
		if f.Slug == def.Slug {
			found = true
			if !f.Legacy || f.Value != "Falcon" {
				t.Errorf("deleted-def value must remain read-only 'Falcon', got %+v", f)
			}
		}
	}
	if !found {
		t.Error("value must survive definition deletion (no silent data loss)")
	}
}

func TestRenderForItem_ActiveWithValues(t *testing.T) {
	defs, vals := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, vals)
	org, space, item := uuid.New(), uuid.New(), uuid.New()

	a, _ := svc.CreateDef(context.Background(), org, "Alpha", TypeText, nil)
	_, _ = svc.CreateDef(context.Background(), org, "Beta", TypeText, nil)
	_ = svc.SetValue(context.Background(), org, item, a.Slug, "hello")

	rendered, err := svc.RenderForItem(context.Background(), org, space, item)
	if err != nil {
		t.Fatalf("RenderForItem: %v", err)
	}
	if len(rendered) != 2 {
		t.Fatalf("expected 2 active fields, got %d", len(rendered))
	}
	for _, f := range rendered {
		if f.Legacy {
			t.Errorf("active field %s should not be legacy", f.Slug)
		}
		if f.Slug == a.Slug && f.Value != "hello" {
			t.Errorf("expected value 'hello', got %q", f.Value)
		}
	}
}

func TestUpdateDef_Branches(t *testing.T) {
	defs, vals := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, vals)
	org := uuid.New()

	sel, err := svc.CreateDef(context.Background(), org, "Tier", TypeSingleSelect, []string{"gold"})
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}

	// Rename + replace options — slug and type are immutable.
	updated, err := svc.UpdateDef(context.Background(), org, sel.ID, "Level", []string{"a", "b"})
	if err != nil {
		t.Fatalf("UpdateDef: %v", err)
	}
	if updated.Name != "Level" || updated.Slug != "tier" || updated.Type != TypeSingleSelect {
		t.Errorf("unexpected update: %+v", updated)
	}
	if len(updated.Options) != 2 {
		t.Errorf("expected 2 options, got %v", updated.Options)
	}

	// A single_select cannot be left without options.
	if _, err := svc.UpdateDef(context.Background(), org, sel.ID, "Level", nil); !errors.Is(err, ErrOptionsRequired) {
		t.Errorf("expected ErrOptionsRequired, got %v", err)
	}
	// Empty name → ErrNameRequired.
	if _, err := svc.UpdateDef(context.Background(), org, sel.ID, "  ", []string{"a"}); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
	// Cross-org id → ErrNotFound.
	if _, err := svc.UpdateDef(context.Background(), uuid.New(), sel.ID, "X", []string{"a"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-org: expected ErrNotFound, got %v", err)
	}

	// A text field's options are always dropped on update.
	txt, _ := svc.CreateDef(context.Background(), org, "Squad", TypeText, nil)
	tu, err := svc.UpdateDef(context.Background(), org, txt.ID, "Team", []string{"ignored"})
	if err != nil {
		t.Fatalf("UpdateDef text: %v", err)
	}
	if len(tu.Options) != 0 {
		t.Errorf("text field must carry no options, got %v", tu.Options)
	}
}

func TestListDefs(t *testing.T) {
	defs, vals := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, vals)
	org := uuid.New()
	_, _ = svc.CreateDef(context.Background(), org, "A", TypeText, nil)
	_, _ = svc.CreateDef(context.Background(), org, "B", TypeText, nil)

	list, err := svc.ListDefs(context.Background(), org)
	if err != nil {
		t.Fatalf("ListDefs: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 defs, got %d", len(list))
	}
}

// S12 — a new definition may not reuse a slug that still holds legacy values.
//
// Deleting a definition deliberately leaves its values behind, keyed by slug,
// so nothing is silently lost. The consequence nobody had guarded: a new field
// whose name derives to the same slug adopts all of them at once, under a type
// they were never validated against.
func TestCreateDef_RefusesSlugStillHeldByLegacyValues(t *testing.T) {
	defs, values := newStubDefRepo(), newStubValueRepo()
	svc := NewService(defs, values)
	ctx := context.Background()
	org, item := uuid.New(), uuid.New()

	d, err := svc.CreateDef(ctx, org, "Story Points", TypeNumber, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	if err := svc.SetValue(ctx, org, item, "story_points", "8"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if err := svc.DeleteDef(ctx, org, d.ID); err != nil {
		t.Fatalf("DeleteDef: %v", err)
	}

	// The value is still there — that is the design, not the defect.
	if got, _ := values.CountByOrgSlug(ctx, org, "story_points"); got != 1 {
		t.Fatalf("precondition: expected the legacy value to survive, got %d", got)
	}

	_, err = svc.CreateDef(ctx, org, "story points", TypeSingleSelect, []string{"gold"})
	if !errors.Is(err, ErrSlugHeldByLegacyValues) {
		t.Fatalf("expected ErrSlugHeldByLegacyValues, got %v", err)
	}
	// The message has to be actionable: which slug, and how much is in the way.
	if !strings.Contains(err.Error(), "story_points") || !strings.Contains(err.Error(), "1 item") {
		t.Errorf("refusal must name the slug and the count, got %q", err.Error())
	}
}

// Clearing the legacy values frees the slug again — the guard is about
// orphaned data, not a permanent reservation of the name.
func TestCreateDef_SlugFreeOnceLegacyValuesAreCleared(t *testing.T) {
	svc := NewService(newStubDefRepo(), newStubValueRepo())
	ctx := context.Background()
	org, item := uuid.New(), uuid.New()

	d, err := svc.CreateDef(ctx, org, "Tier", TypeText, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	if err := svc.SetValue(ctx, org, item, "tier", "gold"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	// An empty value deletes the stored row — the supported way to leave
	// nothing behind.
	if err := svc.SetValue(ctx, org, item, "tier", ""); err != nil {
		t.Fatalf("clearing value: %v", err)
	}
	if err := svc.DeleteDef(ctx, org, d.ID); err != nil {
		t.Fatalf("DeleteDef: %v", err)
	}

	if _, err := svc.CreateDef(ctx, org, "Tier", TypeSingleSelect, []string{"gold", "silver"}); err != nil {
		t.Fatalf("a slug with no values behind it must be reusable, got %v", err)
	}
}
