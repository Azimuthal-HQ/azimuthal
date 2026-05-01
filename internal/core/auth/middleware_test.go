package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testAuthenticator creates an Authenticator wired with in-memory stubs.
func testAuthenticator(t *testing.T) (*Authenticator, *JWTService, *SessionService) {
	t.Helper()
	jwtSvc := NewJWTService(testTokenConfig(t))
	sessSvc := NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
	auth := NewAuthenticator(jwtSvc, sessSvc)
	return auth, jwtSvc, sessSvc
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

	pair, err := jwtSvc.IssueTokenPair(userID, "user@example.com", uuid.New().String(), "member")
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

	pair, err := jwtSvc.IssueTokenPair(userID, "opt@example.com", uuid.New().String(), "member")
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
