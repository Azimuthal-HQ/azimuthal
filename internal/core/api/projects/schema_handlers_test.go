package projects_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// --- Configurable mock repositories for the item-type / custom-field surfaces.
// These drive direct handler unit tests that cover the input-validation and
// error-mapping branches the full-stack integration tests can't reach (a bad
// org id never survives the router's middleware in an end-to-end request).

type mockTypeRepo struct {
	getByID    *itemtypes.ItemType
	getByIDErr error
	bySlug     *itemtypes.ItemType // returned by GetByOrgSlug when non-nil
	count      int
	createErr  error
}

func (m *mockTypeRepo) ListByOrg(context.Context, uuid.UUID) ([]*itemtypes.ItemType, error) {
	return []*itemtypes.ItemType{{ID: uuid.New(), Slug: "task", Name: "Task"}}, nil
}
func (m *mockTypeRepo) ListActiveByOrg(context.Context, uuid.UUID) ([]*itemtypes.ItemType, error) {
	return nil, nil
}
func (m *mockTypeRepo) GetByID(context.Context, uuid.UUID) (*itemtypes.ItemType, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	if m.getByID != nil {
		return m.getByID, nil
	}
	return nil, itemtypes.ErrNotFound
}
func (m *mockTypeRepo) GetByOrgSlug(context.Context, uuid.UUID, string) (*itemtypes.ItemType, error) {
	if m.bySlug != nil {
		return m.bySlug, nil
	}
	return nil, itemtypes.ErrNotFound
}
func (m *mockTypeRepo) Create(context.Context, *itemtypes.ItemType) error { return m.createErr }
func (m *mockTypeRepo) Rename(_ context.Context, id uuid.UUID, name string) (*itemtypes.ItemType, error) {
	return &itemtypes.ItemType{ID: id, Name: name, Slug: "task"}, nil
}
func (m *mockTypeRepo) SetArchived(_ context.Context, id uuid.UUID, _ bool) (*itemtypes.ItemType, error) {
	return &itemtypes.ItemType{ID: id, Slug: "task"}, nil
}
func (m *mockTypeRepo) Delete(context.Context, uuid.UUID) error { return nil }
func (m *mockTypeRepo) CountItemsOfType(context.Context, uuid.UUID, string) (int, error) {
	return m.count, nil
}
func (m *mockTypeRepo) NextPosition(context.Context, uuid.UUID) (int, error) { return 1, nil }
func (m *mockTypeRepo) SeedDefaults(context.Context, uuid.UUID) error        { return nil }

type mockFieldDefRepo struct {
	getByID    *customfields.FieldDef
	getByIDErr error
	bySlug     *customfields.FieldDef
}

func (m *mockFieldDefRepo) ListByOrg(context.Context, uuid.UUID) ([]*customfields.FieldDef, error) {
	return []*customfields.FieldDef{{ID: uuid.New(), Slug: "squad", Name: "Squad", Type: customfields.TypeText}}, nil
}
func (m *mockFieldDefRepo) ListActiveByOrg(context.Context, uuid.UUID) ([]*customfields.FieldDef, error) {
	return nil, nil
}
func (m *mockFieldDefRepo) GetByID(context.Context, uuid.UUID) (*customfields.FieldDef, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	if m.getByID != nil {
		return m.getByID, nil
	}
	return nil, customfields.ErrNotFound
}
func (m *mockFieldDefRepo) GetByOrgSlug(context.Context, uuid.UUID, string) (*customfields.FieldDef, error) {
	if m.bySlug != nil {
		return m.bySlug, nil
	}
	return nil, customfields.ErrNotFound
}
func (m *mockFieldDefRepo) Create(context.Context, *customfields.FieldDef) error { return nil }
func (m *mockFieldDefRepo) Update(_ context.Context, id uuid.UUID, name string, opts []string) (*customfields.FieldDef, error) {
	return &customfields.FieldDef{ID: id, Name: name, Slug: "squad", Options: opts}, nil
}
func (m *mockFieldDefRepo) SetArchived(_ context.Context, id uuid.UUID, _ bool) (*customfields.FieldDef, error) {
	return &customfields.FieldDef{ID: id, Slug: "squad"}, nil
}
func (m *mockFieldDefRepo) Delete(context.Context, uuid.UUID) error              { return nil }
func (m *mockFieldDefRepo) NextPosition(context.Context, uuid.UUID) (int, error) { return 1, nil }

