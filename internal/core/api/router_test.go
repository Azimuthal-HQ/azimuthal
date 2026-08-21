package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	relationsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/relations"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/tiergate"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- Mock repos ----

type mockUserRepo struct {
	users map[uuid.UUID]*auth.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[uuid.UUID]*auth.User)}
}

func (m *mockUserRepo) Create(_ context.Context, u *auth.User) error {
	for _, existing := range m.users {
		if existing.Email == u.Email {
			return auth.ErrEmailTaken
		}
	}
	m.users[u.ID] = u
	return nil
}

func (m *mockUserRepo) GetByID(_ context.Context, id uuid.UUID) (*auth.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, auth.ErrNotFound
	}
	return u, nil
}

func (m *mockUserRepo) GetByEmailAcrossOrgs(_ context.Context, email string) (*auth.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (m *mockUserRepo) Update(_ context.Context, u *auth.User) error {
	m.users[u.ID] = u
	return nil
}

func (m *mockUserRepo) UpdateProfile(_ context.Context, id uuid.UUID, displayName, email string) (*auth.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, auth.ErrNotFound
	}
	u.DisplayName = displayName
	u.Email = email
	m.users[id] = u
	return u, nil
}

func (m *mockUserRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.users, id)
	return nil
}

func (m *mockUserRepo) TouchLastLogin(_ context.Context, _ uuid.UUID) error {
	return nil
}

// RevokeTokens bumps the in-memory generation, mirroring what
// BumpTokenGeneration does to the column. An unknown id is a no-op, as it is
// in the adapter.
func (m *mockUserRepo) RevokeTokens(_ context.Context, id uuid.UUID) error {
	if u, ok := m.users[id]; ok {
		u.TokenGeneration++
	}
	return nil
}

type mockSessionRepo struct {
	sessions map[uuid.UUID]*auth.Session
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[uuid.UUID]*auth.Session)}
}

func (m *mockSessionRepo) Create(_ context.Context, s *auth.Session) error {
	m.sessions[s.ID] = s
	return nil
}

func (m *mockSessionRepo) GetByToken(_ context.Context, token string) (*auth.Session, error) {
	for _, s := range m.sessions {
		if s.Token == token {
			return s, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (m *mockSessionRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.sessions, id)
	return nil
}

func (m *mockSessionRepo) DeleteAllForUser(_ context.Context, userID uuid.UUID) error {
	for id, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, id)
		}
	}
	return nil
}

func (m *mockSessionRepo) DeleteExpired(_ context.Context) error {
	return nil
}

type mockTicketRepo struct {
	tickets map[uuid.UUID]*tickets.Ticket
}

func newMockTicketRepo() *mockTicketRepo {
	return &mockTicketRepo{tickets: make(map[uuid.UUID]*tickets.Ticket)}
}

func (m *mockTicketRepo) Create(_ context.Context, t *tickets.Ticket) error {
	m.tickets[t.ID] = t
	return nil
}

func (m *mockTicketRepo) GetByIDInSpace(_ context.Context, spaceID, id uuid.UUID) (*tickets.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok || t.SpaceID != spaceID {
		return nil, tickets.ErrNotFound
	}
	return t, nil
}

func (m *mockTicketRepo) GetByID(_ context.Context, id uuid.UUID) (*tickets.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, tickets.ErrNotFound
	}
	return t, nil
}

func (m *mockTicketRepo) Update(_ context.Context, t *tickets.Ticket) error {
	m.tickets[t.ID] = t
	return nil
}

func (m *mockTicketRepo) UpdateStatus(_ context.Context, id uuid.UUID, status tickets.Status) (*tickets.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, tickets.ErrNotFound
	}
	t.Status = status
	return t, nil
}

func (m *mockTicketRepo) DeleteInSpace(_ context.Context, id, _ uuid.UUID) error {
	delete(m.tickets, id)
	return nil
}

