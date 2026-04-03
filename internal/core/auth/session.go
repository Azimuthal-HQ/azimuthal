package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Session represents an authenticated user session stored in postgres.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Token     string // opaque random token used as a lookup key
	ExpiresAt time.Time
	CreatedAt time.Time
	// UserAgent and IPAddress are stored for audit purposes only.
	UserAgent string
	IPAddress string
}

// IsExpired reports whether the session is past its expiry time.
func (s *Session) IsExpired() bool {
	return time.Now().UTC().After(s.ExpiresAt)
}

// SessionRepository defines the data access contract for sessions.
// The concrete implementation lives in internal/db once Agent 1A merges.
type SessionRepository interface {
	// Create persists a new session record.
	Create(ctx context.Context, s *Session) error
	// GetByToken retrieves a session by its opaque token.
	// Returns ErrNotFound if the token is unknown.
	GetByToken(ctx context.Context, token string) (*Session, error)
	// Delete removes a single session (logout).
	Delete(ctx context.Context, id uuid.UUID) error
	// DeleteAllForUser removes every session belonging to a user (force-logout all).
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
	// DeleteExpired removes all sessions whose ExpiresAt is in the past.
	DeleteExpired(ctx context.Context) error
}

// SessionConfig controls session lifetime settings.
type SessionConfig struct {
	// TTL is how long a session remains valid after creation.
	TTL time.Duration
}

// SessionService manages postgres-backed user sessions.
type SessionService struct {
	repo SessionRepository
	cfg  SessionConfig
}

// NewSessionService creates a SessionService backed by the given repository.
func NewSessionService(repo SessionRepository, cfg SessionConfig) *SessionService {
	return &SessionService{repo: repo, cfg: cfg}
}

// CreateSession creates and persists a new session for the given user.
// userAgent and ipAddress are recorded for audit purposes.
func (s *SessionService) CreateSession(ctx context.Context, userID uuid.UUID, userAgent, ipAddress string) (*Session, error) {
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:        uuid.New(),
		UserID:    userID,
		Token:     token,
		ExpiresAt: now.Add(s.cfg.TTL),
		CreatedAt: now,
		UserAgent: userAgent,
		IPAddress: ipAddress,
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return sess, nil
}

// GetSession retrieves a session by its opaque token and validates it is
// not expired. Returns ErrNotFound or ErrSessionExpired as appropriate.
func (s *SessionService) GetSession(ctx context.Context, token string) (*Session, error) {
	sess, err := s.repo.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	if sess.IsExpired() {
		// Clean up expired session in the background; ignore errors here.
		_ = s.repo.Delete(ctx, sess.ID)
		return nil, ErrSessionExpired
	}
	return sess, nil
}

// DeleteSession invalidates a specific session (single-device logout).
func (s *SessionService) DeleteSession(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteAllSessions invalidates every session for a user (all-device logout).
func (s *SessionService) DeleteAllSessions(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.DeleteAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("deleting all sessions: %w", err)
	}
	return nil
}

// PurgeExpiredSessions removes all expired sessions from the store.
// Intended to be called periodically by a background job.
func (s *SessionService) PurgeExpiredSessions(ctx context.Context) error {
	if err := s.repo.DeleteExpired(ctx); err != nil {
		return fmt.Errorf("purging expired sessions: %w", err)
	}
	return nil
}
