package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubSessionRepo is an in-memory SessionRepository for testing.
type stubSessionRepo struct {
	sessions map[string]*Session // keyed by token
}

func newStubSessionRepo() *stubSessionRepo {
	return &stubSessionRepo{sessions: make(map[string]*Session)}
}

func (r *stubSessionRepo) Create(_ context.Context, s *Session) error {
	r.sessions[s.Token] = s
	return nil
}

func (r *stubSessionRepo) GetByToken(_ context.Context, token string) (*Session, error) {
	s, ok := r.sessions[token]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (r *stubSessionRepo) Delete(_ context.Context, id uuid.UUID) error {
	for token, s := range r.sessions {
		if s.ID == id {
			delete(r.sessions, token)
			return nil
		}
	}
	return ErrNotFound
}

func (r *stubSessionRepo) DeleteAllForUser(_ context.Context, userID uuid.UUID) error {
	for token, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, token)
		}
	}
	return nil
}

func (r *stubSessionRepo) DeleteExpired(_ context.Context) error {
	for token, s := range r.sessions {
		if s.IsExpired() {
			delete(r.sessions, token)
		}
	}
	return nil
}

func testSessionService(t *testing.T) *SessionService {
	t.Helper()
	return NewSessionService(newStubSessionRepo(), SessionConfig{TTL: time.Hour})
}

func TestSessionService_CreateAndGet(t *testing.T) {
	svc := testSessionService(t)
	userID := uuid.New()

	sess, err := svc.CreateSession(context.Background(), userID, "Mozilla/5.0", "127.0.0.1")
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if sess.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, sess.UserID)
	}
	if sess.IsExpired() {
		t.Error("fresh session should not be expired")
	}

	got, err := svc.GetSession(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("getting session: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("expected session ID %s, got %s", sess.ID, got.ID)
	}
}

func TestSessionService_GetSession_NotFound(t *testing.T) {
	svc := testSessionService(t)
	_, err := svc.GetSession(context.Background(), "nonexistent-token")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSessionService_GetSession_Expired(t *testing.T) {
	repo := newStubSessionRepo()
	svc := NewSessionService(repo, SessionConfig{TTL: -time.Second}) // already expired

	userID := uuid.New()
	sess, err := svc.CreateSession(context.Background(), userID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetSession(context.Background(), sess.Token)
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("expected ErrSessionExpired, got %v", err)
	}
}

func TestSessionService_DeleteSession(t *testing.T) {
	svc := testSessionService(t)
	sess, _ := svc.CreateSession(context.Background(), uuid.New(), "", "")

	if err := svc.DeleteSession(context.Background(), sess.ID); err != nil {
		t.Fatalf("deleting session: %v", err)
	}
	_, err := svc.GetSession(context.Background(), sess.Token)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestSessionService_DeleteAllSessions(t *testing.T) {
	svc := testSessionService(t)
	userID := uuid.New()

	s1, _ := svc.CreateSession(context.Background(), userID, "UA1", "1.2.3.4")
	s2, _ := svc.CreateSession(context.Background(), userID, "UA2", "1.2.3.5")
	// Different user — should not be affected.
	s3, _ := svc.CreateSession(context.Background(), uuid.New(), "UA3", "1.2.3.6")

	if err := svc.DeleteAllSessions(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{s1.Token, s2.Token} {
		if _, err := svc.GetSession(context.Background(), tok); !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound for deleted session, got %v", err)
		}
	}
	if _, err := svc.GetSession(context.Background(), s3.Token); err != nil {
		t.Errorf("other user session should still exist, got %v", err)
	}
}

func TestSessionService_PurgeExpiredSessions(t *testing.T) {
	repo := newStubSessionRepo()
	activeSvc := NewSessionService(repo, SessionConfig{TTL: time.Hour})
	expiredSvc := NewSessionService(repo, SessionConfig{TTL: -time.Second})

	active, _ := activeSvc.CreateSession(context.Background(), uuid.New(), "", "")
	expired, _ := expiredSvc.CreateSession(context.Background(), uuid.New(), "", "")

	if err := activeSvc.PurgeExpiredSessions(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Active session must survive.
	if _, err := repo.GetByToken(context.Background(), active.Token); err != nil {
		t.Errorf("active session should survive purge, got %v", err)
	}
	// Expired session must be removed.
	if _, err := repo.GetByToken(context.Background(), expired.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session should be purged, got %v", err)
	}
}

func TestSession_IsExpired(t *testing.T) {
	future := Session{ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if future.IsExpired() {
		t.Error("future session should not be expired")
	}
	past := Session{ExpiresAt: time.Now().UTC().Add(-time.Second)}
	if !past.IsExpired() {
		t.Error("past session should be expired")
	}
}

func TestTokensAreUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[tok]; exists {
			t.Fatalf("duplicate token generated on iteration %d", i)
		}
		seen[tok] = struct{}{}
	}
}