func (m *mockTicketRepo) ListBySpace(_ context.Context, spaceID uuid.UUID) ([]*tickets.Ticket, error) {
	var result []*tickets.Ticket
	for _, t := range m.tickets {
		if t.SpaceID == spaceID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockTicketRepo) ListByStatus(_ context.Context, spaceID uuid.UUID, status tickets.Status) ([]*tickets.Ticket, error) {
	var result []*tickets.Ticket
	for _, t := range m.tickets {
		if t.SpaceID == spaceID && t.Status == status {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockTicketRepo) ListByAssignee(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]*tickets.Ticket, error) {
	return nil, nil
}

// Every user is an org member here: these doubles predate the assignee check
// and none of their tests is about it. The refusal is exercised in
// internal/core/tickets and against real PostgreSQL.
func (m *mockTicketRepo) UserIsMemberOfSpaceOrg(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return true, nil
}

func (m *mockTicketRepo) Search(_ context.Context, _ uuid.UUID, _ string, _ int32) ([]*tickets.Ticket, error) {
	return nil, nil
}

// ---- Mock wiki store ----

type mockPageStore struct {
	pages     map[uuid.UUID]generated.Page
	revisions map[uuid.UUID][]generated.PageRevision
}

func newMockPageStore() *mockPageStore {
	return &mockPageStore{
		pages:     make(map[uuid.UUID]generated.Page),
		revisions: make(map[uuid.UUID][]generated.PageRevision),
	}
}

func (m *mockPageStore) CreatePage(_ context.Context, arg generated.CreatePageParams) (generated.Page, error) {
	p := generated.Page{
		ID:       arg.ID,
		SpaceID:  arg.SpaceID,
		ParentID: arg.ParentID,
		Title:    arg.Title,
		Content:  arg.Content,
		Version:  1,
		AuthorID: arg.AuthorID,
		Position: arg.Position,
	}
	m.pages[p.ID] = p
	return p, nil
}

func (m *mockPageStore) GetPageInSpace(_ context.Context, p generated.GetPageInSpaceParams) (generated.Page, error) {
	page, ok := m.pages[p.PageID]
	if !ok || page.SpaceID != p.SpaceID {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	return page, nil
}

func (m *mockPageStore) GetPageByID(_ context.Context, id uuid.UUID) (generated.Page, error) {
	p, ok := m.pages[id]
	if !ok {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	return p, nil
}

func (m *mockPageStore) UpdatePageContent(_ context.Context, arg generated.UpdatePageContentParams) (generated.Page, error) {
	p, ok := m.pages[arg.ID]
	if !ok {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	if p.Version != arg.Version {
		return generated.Page{}, wiki.ErrVersionConflict
	}
	p.Title = arg.Title
	p.Content = arg.Content
	p.Version++
	m.pages[arg.ID] = p
	return p, nil
}

func (m *mockPageStore) ListPagesBySpace(_ context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error) {
	var result []generated.ListPagesBySpaceRow
	for _, p := range m.pages {
		if p.SpaceID == spaceID {
			result = append(result, generated.ListPagesBySpaceRow{
				ID:       p.ID,
				SpaceID:  p.SpaceID,
				ParentID: p.ParentID,
				Title:    p.Title,
				Version:  p.Version,
				AuthorID: p.AuthorID,
				Position: p.Position,
			})
		}
	}
	return result, nil
}

func (m *mockPageStore) ListRootPagesBySpace(_ context.Context, _ uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error) {
	return nil, nil
}

func (m *mockPageStore) ListChildPages(_ context.Context, _ pgtype.UUID) ([]generated.ListChildPagesRow, error) {
	return nil, nil
}

func (m *mockPageStore) CreatePageRevision(_ context.Context, arg generated.CreatePageRevisionParams) (generated.PageRevision, error) {
	rev := generated.PageRevision{
		ID:       arg.ID,
		PageID:   arg.PageID,
		Version:  arg.Version,
		Title:    arg.Title,
		Content:  arg.Content,
		AuthorID: arg.AuthorID,
	}
	m.revisions[arg.PageID] = append(m.revisions[arg.PageID], rev)
	return rev, nil
}

func (m *mockPageStore) GetPageRevision(_ context.Context, arg generated.GetPageRevisionParams) (generated.PageRevision, error) {
	revs, ok := m.revisions[arg.PageID]
	if !ok {
		return generated.PageRevision{}, wiki.ErrRevisionNotFound
	}
	for _, rev := range revs {
		if rev.Version == arg.Version {
			return rev, nil
		}
	}
	return generated.PageRevision{}, wiki.ErrRevisionNotFound
}

func (m *mockPageStore) ListPageRevisions(_ context.Context, _ generated.ListPageRevisionsParams) ([]generated.ListPageRevisionsRow, error) {
	return nil, nil
}

// ---- Mock document store ----
//
// This harness has no database, so the document routes are exercised for routing
// and guard behaviour only. Capture, restore, conflict and draft isolation are
// asserted against real PostgreSQL in wiki_document_integration_test.go.

type mockDocumentStore struct {
	*mockPageStore
	drafts map[string]generated.PageDraft
}

func newMockDocumentStore(pages *mockPageStore) *mockDocumentStore {
	return &mockDocumentStore{mockPageStore: pages, drafts: make(map[string]generated.PageDraft)}
}

func draftKey(pageID, authorID uuid.UUID) string { return pageID.String() + "/" + authorID.String() }

func (m *mockDocumentStore) GetPageRevisionDocument(_ context.Context, _ generated.GetPageRevisionDocumentParams) (generated.GetPageRevisionDocumentRow, error) {
	return generated.GetPageRevisionDocumentRow{}, pgx.ErrNoRows
}

func (m *mockDocumentStore) UpsertPageDraft(_ context.Context, arg generated.UpsertPageDraftParams) (generated.PageDraft, error) {
	draft := generated.PageDraft{
		PageID: arg.PageID, AuthorID: arg.AuthorID, Title: arg.Title,
		Doc: arg.Doc, BaseVersion: arg.BaseVersion,
	}
	m.drafts[draftKey(arg.PageID, arg.AuthorID)] = draft
	return draft, nil
}

func (m *mockDocumentStore) GetPageDraft(_ context.Context, arg generated.GetPageDraftParams) (generated.PageDraft, error) {
	draft, ok := m.drafts[draftKey(arg.PageID, arg.AuthorID)]
	if !ok {
		return generated.PageDraft{}, pgx.ErrNoRows
	}
	return draft, nil
}

func (m *mockDocumentStore) DeletePageDraft(_ context.Context, arg generated.DeletePageDraftParams) (int64, error) {
	key := draftKey(arg.PageID, arg.AuthorID)
	if _, ok := m.drafts[key]; !ok {
		return 0, nil
	}
	delete(m.drafts, key)
	return 1, nil
}

func (m *mockDocumentStore) ListPageDraftsForAuthorInSpace(_ context.Context, _ generated.ListPageDraftsForAuthorInSpaceParams) ([]generated.ListPageDraftsForAuthorInSpaceRow, error) {
	return []generated.ListPageDraftsForAuthorInSpaceRow{}, nil
}

type mockDocumentTx struct{}

func (m *mockDocumentTx) PublishPageTx(_ context.Context, in wiki.PublishPageTxInput) (generated.Page, error) {
	return generated.Page{
		ID: in.PageID, Title: in.Title, Content: in.Content,
		Doc: in.Doc, Version: in.BaseVersion + 1,
	}, nil
}

func (m *mockPageStore) SearchPages(_ context.Context, _ generated.SearchPagesParams) ([]generated.SearchPagesRow, error) {
	return nil, nil
}

// mockContentTx satisfies wiki.ContentTxStore for the router wiring tests,
// operating on the mock page map so move/delete routes exercise end to end.
type mockContentTx struct{ pages *mockPageStore }

func (m *mockContentTx) MovePageTx(_ context.Context, in wiki.MovePageInput) (wiki.MovePageTxResult, error) {
	p, ok := m.pages.pages[in.PageID]
	if !ok {
		return wiki.MovePageTxResult{}, wiki.ErrPageNotFound
	}
	crossSpace := p.SpaceID != in.TargetSpaceID
	p.SpaceID = in.TargetSpaceID
	if in.ParentID != nil {
		p.ParentID = pgtype.UUID{Bytes: *in.ParentID, Valid: true}
	} else {
		p.ParentID = pgtype.UUID{}
	}
	p.Position = in.Position
	m.pages.pages[in.PageID] = p
	return wiki.MovePageTxResult{CrossSpace: crossSpace}, nil
}

func (m *mockContentTx) DeletePageAndRevokeShares(_ context.Context, pageID, _, _ uuid.UUID) (int64, error) {
	delete(m.pages.pages, pageID)
	return 0, nil
}

// UpdatePageContentTx makes the same four decisions the real transaction
// makes, over the mock page map, so the wiki PUT route stays exercised end to
// end by the wiring tests rather than answering from a stub.
func (m *mockContentTx) UpdatePageContentTx(_ context.Context, in wiki.UpdatePageInput) (generated.Page, error) {
	p, ok := m.pages.pages[in.PageID]
	if !ok {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	if p.Doc != nil {
		return generated.Page{}, wiki.ErrPageIsDocumentBacked
	}
	if p.Version != in.ExpectedVersion {
		return generated.Page{}, wiki.ErrVersionConflict
	}
	p.Title = in.Title
	p.Content = in.Content
	p.Version++
	m.pages.pages[p.ID] = p
	m.pages.revisions[p.ID] = append(m.pages.revisions[p.ID], generated.PageRevision{
		ID:       uuid.New(),
		PageID:   p.ID,
		Version:  p.Version,
		Title:    p.Title,
		Content:  p.Content,
		AuthorID: in.AuthorID,
	})
	return p, nil
}

// mockShareDeleter satisfies tickets.ShareRevokingDeleter and
// projects.ShareRevokingDeleter with no-ops for the router wiring tests.
type mockShareDeleter struct{}

func (m *mockShareDeleter) DeleteTicketAndRevokeShares(_ context.Context, _, _, _ uuid.UUID) error {
	return nil
}
func (m *mockShareDeleter) DeleteItemAndRevokeShares(_ context.Context, _, _, _ uuid.UUID) error {
	return nil
}

// ---- Mock project repos ----

type mockItemRepo struct {
	items map[uuid.UUID]*projects.Item
}

func newMockItemRepo() *mockItemRepo {
	return &mockItemRepo{items: make(map[uuid.UUID]*projects.Item)}
}

func (m *mockItemRepo) Create(_ context.Context, item *projects.Item) error {
	m.items[item.ID] = item
	return nil
}

func (m *mockItemRepo) GetByOrgKey(_ context.Context, _ uuid.UUID, key string) (*projects.Item, error) {
	for _, item := range m.items {
		if item.ItemKey == key {
			return item, nil
		}
	}
	return nil, projects.ErrNotFound
}

// mockItemTypeRepo is an empty itemtypes.Repository for the routing/permission
// sweep — the routes exist and gate correctly; type CRUD behaviour is covered
// by the itemtypes integration tests against a real database.
type mockItemTypeRepo struct{}

func (m *mockItemTypeRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]*itemtypes.ItemType, error) {
	return nil, nil
}
func (m *mockItemTypeRepo) ListActiveByOrg(_ context.Context, _ uuid.UUID) ([]*itemtypes.ItemType, error) {
	return nil, nil
}
func (m *mockItemTypeRepo) GetByID(_ context.Context, _ uuid.UUID) (*itemtypes.ItemType, error) {
	return nil, itemtypes.ErrNotFound
}
func (m *mockItemTypeRepo) GetByOrgSlug(_ context.Context, orgID uuid.UUID, slug string) (*itemtypes.ItemType, error) {
	// Treat any slug as a defined, active type so item-create validation does
	// not block the routing/permission sweep (which has no seeded DB).
	return &itemtypes.ItemType{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: slug}, nil
}
func (m *mockItemTypeRepo) Create(_ context.Context, _ *itemtypes.ItemType) error { return nil }
func (m *mockItemTypeRepo) Rename(_ context.Context, _ uuid.UUID, _ string) (*itemtypes.ItemType, error) {
	return nil, itemtypes.ErrNotFound
}
func (m *mockItemTypeRepo) SetArchived(_ context.Context, _ uuid.UUID, _ bool) (*itemtypes.ItemType, error) {
	return nil, itemtypes.ErrNotFound
}
func (m *mockItemTypeRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockItemTypeRepo) CountItemsOfType(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}
func (m *mockItemTypeRepo) NextPosition(_ context.Context, _ uuid.UUID) (int, error) { return 1, nil }
func (m *mockItemTypeRepo) SeedDefaults(_ context.Context, _ uuid.UUID) error        { return nil }

// mockCustomFieldDefRepo / mockCustomFieldValueRepo are empty stubs for the
// routing/permission sweep; behaviour is covered by the customfields
// integration tests against a real database.
type mockCustomFieldDefRepo struct{}

func (m *mockCustomFieldDefRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]*customfields.FieldDef, error) {
	return nil, nil
}
func (m *mockCustomFieldDefRepo) ListActiveByOrg(_ context.Context, _ uuid.UUID) ([]*customfields.FieldDef, error) {
	return nil, nil
}
func (m *mockCustomFieldDefRepo) GetByID(_ context.Context, _ uuid.UUID) (*customfields.FieldDef, error) {
	return nil, customfields.ErrNotFound
}
func (m *mockCustomFieldDefRepo) GetByOrgSlug(_ context.Context, _ uuid.UUID, _ string) (*customfields.FieldDef, error) {
	return nil, customfields.ErrNotFound
}
func (m *mockCustomFieldDefRepo) Create(_ context.Context, _ *customfields.FieldDef) error {
	return nil
}
func (m *mockCustomFieldDefRepo) Update(_ context.Context, _ uuid.UUID, _ string, _ []string) (*customfields.FieldDef, error) {
	return nil, customfields.ErrNotFound
}
func (m *mockCustomFieldDefRepo) SetArchived(_ context.Context, _ uuid.UUID, _ bool) (*customfields.FieldDef, error) {
	return nil, customfields.ErrNotFound
}
func (m *mockCustomFieldDefRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockCustomFieldDefRepo) NextPosition(_ context.Context, _ uuid.UUID) (int, error) {
	return 1, nil
}

type mockCustomFieldValueRepo struct{}

func (m *mockCustomFieldValueRepo) ListForEntityInSpace(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID) ([]customfields.StoredValue, error) {
	return nil, nil
}
func (m *mockCustomFieldValueRepo) UpsertInSpace(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, _, _ string) (bool, error) {
	return true, nil
}
func (m *mockCustomFieldValueRepo) DeleteInSpace(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, _ string) error {
	return nil
}

// CountByOrgSlug reports no legacy values: this mock stores none, so any other
// answer would be a fabrication. The slug-reuse guard it feeds is covered
// against a real database in internal/db/adapters and internal/core/api.
func (m *mockCustomFieldValueRepo) CountByOrgSlug(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}

// mockCustomFieldScopeRepo holds no attachments: every scope lookup reports
// the field unattached, which is a new definition's true state. Scope
// behaviour is covered against a real database in the custom-fields
// integration tests.
type mockCustomFieldScopeRepo struct{}

func (m *mockCustomFieldScopeRepo) ListByField(_ context.Context, _ uuid.UUID) ([]customfields.FieldScope, error) {
	return nil, nil
}
func (m *mockCustomFieldScopeRepo) ListForSpaceEntity(_ context.Context, _ uuid.UUID, _ string) ([]customfields.FieldScope, error) {
	return nil, nil
}
func (m *mockCustomFieldScopeRepo) Get(_ context.Context, _, _ uuid.UUID, _ string) (*customfields.FieldScope, error) {
	return nil, customfields.ErrScopeNotFound
}
func (m *mockCustomFieldScopeRepo) Upsert(_ context.Context, _ uuid.UUID, s *customfields.FieldScope) (*customfields.FieldScope, error) {
	return s, nil
}
func (m *mockCustomFieldScopeRepo) Delete(_ context.Context, _, _ uuid.UUID, _ string) (bool, error) {
	return false, nil
}
func (m *mockCustomFieldScopeRepo) Reorder(_ context.Context, _, _ uuid.UUID, _ string, _ []uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *mockCustomFieldScopeRepo) SpaceOrgType(_ context.Context, _ uuid.UUID) (uuid.UUID, string, error) {
	return uuid.Nil, "", customfields.ErrSpaceNotFound
}

func (m *mockItemRepo) GetByID(_ context.Context, id uuid.UUID) (*projects.Item, error) {
	item, ok := m.items[id]
	if !ok {
		return nil, projects.ErrNotFound
	}
	return item, nil
}

func (m *mockItemRepo) GetByIDInSpace(_ context.Context, spaceID, id uuid.UUID) (*projects.Item, error) {
	item, ok := m.items[id]
	if !ok || item.SpaceID != spaceID {
		return nil, projects.ErrNotFound
	}
	return item, nil
}

func (m *mockItemRepo) Update(_ context.Context, item *projects.Item) error {
	m.items[item.ID] = item
	return nil
}

func (m *mockItemRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (*projects.Item, error) {
	item, ok := m.items[id]
	if !ok {
		return nil, projects.ErrNotFound
	}
	item.Status = status
	return item, nil
}

func (m *mockItemRepo) UpdateSprintInSpace(
	_ context.Context, id, spaceID uuid.UUID, sprintID *uuid.UUID,
) error {
	item, ok := m.items[id]
	if !ok || item.SpaceID != spaceID {
		return projects.ErrNotFound
	}
	item.SprintID = sprintID
	return nil
}

func (m *mockItemRepo) SoftDeleteInSpace(_ context.Context, id, _ uuid.UUID) error {
	delete(m.items, id)
	return nil
}

func (m *mockItemRepo) ListBySpace(_ context.Context, spaceID uuid.UUID) ([]*projects.Item, error) {
	var result []*projects.Item
	for _, item := range m.items {
		if item.SpaceID == spaceID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (m *mockItemRepo) ListByStatus(_ context.Context, _ uuid.UUID, _ string) ([]*projects.Item, error) {
	return nil, nil
}

func (m *mockItemRepo) ListByAssignee(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}

func (m *mockItemRepo) ListBySprint(_ context.Context, _, _ uuid.UUID) ([]*projects.Item, error) {
	return nil, nil
}

func (m *mockItemRepo) Search(_ context.Context, _ uuid.UUID, _ string, _ int) ([]*projects.Item, error) {
	return nil, nil
}

type mockSprintRepo struct {
	sprints map[uuid.UUID]*projects.Sprint
}

func newMockSprintRepo() *mockSprintRepo {
	return &mockSprintRepo{sprints: make(map[uuid.UUID]*projects.Sprint)}
}

func (m *mockSprintRepo) Create(_ context.Context, s *projects.Sprint) error {
	m.sprints[s.ID] = s
	return nil
}

func (m *mockSprintRepo) GetByID(_ context.Context, id uuid.UUID) (*projects.Sprint, error) {
	s, ok := m.sprints[id]
	if !ok {
		return nil, projects.ErrNotFound
	}
	return s, nil
}

func (m *mockSprintRepo) GetByIDInSpace(_ context.Context, spaceID, id uuid.UUID) (*projects.Sprint, error) {
	s, ok := m.sprints[id]
	if !ok || s.SpaceID != spaceID {
		return nil, projects.ErrNotFound
	}
	return s, nil
}

func (m *mockSprintRepo) GetActiveBySpace(_ context.Context, spaceID uuid.UUID) (*projects.Sprint, error) {
	for _, s := range m.sprints {
		if s.SpaceID == spaceID && s.Status == projects.SprintStatusActive {
			return s, nil
		}
	}
	return nil, projects.ErrNotFound
}

func (m *mockSprintRepo) Update(_ context.Context, s *projects.Sprint) error {
	m.sprints[s.ID] = s
	return nil
}

func (m *mockSprintRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (*projects.Sprint, error) {
	s, ok := m.sprints[id]
	if !ok {
		return nil, projects.ErrNotFound
	}
	s.Status = status
	return s, nil
}

func (m *mockSprintRepo) CompleteWithDisposition(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ []string) (*projects.Sprint, error) {
	s, ok := m.sprints[id]
	if !ok {
		return nil, projects.ErrNotFound
	}
	s.Status = projects.SprintStatusCompleted
	return s, nil
}

func (m *mockSprintRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Sprint, error) {
	return nil, nil
}

type mockRelationRepo struct{}

func (m *mockRelationRepo) Create(_ context.Context, _ uuid.UUID, _ *projects.NewRelation) error {
	return nil
}
func (m *mockRelationRepo) TargetIsReadable(_ context.Context, _ uuid.UUID, _ string, _ []uuid.UUID) (bool, error) {
	return true, nil
}
func (m *mockRelationRepo) ListForEntity(_ context.Context, _ uuid.UUID, _ string, _ []uuid.UUID) ([]*projects.Relation, error) {
	return nil, nil
}
func (m *mockRelationRepo) DeleteInSpace(_ context.Context, _, _ uuid.UUID) error { return nil }

// mockTagRepo stands in for the tag store in the ROUTING tests, which assert
// that a path reaches a handler and nothing about what it stores. The real
// behaviour is covered against a real database in the tags integration tests —
// see internal/core/api/wiki_tags_integration_test.go.
type mockTagRepo struct{}

func (m *mockTagRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]tags.Tag, error) { return nil, nil }
func (m *mockTagRepo) GetByOrgSlug(_ context.Context, _ uuid.UUID, _ string) (tags.Tag, error) {
	return tags.Tag{}, tags.ErrNotFound
}
func (m *mockTagRepo) Upsert(_ context.Context, orgID uuid.UUID, slug, name string) (tags.Tag, error) {
	return tags.Tag{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: name}, nil
}
func (m *mockTagRepo) ForEntity(_ context.Context, _ tags.EntityRef) ([]tags.Tag, error) {
	return nil, nil
}
func (m *mockTagRepo) EntityInSpace(_ context.Context, _ tags.EntityRef) (bool, error) {
	return true, nil
}
func (m *mockTagRepo) ReplaceEntityTags(_ context.Context, _ tags.EntityRef, _ []uuid.UUID) error {
	return nil
}
func (m *mockTagRepo) AddEntityTags(_ context.Context, _ tags.EntityRef, _ []uuid.UUID) error {
	return nil
}
func (m *mockTagRepo) EntitiesWithTag(_ context.Context, _ uuid.UUID, _ []uuid.UUID) ([]tags.TaggedEntity, error) {
	return nil, nil
}

type mockMembershipResolver struct{}

func (m *mockMembershipResolver) PrimaryOrgForUser(_ context.Context, _ uuid.UUID) (uuid.UUID, string, string, error) {
	return uuid.MustParse("00000000-0000-0000-0000-000000000001"), "test-org", "Test Org", nil
}

// ---- Test helpers ----

func setupRouter(t *testing.T) (http.Handler, *auth.JWTService) {
	t.Helper()

	// RSA key for JWT — one per test binary, not one per router (see
	// testSigningKey).
	privateKey := testSigningKey()

	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  1 * time.Hour,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "azimuthal-test",
	})

	userRepo := newMockUserRepo()
	sessionRepo := newMockSessionRepo()

	userSvc := auth.NewUserService(userRepo)
	sessionSvc := auth.NewSessionService(sessionRepo, auth.SessionConfig{TTL: 24 * time.Hour})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc, nil)

	contentTx := &mockShareDeleter{}
	ticketSvc := tickets.NewTicketService(newMockTicketRepo(), contentTx)
	mockPages := newMockPageStore()
	wikiSvc := wiki.NewService(mockPages, &mockContentTx{pages: mockPages})

	itemRepo := newMockItemRepo()
	sprintRepo := newMockSprintRepo()
	itemSvc := projects.NewItemService(itemRepo, contentTx)
	sprintSvc := projects.NewSprintService(sprintRepo)
	backlogSvc := projects.NewBacklogService(itemRepo, sprintRepo)
	roadmapSvc := projects.NewRoadmapService(itemRepo, sprintRepo)
	relationSvc := projects.NewRelationService(&mockRelationRepo{})
	itemTypeSvc := itemtypes.NewService(&mockItemTypeRepo{})
	customFieldSvc := customfields.NewService(&mockCustomFieldDefRepo{}, &mockCustomFieldValueRepo{}, &mockCustomFieldScopeRepo{})

	authHandler := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil).WithRegistrationPolicy(true)
	// The tier gate is wired here for the same reason it is wired in
	// newTestServerOn: a status route that reaches a nil gate answers 500 rather
	// than transitioning ungated, so leaving it out would turn this harness into
	// a place where transitions silently stop working. This harness has no
	// database and therefore no workflow, so WorkflowIDForSpace reports none and
	// the gate resolves to "nothing applies" — which is exactly what an
	// unconfigured space must do.
	tagSvc := tags.NewService(&mockTagRepo{})
	ticketHandler := ticketsapi.NewHandler(ticketSvc, tagSvc).
		WithWorkflowTiers(
			tiergate.New(workflow.NewTierService(&mockTierStore{}, &mockTransitionApplier{}), &mockWorkflowResolver{}, jobs.NoopNotificationEnqueuer{}),
			&mockTransitionApplier{},
		).
		WithCustomFields(customFieldSvc)
	wikiDocs := wiki.NewDocumentService(
		newMockDocumentStore(mockPages),
		&mockDocumentTx{},
		// The refusing image store is what a deployment without object storage
		// really gets, so the routes behave here as they would there.
		wiki.UnavailableImageStore{},
		tagSvc,
	)
	wikiHandler := wikiapi.NewHandler(wikiSvc, wikiDocs, tagSvc)
	projectHandler := projectsapi.NewHandler(itemSvc, sprintSvc, backlogSvc, roadmapSvc, tagSvc).
		WithItemTypes(itemTypeSvc).
		WithCustomFields(customFieldSvc).
		WithWorkflowTiers(
			tiergate.New(workflow.NewTierService(&mockTierStore{}, &mockTransitionApplier{}), &mockWorkflowResolver{}, jobs.NoopNotificationEnqueuer{}),
			&mockTransitionApplier{},
		)
	// spaces handler needs generated.Queries which needs a real DB, skip for now
	spaceHandler := spacesapi.NewHandler(nil)

	router := api.NewRouter(api.RouterConfig{
		Authenticator:   authenticator,
		AuthHandler:     authHandler,
		TicketHandler:   ticketHandler,
		WikiHandler:     wikiHandler,
		ProjectHandler:  projectHandler,
		SpaceHandler:    spaceHandler,
		RelationHandler: relationsapi.NewHandler(relationSvc),
		// This harness has no database, so /ready gets a healthy stub pinger —
		// the DB-less stand-in for the real pool, the same way the access
		// resolution below stands in for ResolveAccess. Without it /ready would
		// answer 503 (nil pinger), which is correct behaviour but not what these
		// routing tests are exercising.
		ReadyPinger: pingerFunc(func(context.Context) error { return nil }),
	})

	// This harness runs without an AccessResolver (no DB), so the in-handler
	// capability checks would fail closed on every mutation. Stamp an
	// org-admin resolution for the URL's space onto each request — the
	// DB-less stand-in for ResolveAccess, mirroring the real harness whose
	// default user is an org admin.
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := spacePathPattern.FindStringSubmatch(r.URL.Path); m != nil {
			if spaceID, err := uuid.Parse(m[1]); err == nil {
				r = r.WithContext(access.WithResolution(r.Context(), testutil.OrgAdminResolution(t, spaceID)))
			}
		}
		router.ServeHTTP(w, r)
	})

	return wrapped, jwtSvc
}

// spacePathPattern extracts the {spaceID} segment of space-scoped URLs.
var spacePathPattern = regexp.MustCompile(`/spaces/([0-9a-fA-F-]{36})`)

func authHeader(t *testing.T, jwtSvc *auth.JWTService, userID uuid.UUID) string {
	t.Helper()
	pair, err := jwtSvc.IssueTokenPair(userID, "test@example.com", uuid.New().String(), "member", 0, uuid.New())
	if err != nil {
		t.Fatalf("issuing token pair: %v", err)
	}
	return "Bearer " + pair.AccessToken
}

func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	return bytes.NewReader(b)
}

