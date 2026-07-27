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

func (m *mockFieldValueRepo) ListByItem(context.Context, uuid.UUID) ([]customfields.StoredValue, error) {
	return nil, nil
}
func (m *mockFieldValueRepo) Upsert(context.Context, uuid.UUID, string, string) error { return nil }
func (m *mockFieldValueRepo) Delete(context.Context, uuid.UUID, string) error         { return nil }

// CountByOrgSlug reports no legacy values: this mock stores none, so any other
// answer would be a fabrication.
func (m *mockFieldValueRepo) CountByOrgSlug(context.Context, uuid.UUID, string) (int, error) {
	return 0, nil
}

func schemaHandler(types *mockTypeRepo, defs *mockFieldDefRepo) *projectsapi.Handler {
	ir := &mockItemRepo{}
	sr := &mockSprintRepo{}
	h := projectsapi.NewHandler(
		projects.NewItemService(ir, noopShareDeleter{}),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		projects.NewRelationService(&mockRelationRepo{}),
		projects.NewLabelService(&mockLabelRepo{}),
	)
	return h.
		WithItemTypes(itemtypes.NewService(types)).
		WithCustomFields(customfields.NewService(defs, &mockFieldValueRepo{}))
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
		if code := run(t, h, h.GetItemFields, req(t, http.MethodGet, "", map[string]string{"orgID": org, "itemID": uuid.New().String()})); code != http.StatusOK {
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
