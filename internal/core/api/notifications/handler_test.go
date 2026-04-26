package notifications_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	notifyapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/notifications"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// setupHandler wires a real notifications.Service against a fresh test
// schema and returns the handler plus the org/user that owns the
// notifications.
func setupHandler(t *testing.T) (*notifyapi.Handler, *notifications.Service, testutil.User) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	svc := notifications.NewService(generated.New(db.Pool))
	return notifyapi.NewHandler(svc), svc, user
}

// TestList_RequiresAuth asserts handlers reject unauthenticated callers.
func TestList_RequiresAuth(t *testing.T) {
	h, _, _ := setupHandler(t)
	r := chi.NewRouter()
	r.Mount("/", h.Routes())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestList_Empty_OK asserts an authenticated user gets a 200 with zero rows.
func TestList_Empty_OK(t *testing.T) {
	h, _, user := setupHandler(t)
	authenticator, jwtSvc := buildAuth(t)
	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, uuid.Nil.String(), "member")
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authenticator.RequireAuth)
		r.Mount("/notifications", h.Routes())
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Notifications []notifications.Notification `json:"notifications"`
		UnreadCount   int                          `json:"unread_count"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Empty(t, body.Notifications)
	require.Equal(t, 0, body.UnreadCount)
}

// TestList_AndMarkRead_FullFlow runs the round-trip: create → list →
// mark-read → verify unread_count drops.
func TestList_AndMarkRead_FullFlow(t *testing.T) {
	h, svc, user := setupHandler(t)
	authenticator, jwtSvc := buildAuth(t)
	pair, err := jwtSvc.IssueTokenPair(user.ID, user.Email, uuid.Nil.String(), "member")
	require.NoError(t, err)

	created, err := svc.Create(context.Background(), notifications.CreateInput{
		UserID: user.ID, Kind: notifications.KindAssigned, Title: "x",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authenticator.RequireAuth)
		r.Mount("/notifications", h.Routes())
	})

	// List initially → 1 unread
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"unread_count":1`)

	// Mark read
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+created.ID.String()+"/read", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	// Verify unread_count back to 0
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"unread_count":0`)
}

// buildAuth returns a JWTService-backed Authenticator suitable for unit
// tests. Uses an ephemeral RSA key so tokens cannot leak across processes.
func buildAuth(t *testing.T) (*auth.Authenticator, *auth.JWTService) {
	t.Helper()
	key, err := auth.LoadOrGenerateRSAKey("")
	require.NoError(t, err)
	jwtSvc := auth.NewJWTService(auth.TokenConfig{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		AccessTTL:  time.Hour,
		RefreshTTL: time.Hour,
		Issuer:     "test",
	})
	sessionSvc := auth.NewSessionService(&fakeSessionRepo{}, auth.SessionConfig{TTL: time.Hour})
	return auth.NewAuthenticator(jwtSvc, sessionSvc), jwtSvc
}

type fakeSessionRepo struct{}

func (f *fakeSessionRepo) Create(_ context.Context, _ *auth.Session) error { return nil }
func (f *fakeSessionRepo) GetByToken(_ context.Context, _ string) (*auth.Session, error) {
	return nil, auth.ErrInvalidToken
}
func (f *fakeSessionRepo) Delete(_ context.Context, _ uuid.UUID) error           { return nil }
func (f *fakeSessionRepo) DeleteAllForUser(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeSessionRepo) DeleteExpired(_ context.Context) error                 { return nil }
