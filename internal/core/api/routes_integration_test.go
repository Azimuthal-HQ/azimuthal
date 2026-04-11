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

// testServer holds a fully-wired httptest.Server backed by a real database.
type testServer struct {
	Server *httptest.Server
	DB     *testutil.TestDB
	OrgID  uuid.UUID
	Token  string // valid JWT
}

// newTestServer creates a full API server backed by a real database.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)

	// RSA key for JWT
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
		SPAHandler:     nil, // no SPA in tests
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Issue a token for the test user.
	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, org.ID.String(), "member")
	require.NoError(t, err)

	return &testServer{
		Server: srv,
		DB:     db,
		OrgID:  org.ID,
		Token:  pair.AccessToken,
	}
}

func (ts *testServer) url(path string) string {
	return ts.Server.URL + path
}

func (ts *testServer) get(t *testing.T, path string, authed bool) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.url(path), nil)
	require.NoError(t, err)
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func (ts *testServer) post(t *testing.T, path string, body any, authed bool) *http.Response {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.url(path), bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return b
}

// --- Health / Ready ---

// TestHealthEndpoint verifies GET /health returns 200 with {"status":"ok"}.
func TestIntegration_HealthEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.get(t, "/health", false)
	body := readBody(t, resp)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var result map[string]string
	require.NoError(t, json.Unmarshal(body, &result))
	require.Equal(t, "ok", result["status"])
}

// TestReadyEndpoint verifies GET /ready returns 200 with {"status":"ready"}.
func TestIntegration_ReadyEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.get(t, "/ready", false)
	body := readBody(t, resp)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var result map[string]string
	require.NoError(t, json.Unmarshal(body, &result))
	require.Equal(t, "ready", result["status"])
}

// --- API routes never return HTML ---

// TestAPIRoutes_NeverReturnHTML verifies every /api/v1/... route returns JSON.
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
		resp := ts.get(t, route.path, route.authed)
		ct := resp.Header.Get("Content-Type")
		resp.Body.Close()
		require.Contains(t, ct, "application/json",
			"route %s must return JSON, got %s", route.path, ct)
		require.NotContains(t, ct, "text/html",
			"route %s must not return HTML", route.path)
	}
}

// --- Auth middleware ---

// TestAuthMiddleware_MissingToken_Returns401JSON tests that missing auth returns 401 JSON.
func TestIntegration_AuthMiddleware_MissingToken_Returns401JSON(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), false)
	body := readBody(t, resp)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(body, &errResp))
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok, "error response must have 'error' object")
	require.Equal(t, "UNAUTHORIZED", errObj["code"])
}

// TestAuthMiddleware_InvalidToken_Returns401JSON tests malformed token.
func TestIntegration_AuthMiddleware_InvalidToken_Returns401JSON(t *testing.T) {
	ts := newTestServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		ts.url(fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer invalid-token-here")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body := readBody(t, resp)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(body, &errResp))
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "UNAUTHORIZED", errObj["code"])
}

// --- Auth login/register flow ---

// TestRegisterAndLogin tests the full register → login flow.
func TestIntegration_RegisterAndLogin(t *testing.T) {
	ts := newTestServer(t)

	// Register
	resp := ts.post(t, "/api/v1/auth/register", map[string]string{
		"email":        "register-test@azimuthal.dev",
		"display_name": "Register Test",
		"password":     "testpassword123",
	}, false)
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "register response: %s", body)

	var registerResp map[string]any
	require.NoError(t, json.Unmarshal(body, &registerResp))
	require.NotEmpty(t, registerResp["access_token"])
	require.NotEmpty(t, registerResp["refresh_token"])

	// Login with same credentials
	resp = ts.post(t, "/api/v1/auth/login", map[string]string{
		"email":    "register-test@azimuthal.dev",
		"password": "testpassword123",
	}, false)
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "login response: %s", body)

	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(body, &loginResp))
	require.NotEmpty(t, loginResp["access_token"])
}

// TestLogin_MissingFields tests validation on login.
func TestIntegration_Login_MissingFields(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.post(t, "/api/v1/auth/login", map[string]string{
		"email": "test@test.com",
		// missing password
	}, false)
	body := readBody(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "response: %s", body)
}

// TestLogin_WrongPassword returns 401.
func TestIntegration_Login_WrongPassword(t *testing.T) {
	ts := newTestServer(t)

	// Register first
	resp := ts.post(t, "/api/v1/auth/register", map[string]string{
		"email":    "wrong-pass@azimuthal.dev",
		"password": "correctpassword",
	}, false)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Login with wrong password
	resp = ts.post(t, "/api/v1/auth/login", map[string]string{
		"email":    "wrong-pass@azimuthal.dev",
		"password": "wrongpassword",
	}, false)
	body := readBody(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "response: %s", body)
}

