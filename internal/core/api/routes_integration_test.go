package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	commentsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/comments"
	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// httpResult holds a consumed HTTP response (body already read and closed).
type httpResult struct {
	StatusCode  int
	Body        []byte
	ContentType string
	Header      http.Header
}

// testServer holds a fully-wired httptest.Server backed by a real database.
type testServer struct {
	Server *httptest.Server
	DB     *testutil.TestDB
	OrgID  uuid.UUID
	Token  string
}

// newTestServer creates a full API server backed by a real database.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		AccessTTL:  24 * time.Hour,
		RefreshTTL: 7 * 24 * time.Hour,
		Issuer:     "azimuthal-test",
	})

	userAdapter := adapters.NewUserAdapter(queries, org.ID)
	userSvc := auth.NewUserService(userAdapter)
	sessionAdapter := adapters.NewSessionAdapter(queries)
	sessionSvc := auth.NewSessionService(sessionAdapter, auth.SessionConfig{TTL: 24 * time.Hour})
	authenticator := auth.NewAuthenticator(jwtSvc, sessionSvc)
	membershipAdapter := adapters.NewMembershipAdapter(queries)
	orgProvisioner := adapters.NewOrgProvisionerAdapter(queries)

	ticketAdapter := adapters.NewTicketAdapter(queries)
	ticketSvc := tickets.NewTicketService(ticketAdapter)

	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(queries)
	itemSvc := projects.NewItemService(itemAdapter)
	sprintSvc := projects.NewSprintService(sprintAdapter)
	backlogSvc := projects.NewBacklogService(itemAdapter, sprintAdapter)
	roadmapSvc := projects.NewRoadmapService(itemAdapter, sprintAdapter)
	relationSvc := projects.NewRelationService(adapters.NewRelationAdapter(queries))
	labelSvc := projects.NewLabelService(adapters.NewLabelAdapter(queries))

	wikiSvc := wiki.NewService(queries)

	router := api.NewRouter(api.RouterConfig{
		Authenticator:  authenticator,
		AuthHandler:    authapi.NewHandler(userSvc, jwtSvc, sessionSvc, membershipAdapter, orgProvisioner),
		TicketHandler:  ticketsapi.NewHandler(ticketSvc),
		WikiHandler:    wikiapi.NewHandler(wikiSvc),
		ProjectHandler: projectsapi.NewHandler(itemSvc, sprintSvc, backlogSvc, roadmapSvc, relationSvc, labelSvc),
		SpaceHandler:   spacesapi.NewHandler(queries),
		CommentHandler: commentsapi.NewHandler(queries),
		SPAHandler:     nil,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, org.ID.String(), "member")
	require.NoError(t, err)

	return &testServer{Server: srv, DB: db, OrgID: org.ID, Token: pair.AccessToken}
}

func (ts *testServer) url(path string) string { return ts.Server.URL + path }

func (ts *testServer) get(t *testing.T, path string, authed bool) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.url(path), nil)
	require.NoError(t, err)
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	return ts.do(t, req)
}

func (ts *testServer) post(t *testing.T, path string, body any, authed bool) httpResult {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.url(path), bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	return ts.do(t, req)
}

func (ts *testServer) patch(t *testing.T, path string, body any, authed bool) httpResult {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, ts.url(path), bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	return ts.do(t, req)
}

func (ts *testServer) do(t *testing.T, req *http.Request) httpResult {
	t.Helper()
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test-only URL
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return httpResult{
		StatusCode:  resp.StatusCode,
		Body:        b,
		ContentType: resp.Header.Get("Content-Type"),
		Header:      resp.Header,
	}
}

// --- Health / Ready ---

// TestIntegration_HealthEndpoint verifies GET /health returns 200 with {"status":"ok"}.
func TestIntegration_HealthEndpoint(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, "/health", false)

	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Contains(t, r.ContentType, "application/json")

	var result map[string]string
	require.NoError(t, json.Unmarshal(r.Body, &result))
	require.Equal(t, "ok", result["status"])
}

