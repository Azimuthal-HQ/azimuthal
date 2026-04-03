package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
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

func (m *mockUserRepo) GetByEmail(_ context.Context, email string) (*auth.User, error) {
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

func (m *mockUserRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.users, id)
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

func (m *mockTicketRepo) Delete(_ context.Context, id uuid.UUID) error {
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

func (m *mockPageStore) UpdatePagePosition(_ context.Context, _ generated.UpdatePagePositionParams) error {
	return nil
}

func (m *mockPageStore) SoftDeletePage(_ context.Context, id uuid.UUID) error {
	delete(m.pages, id)
	return nil
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

func (m *mockPageStore) ListPageRevisions(_ context.Context, pageID uuid.UUID) ([]generated.ListPageRevisionsRow, error) {
	return nil, nil
}

func (m *mockPageStore) SearchPages(_ context.Context, _ generated.SearchPagesParams) ([]generated.SearchPagesRow, error) {
	return nil, nil
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

func (m *mockItemRepo) GetByID(_ context.Context, id uuid.UUID) (*projects.Item, error) {
	item, ok := m.items[id]
	if !ok {
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

func (m *mockItemRepo) UpdateSprint(_ context.Context, id uuid.UUID, sprintID *uuid.UUID) error {
	item, ok := m.items[id]
	if !ok {
		return projects.ErrNotFound
	}
	item.SprintID = sprintID
	return nil
}

func (m *mockItemRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
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

func (m *mockItemRepo) ListBySprint(_ context.Context, _ uuid.UUID) ([]*projects.Item, error) {
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

func (m *mockSprintRepo) ListBySpace(_ context.Context, _ uuid.UUID) ([]*projects.Sprint, error) {
	return nil, nil
}

type mockRelationRepo struct{}

func (m *mockRelationRepo) Create(_ context.Context, rel *projects.Relation) error {
	rel.ID = uuid.New()
	return nil
}
func (m *mockRelationRepo) ListByItem(_ context.Context, _ uuid.UUID) ([]*projects.Relation, error) {
	return nil, nil
}
func (m *mockRelationRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

type mockLabelRepo struct{}

func (m *mockLabelRepo) Create(_ context.Context, l *projects.Label) error {
	l.ID = uuid.New()
	return nil
}
func (m *mockLabelRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]*projects.Label, error) {
	return nil, nil
}
func (m *mockLabelRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

// ---- Test helpers ----

func setupRouter(t *testing.T) (http.Handler, *auth.JWTService) {
	t.Helper()

	// RSA keys for JWT
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

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
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)

	ticketSvc := tickets.NewTicketService(newMockTicketRepo())
	wikiSvc := wiki.NewService(newMockPageStore())

	itemRepo := newMockItemRepo()
	sprintRepo := newMockSprintRepo()
	itemSvc := projects.NewItemService(itemRepo)
	sprintSvc := projects.NewSprintService(sprintRepo)
	backlogSvc := projects.NewBacklogService(itemRepo, sprintRepo)
	roadmapSvc := projects.NewRoadmapService(itemRepo, sprintRepo)
	relationSvc := projects.NewRelationService(&mockRelationRepo{})
	labelSvc := projects.NewLabelService(&mockLabelRepo{})

	authHandler := authapi.NewHandler(userSvc, jwtSvc, sessionSvc)
	ticketHandler := ticketsapi.NewHandler(ticketSvc)
	wikiHandler := wikiapi.NewHandler(wikiSvc)
	projectHandler := projectsapi.NewHandler(itemSvc, sprintSvc, backlogSvc, roadmapSvc, relationSvc, labelSvc)
	// spaces handler needs generated.Queries which needs a real DB, skip for now
	spaceHandler := spacesapi.NewHandler(nil)

	router := api.NewRouter(api.RouterConfig{
		Authenticator:  authenticator,
		AuthHandler:    authHandler,
		TicketHandler:  ticketHandler,
		WikiHandler:    wikiHandler,
		ProjectHandler: projectHandler,
		SpaceHandler:   spaceHandler,
	})

	return router, jwtSvc
}

func authHeader(t *testing.T, jwtSvc *auth.JWTService, userID uuid.UUID) string {
	t.Helper()
	pair, err := jwtSvc.IssueTokenPair(userID, "test@example.com")
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

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCORSPreflight(t *testing.T) {
	router, _ := setupRouter(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q, want '*'", got)
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
	pair, err := jwtSvc.IssueTokenPair(userID, "test@example.com")
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces/"+spaceID.String()+"/tickets/", nil)
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
	baseURL := "/api/v1/spaces/" + spaceID.String() + "/tickets"

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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces/"+spaceID.String()+"/tickets/"+fakeID.String(), nil)
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
	baseURL := "/api/v1/spaces/" + spaceID.String() + "/wiki"

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
	baseURL := "/api/v1/spaces/" + spaceID.String() + "/projects/items"

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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spaces/not-a-uuid/tickets/also-not-uuid", nil)
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
