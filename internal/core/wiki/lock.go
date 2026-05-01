package wiki

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// timestamptz converts a time.Time to pgtype.Timestamptz.
func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

const defaultLockDuration = 5 * time.Minute

// ErrPageLocked is returned when a page is locked by another user.
var ErrPageLocked = errors.New("page is locked by another user")

// PageLock is a view of an active lock returned to callers.
type PageLock struct {
	PageID    uuid.UUID `json:"page_id"`
	UserID    uuid.UUID `json:"user_id"`
	UserName  string    `json:"user_name"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LockStore defines the lock-related DB operations used by LockService.
type LockStore interface {
	UpsertPageLock(ctx context.Context, arg generated.UpsertPageLockParams) (generated.PageLock, error)
	GetPageLock(ctx context.Context, pageID uuid.UUID) (generated.PageLock, error)
	DeletePageLock(ctx context.Context, arg generated.DeletePageLockParams) error
	DeleteExpiredPageLocks(ctx context.Context) error
}

// LockService manages per-page edit locks.
type LockService struct {
	store    LockStore
	duration time.Duration
}

// NewLockService creates a LockService with the default lock duration (5 min).
func NewLockService(store LockStore) *LockService {
	return &LockService{store: store, duration: defaultLockDuration}
}

// AcquireLock attempts to acquire or renew the lock on a page for a user.
// Returns ErrPageLocked if the page is already locked by a different user.
func (s *LockService) AcquireLock(ctx context.Context, pageID, userID uuid.UUID, userName string) (*PageLock, error) {
	existing, err := s.store.GetPageLock(ctx, pageID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("checking page lock: %w", err)
	}
	if err == nil && existing.UserID != userID {
		return nil, ErrPageLocked
	}

	expiresAt := time.Now().Add(s.duration)
	lock, err := s.store.UpsertPageLock(ctx, generated.UpsertPageLockParams{
		PageID:    pageID,
		UserID:    userID,
		UserName:  userName,
		ExpiresAt: timestamptz(expiresAt),
	})
	if err != nil {
		return nil, fmt.Errorf("acquiring page lock: %w", err)
	}
	return pageLockFromDB(lock), nil
}

// GetLock returns the active lock for a page, or nil if there is none.
func (s *LockService) GetLock(ctx context.Context, pageID uuid.UUID) (*PageLock, error) {
	lock, err := s.store.GetPageLock(ctx, pageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting page lock: %w", err)
	}
	return pageLockFromDB(lock), nil
}

// ReleaseLock releases the lock on a page if it is held by the given user.
func (s *LockService) ReleaseLock(ctx context.Context, pageID, userID uuid.UUID) error {
	if err := s.store.DeletePageLock(ctx, generated.DeletePageLockParams{
		PageID: pageID,
		UserID: userID,
	}); err != nil {
		return fmt.Errorf("releasing page lock: %w", err)
	}
	return nil
}

// PurgeExpired removes all expired locks. Intended to be called periodically.
func (s *LockService) PurgeExpired(ctx context.Context) error {
	if err := s.store.DeleteExpiredPageLocks(ctx); err != nil {
		return fmt.Errorf("purging expired locks: %w", err)
	}
	return nil
}

func pageLockFromDB(l generated.PageLock) *PageLock {
	return &PageLock{
		PageID:    l.PageID,
		UserID:    l.UserID,
		UserName:  l.UserName,
		ExpiresAt: l.ExpiresAt.Time,
	}
}