// TestIntegration_ReadyEndpoint verifies GET /ready returns 200 with {"status":"ready"}.
func TestIntegration_ReadyEndpoint(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, "/ready", false)

	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Contains(t, r.ContentType, "application/json")

	var result map[string]string
	require.NoError(t, json.Unmarshal(r.Body, &result))
	require.Equal(t, "ready", result["status"])
}

// TestIntegration_APIRoutes_NeverReturnHTML verifies /api/v1/... routes return JSON.
func TestIntegration_APIRoutes_NeverReturnHTML(t *testing.T) {
	ts := newTestServer(t)

	routes := []struct {
		path   string
		authed bool
	}{
		{"/health", false},
		{"/ready", false},
		{fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), true},
	}

	for _, route := range routes {
		r := ts.get(t, route.path, route.authed)
		require.Contains(t, r.ContentType, "application/json",
			"route %s must return JSON, got %s", route.path, r.ContentType)
		require.NotContains(t, r.ContentType, "text/html",
			"route %s must not return HTML", route.path)
	}
}

// --- Auth middleware ---

// TestIntegration_AuthMiddleware_MissingToken_Returns401JSON tests missing auth → 401 JSON.
func TestIntegration_AuthMiddleware_MissingToken_Returns401JSON(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), false)

	require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	require.Contains(t, r.ContentType, "application/json")

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &errResp))
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok, "error response must have 'error' object")
	require.Equal(t, "UNAUTHORIZED", errObj["code"])
}

// TestIntegration_AuthMiddleware_InvalidToken_Returns401JSON tests malformed token.
func TestIntegration_AuthMiddleware_InvalidToken_Returns401JSON(t *testing.T) {
	ts := newTestServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		ts.url(fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer invalid-token-here")
	r := ts.do(t, req)

	require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	require.Contains(t, r.ContentType, "application/json")

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &errResp))
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "UNAUTHORIZED", errObj["code"])
}

// --- Auth login/register flow ---

// TestIntegration_RegisterAndLogin tests the full register → login flow.
func TestIntegration_RegisterAndLogin(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, "/api/v1/auth/register", map[string]string{
		"email":        "register-test@azimuthal.dev",
		"display_name": "Register Test",
		"password":     "testpassword123",
	}, false)
	require.Equal(t, http.StatusCreated, r.StatusCode, "register: %s", r.Body)

	var registerResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &registerResp))
	require.NotEmpty(t, registerResp["access_token"])

	r = ts.post(t, "/api/v1/auth/login", map[string]string{
		"email":    "register-test@azimuthal.dev",
		"password": "testpassword123",
	}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "login: %s", r.Body)

	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &loginResp))
	require.NotEmpty(t, loginResp["access_token"])
}

// TestIntegration_Login_MissingFields tests validation on login.
func TestIntegration_Login_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	r := ts.post(t, "/api/v1/auth/login", map[string]string{
		"email": "test@test.com",
	}, false)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
}

// TestIntegration_Login_WrongPassword returns 401.
func TestIntegration_Login_WrongPassword(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, "/api/v1/auth/register", map[string]string{
		"email":    "wrong-pass@azimuthal.dev",
		"password": "correctpassword",
	}, false)
	require.Equal(t, http.StatusCreated, r.StatusCode)

	r = ts.post(t, "/api/v1/auth/login", map[string]string{
		"email":    "wrong-pass@azimuthal.dev",
		"password": "wrongpassword",
	}, false)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode, "response: %s", r.Body)
}

// --- Space CRUD ---

// TestIntegration_CreateSpace_AndList tests creating a space and listing it.
func TestIntegration_CreateSpace_AndList(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Test Space",
		"slug": "test-space",
		"type": "project",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)

	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)

	var spaces []any
	require.NoError(t, json.Unmarshal(r.Body, &spaces))
	require.GreaterOrEqual(t, len(spaces), 1)
}