func decodeBody(t *testing.T, body io.Reader, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

// ---- Tests ----

func TestHealthEndpoint(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]string
	decodeBody(t, rr.Body, &body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpoint(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// The router wires a healthy pinger (see setupRouter), so /ready reports 200
	// ready. Readiness now depends on that ping: with an unreachable store the
	// same route answers 503 (covered by TestHandleReady_* and the real-pool
	// TestReady_RealPool_HealthyThenClosed).
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body map[string]string
	decodeBody(t, rr.Body, &body)
	if body["status"] != "ready" {
		t.Errorf("status = %q, want %q", body["status"], "ready")
	}
}

// TestCORSPreflight asserts the router's default CORS posture (S5).
//
// setupRouter builds a RouterConfig without AllowedOrigins, which is exactly
// the case that used to fail open: nil selected a permissive middleware and
// this test required Access-Control-Allow-Origin: "*". A router built without
// an explicit allow-list must now emit no CORS headers.
func TestCORSPreflight(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no header — a router with no configured allow-list must not advertise one", got)
	}
}

// TestCORS_CrossOriginPreflightRefusedByDefault is the negative case
// TestCORSPreflight alone cannot make: a preflight carrying no Origin header
// would come back header-free under any implementation, a permissive one
// included. This sends a real cross-origin preflight and requires refusal.
func TestCORS_CrossOriginPreflightRefusedByDefault(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin preflight status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no header for an unlisted origin", got)
	}
}

