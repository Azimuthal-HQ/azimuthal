package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testAuthenticator creates an Authenticator wired with in-memory stubs and
// no auth-state store (the nil routing-only-test convention: the
// generation/active check is exercised by stubStateAuthenticator below and
// by the integration suite against real PostgreSQL).
func testAuthenticator(t *testing.T) (*Authenticator, *JWTService, *SessionService) {
	t.Helper()
	jwtSvc := NewJWTService(testTokenConfig(t))
	sessSvc := NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
	auth := NewAuthenticator(jwtSvc, sessSvc, nil)
	return auth, jwtSvc, sessSvc
}

// stubStateStore serves a fixed State for every user id.
type stubStateStore struct {
	state State
	err   error
}

func (s *stubStateStore) AuthState(context.Context, uuid.UUID) (State, error) {
	return s.state, s.err
}

// stubStateAuthenticator wires an Authenticator over a fixed auth state.
func stubStateAuthenticator(t *testing.T, state State, err error) (*Authenticator, *JWTService) {
	t.Helper()
	jwtSvc := NewJWTService(testTokenConfig(t))
	sessSvc := NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
	return NewAuthenticator(jwtSvc, sessSvc, &stubStateStore{state: state, err: err}), jwtSvc
}

func TestRequireAuth_StaleGenerationRejected(t *testing.T) {
	// The live column moved past the claim (force logout / deactivation /
	// password change) — the very next request must fail.
	a, jwtSvc := stubStateAuthenticator(t, State{TokenGeneration: 1, IsActive: true}, nil)
	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "stale@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("stale generation: expected 401, got %d", rr.Code)
	}
	if inner.called {
		t.Error("inner handler must not run for a stale-generation token")
	}
}

func TestRequireAuth_MatchingGenerationAccepted(t *testing.T) {
	a, jwtSvc := stubStateAuthenticator(t, State{TokenGeneration: 4, IsActive: true}, nil)
	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "gen@example.com", uuid.New().String(), "member", 4)
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("matching generation: expected 200, got %d", rr.Code)
	}
}

func TestRequireAuth_InactiveAccountRejected(t *testing.T) {
	// Deactivated account, token minted before deactivation with a
	// generation that would otherwise match: is_active alone must reject.
	a, jwtSvc := stubStateAuthenticator(t, State{TokenGeneration: 0, IsActive: false}, nil)
	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "inactive@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("inactive account: expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_UnknownUserRejected(t *testing.T) {
	a, jwtSvc := stubStateAuthenticator(t, State{}, ErrNotFound)
	pair, err := jwtSvc.IssueTokenPair(uuid.New(), "ghost@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unknown user: expected 401, got %d", rr.Code)
	}
}

// okHandler is a simple handler that records whether it was called.
type okHandler struct{ called bool }

func (h *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

func TestRequireAuth_BearerToken_Valid(t *testing.T) {
	a, jwtSvc, _ := testAuthenticator(t)
	userID := uuid.New()

	pair, err := jwtSvc.IssueTokenPair(userID, "user@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}

	inner := &okHandler{}
	handler := a.RequireAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !inner.called {
		t.Error("inner handler should have been called")
	}
}

func TestRequireAuth_NoCredentials(t *testing.T) {
	a, _, _ := testAuthenticator(t)
	inner := &okHandler{}
	handler := a.RequireAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if inner.called {
		t.Error("inner handler should not be called on 401")
	}
}

func TestRequireAuth_InvalidBearerToken(t *testing.T) {
	a, _, _ := testAuthenticator(t)
	inner := &okHandler{}
	handler := a.RequireAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer notavalidtoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_SessionCookie_Valid(t *testing.T) {
	a, _, sessSvc := testAuthenticator(t)
	userID := uuid.New()

	sess, err := sessSvc.CreateSession(context.Background(), userID, "Mozilla/5.0", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	inner := &okHandler{}
	handler := a.RequireAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequireAuth_ExpiredSessionCookie(t *testing.T) {
	a, _, _ := testAuthenticator(t)
	inner := &okHandler{}
	handler := a.RequireAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "expired-or-unknown"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestOptionalAuth_WithToken(t *testing.T) {
	a, jwtSvc, _ := testAuthenticator(t)
	userID := uuid.New()

	pair, err := jwtSvc.IssueTokenPair(userID, "opt@example.com", uuid.New().String(), "member", 0)
	if err != nil {
		t.Fatal(err)
	}

	var capturedClaims *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := a.OptionalAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if capturedClaims == nil {
		t.Fatal("expected claims in context")
	}
	if capturedClaims.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, capturedClaims.UserID)
	}
}

func TestOptionalAuth_WithoutToken_PassesThrough(t *testing.T) {
	a, _, _ := testAuthenticator(t)

	var capturedClaims *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := a.OptionalAuth(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if capturedClaims != nil {
		t.Error("expected nil claims when no auth is provided")
	}
}

func TestClaimsFromContext_NilOnMissingKey(t *testing.T) {
	if got := ClaimsFromContext(context.Background()); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestBearerToken_Extraction(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer mytoken", "mytoken"},
		{"bearer mytoken", "mytoken"},
		{"BEARER mytoken", "mytoken"},
		{"Basic credentials", ""},
		{"", ""},
		{"BearerNoSpace", ""},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		got := bearerToken(req)
		if got != tt.want {
			t.Errorf("header %q: expected %q, got %q", tt.header, tt.want, got)
		}
	}
}

func TestRequireAuth_SessionCookie_InactiveAccountRejected(t *testing.T) {
	// The cookie path carries no generation claim (DB sessions are
	// revocable server-side), but a deactivated account must still be
	// rejected by the live-state check.
	jwtSvc := NewJWTService(testTokenConfig(t))
	sessSvc := NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
	a := NewAuthenticator(jwtSvc, sessSvc, &stubStateStore{state: State{TokenGeneration: 0, IsActive: false}})

	sess, err := sessSvc.CreateSession(context.Background(), uuid.New(), "UA", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("inactive account via session cookie: expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_SessionCookie_ActiveAccountAccepted(t *testing.T) {
	jwtSvc := NewJWTService(testTokenConfig(t))
	sessSvc := NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
	// Any generation: session credentials carry no claim to compare.
	a := NewAuthenticator(jwtSvc, sessSvc, &stubStateStore{state: State{TokenGeneration: 7, IsActive: true}})

	sess, err := sessSvc.CreateSession(context.Background(), uuid.New(), "UA", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	inner := &okHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rr := httptest.NewRecorder()
	a.RequireAuth(inner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("active account via session cookie: expected 200, got %d", rr.Code)
	}
}