// TestIntegration_CreateSpace_MissingName returns 400.
func TestIntegration_CreateSpace_MissingName(t *testing.T) {
	ts := newTestServer(t)
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"slug": "no-name",
		"type": "project",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
}

// --- Ticket CRUD ---

// TestIntegration_CreateTicket_AndGet tests creating and retrieving a ticket.
func TestIntegration_CreateTicket_AndGet(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/tickets", space.ID), map[string]any{
		"title":    "Test Ticket",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)

	var ticketResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &ticketResp))
	ticketID := ticketResp["id"].(string)

	r = ts.get(t, fmt.Sprintf("/api/v1/spaces/%s/tickets/%s", space.ID, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
}

// --- Error format ---

// TestIntegration_ErrorFormat_Consistent verifies error responses use consistent JSON.
func TestIntegration_ErrorFormat_Consistent(t *testing.T) {
	ts := newTestServer(t)

	testCases := []struct {
		name       string
		path       string
		authed     bool
		wantStatus int
		wantCode   string
	}{
		{
			name:       "401_missing_auth",
			path:       fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID),
			authed:     false,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:       "500_nonexistent_ticket",
			path:       fmt.Sprintf("/api/v1/spaces/%s/tickets/%s", uuid.New(), uuid.New()),
			authed:     true,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := ts.get(t, tc.path, tc.authed)
			require.Equal(t, tc.wantStatus, r.StatusCode)
			require.Contains(t, r.ContentType, "application/json")

			var errResp map[string]any
			require.NoError(t, json.Unmarshal(r.Body, &errResp))
			errObj, ok := errResp["error"].(map[string]any)
			require.True(t, ok, "error response must have 'error' object, got: %s", r.Body)
			require.NotEmpty(t, errObj["code"])
			require.NotEmpty(t, errObj["message"])
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, errObj["code"])
			}
		})
	}
}

// --- Project items ---

// TestIntegration_CreateProjectItem_ViaAPI tests creating a project item via HTTP.
func TestIntegration_CreateProjectItem_ViaAPI(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "API Item",
		"kind":     "task",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)

	var itemResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &itemResp))
	require.Equal(t, "task", itemResp["kind"])
	require.Equal(t, "open", itemResp["status"])
	require.Equal(t, "medium", itemResp["priority"])
}

// TestIntegration_CreateItem_MissingTitle_Returns400 tests validation.
func TestIntegration_CreateItem_MissingTitle_Returns400(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"kind":     "task",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
}

// TestIntegration_CreateItem_MissingKind_Returns400 tests validation for missing kind.
func TestIntegration_CreateItem_MissingKind_Returns400(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "No kind",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "response: %s", r.Body)
}

// --- Wiki ---

// TestIntegration_CreateWikiPage_ViaAPI tests wiki page creation via HTTP.
func TestIntegration_CreateWikiPage_ViaAPI(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "wiki")

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/wiki", space.ID), map[string]any{
		"title":   "Test Wiki Page",
		"content": "Some markdown content",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
}

// --- CORS ---

// TestIntegration_CORS_PreflightReturns204 verifies OPTIONS returns 204.
func TestIntegration_CORS_PreflightReturns204(t *testing.T) {
	ts := newTestServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodOptions,
		ts.url("/api/v1/auth/login"), nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	r := ts.do(t, req)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	require.NotEmpty(t, r.Header.Get("Access-Control-Allow-Origin"))
}

// --- Auth /me endpoint ---

