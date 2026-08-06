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

// stubValueRepo keys values by (entityType, entityID, slug). The space
// predicate is NOT modelled here — the real reconciliation is a statement
// proved against a real database in internal/db/adapters, and modelling it in
// the stub would be modelling it in the one place it is not implemented. What
// this stub models faithfully is the composition the service performs over
// whatever values come back.
type stubValueRepo struct {
	values map[string]string
}

func newStubValueRepo() *stubValueRepo { return &stubValueRepo{values: map[string]string{}} }
func vkey(entityType string, id uuid.UUID, s string) string {
	return entityType + "|" + id.String() + "|" + s
}

func (r *stubValueRepo) ListForEntityInSpace(_ context.Context, _ uuid.UUID, entityType string, entityID uuid.UUID) ([]StoredValue, error) {
	prefix := entityType + "|" + entityID.String() + "|"
	var out []StoredValue
	for k, v := range r.values {
		if strings.HasPrefix(k, prefix) {
			out = append(out, StoredValue{FieldSlug: strings.TrimPrefix(k, prefix), Value: v})
		}
	}
	return out, nil
}
func (r *stubValueRepo) UpsertInSpace(_ context.Context, _ uuid.UUID, entityType string, entityID uuid.UUID, slug, value string) (bool, error) {
	r.values[vkey(entityType, entityID, slug)] = value
	return true, nil
}
func (r *stubValueRepo) DeleteInSpace(_ context.Context, _ uuid.UUID, entityType string, entityID uuid.UUID, slug string) error {
	delete(r.values, vkey(entityType, entityID, slug))
	return nil
}

// CountByOrgSlug counts every stored value under slug. The stub holds one org,
// so org scoping is the real repository's job (proved against a real database
// in internal/db/adapters); what this models faithfully is the fact the guard
// turns on — that values survive their definitions and are found by slug.
func (r *stubValueRepo) CountByOrgSlug(_ context.Context, _ uuid.UUID, slug string) (int, error) {
	n := 0
	for k := range r.values {
		if strings.HasSuffix(k, "|"+slug) {
			n++
		}
	}
	return n, nil
}

// stubScopeRepo holds scope rows keyed by the (field, space, entityType)
// triple, plus the space directory SetScope validates against.
type stubScopeRepo struct {
	scopes map[string]*FieldScope
	spaces map[uuid.UUID]stubSpace
}

type stubSpace struct {
	org       uuid.UUID
	spaceType string
}

func newStubScopeRepo() *stubScopeRepo {
	return &stubScopeRepo{scopes: map[string]*FieldScope{}, spaces: map[uuid.UUID]stubSpace{}}
}
func skey(f, sp uuid.UUID, et string) string { return f.String() + "|" + sp.String() + "|" + et }

func (r *stubScopeRepo) addSpace(id, org uuid.UUID, spaceType string) {
	r.spaces[id] = stubSpace{org: org, spaceType: spaceType}
}

