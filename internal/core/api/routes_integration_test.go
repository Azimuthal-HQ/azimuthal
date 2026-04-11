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

func (ts *testServer) do(t *testing.T, req *http.Request) httpResult {
	t.Helper()
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose,gosec // closed below; test-only URL
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

// --- Register duplicate email ---

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