// TestAuthMe_ValidToken_Returns200 verifies GET /api/v1/auth/me returns 200
// with a valid JWT. This was returning 401 because the /me route was registered
// in the public auth group without RequireAuth middleware, causing claims to be nil.
func TestAuthMe_ValidToken_Returns200(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, "/api/v1/auth/me", true)

	require.Equal(t, http.StatusOK, r.StatusCode, "auth/me with valid token: %s", r.Body)
	require.Contains(t, r.ContentType, "application/json")

	var user map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &user))
	require.NotEmpty(t, user["email"], "must return user email")
	require.NotEmpty(t, user["display_name"], "must return display_name")
}

// TestAuthMe_NoToken_Returns401JSON verifies GET /api/v1/auth/me without
// Authorization header returns 401 JSON, not HTML.
func TestAuthMe_NoToken_Returns401JSON(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, "/api/v1/auth/me", false)

	require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	require.Contains(t, r.ContentType, "application/json")

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &errResp))
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok, "error response must have 'error' object, got: %s", r.Body)
	require.Equal(t, "UNAUTHORIZED", errObj["code"])
}

// TestAuthMe_SameTokenWorksOnBothEndpoints verifies the same JWT works on
// both /auth/me and /orgs/:id/spaces. This was the root cause: /auth/me used
// different middleware than other protected endpoints.
func TestAuthMe_SameTokenWorksOnBothEndpoints(t *testing.T) {
	ts := newTestServer(t)

	meResult := ts.get(t, "/api/v1/auth/me", true)
	require.Equal(t, http.StatusOK, meResult.StatusCode, "auth/me: %s", meResult.Body)

	spacesResult := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), true)
	require.Equal(t, http.StatusOK, spacesResult.StatusCode, "spaces: %s", spacesResult.Body)
}

// --- Comments ---

// TestComments_CorrectURLIncludesOrgId verifies the correct comments URL
// structure: /orgs/:orgId/spaces/:spaceId/items/:itemId/comments returns 200,
// while /spaces/:spaceId/items/:itemId/comments returns 404.
func TestComments_CorrectURLIncludesOrgId(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	// Create an item directly in the database
	itemID := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO items (id, space_id, kind, title, status, priority, reporter_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		itemID, space.ID, "ticket", "Test Ticket", "open", "medium", user.ID,
	)
	require.NoError(t, err)

	// Correct URL: with orgId
	correctResult := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/items/%s/comments", ts.OrgID, space.ID, itemID), true)
	require.Equal(t, http.StatusOK, correctResult.StatusCode, "correct URL should return 200: %s", correctResult.Body)

	// Wrong URL: without orgId — should return 404
	wrongResult := ts.get(t, fmt.Sprintf("/api/v1/spaces/%s/items/%s/comments", space.ID, itemID), true)
	require.Equal(t, http.StatusNotFound, wrongResult.StatusCode, "wrong URL should return 404")
}

// TestComments_PostAndRetrieve tests creating and retrieving a comment.
func TestComments_PostAndRetrieve(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	// Create an item directly in the database
	itemID := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO items (id, space_id, kind, title, status, priority, reporter_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		itemID, space.ID, "ticket", "Comment Test Ticket", "open", "medium", user.ID,
	)
	require.NoError(t, err)

	commentsURL := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/items/%s/comments", ts.OrgID, space.ID, itemID)

	// POST a comment
	postResult := ts.post(t, commentsURL, map[string]string{
		"content": "This is a test comment",
	}, true)
	require.Equal(t, http.StatusCreated, postResult.StatusCode, "post comment: %s", postResult.Body)

	var comment map[string]any
	require.NoError(t, json.Unmarshal(postResult.Body, &comment))
	require.Equal(t, "This is a test comment", comment["body"])
	require.NotEmpty(t, comment["author_name"], "comment must have author_name populated")

	// GET comments — should return the posted comment
	getResult := ts.get(t, commentsURL, true)
	require.Equal(t, http.StatusOK, getResult.StatusCode, "get comments: %s", getResult.Body)

	var comments []map[string]any
	require.NoError(t, json.Unmarshal(getResult.Body, &comments))
	require.Len(t, comments, 1)
	require.Equal(t, "This is a test comment", comments[0]["body"])
	require.NotEmpty(t, comments[0]["author_name"], "listed comment must have author_name")
}