type mockFieldValueRepo struct{}

func (m *mockFieldValueRepo) ListForEntityInSpace(context.Context, uuid.UUID, string, uuid.UUID) ([]customfields.StoredValue, error) {
	return nil, nil
}
func (m *mockFieldValueRepo) UpsertInSpace(context.Context, uuid.UUID, string, uuid.UUID, string, string) (bool, error) {
	return true, nil
}
func (m *mockFieldValueRepo) DeleteInSpace(context.Context, uuid.UUID, string, uuid.UUID, string) error {
	return nil
}

// CountByOrgSlug reports no legacy values: this mock stores none, so any other
// answer would be a fabrication.
func (m *mockFieldValueRepo) CountByOrgSlug(context.Context, uuid.UUID, string) (int, error) {
	return 0, nil
}

// mockFieldScopeRepo is configurable the same way mockFieldDefRepo is: the
// branch tests below need "attached" and "not attached" both reachable.
type mockFieldScopeRepo struct {
	get       *customfields.FieldScope
	deleted   bool
	spaceOrg  uuid.UUID
	spaceType string
}

func (m *mockFieldScopeRepo) ListByField(context.Context, uuid.UUID) ([]customfields.FieldScope, error) {
	return nil, nil
}
func (m *mockFieldScopeRepo) ListForSpaceEntity(context.Context, uuid.UUID, string) ([]customfields.FieldScope, error) {
	return nil, nil
}
func (m *mockFieldScopeRepo) Get(context.Context, uuid.UUID, uuid.UUID, string) (*customfields.FieldScope, error) {
	if m.get != nil {
		return m.get, nil
	}
	return nil, customfields.ErrScopeNotFound
}
func (m *mockFieldScopeRepo) Upsert(_ context.Context, _ uuid.UUID, s *customfields.FieldScope) (*customfields.FieldScope, error) {
	return s, nil
}
func (m *mockFieldScopeRepo) Delete(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return m.deleted, nil
}
func (m *mockFieldScopeRepo) SpaceOrgType(context.Context, uuid.UUID) (uuid.UUID, string, error) {
	if m.spaceType == "" {
		return uuid.Nil, "", customfields.ErrSpaceNotFound
	}
	return m.spaceOrg, m.spaceType, nil
}

func schemaHandler(types *mockTypeRepo, defs *mockFieldDefRepo) *projectsapi.Handler {
	return schemaHandlerWithScopes(types, defs, &mockFieldScopeRepo{})
}

func schemaHandlerWithScopes(types *mockTypeRepo, defs *mockFieldDefRepo, scopes *mockFieldScopeRepo) *projectsapi.Handler {
	ir := &mockItemRepo{}
	sr := &mockSprintRepo{}
	h := projectsapi.NewHandler(
		projects.NewItemService(ir, noopShareDeleter{}),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		projects.NewLabelService(&mockLabelRepo{}),
	)
	return h.
		WithItemTypes(itemtypes.NewService(types)).
		WithCustomFields(customfields.NewService(defs, &mockFieldValueRepo{}, scopes))
}

func req(t *testing.T, method, body string, params map[string]string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/", nil)
	} else {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	}
	for k, v := range params {
		r = withParam(r, k, v)
	}
	return r
}

func run(t *testing.T, _ *projectsapi.Handler, fn func(http.ResponseWriter, *http.Request), r *http.Request) int {
	t.Helper()
	rr := httptest.NewRecorder()
	fn(rr, r)
	return rr.Code
}

