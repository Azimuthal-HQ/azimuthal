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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// testSigningKey is the one RS256 key this test binary signs with
// (known-issues #19). setupHandler alone is called sixteen times and
// rsa.GenerateKey(rand.Reader, 2048) is not cheap — several times less cheap
// under -race — and no test here asserts anything about the key itself.
//
// It hands out the same key every time, so it cannot be used to prove that a
// token signed by one service is rejected by another. The one test in the
// repository that needs that property — TestJWTService_WrongKey in
// internal/core/auth — mints its second key explicitly and says why.
var testSigningKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generating the shared test signing key: " + err.Error())
	}
	return key
})

type mockUserRepo struct {
	users map[uuid.UUID]*auth.User
	// revoked records every RevokeTokens call, in order. A fake that swallowed
	// the call and returned nil would let TestLogoutAllBumpsGenerationAndClearsSessions
	// pass with the revocation deleted from the handler — which is one of the
	// things that test exists to prove — and let TestLogoutRevokesCurrentSessionOnly
	// stop noticing a stray generation bump.
	revoked []uuid.UUID
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

// RevokeTokens mirrors the adapter: it records the call, bumps the generation
// when the user is known, and treats an unknown id as a no-op rather than an
// error (the real statement filters `deleted_at IS NULL` and reports zero rows).
func (m *mockUserRepo) RevokeTokens(_ context.Context, id uuid.UUID) error {
	m.revoked = append(m.revoked, id)
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
	h, jwtSvc, _, _ := setupHandlerWithRepo(t)
	return h, jwtSvc
}

// setupHandlerWithRepo is setupHandler plus the user and session repositories
// the handler was built on, for the tests that must assert what the handler
// wrote to them — a single-session logout revokes a session row, and
// logout-all revokes tokens, so both need to be observable.
func setupHandlerWithRepo(t *testing.T) (*authapi.Handler, *auth.JWTService, *mockUserRepo, *mockSessionRepo) {
	t.Helper()
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk,
		PublicKey:  &pk.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "test",
	})
	repo := newMockUserRepo()
	userSvc := auth.NewUserService(repo)
	sessRepo := newMockSessionRepo()
	sessionSvc := auth.NewSessionService(sessRepo, auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil).WithRegistrationPolicy(true)
	return h, jwtSvc, repo, sessRepo
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

// TestLogoutRevokesCurrentSessionOnly is the B1 behaviour: a single-device
// logout revokes the session row named by the token's sid, and does NOT bump
// the generation — so other sessions the same user holds are untouched.
func TestLogoutRevokesCurrentSessionOnly(t *testing.T) {
	h, jwtSvc, userRepo, sessRepo := setupHandlerWithRepo(t)
	userID := uuid.New()

	// Two live sessions for the one user. Logout should take out only the
	// one the presented token names.
	thisSession := uuid.New()
	otherSession := uuid.New()
	for _, id := range []uuid.UUID{thisSession, otherSession} {
		require.NoError(t, sessRepo.Create(context.Background(), &auth.Session{
			ID: id, UserID: userID, ExpiresAt: time.Now().Add(time.Hour),
		}))
	}

	pair, err := jwtSvc.IssueTokenPair(userID, "test@test.com", uuid.New().String(), "member", 0, thisSession)
	if err != nil {
		t.Fatal(err)
	}

	// Nil states, so the middleware admits the token and puts its claims (sid
	// included) on the context; the handler is what does the revocation.
	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}), nil)
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Post("/logout", h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	// The presented session is gone; the other one survives.
	_, thisStillThere := sessRepo.sessions[thisSession]
	require.False(t, thisStillThere, "logout must revoke the session named by the token")
	_, otherStillThere := sessRepo.sessions[otherSession]
	require.True(t, otherStillThere, "logout must NOT touch the user's other sessions")

	// And it did NOT bump the generation — that is logout-all's job, and doing
	// it here would sign every device out, which is the behaviour B1 removes.
	require.Empty(t, userRepo.revoked, "single-device logout must not bump token_generation")
}

// TestLogoutReportsRevocationFailure pins the direction that is easy to get
// wrong: a logout whose session revocation failed must NOT answer 200.
// Somebody signing out because they believe they are compromised would be told
// the opposite of the truth.
func TestLogoutReportsRevocationFailure(t *testing.T) {
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk, PublicKey: &pk.PublicKey,
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, Issuer: "test",
	})
	userSvc := auth.NewUserService(newMockUserRepo())
	sessionSvc := auth.NewSessionService(&failingSessionRepo{}, auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil)

	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "test@test.com", uuid.New().String(), "member", 0, uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}), nil)
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Post("/logout", h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
}

// TestLogoutAllBumpsGenerationAndClearsSessions is the org-wide hammer plain
// logout used to be: it revokes every session row AND bumps the generation, so
// every device the user holds dies on its next request.
func TestLogoutAllBumpsGenerationAndClearsSessions(t *testing.T) {
	h, jwtSvc, userRepo, sessRepo := setupHandlerWithRepo(t)
	userID := uuid.New()

	sidA, sidB := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{sidA, sidB} {
		require.NoError(t, sessRepo.Create(context.Background(), &auth.Session{
			ID: id, UserID: userID, ExpiresAt: time.Now().Add(time.Hour),
		}))
	}

	pair, err := jwtSvc.IssueTokenPair(userID, "test@test.com", uuid.New().String(), "member", 0, sidA)
	if err != nil {
		t.Fatal(err)
	}

	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}), nil)
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Post("/logout-all", h.LogoutAll)

	req := httptest.NewRequest(http.MethodPost, "/logout-all", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	// Every session gone, and the generation bumped exactly once for this user.
	require.Empty(t, sessRepo.sessions, "logout-all must revoke every session row")
	require.Equal(t, []uuid.UUID{userID}, userRepo.revoked,
		"logout-all must bump the caller's token_generation exactly once")
}

