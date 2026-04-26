package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

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

func (m *mockSessionRepo) DeleteExpired(_ context.Context) error { return nil }

// mockMembershipResolver returns a fixed org for any user.
type mockMembershipResolver struct{}

func (m *mockMembershipResolver) PrimaryOrgForUser(_ context.Context, _ uuid.UUID) (uuid.UUID, string, string, error) {
	return uuid.MustParse("00000000-0000-0000-0000-000000000001"), "test-org", "Test Org", nil
}

// failingMembershipResolver always returns an error.
type failingMembershipResolver struct{}

func (m *failingMembershipResolver) PrimaryOrgForUser(_ context.Context, _ uuid.UUID) (uuid.UUID, string, string, error) {
	return uuid.Nil, "", "", fmt.Errorf("no memberships found")
}

func setupHandler(t *testing.T) (*authapi.Handler, *auth.JWTService) {
	t.Helper()
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk,
		PublicKey:  &pk.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "test",
	})
	userSvc := auth.NewUserService(newMockUserRepo())
	sessionSvc := auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil)
	return h, jwtSvc
}

func TestLoginNilBody(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Body = nil
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRegisterNilBody(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/register", nil)
	req.Body = nil
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRefreshNilBody(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.Body = nil
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestLogoutWithClaims(t *testing.T) {
	h, jwtSvc := setupHandler(t)
	userID := uuid.New()
	pair, err := jwtSvc.IssueTokenPair(userID, "test@test.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}

	// Create a chi router to properly wire RequireAuth
	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}))
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Post("/logout", h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestLoginEmptyFields(t *testing.T) {
	h, _ := setupHandler(t)
	body := bytes.NewBufferString(`{"email":"","password":""}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRegisterEmptyFields(t *testing.T) {
	h, _ := setupHandler(t)
	body := bytes.NewBufferString(`{"email":"","password":""}`)
	req := httptest.NewRequest(http.MethodPost, "/register", body)
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRefreshEmptyToken(t *testing.T) {
	h, _ := setupHandler(t)
	body := bytes.NewBufferString(`{"refresh_token":""}`)
	req := httptest.NewRequest(http.MethodPost, "/refresh", body)
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRefreshBadToken(t *testing.T) {
	h, _ := setupHandler(t)
	body := bytes.NewBufferString(`{"refresh_token":"not-a-valid-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/refresh", body)
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h, _ := setupHandler(t)

	// Register first
	regBody, _ := json.Marshal(map[string]string{
		"email":    "user@test.com",
		"password": "correct-password",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	rr := httptest.NewRecorder()
	h.Register(rr, req)

	// Login with wrong password
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "user@test.com",
		"password": "wrong-password",
	})
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(loginBody))
	rr = httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestRoutesReturnsRouter(t *testing.T) {
	h, _ := setupHandler(t)
	r := h.Routes()
	if r == nil {
		t.Fatal("Routes() returned nil")
	}
}

func TestRegisterAndLoginSuccess(t *testing.T) {
	h, _ := setupHandler(t)

	// Register a user
	regBody, _ := json.Marshal(map[string]string{
		"email":        "newuser@test.com",
		"display_name": "New User",
		"password":     "secure-password-123",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body: %s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	// Login with the same credentials
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "newuser@test.com",
		"password": "secure-password-123",
	})
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(loginBody))
	rr = httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("expected access_token in response")
	}
	if resp["token"] == nil || resp["token"] == "" {
		t.Error("expected token in response")
	}
	org, ok := resp["org"].(map[string]interface{})
	if !ok {
		t.Fatal("expected org object in login response")
	}
	if org["slug"] != "test-org" {
		t.Errorf("expected org slug 'test-org', got %v", org["slug"])
	}
	if org["name"] != "Test Org" {
		t.Errorf("expected org name 'Test Org', got %v", org["name"])
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	h, _ := setupHandler(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "dup@test.com",
		"password": "password123",
	})

	// First registration succeeds
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first register = %d, want %d", rr.Code, http.StatusCreated)
	}

	// Second registration with same email returns 409
	body2, _ := json.Marshal(map[string]string{
		"email":    "dup@test.com",
		"password": "different-password",
	})
	req = httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body2))
	rr = httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate register = %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestLogoutNoAuth(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rr := httptest.NewRecorder()
	h.Logout(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMeNoAuth(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rr := httptest.NewRecorder()
	h.Me(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMeWithAuth(t *testing.T) {
	h, jwtSvc := setupHandler(t)
	userID := uuid.New()
	orgID := uuid.New()

	// Register a user first so GetUser can find them
	regBody, _ := json.Marshal(map[string]string{
		"email":        "me@test.com",
		"display_name": "Me User",
		"password":     "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", rr.Code, http.StatusCreated)
	}

	// Decode to get actual user ID
	var resp map[string]interface{}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	userMap := resp["user"].(map[string]interface{})
	actualID, _ := uuid.Parse(userMap["id"].(string))

	// Issue a token with the actual user ID
	pair, err := jwtSvc.IssueTokenPair(actualID, "me@test.com", orgID.String(), "member")
	if err != nil {
		t.Fatal(err)
	}

	// Create chi router with auth middleware
	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}))
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Get("/me", h.Me)

	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	_ = userID // used for clarity
}

func TestRefreshWithValidToken(t *testing.T) {
	h, jwtSvc := setupHandler(t)
	userID := uuid.New()
	pair, err := jwtSvc.IssueTokenPair(userID, "test@test.com", uuid.New().String(), "member")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"refresh_token": pair.RefreshToken,
	})
	req := httptest.NewRequest(http.MethodPost, "/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestLoginMembershipResolutionFailure(t *testing.T) {
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk,
		PublicKey:  &pk.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "test",
	})
	userSvc := auth.NewUserService(newMockUserRepo())
	sessionSvc := auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &failingMembershipResolver{}, nil, nil)

	// Register a user first
	regBody, _ := json.Marshal(map[string]string{
		"email":    "failmember@test.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", rr.Code, http.StatusCreated)
	}

	// Login should still succeed by falling back to user's org_id
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "failmember@test.com",
		"password": "password123",
	})
	req = httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(loginBody))
	rr = httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}