// --- Item type handler branches ---

func TestItemTypeHandlers_Branches(t *testing.T) {
	org := uuid.New().String()
	tid := uuid.New().String()

	t.Run("list invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ListItemTypes, req(t, http.MethodGet, "", map[string]string{"orgID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("list ok", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ListItemTypes, req(t, http.MethodGet, "", map[string]string{"orgID": org})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateItemType, req(t, http.MethodPost, `{"name":"X"}`, map[string]string{"orgID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create bad body", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateItemType, req(t, http.MethodPost, `{bad`, map[string]string{"orgID": org})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create duplicate -> 409", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{bySlug: &itemtypes.ItemType{Slug: "x"}}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateItemType, req(t, http.MethodPost, `{"name":"X"}`, map[string]string{"orgID": org})); code != http.StatusConflict {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create empty name -> 400", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateItemType, req(t, http.MethodPost, `{"name":"  "}`, map[string]string{"orgID": org})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update invalid type id", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateItemType, req(t, http.MethodPatch, `{"name":"X"}`, map[string]string{"orgID": org, "typeID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update nothing to update", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateItemType, req(t, http.MethodPatch, `{}`, map[string]string{"orgID": org, "typeID": tid})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update unknown -> 404", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{getByIDErr: itemtypes.ErrNotFound}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateItemType, req(t, http.MethodPatch, `{"name":"X"}`, map[string]string{"orgID": org, "typeID": tid})); code != http.StatusNotFound {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update rename ok", func(t *testing.T) {
		owned := &itemtypes.ItemType{ID: uuid.MustParse(tid), OrgID: uuid.MustParse(org), Slug: "task"}
		h := schemaHandler(&mockTypeRepo{getByID: owned}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateItemType, req(t, http.MethodPatch, `{"name":"X"}`, map[string]string{"orgID": org, "typeID": tid})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete invalid id", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.DeleteItemType, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "typeID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete referenced -> 409", func(t *testing.T) {
		owned := &itemtypes.ItemType{ID: uuid.MustParse(tid), OrgID: uuid.MustParse(org), Slug: "task"}
		h := schemaHandler(&mockTypeRepo{getByID: owned, count: 3}, &mockFieldDefRepo{})
		if code := run(t, h, h.DeleteItemType, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "typeID": tid})); code != http.StatusConflict {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete ok", func(t *testing.T) {
		owned := &itemtypes.ItemType{ID: uuid.MustParse(tid), OrgID: uuid.MustParse(org), Slug: "task"}
		h := schemaHandler(&mockTypeRepo{getByID: owned}, &mockFieldDefRepo{})
		if code := run(t, h, h.DeleteItemType, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "typeID": tid})); code != http.StatusNoContent {
			t.Errorf("got %d", code)
		}
	})
}

// --- Custom field handler branches ---