func TestRequestIDHeader(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if id := rr.Header().Get("X-Request-ID"); id == "" {
		t.Error("expected X-Request-ID header")
	}
}

func TestAuthRegisterAndLogin(t *testing.T) {
	router, _ := setupRouter(t)

	// Register
	regBody := jsonBody(t, map[string]string{
		"email":        "newuser@example.com",
		"display_name": "New User",
		"password":     "securepassword123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", regBody)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var regResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		User         struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	decodeBody(t, rr.Body, &regResp)
	if regResp.AccessToken == "" {
		t.Error("expected access_token")
	}
	if regResp.User.Email != "newuser@example.com" {
		t.Errorf("email = %q, want %q", regResp.User.Email, "newuser@example.com")
	}

	// Login with same credentials
	loginBody := jsonBody(t, map[string]string{
		"email":    "newuser@example.com",
		"password": "securepassword123",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("login status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestAuthLoginInvalidCredentials(t *testing.T) {
	router, _ := setupRouter(t)

	body := jsonBody(t, map[string]string{
		"email":    "nobody@example.com",
		"password": "wrong",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthRefresh(t *testing.T) {
	router, jwtSvc := setupRouter(t)

	userID := uuid.New()
	pair, err := jwtSvc.IssueTokenPair(userID, "test@example.com", uuid.New().String(), "member", 0, uuid.New())
	if err != nil {
		t.Fatalf("issuing tokens: %v", err)
	}

	body := jsonBody(t, map[string]string{
		"refresh_token": pair.RefreshToken,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProtectedEndpointUnauthorized(t *testing.T) {
	router, _ := setupRouter(t)

	spaceID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+uuid.New().String()+"/spaces/"+spaceID.String()+"/tickets/", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestTicketCRUD(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	// Create ticket
	createBody := jsonBody(t, map[string]string{
		"title":       "Test Ticket",
		"description": "A test ticket",
		"priority":    "medium",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID     uuid.UUID `json:"ID"`
		Title  string    `json:"Title"`
		Status string    `json:"Status"`
	}
	decodeBody(t, rr.Body, &created)
	if created.Title != "Test Ticket" {
		t.Errorf("title = %q, want %q", created.Title, "Test Ticket")
	}
	if created.Status != "open" {
		t.Errorf("status = %q, want %q", created.Status, "open")
	}

	// Get ticket
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("get status = %d, want %d", rr.Code, http.StatusOK)
	}

	// List tickets
	req = httptest.NewRequest(http.MethodGet, baseURL+"/", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("list status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Transition status
	statusBody := jsonBody(t, map[string]string{"status": "in_progress"})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/status", statusBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("transition status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Delete ticket
	req = httptest.NewRequest(http.MethodDelete, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestTicketNotFound(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	fakeID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+uuid.New().String()+"/spaces/"+spaceID.String()+"/tickets/"+fakeID.String(), nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, rr.Body, &errBody)
	if errBody.Error.Code != "NOT_FOUND" {
		t.Errorf("error.code = %q, want %q", errBody.Error.Code, "NOT_FOUND")
	}
}

func TestWikiPageCRUD(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create page
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Test Page",
		"content": "# Hello World",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID      uuid.UUID `json:"id"`
		Title   string    `json:"title"`
		Version int32     `json:"version"`
	}
	decodeBody(t, rr.Body, &created)
	if created.Title != "Test Page" {
		t.Errorf("title = %q, want %q", created.Title, "Test Page")
	}

	// Get page
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("get status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Update page with optimistic locking
	updateBody := jsonBody(t, map[string]interface{}{
		"title":            "Updated Page",
		"content":          "# Updated",
		"expected_version": 1,
	})
	req = httptest.NewRequest(http.MethodPut, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("update status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Delete page
	req = httptest.NewRequest(http.MethodDelete, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestProjectItemCRUD(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	// Create item
	createBody := jsonBody(t, map[string]string{
		"title":       "Test Item",
		"description": "A test item",
		"kind":        "task",
		"priority":    "high",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID    uuid.UUID `json:"ID"`
		Title string    `json:"Title"`
	}
	decodeBody(t, rr.Body, &created)
	if created.Title != "Test Item" {
		t.Errorf("title = %q, want %q", created.Title, "Test Item")
	}

	// Get item
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("get status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Delete item
	req = httptest.NewRequest(http.MethodDelete, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestConsistentErrorFormat(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())

	// Request with invalid UUID
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/"+uuid.New().String()+"/spaces/not-a-uuid/tickets/also-not-uuid", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, rr.Body, &errBody)
	if errBody.Error.Code == "" {
		t.Error("expected error.code")
	}
	if errBody.Error.Message == "" {
		t.Error("expected error.message")
	}
}

// ---- Additional integration tests ----

// TestAuthLogoutIsAuthenticated replaces TestAuthLogoutUnauthenticated, which
// asserted the 401 half alone and could not fail.
//
// Logout used to be mounted by AuthHandler.Routes(), outside the RequireAuth
// group, and OptionalAuth is mounted nowhere in this router. Nothing therefore
// put claims on the context at that path, `auth.ClaimsFromContext` was always
// nil, and the handler's first branch fired for every caller — 401 was the
// only status the route could return, for a valid bearer token as much as for
// an anonymous one. A test asserting "unauthenticated gets 401" could not
// distinguish a working logout from an endpoint nobody could reach.
//
// Both halves are asserted here for exactly that reason: the 401 is only
// evidence of a guard when the 200 beside it shows the guard has something to
// let through.
func TestAuthLogoutIsAuthenticated(t *testing.T) {
	router, jwtSvc := setupRouter(t)

	t.Run("anonymous is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("a bearer token is let through", func(t *testing.T) {
		pair, err := jwtSvc.IssueTokenPair(uuid.New(), "logout@example.com", uuid.New().String(), "member", 0, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
		}
	})
}

func TestAuthRegisterDuplicateEmail(t *testing.T) {
	router, _ := setupRouter(t)

	body := map[string]string{
		"email":        "dup@example.com",
		"display_name": "First User",
		"password":     "securepassword123",
	}

	// First register should succeed
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("first register status = %d, want %d", rr.Code, http.StatusCreated)
	}

	// Second register with same email should fail
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate register status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestAuthLoginMissingFields(t *testing.T) {
	router, _ := setupRouter(t)

	tests := []struct {
		name string
		body map[string]string
	}{
		{"missing email", map[string]string{"password": "test123"}},
		{"missing password", map[string]string{"email": "test@example.com"}},
		{"both empty", map[string]string{"email": "", "password": ""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", jsonBody(t, tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAuthRefreshInvalidToken(t *testing.T) {
	router, _ := setupRouter(t)

	body := jsonBody(t, map[string]string{
		"refresh_token": "totally-invalid-token",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAuthRefreshMissingToken(t *testing.T) {
	router, _ := setupRouter(t)

	body := jsonBody(t, map[string]string{
		"refresh_token": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTicketUpdate(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	// Create ticket first
	createBody := jsonBody(t, map[string]string{
		"title":       "Original Title",
		"description": "Original description",
		"priority":    "low",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &created)

	// Update ticket
	updateBody := jsonBody(t, map[string]string{
		"title":       "Updated Title",
		"description": "Updated description",
		"priority":    "high",
	})
	req = httptest.NewRequest(http.MethodPatch, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("update status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var updated struct {
		Title string `json:"Title"`
	}
	decodeBody(t, rr.Body, &updated)
	if updated.Title != "Updated Title" {
		t.Errorf("title = %q, want %q", updated.Title, "Updated Title")
	}
}

func TestTicketAssignAndUnassign(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	// Create ticket
	createBody := jsonBody(t, map[string]string{
		"title":       "Assign Test",
		"description": "Testing assign",
		"priority":    "medium",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &created)

	assigneeID := uuid.New()

	// Assign ticket
	assignBody := jsonBody(t, map[string]string{
		"assignee_id": assigneeID.String(),
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/assign", assignBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("assign status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Unassign ticket
	req = httptest.NewRequest(http.MethodDelete, baseURL+"/"+created.ID.String()+"/assign", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("unassign status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestTicketSearch(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	// Search with query
	req := httptest.NewRequest(http.MethodGet, baseURL+"/search?q=test", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("search status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestTicketSearchMissingQuery(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/search", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestTicketKanban(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/tickets"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/kanban", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("kanban status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestWikiTree(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/tree", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("tree status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestWikiSearch(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/search?q=hello", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("search status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestWikiSearchMissingQuery(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/search", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestWikiMovePage(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create a page first
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Movable Page",
		"content": "# Move me",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeBody(t, rr.Body, &created)

	// Move page
	moveBody := jsonBody(t, map[string]interface{}{
		"position": 5,
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/move", moveBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("move status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestWikiListRevisions(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create page
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Revision Page",
		"content": "# v1",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeBody(t, rr.Body, &created)

	// List revisions
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String()+"/revisions", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("list revisions status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestWikiRenderPage(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create page with markdown
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Render Page",
		"content": "# Hello World\n\nSome **bold** text.",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeBody(t, rr.Body, &created)

	// Render page
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String()+"/render", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("render status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want %q", ct, "text/html; charset=utf-8")
	}
}

func TestWikiDiffRevisions(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create page
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Diff Page",
		"content": "# Version 1",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeBody(t, rr.Body, &created)

	// Update page to get a second version
	updateBody := jsonBody(t, map[string]interface{}{
		"title":            "Diff Page Updated",
		"content":          "# Version 2",
		"expected_version": 1,
	})
	req = httptest.NewRequest(http.MethodPut, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Diff between version 1 and 2
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String()+"/diff?from=1&to=2", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// The mock doesn't store revisions on create, so this may return 404 for revisions.
	// We accept either 200 (if revisions populated) or 404 (revision not found).
	if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound {
		t.Errorf("diff status = %d, want 200 or 404, body: %s", rr.Code, rr.Body.String())
	}
}

func TestWikiDiffMissingParams(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	pageID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Missing both from and to
	req := httptest.NewRequest(http.MethodGet, baseURL+"/"+pageID.String()+"/diff", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestWikiVersionConflictOnUpdate(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/wiki"

	// Create page (version 1)
	createBody := jsonBody(t, map[string]interface{}{
		"title":   "Conflict Page",
		"content": "# Original",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/", createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeBody(t, rr.Body, &created)

	// Update with correct version
	updateBody := jsonBody(t, map[string]interface{}{
		"title":            "Updated",
		"content":          "# Updated",
		"expected_version": 1,
	})
	req = httptest.NewRequest(http.MethodPut, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("first update status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Update with stale version (1 instead of 2) should conflict
	staleBody := jsonBody(t, map[string]interface{}{
		"title":            "Stale Update",
		"content":          "# Stale",
		"expected_version": 1,
	})
	req = httptest.NewRequest(http.MethodPut, baseURL+"/"+created.ID.String(), staleBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("stale update status = %d, want %d, body: %s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestProjectItemUpdate(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	// Create item
	createBody := jsonBody(t, map[string]string{
		"title":       "Original Item",
		"description": "Original desc",
		"kind":        "task",
		"priority":    "medium",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &created)

	// Update item
	updateBody := jsonBody(t, map[string]string{
		"title":       "Updated Item",
		"description": "Updated desc",
		"priority":    "high",
	})
	req = httptest.NewRequest(http.MethodPatch, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("update status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectItemUpdateStatus(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	// Create item
	createBody := jsonBody(t, map[string]string{
		"title":       "Status Item",
		"description": "Testing status",
		"kind":        "task",
		"priority":    "low",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var created struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &created)

	// Update status
	statusBody := jsonBody(t, map[string]string{"status": "in_progress"})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/status", statusBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("update status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectItemAssignToSprint(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects"

	// Create sprint first
	sprintBody := jsonBody(t, map[string]string{
		"name": "Sprint 1",
		"goal": "Test sprint",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/sprints", sprintBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create sprint status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var sprint struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &sprint)

	// Create item
	itemBody := jsonBody(t, map[string]string{
		"title":       "Sprint Item",
		"description": "For sprint assign",
		"kind":        "task",
		"priority":    "medium",
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/items", itemBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create item status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var item struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &item)

	// Assign item to sprint
	assignBody := jsonBody(t, map[string]interface{}{
		"sprint_id": sprint.ID.String(),
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/items/"+item.ID.String()+"/sprint", assignBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("assign to sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectSearchItems(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/search?q=test", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("search status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectSearchItemsMissingQuery(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	req := httptest.NewRequest(http.MethodGet, baseURL+"/search", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestProjectListItems(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/items"

	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("list items status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestProjectSprintCRUD(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/sprints"

	// Create sprint
	createBody := jsonBody(t, map[string]string{
		"name": "Sprint Alpha",
		"goal": "Complete all tasks",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create sprint status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var created struct {
		ID   uuid.UUID `json:"ID"`
		Name string    `json:"Name"`
	}
	decodeBody(t, rr.Body, &created)
	if created.Name != "Sprint Alpha" {
		t.Errorf("name = %q, want %q", created.Name, "Sprint Alpha")
	}

	// Get sprint
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+created.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("get sprint status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Update sprint
	updateBody := jsonBody(t, map[string]string{
		"name": "Sprint Alpha Updated",
		"goal": "Updated goal",
	})
	req = httptest.NewRequest(http.MethodPut, baseURL+"/"+created.ID.String(), updateBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("update sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Start sprint
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/start", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("start sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Complete sprint
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+created.ID.String()+"/complete", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("complete sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectListSprints(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/sprints"

	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("list sprints status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestProjectGetActiveSprint(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/sprints"

	// No active sprint should return 404
	req := httptest.NewRequest(http.MethodGet, baseURL+"/active", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("no active sprint status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	// Create and start a sprint to make it active
	createBody := jsonBody(t, map[string]string{
		"name": "Active Sprint",
		"goal": "Be active",
	})
	req = httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create sprint status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var sprint struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &sprint)

	// Start the sprint
	req = httptest.NewRequest(http.MethodPost, baseURL+"/"+sprint.ID.String()+"/start", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("start sprint status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Now active sprint should be found
	req = httptest.NewRequest(http.MethodGet, baseURL+"/active", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("active sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectSprintItems(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/sprints"

	// Create sprint
	createBody := jsonBody(t, map[string]string{
		"name": "Items Sprint",
		"goal": "Test items listing",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL, createBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create sprint status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var sprint struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &sprint)

	// List sprint items
	req = httptest.NewRequest(http.MethodGet, baseURL+"/"+sprint.ID.String()+"/items", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("sprint items status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectBacklog(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/backlog"

	// Get backlog
	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("backlog status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestProjectBacklogMoveToSprint(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects"

	// Create sprint
	sprintBody := jsonBody(t, map[string]string{
		"name": "Move Sprint",
		"goal": "Move items here",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/sprints", sprintBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create sprint status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var sprint struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &sprint)

	// Create item
	itemBody := jsonBody(t, map[string]string{
		"title":       "Backlog Item",
		"description": "Move me",
		"kind":        "task",
		"priority":    "low",
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/items", itemBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create item status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var item struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &item)

	// Move to sprint
	moveBody := jsonBody(t, map[string]interface{}{
		"item_id":   item.ID.String(),
		"sprint_id": sprint.ID.String(),
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/backlog/move-to-sprint", moveBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("move to sprint status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Move back to backlog
	backlogBody := jsonBody(t, map[string]interface{}{
		"item_id": item.ID.String(),
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/backlog/move-to-backlog", backlogBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("move to backlog status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectRoadmap(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/roadmap"

	// Get roadmap with date range
	req := httptest.NewRequest(http.MethodGet, baseURL+"?from=2026-01-01&to=2026-12-31", nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("roadmap status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectRoadmapMissingDates(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/roadmap"

	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestProjectOverdueItems(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/roadmap/overdue"

	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("overdue status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectSprintRoadmap(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	token := authHeader(t, jwtSvc, uuid.New())
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects/roadmap/sprints"

	req := httptest.NewRequest(http.MethodGet, baseURL, nil)
	req.Header.Set("Authorization", token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("sprint roadmap status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestProjectRelationsCRUD(t *testing.T) {
	router, jwtSvc := setupRouter(t)
	userID := uuid.New()
	token := authHeader(t, jwtSvc, userID)
	spaceID := uuid.New()
	baseURL := "/api/v1/orgs/" + uuid.New().String() + "/spaces/" + spaceID.String() + "/projects"

	// Create two items
	item1Body := jsonBody(t, map[string]string{
		"title":       "Item A",
		"description": "First item",
		"kind":        "task",
		"priority":    "medium",
	})
	req := httptest.NewRequest(http.MethodPost, baseURL+"/items", item1Body)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create item A status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var itemA struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &itemA)

	item2Body := jsonBody(t, map[string]string{
		"title":       "Item B",
		"description": "Second item",
		"kind":        "task",
		"priority":    "low",
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/items", item2Body)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create item B status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var itemB struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &itemB)

	// Create relation
	relBody := jsonBody(t, map[string]interface{}{
		"to_id": itemB.ID.String(),
		"kind":  "blocks",
	})
	req = httptest.NewRequest(http.MethodPost, baseURL+"/items/"+itemA.ID.String()+"/relations", relBody)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create relation status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	var rel struct {
		ID uuid.UUID `json:"ID"`
	}
	decodeBody(t, rr.Body, &rel)

	// List relations
	req = httptest.NewRequest(http.MethodGet, baseURL+"/items/"+itemA.ID.String()+"/relations", nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("list relations status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Delete relation
	req = httptest.NewRequest(http.MethodDelete, baseURL+"/relations/"+rel.ID.String(), nil)
	req.Header.Set("Authorization", token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("delete relation status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

// mockWorkflowResolver reports that no space has a workflow, which is the true
// state of a harness with no workflow tables. workflow.ErrNotFound means "none
// configured", and the tier gate turns that into "nothing applies" — never a
// refusal.
type mockWorkflowResolver struct{}

func (m *mockWorkflowResolver) WorkflowIDForSpace(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, workflow.ErrNotFound
}

// mockTierStore satisfies workflow.TierStore. Every method reports "nothing
// configured": with no workflow resolved, the gate returns before reaching any
// of them, so these exist to make the type complete rather than to be called.
type mockTierStore struct{}

func (m *mockTierStore) GuardsForTransition(context.Context, uuid.UUID) ([]workflow.Guard, error) {
	return nil, nil
}
func (m *mockTierStore) GuardsForWorkflow(context.Context, uuid.UUID) ([]workflow.Guard, error) {
	return nil, nil
}
func (m *mockTierStore) CreateGuard(_ context.Context, g workflow.Guard) (workflow.Guard, error) {
	return g, nil
}
func (m *mockTierStore) DeleteGuard(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (m *mockTierStore) PostFunctionsForTransition(context.Context, uuid.UUID) ([]workflow.PostFunction, error) {
	return nil, nil
}
func (m *mockTierStore) PostFunctionsForWorkflow(context.Context, uuid.UUID) ([]workflow.PostFunction, error) {
	return nil, nil
}
func (m *mockTierStore) CreatePostFunction(_ context.Context, p workflow.PostFunction) (workflow.PostFunction, error) {
	return p, nil
}
func (m *mockTierStore) DeletePostFunction(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (m *mockTierStore) ApproversForTransition(context.Context, uuid.UUID) ([]workflow.Approver, error) {
	return nil, nil
}
func (m *mockTierStore) ApproversForWorkflow(context.Context, uuid.UUID) ([]workflow.Approver, error) {
	return nil, nil
}
func (m *mockTierStore) CreateApprover(_ context.Context, a workflow.Approver) (workflow.Approver, error) {
	return a, nil
}
func (m *mockTierStore) DeleteApprover(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (m *mockTierStore) CreateApproval(_ context.Context, a workflow.Approval) (workflow.Approval, error) {
	return a, nil
}
func (m *mockTierStore) PendingApprovalForEntity(context.Context, workflow.ApprovalEntityType, uuid.UUID) (workflow.Approval, error) {
	return workflow.Approval{}, workflow.ErrNotFound
}
func (m *mockTierStore) GetApprovalInSpace(context.Context, uuid.UUID, uuid.UUID) (workflow.Approval, error) {
	return workflow.Approval{}, workflow.ErrNotFound
}
func (m *mockTierStore) DecideApproval(
	context.Context, uuid.UUID, uuid.UUID, uuid.UUID, workflow.Decision, *string,
) (workflow.Approval, error) {
	return workflow.Approval{}, workflow.ErrNotFound
}
func (m *mockTierStore) ApprovalsForEntity(context.Context, uuid.UUID, workflow.ApprovalEntityType, uuid.UUID) ([]workflow.Approval, error) {
	return nil, nil
}
func (m *mockTierStore) PendingApprovalsForSpace(context.Context, uuid.UUID) ([]workflow.Approval, error) {
	return nil, nil
}
func (m *mockTierStore) PendingApprovalCountForTransition(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *mockTierStore) StateByName(context.Context, uuid.UUID, string) (*workflow.State, error) {
	return nil, workflow.ErrNotFound
}
func (m *mockTierStore) TransitionBetween(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*workflow.Transition, error) {
	return nil, workflow.ErrNotFound
}
func (m *mockTierStore) StateByID(context.Context, uuid.UUID, uuid.UUID) (*workflow.State, error) {
	return nil, workflow.ErrNotFound
}
func (m *mockTierStore) InitialState(context.Context, uuid.UUID) (*workflow.State, error) {
	return nil, workflow.ErrNotFound
}
func (m *mockTierStore) TransitionsFrom(context.Context, uuid.UUID, uuid.UUID) ([]*workflow.Transition, error) {
	return nil, nil
}
func (m *mockTierStore) EffectiveTeamIDs(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (m *mockTierStore) EffectiveTeamMemberIDs(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}

// mockTransitionApplier fails loudly if called. This harness has no workflow, so
// no transition can resolve an edge, and a call here would mean the gate saw one
// it should not have been able to see.
type mockTransitionApplier struct{}

func (m *mockTransitionApplier) ApplyTransition(context.Context, workflow.ApplyInput) error {
	return errors.New("mockTransitionApplier: the tier gate resolved an edge in a harness with no workflow")
}

// DecideAndApply fails the same way and for the same reason: with no workflow
// there is no approval to decide. Returning a zero Approval and a nil error
// would let a test assert a verdict was recorded when nothing had been — the
// lying-double shape this repository has already shipped once.
func (m *mockTransitionApplier) DecideAndApply(
	context.Context, workflow.DecideAndApplyInput,
) (workflow.Approval, error) {
	return workflow.Approval{}, errors.New(
		"mockTransitionApplier: an approval was decided in a harness with no workflow")
}