func (r *stubScopeRepo) ListByField(_ context.Context, fieldID uuid.UUID) ([]FieldScope, error) {
	var out []FieldScope
	for _, s := range r.scopes {
		if s.FieldID == fieldID {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (r *stubScopeRepo) ListForSpaceEntity(_ context.Context, spaceID uuid.UUID, entityType string) ([]FieldScope, error) {
	var out []FieldScope
	for _, s := range r.scopes {
		if s.SpaceID == spaceID && s.EntityType == entityType {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (r *stubScopeRepo) Get(_ context.Context, fieldID, spaceID uuid.UUID, entityType string) (*FieldScope, error) {
	if s, ok := r.scopes[skey(fieldID, spaceID, entityType)]; ok {
		out := *s
		return &out, nil
	}
	return nil, ErrScopeNotFound
}
func (r *stubScopeRepo) Upsert(_ context.Context, orgID uuid.UUID, s *FieldScope) (*FieldScope, error) {
	sp, ok := r.spaces[s.SpaceID]
	if !ok || sp.org != orgID {
		return nil, ErrSpaceNotFound
	}
	if existing, ok := r.scopes[skey(s.FieldID, s.SpaceID, s.EntityType)]; ok {
		existing.Required = s.Required // position kept, matching the statement
		out := *existing
		return &out, nil
	}
	cp := *s
	r.scopes[skey(s.FieldID, s.SpaceID, s.EntityType)] = &cp
	out := cp
	return &out, nil
}
func (r *stubScopeRepo) Delete(_ context.Context, fieldID, spaceID uuid.UUID, entityType string) (bool, error) {
	k := skey(fieldID, spaceID, entityType)
	if _, ok := r.scopes[k]; !ok {
		return false, nil
	}
	delete(r.scopes, k)
	return true, nil
}
func (r *stubScopeRepo) SpaceOrgType(_ context.Context, spaceID uuid.UUID) (uuid.UUID, string, error) {
	sp, ok := r.spaces[spaceID]
	if !ok {
		return uuid.Nil, "", ErrSpaceNotFound
	}
	return sp.org, sp.spaceType, nil
}

// fixture wires the three stubs plus one Vector space with helpers that keep
// the older tests readable after the entity-generic reshape.
type fixture struct {
	svc    *Service
	defs   *stubDefRepo
	vals   *stubValueRepo
	scopes *stubScopeRepo
	org    uuid.UUID
	space  uuid.UUID
}

func newFixture() *fixture {
	f := &fixture{
		defs: newStubDefRepo(), vals: newStubValueRepo(), scopes: newStubScopeRepo(),
		org: uuid.New(), space: uuid.New(),
	}
	f.svc = NewService(f.defs, f.vals, f.scopes)
	f.scopes.addSpace(f.space, f.org, "vector")
	return f
}

// attach scopes a def to the fixture space's item form, as migration 053's
// backfill did for every pre-existing def.
func (f *fixture) attach(t *testing.T, def *FieldDef, required bool) {
	t.Helper()
	if _, err := f.svc.SetScope(context.Background(), f.org, def.ID, f.space, EntityTypeProjectItem, required); err != nil {
		t.Fatalf("SetScope: %v", err)
	}
}

func (f *fixture) setItemValue(ctx context.Context, itemID uuid.UUID, slug, value string) error {
	return f.svc.SetValue(ctx, f.org, f.space, EntityTypeProjectItem, itemID, slug, value)
}

func (f *fixture) renderItem(ctx context.Context, itemID uuid.UUID) ([]RenderedField, error) {
	return f.svc.RenderForEntity(ctx, f.org, f.space, EntityTypeProjectItem, itemID)
}

func TestCreateDef_Validation(t *testing.T) {
	f := newFixture()
	org := f.org

	d, err := f.svc.CreateDef(context.Background(), org, "Story Points", TypeNumber, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	if d.Slug != "story_points" || d.Type != TypeNumber {
		t.Errorf("unexpected def: %+v", d)
	}

	// Duplicate name/slug → ErrDuplicate.
	if _, err := f.svc.CreateDef(context.Background(), org, "story points", TypeText, nil); !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
	// Invalid type.
	if _, err := f.svc.CreateDef(context.Background(), org, "X", "formula", nil); !errors.Is(err, ErrInvalidType) {
		t.Errorf("expected ErrInvalidType, got %v", err)
	}
	// single_select needs options.
	if _, err := f.svc.CreateDef(context.Background(), org, "Team", TypeSingleSelect, nil); !errors.Is(err, ErrOptionsRequired) {
		t.Errorf("expected ErrOptionsRequired, got %v", err)
	}
	// Empty name.
	if _, err := f.svc.CreateDef(context.Background(), org, "  ", TypeText, nil); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestSetValue_TypeValidation(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	num, _ := f.svc.CreateDef(ctx, f.org, "Points", TypeNumber, nil)
	sel, _ := f.svc.CreateDef(ctx, f.org, "Tier", TypeSingleSelect, []string{"gold", "silver"})
	dt, _ := f.svc.CreateDef(ctx, f.org, "Target", TypeDate, nil)
	for _, d := range []*FieldDef{num, sel, dt} {
		f.attach(t, d, false)
	}

	// Number: rejects non-numeric, accepts numeric.
	if err := f.setItemValue(ctx, item, num.Slug, "abc"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("number reject: got %v", err)
	}
	if err := f.setItemValue(ctx, item, num.Slug, "13"); err != nil {
		t.Errorf("number accept: %v", err)
	}
	// Select: rejects out-of-set, accepts in-set.
	if err := f.setItemValue(ctx, item, sel.Slug, "bronze"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("select reject: got %v", err)
	}
	if err := f.setItemValue(ctx, item, sel.Slug, "gold"); err != nil {
		t.Errorf("select accept: %v", err)
	}
	// Date: rejects bad format.
	if err := f.setItemValue(ctx, item, dt.Slug, "not-a-date"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("date reject: got %v", err)
	}
	if err := f.setItemValue(ctx, item, dt.Slug, "2026-07-23"); err != nil {
		t.Errorf("date accept: %v", err)
	}

	// Undefined field → ErrUndefinedField.
	if err := f.setItemValue(ctx, item, "nonexistent", "x"); !errors.Is(err, ErrUndefinedField) {
		t.Errorf("undefined field: got %v", err)
	}

	// Empty value clears.
	if err := f.setItemValue(ctx, item, num.Slug, ""); err != nil {
		t.Errorf("clear: %v", err)
	}
	if _, ok := f.vals.values[vkey(EntityTypeProjectItem, item, num.Slug)]; ok {
		t.Error("empty value must clear the stored value")
	}
}

// TestLegacyValuesSurvive is the zero-data-loss guarantee (D48): a value whose
// definition is archived or deleted remains stored, renders read-only as
// legacy, and cannot be overwritten through the write path.
func TestLegacyValuesSurvive(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	def, _ := f.svc.CreateDef(ctx, f.org, "Squad", TypeText, nil)
	f.attach(t, def, false)
	if err := f.setItemValue(ctx, item, def.Slug, "Falcon"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Archive the definition.
	if _, err := f.svc.SetDefArchived(ctx, f.org, def.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	rendered, err := f.renderItem(ctx, item)
	if err != nil {
		t.Fatalf("RenderForEntity: %v", err)
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
	if err := f.setItemValue(ctx, item, def.Slug, "Hawk"); !errors.Is(err, ErrUndefinedField) {
		t.Errorf("legacy field must be read-only, got %v", err)
	}

	// Deleting the definition entirely still preserves the value.
	if _, err := f.svc.SetDefArchived(ctx, f.org, def.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if err := f.svc.DeleteDef(ctx, f.org, def.ID); err != nil {
		t.Fatalf("delete def: %v", err)
	}
	rendered, _ = f.renderItem(ctx, item)
	found := false
	for _, rf := range rendered {
		if rf.Slug == def.Slug {
			found = true
			if !rf.Legacy || rf.Value != "Falcon" {
				t.Errorf("deleted-def value must remain read-only 'Falcon', got %+v", rf)
			}
		}
	}
	if !found {
		t.Error("value must survive definition deletion (no silent data loss)")
	}
}

func TestRenderForEntity_ScopedWithValues(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	a, _ := f.svc.CreateDef(ctx, f.org, "Alpha", TypeText, nil)
	b, _ := f.svc.CreateDef(ctx, f.org, "Beta", TypeText, nil)
	f.attach(t, a, false)
	f.attach(t, b, false)
	_ = f.setItemValue(ctx, item, a.Slug, "hello")

	rendered, err := f.renderItem(ctx, item)
	if err != nil {
		t.Fatalf("RenderForEntity: %v", err)
	}
	if len(rendered) != 2 {
		t.Fatalf("expected 2 scoped fields, got %d", len(rendered))
	}
	for _, rf := range rendered {
		if rf.Legacy {
			t.Errorf("scoped field %s should not be legacy", rf.Slug)
		}
		if rf.Slug == a.Slug && rf.Value != "hello" {
			t.Errorf("expected value 'hello', got %q", rf.Value)
		}
	}
}

// A definition with no scope rows appears on no form — there is deliberately
// no "unscoped means everywhere" fallback, because that would make "scoped
// nowhere" and "scoped everywhere" the same observable state. Its stored
// values still render read-only: scoping governs the form, never data
// visibility.
func TestRenderForEntity_UnscopedFieldIsAbsentButItsValuesSurvive(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	d, _ := f.svc.CreateDef(ctx, f.org, "Cost Centre", TypeText, nil)
	// No attach. A value exists anyway (written while the field was scoped
	// here, or in another space — history the form change cannot erase).
	f.vals.values[vkey(EntityTypeProjectItem, item, d.Slug)] = "CC-42"

	rendered, err := f.renderItem(ctx, item)
	if err != nil {
		t.Fatalf("RenderForEntity: %v", err)
	}
	if len(rendered) != 1 {
		t.Fatalf("expected only the surviving value, got %d fields", len(rendered))
	}
	got := rendered[0]
	if !got.Legacy {
		t.Error("an unscoped field's value must render read-only (legacy)")
	}
	if got.Name != "Cost Centre" || got.Type != TypeText {
		t.Errorf("an unscoped-but-defined field renders with its real name and type, got %+v", got)
	}
	if got.Value != "CC-42" {
		t.Errorf("value must survive descoping, got %q", got.Value)
	}

	// And the write path refuses it here, like any read-only field.
	if err := f.setItemValue(ctx, item, d.Slug, "CC-43"); !errors.Is(err, ErrFieldNotInScope) {
		t.Errorf("unscoped field must be read-only, got %v", err)
	}
}

// Required, both directions. The write that would leave a required field
// absent is refused and names the field; a read of an entity that already
// lacks the value succeeds with the value absent — the never-retroactively
// rule. Marking a field required must never invalidate stored rows.
func TestRequired_RefusesClearButNeverRefusesARead(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	filled, incomplete := uuid.New(), uuid.New()

	d, _ := f.svc.CreateDef(ctx, f.org, "Severity", TypeText, nil)
	f.attach(t, d, false)
	if err := f.setItemValue(ctx, filled, d.Slug, "high"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Flip the attachment to required AFTER one entity has a value and one
	// does not.
	f.attach(t, d, true)

	// Refused direction: the clear.
	err := f.setItemValue(ctx, filled, d.Slug, "")
	if !errors.Is(err, ErrValueRequired) {
		t.Fatalf("clearing a required field must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), d.Slug) {
		t.Errorf("the refusal must name the field for the form to render against, got %q", err.Error())
	}
	if f.vals.values[vkey(EntityTypeProjectItem, filled, d.Slug)] != "high" {
		t.Error("a refused clear must leave the stored value untouched")
	}
	// Writing empty-with-whitespace is the same clear.
	if err := f.setItemValue(ctx, filled, d.Slug, "   "); !errors.Is(err, ErrValueRequired) {
		t.Errorf("whitespace-only value is a clear and must be refused, got %v", err)
	}

	// Never-retroactive direction: the incomplete entity still reads fine.
	rendered, err := f.renderItem(ctx, incomplete)
	if err != nil {
		t.Fatalf("a read of a pre-existing incomplete entity must succeed, got %v", err)
	}
	var got *RenderedField
	for i := range rendered {
		if rendered[i].Slug == d.Slug {
			got = &rendered[i]
		}
	}
	if got == nil {
		t.Fatal("the required field must render on the form")
	}
	if !got.Required {
		t.Error("the render must carry the required flag for the form control")
	}
	if got.Value != "" {
		t.Errorf("the absent value must surface as absent — never synthesized, got %q", got.Value)
	}

	// Supplying the value is always accepted; so is clearing again once the
	// flag is off.
	if err := f.setItemValue(ctx, incomplete, d.Slug, "low"); err != nil {
		t.Errorf("writing a value to a required field: %v", err)
	}
	f.attach(t, d, false)
	if err := f.setItemValue(ctx, filled, d.Slug, ""); err != nil {
		t.Errorf("clearing after the flag is lifted: %v", err)
	}
}

// Required is a property of one attachment. A field required in space A and
// not in space B is enforced in A alone; a space where the field is not
// scoped at all refuses the write for scoping reasons, never for required
// ones.
func TestRequired_IsPerAttachment(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	spaceB, spaceC := uuid.New(), uuid.New()
	f.scopes.addSpace(spaceB, f.org, "vector")
	f.scopes.addSpace(spaceC, f.org, "vector")
	itemA, itemB := uuid.New(), uuid.New()

	d, _ := f.svc.CreateDef(ctx, f.org, "Env", TypeText, nil)
	f.attach(t, d, true) // required in the fixture space (A)
	if _, err := f.svc.SetScope(ctx, f.org, d.ID, spaceB, EntityTypeProjectItem, false); err != nil {
		t.Fatalf("SetScope B: %v", err)
	}

	set := func(space, item uuid.UUID, v string) error {
		return f.svc.SetValue(ctx, f.org, space, EntityTypeProjectItem, item, d.Slug, v)
	}
	if err := set(f.space, itemA, "prod"); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := set(spaceB, itemB, "dev"); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// A refuses the clear; B allows it.
	if err := set(f.space, itemA, ""); !errors.Is(err, ErrValueRequired) {
		t.Errorf("space A must refuse the clear, got %v", err)
	}
	if err := set(spaceB, itemB, ""); err != nil {
		t.Errorf("space B must allow the clear, got %v", err)
	}

	// C, where the field is not scoped, refuses any write — as not-attached,
	// which is not a requiredness refusal.
	if err := set(spaceC, uuid.New(), "x"); !errors.Is(err, ErrFieldNotInScope) {
		t.Errorf("space C must refuse as unscoped, got %v", err)
	}
	// And renders no form field for it.
	rendered, err := f.svc.RenderForEntity(ctx, f.org, spaceC, EntityTypeProjectItem, uuid.New())
	if err != nil {
		t.Fatalf("render C: %v", err)
	}
	if len(rendered) != 0 {
		t.Errorf("a field scoped elsewhere must not appear on C's form, got %+v", rendered)
	}
}

func TestSetScope_Validation(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	beacon := uuid.New()
	f.scopes.addSpace(beacon, f.org, "beacon")

	d, _ := f.svc.CreateDef(ctx, f.org, "Env", TypeText, nil)

	// Ticket attachment on a beacon space works, and re-attaching only flips
	// the flag.
	sc, err := f.svc.SetScope(ctx, f.org, d.ID, beacon, EntityTypeTicket, false)
	if err != nil {
		t.Fatalf("SetScope ticket: %v", err)
	}
	if sc.Position != d.Position {
		t.Errorf("first attach takes the definition's position, got %d want %d", sc.Position, d.Position)
	}
	sc2, err := f.svc.SetScope(ctx, f.org, d.ID, beacon, EntityTypeTicket, true)
	if err != nil {
		t.Fatalf("SetScope re-attach: %v", err)
	}
	if !sc2.Required || sc2.Position != sc.Position {
		t.Errorf("re-attach must flip required and keep position, got %+v", sc2)
	}

	// A ticket scope on a vector space is a row nothing would read.
	if _, err := f.svc.SetScope(ctx, f.org, d.ID, f.space, EntityTypeTicket, false); !errors.Is(err, ErrScopeSpaceMismatch) {
		t.Errorf("module mismatch must be refused, got %v", err)
	}
	// Pages are in the value vocabulary but have no field surface.
	if _, err := f.svc.SetScope(ctx, f.org, d.ID, f.space, EntityTypePage, false); !errors.Is(err, ErrUnscopableEntityType) {
		t.Errorf("page attachment must be refused, got %v", err)
	}
	// Garbage entity type.
	if _, err := f.svc.SetScope(ctx, f.org, d.ID, f.space, "epic", false); !errors.Is(err, ErrInvalidEntityType) {
		t.Errorf("unknown entity type must be refused, got %v", err)
	}
	// A space in another org answers as nonexistent — no oracle.
	foreign := uuid.New()
	f.scopes.addSpace(foreign, uuid.New(), "vector")
	if _, err := f.svc.SetScope(ctx, f.org, d.ID, foreign, EntityTypeProjectItem, false); !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("foreign space must answer not-found, got %v", err)
	}
	// A field in another org is not yours to scope.
	if _, err := f.svc.SetScope(ctx, uuid.New(), d.ID, f.space, EntityTypeProjectItem, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-org field must answer not-found, got %v", err)
	}

	// RemoveScope: absent row 404s; present row goes.
	if err := f.svc.RemoveScope(ctx, f.org, d.ID, f.space, EntityTypeProjectItem); !errors.Is(err, ErrScopeNotFound) {
		t.Errorf("removing an absent scope must answer not-found, got %v", err)
	}
	if err := f.svc.RemoveScope(ctx, f.org, d.ID, beacon, EntityTypeTicket); err != nil {
		t.Errorf("RemoveScope: %v", err)
	}
}

func TestUpdateDef_Branches(t *testing.T) {
	f := newFixture()
	org := f.org

	sel, err := f.svc.CreateDef(context.Background(), org, "Tier", TypeSingleSelect, []string{"gold"})
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}

	// Rename + replace options — slug and type are immutable.
	updated, err := f.svc.UpdateDef(context.Background(), org, sel.ID, "Level", []string{"a", "b"})
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
	if _, err := f.svc.UpdateDef(context.Background(), org, sel.ID, "Level", nil); !errors.Is(err, ErrOptionsRequired) {
		t.Errorf("expected ErrOptionsRequired, got %v", err)
	}
	// Empty name → ErrNameRequired.
	if _, err := f.svc.UpdateDef(context.Background(), org, sel.ID, "  ", []string{"a"}); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
	// Cross-org id → ErrNotFound.
	if _, err := f.svc.UpdateDef(context.Background(), uuid.New(), sel.ID, "X", []string{"a"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-org: expected ErrNotFound, got %v", err)
	}

	// A text field's options are always dropped on update.
	txt, _ := f.svc.CreateDef(context.Background(), org, "Squad", TypeText, nil)
	tu, err := f.svc.UpdateDef(context.Background(), org, txt.ID, "Team", []string{"ignored"})
	if err != nil {
		t.Fatalf("UpdateDef text: %v", err)
	}
	if len(tu.Options) != 0 {
		t.Errorf("text field must carry no options, got %v", tu.Options)
	}
}

func TestListDefs(t *testing.T) {
	f := newFixture()
	_, _ = f.svc.CreateDef(context.Background(), f.org, "A", TypeText, nil)
	_, _ = f.svc.CreateDef(context.Background(), f.org, "B", TypeText, nil)

	list, err := f.svc.ListDefs(context.Background(), f.org)
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
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	d, err := f.svc.CreateDef(ctx, f.org, "Story Points", TypeNumber, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	f.attach(t, d, false)
	if err := f.setItemValue(ctx, item, "story_points", "8"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if err := f.svc.DeleteDef(ctx, f.org, d.ID); err != nil {
		t.Fatalf("DeleteDef: %v", err)
	}

	// The value is still there — that is the design, not the defect.
	if got, _ := f.vals.CountByOrgSlug(ctx, f.org, "story_points"); got != 1 {
		t.Fatalf("precondition: expected the legacy value to survive, got %d", got)
	}

	_, err = f.svc.CreateDef(ctx, f.org, "story points", TypeSingleSelect, []string{"gold"})
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
	f := newFixture()
	ctx := context.Background()
	item := uuid.New()

	d, err := f.svc.CreateDef(ctx, f.org, "Tier", TypeText, nil)
	if err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	f.attach(t, d, false)
	if err := f.setItemValue(ctx, item, "tier", "gold"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	// An empty value deletes the stored row — the supported way to leave
	// nothing behind.
	if err := f.setItemValue(ctx, item, "tier", ""); err != nil {
		t.Fatalf("clearing value: %v", err)
	}
	if err := f.svc.DeleteDef(ctx, f.org, d.ID); err != nil {
		t.Fatalf("DeleteDef: %v", err)
	}

	if _, err := f.svc.CreateDef(ctx, f.org, "Tier", TypeSingleSelect, []string{"gold", "silver"}); err != nil {
		t.Fatalf("a slug with no values behind it must be reusable, got %v", err)
	}
}