func TestCustomFieldHandlers_Branches(t *testing.T) {
	org := uuid.New().String()
	fid := uuid.New().String()

	t.Run("list invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ListCustomFields, req(t, http.MethodGet, "", map[string]string{"orgID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("list ok", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ListCustomFields, req(t, http.MethodGet, "", map[string]string{"orgID": org})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateCustomField, req(t, http.MethodPost, `{"name":"X","field_type":"text"}`, map[string]string{"orgID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create bad body", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateCustomField, req(t, http.MethodPost, `{bad`, map[string]string{"orgID": org})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create invalid type -> 400", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateCustomField, req(t, http.MethodPost, `{"name":"X","field_type":"formula"}`, map[string]string{"orgID": org})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("create ok", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.CreateCustomField, req(t, http.MethodPost, `{"name":"X","field_type":"text"}`, map[string]string{"orgID": org})); code != http.StatusCreated {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update invalid id", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateCustomField, req(t, http.MethodPatch, `{"name":"X"}`, map[string]string{"orgID": org, "fieldID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update nothing to update", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.UpdateCustomField, req(t, http.MethodPatch, `{}`, map[string]string{"orgID": org, "fieldID": fid})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("update archive ok", func(t *testing.T) {
		owned := &customfields.FieldDef{ID: uuid.MustParse(fid), OrgID: uuid.MustParse(org), Slug: "squad", Type: customfields.TypeText}
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned})
		if code := run(t, h, h.UpdateCustomField, req(t, http.MethodPatch, `{"archived":true}`, map[string]string{"orgID": org, "fieldID": fid})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete invalid id", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.DeleteCustomField, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "fieldID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete unknown -> 404", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{getByIDErr: customfields.ErrNotFound})
		if code := run(t, h, h.DeleteCustomField, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "fieldID": fid})); code != http.StatusNotFound {
			t.Errorf("got %d", code)
		}
	})
	t.Run("delete ok", func(t *testing.T) {
		owned := &customfields.FieldDef{ID: uuid.MustParse(fid), OrgID: uuid.MustParse(org), Slug: "squad"}
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned})
		if code := run(t, h, h.DeleteCustomField, req(t, http.MethodDelete, "", map[string]string{"orgID": org, "fieldID": fid})); code != http.StatusNoContent {
			t.Errorf("got %d", code)
		}
	})
	t.Run("get item fields invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.GetItemFields, req(t, http.MethodGet, "", map[string]string{"orgID": "bad", "itemID": uuid.New().String()})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("get item fields invalid item", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.GetItemFields, req(t, http.MethodGet, "", map[string]string{"orgID": org, "itemID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("get item fields ok", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		// spaceID is required now: the field values are reconciled against the
		// item's space, so the route cannot answer without one.
		if code := run(t, h, h.GetItemFields, req(t, http.MethodGet, "", map[string]string{"orgID": org, "spaceID": uuid.New().String(), "itemID": uuid.New().String()})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set item field invalid item", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		r := req(t, http.MethodPut, `{"value":"x"}`, map[string]string{"orgID": org, "spaceID": uuid.New().String(), "itemID": "bad", "slug": "squad"})
		if code := run(t, h, h.SetItemField, r); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
}

// --- Custom field scope handler branches ---

func TestCustomFieldScopeHandlers_Branches(t *testing.T) {
	org := uuid.New()
	fid := uuid.New()
	sid := uuid.New()
	owned := &customfields.FieldDef{ID: fid, OrgID: org, Slug: "squad", Type: customfields.TypeText, Position: 2}
	params := func(entityType string) map[string]string {
		return map[string]string{"orgID": org.String(), "fieldID": fid.String(), "spaceID": sid.String(), "entityType": entityType}
	}

	t.Run("list scopes unknown field -> 404", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ListFieldScopes, req(t, http.MethodGet, "", map[string]string{"orgID": org.String(), "fieldID": fid.String()})); code != http.StatusNotFound {
			t.Errorf("got %d", code)
		}
	})
	t.Run("list scopes ok", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned})
		if code := run(t, h, h.ListFieldScopes, req(t, http.MethodGet, "", map[string]string{"orgID": org.String(), "fieldID": fid.String()})); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set scope ok", func(t *testing.T) {
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{spaceOrg: org, spaceType: "vector"})
		if code := run(t, h, h.SetFieldScope, req(t, http.MethodPut, `{"required":true}`, params("project_item"))); code != http.StatusOK {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set scope on a page -> 400", func(t *testing.T) {
		// page is in the value vocabulary but has no field surface; attaching
		// would create rows nothing reads.
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{spaceOrg: org, spaceType: "codex"})
		if code := run(t, h, h.SetFieldScope, req(t, http.MethodPut, `{"required":false}`, params("page"))); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set scope unknown entity type -> 400", func(t *testing.T) {
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{spaceOrg: org, spaceType: "vector"})
		if code := run(t, h, h.SetFieldScope, req(t, http.MethodPut, `{"required":false}`, params("epic"))); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set scope module mismatch -> 400", func(t *testing.T) {
		// A ticket scope needs a beacon space; this one is vector.
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{spaceOrg: org, spaceType: "vector"})
		if code := run(t, h, h.SetFieldScope, req(t, http.MethodPut, `{"required":false}`, params("ticket"))); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("set scope on another org's space -> 404, not 400", func(t *testing.T) {
		// The org check must come before the type check: a mismatch message on
		// a foreign space would be a probe for what kind of space an id is.
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{spaceOrg: uuid.New(), spaceType: "beacon"})
		if code := run(t, h, h.SetFieldScope, req(t, http.MethodPut, `{"required":false}`, params("ticket"))); code != http.StatusNotFound {
			t.Errorf("got %d", code)
		}
	})
	t.Run("remove scope absent -> 404", func(t *testing.T) {
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{deleted: false})
		if code := run(t, h, h.RemoveFieldScope, req(t, http.MethodDelete, "", params("project_item"))); code != http.StatusNotFound {
			t.Errorf("got %d", code)
		}
	})
	t.Run("remove scope ok", func(t *testing.T) {
		h := schemaHandlerWithScopes(&mockTypeRepo{}, &mockFieldDefRepo{getByID: owned},
			&mockFieldScopeRepo{deleted: true})
		if code := run(t, h, h.RemoveFieldScope, req(t, http.MethodDelete, "", params("project_item"))); code != http.StatusNoContent {
			t.Errorf("got %d", code)
		}
	})
}