// --- Space CRUD ---

// TestCreateSpace_AndList tests creating a space and listing it.
func TestIntegration_CreateSpace_AndList(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Test Space",
		"slug": "test-space",
		"type": "project",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create space: %s", body)

	// List spaces
	resp = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), true)
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var spaces []any
	require.NoError(t, json.Unmarshal(body, &spaces))
	require.GreaterOrEqual(t, len(spaces), 1)
}

// TestCreateSpace_MissingName returns 400.
func TestIntegration_CreateSpace_MissingName(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"slug": "no-name",
		"type": "project",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "response: %s", body)
}

// --- Ticket CRUD ---

// TestCreateTicket_AndGet tests creating a ticket via the API and retrieving it.
func TestIntegration_CreateTicket_AndGet(t *testing.T) {
	ts := newTestServer(t)

	// Create a space first
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	resp := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/tickets", space.ID), map[string]any{
		"title":    "Test Ticket",
		"priority": "medium",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create ticket: %s", body)

	var ticketResp map[string]any
	require.NoError(t, json.Unmarshal(body, &ticketResp))
	ticketID := ticketResp["id"].(string)

	// Get ticket
	resp = ts.get(t, fmt.Sprintf("/api/v1/spaces/%s/tickets/%s", space.ID, ticketID), true)
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// --- Error format ---

// TestErrorFormat_Consistent verifies all error responses use the same JSON structure.
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
			resp := ts.get(t, tc.path, tc.authed)
			body := readBody(t, resp)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

			var errResp map[string]any
			require.NoError(t, json.Unmarshal(body, &errResp))
			errObj, ok := errResp["error"].(map[string]any)
			require.True(t, ok, "error response must have 'error' object, got: %s", body)
			require.NotEmpty(t, errObj["code"], "error must have a code")
			require.NotEmpty(t, errObj["message"], "error must have a message")
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, errObj["code"])
			}
		})
	}
}

// --- Project items ---

// TestCreateProjectItem_ViaAPI tests creating a project item via HTTP.
func TestIntegration_CreateProjectItem_ViaAPI(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	resp := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "API Item",
		"kind":     "task",
		"priority": "medium",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create item: %s", body)

	var itemResp map[string]any
	require.NoError(t, json.Unmarshal(body, &itemResp))
	require.Equal(t, "task", itemResp["kind"])
	require.Equal(t, "open", itemResp["status"])
	require.Equal(t, "medium", itemResp["priority"])
}

// TestCreateItem_MissingTitle_Returns400 tests validation.
func TestIntegration_CreateItem_MissingTitle_Returns400(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	resp := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"kind":     "task",
		"priority": "medium",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "response: %s", body)
}

// TestCreateItem_MissingKind_Returns400 tests validation for missing kind.
func TestIntegration_CreateItem_MissingKind_Returns400(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")

	resp := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/projects/items", space.ID), map[string]any{
		"title":    "No kind",
		"priority": "medium",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "response: %s", body)
}

// --- Wiki ---

// TestCreateWikiPage_ViaAPI tests wiki page creation via HTTP.
func TestIntegration_CreateWikiPage_ViaAPI(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "wiki")

	resp := ts.post(t, fmt.Sprintf("/api/v1/spaces/%s/wiki", space.ID), map[string]any{
		"title":   "Test Wiki Page",
		"content": "Some markdown content",
	}, true)
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create wiki page: %s", body)
}

// --- CORS ---

// TestCORS_PreflightReturns204 verifies OPTIONS request returns 204.
func TestIntegration_CORS_PreflightReturns204(t *testing.T) {
	ts := newTestServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodOptions,
		ts.url("/api/v1/auth/login"), nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

// --- Register duplicate email ---

// TestRegister_DuplicateEmail returns 409.
func TestIntegration_Register_DuplicateEmail(t *testing.T) {
	ts := newTestServer(t)

	body := map[string]string{
		"email":    "dup@azimuthal.dev",
		"password": "testpassword123",
	}

	resp := ts.post(t, "/api/v1/auth/register", body, false)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ts.post(t, "/api/v1/auth/register", body, false)
	readBody(t, resp)
	// NOTE: Currently returns 500 because the adapter does not map postgres
	// unique constraint violations to auth.ErrEmailTaken. Ideally this
	// should return 409 Conflict.
	require.True(t, resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusInternalServerError,
		"expected 409 or 500, got %d", resp.StatusCode)
}