// TestLogoutAllReportsRevocationFailure: logout-all whose generation bump
// failed must NOT answer 200, for the same reason plain logout must not.
func TestLogoutAllReportsRevocationFailure(t *testing.T) {
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk, PublicKey: &pk.PublicKey,
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, Issuer: "test",
	})
	userSvc := auth.NewUserService(&failingRevokeUserRepo{mockUserRepo: newMockUserRepo()})
	sessionSvc := auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil)

	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "test@test.com", uuid.New().String(), "member", 0, uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}), nil)
	r := chi.NewRouter()
	r.Use(authenticator.RequireAuth)
	r.Post("/logout-all", h.LogoutAll)

	req := httptest.NewRequest(http.MethodPost, "/logout-all", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d, body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
}

// failingRevokeUserRepo succeeds at everything except revocation.
type failingRevokeUserRepo struct {
	*mockUserRepo
}

func (r *failingRevokeUserRepo) RevokeTokens(_ context.Context, _ uuid.UUID) error {
	return fmt.Errorf("the database said no")
}

// failingSessionRepo fails every session operation — used to prove logout
// reports a failed session revocation rather than swallowing it.
type failingSessionRepo struct{}

func (failingSessionRepo) Create(context.Context, *auth.Session) error { return nil }
func (failingSessionRepo) GetByToken(context.Context, string) (*auth.Session, error) {
	return nil, auth.ErrNotFound
}
func (failingSessionRepo) Delete(context.Context, uuid.UUID) error {
	return fmt.Errorf("the database said no")
}
func (failingSessionRepo) DeleteAllForUser(context.Context, uuid.UUID) error {
	return fmt.Errorf("the database said no")
}
func (failingSessionRepo) DeleteExpired(context.Context) error { return nil }

// failingCreateSessionRepo fails only Create — used to prove a login that
// cannot open a session refuses rather than handing back a sessionless pair
// the very next request would 401.
type failingCreateSessionRepo struct{ mockSessionRepo }

func (failingCreateSessionRepo) Create(context.Context, *auth.Session) error {
	return fmt.Errorf("the database said no")
}

// TestLoginSessionCreationFailureReturns500 pins the fail-closed direction of
// the login-opens-a-session change: if the session cannot be opened, login
// must NOT mint tokens. Deleting the error guard in finishLogin would dereference
// the nil session CreateSession returned — so the login path could not silently
// hand back a sessionless pair with this in place.
func TestLoginSessionCreationFailureReturns500(t *testing.T) {
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk, PublicKey: &pk.PublicKey,
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, Issuer: "test",
	})
	// Seed a user with a real password hash so Authenticate succeeds and the
	// flow reaches the session open.
	hash, err := auth.HashPassword("correct-horse-battery")
	require.NoError(t, err)
	userRepo := newMockUserRepo()
	uid := uuid.New()
	userRepo.users[uid] = &auth.User{
		ID: uid, Email: "sess-fail@test.com", PasswordHash: hash,
		Role: "member", IsActive: true, OrgID: uuid.New(),
	}
	userSvc := auth.NewUserService(userRepo)
	sessionSvc := auth.NewSessionService(&failingCreateSessionRepo{}, auth.SessionConfig{TTL: time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil)

	body := bytes.NewBufferString(`{"email":"sess-fail@test.com","password":"correct-horse-battery"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code, "body: %s", rr.Body.String())
	require.NotContains(t, rr.Body.String(), "access_token",
		"a login that could not open a session must not hand back tokens")
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
	pair, err := jwtSvc.IssueTokenPair(actualID, "me@test.com", orgID.String(), "member", 0, uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	// Create chi router with auth middleware
	authenticator := auth.NewAuthenticator(jwtSvc, auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: time.Hour}), nil)
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
	pair, err := jwtSvc.IssueTokenPair(userID, "test@test.com", uuid.New().String(), "member", 0, uuid.New())
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
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk,
		PublicKey:  &pk.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "test",
	})
	userSvc := auth.NewUserService(newMockUserRepo())
	sessionSvc := auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &failingMembershipResolver{}, nil, nil).WithRegistrationPolicy(true)

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

func TestRegister_DisabledByDefault_404s_Regression(t *testing.T) {
	// P2.5: allow_registration defaults false everywhere — the fluent
	// setter is the ONLY way to open registration, so a handler built
	// without it must answer 404 before touching the body. Verified to
	// fail against the pre-P2.5 handler (which had no gate) and pass now.
	pk := testSigningKey()
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: pk, PublicKey: &pk.PublicKey,
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, Issuer: "test",
	})
	userSvc := auth.NewUserService(newMockUserRepo())
	sessionSvc := auth.NewSessionService(newMockSessionRepo(), auth.SessionConfig{TTL: 24 * time.Hour})
	h := authapi.NewHandler(userSvc, jwtSvc, sessionSvc, &mockMembershipResolver{}, nil, nil)

	body := bytes.NewBufferString(`{"email":"a@b.com","display_name":"A","password":"long-enough"}`)
	req := httptest.NewRequest(http.MethodPost, "/register", body)
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("register with registration disabled: status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if !strings.Contains(rr.Body.String(), "NOT_FOUND") {
		t.Errorf("expected the standard NOT_FOUND error shape, got %s", rr.Body.String())
	}
}