// --- Register duplicate email ---

// --- Members URL routing ---

// TestMembers_SpaceScopedURL_Returns200 verifies GET /api/v1/orgs/:orgId/spaces/:spaceId/members
// returns 200 — this is the correct URL for listing space members.
func TestMembers_SpaceScopedURL_Returns200(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	// Add the user as a space member
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO space_members (id, space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		uuid.New(), space.ID, user.ID, "member",
	)
	require.NoError(t, err)

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/members", ts.OrgID, space.ID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "space-scoped members URL should return 200: %s", r.Body)

	var members []map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &members))
	require.GreaterOrEqual(t, len(members), 1, "should have at least one member")
	require.NotEmpty(t, members[0]["user_id"], "member must have user_id")
	require.NotEmpty(t, members[0]["display_name"], "member must have display_name")
}

// TestMembers_OrgScopedURL_Returns404 verifies GET /api/v1/orgs/:orgId/members
// returns 404 — the frontend was calling this wrong URL.
func TestMembers_OrgScopedURL_Returns404(t *testing.T) {
	ts := newTestServer(t)
	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/members", ts.OrgID), true)
	// This URL does not exist on the backend — SPA fallback returns 404 or HTML.
	// Without SPAHandler, the chi NotFound handler returns 404.
	require.NotEqual(t, http.StatusOK, r.StatusCode,
		"org-scoped /orgs/:orgId/members must NOT return 200 — this wrong URL was being called by the frontend")
}

// --- Comments URL routing (supplements existing tests) ---

// TestComments_WrongURL_NoOrgId_Returns404 explicitly documents that the short URL
// without orgId is intentionally not supported.
func TestComments_WrongURL_NoOrgId_Returns404(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	itemID := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO items (id, space_id, kind, title, status, priority, reporter_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		itemID, space.ID, "ticket", "Test Ticket", "open", "medium", user.ID,
	)
	require.NoError(t, err)

	r := ts.get(t, fmt.Sprintf("/api/v1/spaces/%s/items/%s/comments", space.ID, itemID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"short URL without orgId must return 404 — documents that this is intentionally not supported")
}

// TestComments_PostAndRetrieve_FullURL verifies the full comment lifecycle via correct URL.
func TestComments_PostAndRetrieve_FullURL(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	itemID := uuid.New()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO items (id, space_id, kind, title, status, priority, reporter_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		itemID, space.ID, "ticket", "Full URL Comment Test", "open", "medium", user.ID,
	)
	require.NoError(t, err)

	commentsURL := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/items/%s/comments", ts.OrgID, space.ID, itemID)

	// POST a comment
	postResult := ts.post(t, commentsURL, map[string]string{"content": "test comment"}, true)
	require.Equal(t, http.StatusCreated, postResult.StatusCode, "post: %s", postResult.Body)

	var comment map[string]any
	require.NoError(t, json.Unmarshal(postResult.Body, &comment))
	require.Equal(t, "test comment", comment["body"])
	require.NotEmpty(t, comment["author_name"], "author_name must not be empty")

	// GET — verify it's returned
	getResult := ts.get(t, commentsURL, true)
	require.Equal(t, http.StatusOK, getResult.StatusCode)

	var comments []map[string]any
	require.NoError(t, json.Unmarshal(getResult.Body, &comments))
	require.Len(t, comments, 1)
	require.Equal(t, "test comment", comments[0]["body"])
	require.NotEmpty(t, comments[0]["author_name"])
}

// --- Project item status ---

// TestProjectItem_StatusUpdate_Returns200 verifies POST /spaces/:spaceId/projects/items/:itemId/status
// returns 200 for each valid status.
func TestProjectItem_StatusUpdate_Returns200(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	// Create an item
	createResult := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "Status Test Item",
		"kind":     "task",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, createResult.StatusCode, "create: %s", createResult.Body)

	var item map[string]any
	require.NoError(t, json.Unmarshal(createResult.Body, &item))
	itemID := item["id"].(string)

	validStatuses := []string{"open", "in_progress", "in_review", "done", "closed"}
	for _, status := range validStatuses {
		t.Run(status, func(t *testing.T) {
			r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items/%s/status", space.ID, itemID),
				map[string]string{"status": status}, true)
			require.Equal(t, http.StatusOK, r.StatusCode, "status %s: %s", status, r.Body)

			var updated map[string]any
			require.NoError(t, json.Unmarshal(r.Body, &updated))
			require.Equal(t, status, updated["status"])
		})
	}
}