// A handler built without WithCustomFields answers the conventional
// feature-disabled 404 on every custom-field route. Before the guard existed
// this was a nil-pointer dereference: the sibling itemTypes collaborator was
// checked, customFields never was. (Fails before the fix by panicking here.)
func TestCustomFieldHandlers_NilServiceAnswers404(t *testing.T) {
	ir := &mockItemRepo{}
	sr := &mockSprintRepo{}
	h := projectsapi.NewHandler(
		projects.NewItemService(ir, noopShareDeleter{}),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		projects.NewRelationService(&mockRelationRepo{}),
		projects.NewLabelService(&mockLabelRepo{}),
	) // deliberately no WithCustomFields
	org := uuid.New().String()

	if code := run(t, h, h.ListCustomFields, req(t, http.MethodGet, "", map[string]string{"orgID": org})); code != http.StatusNotFound {
		t.Errorf("ListCustomFields: got %d, want 404", code)
	}
	if code := run(t, h, h.GetItemFields, req(t, http.MethodGet, "", map[string]string{"orgID": org, "spaceID": uuid.New().String(), "itemID": uuid.New().String()})); code != http.StatusNotFound {
		t.Errorf("GetItemFields: got %d, want 404", code)
	}
	if code := run(t, h, h.SetItemField, req(t, http.MethodPut, `{"value":"x"}`, map[string]string{"orgID": org, "spaceID": uuid.New().String(), "itemID": uuid.New().String(), "slug": "s"})); code != http.StatusNotFound {
		t.Errorf("SetItemField: got %d, want 404", code)
	}
	if code := run(t, h, h.ListFieldScopes, req(t, http.MethodGet, "", map[string]string{"orgID": org, "fieldID": uuid.New().String()})); code != http.StatusNotFound {
		t.Errorf("ListFieldScopes: got %d, want 404", code)
	}
}

// --- ResolveItem branches ---

func TestResolveItem_Branches(t *testing.T) {
	org := uuid.New().String()
	t.Run("invalid org", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ResolveItem, req(t, http.MethodGet, "", map[string]string{"orgID": "bad"})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
	t.Run("missing key", func(t *testing.T) {
		h := schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{})
		if code := run(t, h, h.ResolveItem, req(t, http.MethodGet, "", map[string]string{"orgID": org})); code != http.StatusBadRequest {
			t.Errorf("got %d", code)
		}
	})
}