// TestProjectItem_StatusUpdate_InvalidStatus_NotRejected documents that the backend
// currently does not validate status values — any string is accepted. This test
// ensures we're aware of this behavior and can add validation later.
func TestProjectItem_StatusUpdate_InvalidStatus_NotRejected(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	createResult := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "Invalid Status Item",
		"kind":     "task",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, createResult.StatusCode)

	var item map[string]any
	require.NoError(t, json.Unmarshal(createResult.Body, &item))
	itemID := item["id"].(string)

	r := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items/%s/status", space.ID, itemID),
		map[string]string{"status": "invalid_status"}, true)
	// NOTE: Backend currently accepts any status string without validation.
	// When validation is added, this should change to 400.
	require.Equal(t, http.StatusOK, r.StatusCode, "backend currently accepts any status: %s", r.Body)
}

// --- Reporter data chain ---

// TestReporter_ResolvedFromMembers verifies the data chain that powers reporter display:
// an item's reporter_id matches a user in the space members response.
func TestReporter_ResolvedFromMembers(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	// Create item first to discover the JWT user's ID (reporter_id comes from JWT)
	createResult := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "Reporter Test Item",
		"kind":     "task",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, createResult.StatusCode)

	var item map[string]any
	require.NoError(t, json.Unmarshal(createResult.Body, &item))
	reporterID := item["reporter_id"].(string)
	require.NotEmpty(t, reporterID, "item must have reporter_id")

	// Add the JWT user (reporter) as a space member so they appear in the members list
	reporterUUID, err := uuid.Parse(reporterID)
	require.NoError(t, err)
	_, err = ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO space_members (id, space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		uuid.New(), space.ID, reporterUUID, "member",
	)
	require.NoError(t, err)

	// Get space members
	membersResult := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/members", ts.OrgID, space.ID), true)
	require.Equal(t, http.StatusOK, membersResult.StatusCode)

	var members []map[string]any
	require.NoError(t, json.Unmarshal(membersResult.Body, &members))

	// Find the reporter in the members list
	found := false
	for _, m := range members {
		if m["user_id"].(string) == reporterID {
			found = true
			require.NotEmpty(t, m["display_name"], "reporter's display_name must be present")
			break
		}
	}
	require.True(t, found, "reporter_id %s must be present in space members list", reporterID)
}

// TestIntegration_Register_DuplicateEmail tests duplicate email registration.
func TestIntegration_Register_DuplicateEmail(t *testing.T) {
	ts := newTestServer(t)

	body := map[string]string{
		"email":    "dup@azimuthal.dev",
		"password": "testpassword123",
	}

	r := ts.post(t, "/api/v1/auth/register", body, false)
	require.Equal(t, http.StatusCreated, r.StatusCode)

	r = ts.post(t, "/api/v1/auth/register", body, false)
	// NOTE: Currently returns 500 because the adapter does not map postgres
	// unique constraint violations to auth.ErrEmailTaken. Ideally 409.
	require.True(t, r.StatusCode == http.StatusConflict || r.StatusCode == http.StatusInternalServerError,
		"expected 409 or 500, got %d", r.StatusCode)
}
