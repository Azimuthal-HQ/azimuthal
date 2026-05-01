package wiki_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// --- mockLockStore implements wiki.LockStore for unit tests ---

type mockLockStore struct {
	locks map[uuid.UUID]generated.PageLock
	err   error
}

func newMockLockStore() *mockLockStore {
	return &mockLockStore{locks: make(map[uuid.UUID]generated.PageLock)}
}

func (m *mockLockStore) UpsertPageLock(_ context.Context, arg generated.UpsertPageLockParams) (generated.PageLock, error) {
	if m.err != nil {
		return generated.PageLock{}, m.err
	}
	lock := generated.PageLock{
		PageID:    arg.PageID,
		UserID:    arg.UserID,
		UserName:  arg.UserName,
		ExpiresAt: arg.ExpiresAt,
	}
	m.locks[arg.PageID] = lock
	return lock, nil
}

func (m *mockLockStore) GetPageLock(_ context.Context, pageID uuid.UUID) (generated.PageLock, error) {
	if m.err != nil {
		return generated.PageLock{}, m.err
	}
	lock, ok := m.locks[pageID]
	if !ok {
		return generated.PageLock{}, pgx.ErrNoRows
	}
	return lock, nil
}

func (m *mockLockStore) DeletePageLock(_ context.Context, arg generated.DeletePageLockParams) error {
	if m.err != nil {
		return m.err
	}
	delete(m.locks, arg.PageID)
	return nil
}

func (m *mockLockStore) DeleteExpiredPageLocks(_ context.Context) error {
	if m.err != nil {
		return m.err
	}
	now := time.Now()
	for k, l := range m.locks {
		if l.ExpiresAt.Time.Before(now) {
			delete(m.locks, k)
		}
	}
	return nil
}

func newLockService() (*wiki.LockService, *mockLockStore) {
	store := newMockLockStore()
	svc := wiki.NewLockService(store)
	return svc, store
}

func TestLockService_AcquireLock_New(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	pageID := uuid.New()
	userID := uuid.New()

	lock, err := svc.AcquireLock(ctx, pageID, userID, "Alice")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if lock.PageID != pageID {
		t.Errorf("PageID mismatch: got %v, want %v", lock.PageID, pageID)
	}
	if lock.UserID != userID {
		t.Errorf("UserID mismatch: got %v, want %v", lock.UserID, userID)
	}
	if lock.UserName != "Alice" {
		t.Errorf("UserName mismatch: got %v, want Alice", lock.UserName)
	}
}

func TestLockService_AcquireLock_Renew(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	pageID := uuid.New()
	userID := uuid.New()

	_, err := svc.AcquireLock(ctx, pageID, userID, "Alice")
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	// Same user can renew.
	lock, err := svc.AcquireLock(ctx, pageID, userID, "Alice")
	if err != nil {
		t.Fatalf("renew AcquireLock: %v", err)
	}
	if lock.UserID != userID {
		t.Errorf("UserID mismatch on renew")
	}
}

func TestLockService_AcquireLock_Blocked(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	pageID := uuid.New()
	userA := uuid.New()
	userB := uuid.New()

	_, err := svc.AcquireLock(ctx, pageID, userA, "Alice")
	if err != nil {
		t.Fatalf("AcquireLock for Alice: %v", err)
	}

	// Different user should get ErrPageLocked.
	_, err = svc.AcquireLock(ctx, pageID, userB, "Bob")
	if !errors.Is(err, wiki.ErrPageLocked) {
		t.Errorf("expected ErrPageLocked, got %v", err)
	}
}

func TestLockService_GetLock_None(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	lock, err := svc.GetLock(ctx, uuid.New())
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}
	if lock != nil {
		t.Errorf("expected nil lock, got %+v", lock)
	}
}

func TestLockService_GetLock_Existing(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	pageID := uuid.New()
	userID := uuid.New()

	_, err := svc.AcquireLock(ctx, pageID, userID, "Charlie")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	lock, err := svc.GetLock(ctx, pageID)
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock")
	}
	if lock.UserID != userID {
		t.Errorf("UserID mismatch: got %v, want %v", lock.UserID, userID)
	}
}

func TestLockService_ReleaseLock(t *testing.T) {
	svc, _ := newLockService()
	ctx := context.Background()

	pageID := uuid.New()
	userID := uuid.New()

	_, err := svc.AcquireLock(ctx, pageID, userID, "Dan")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	err = svc.ReleaseLock(ctx, pageID, userID)
	if err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}

	lock, err := svc.GetLock(ctx, pageID)
	if err != nil {
		t.Fatalf("GetLock after release: %v", err)
	}
	if lock != nil {
		t.Errorf("expected nil lock after release, got %+v", lock)
	}
}

func TestLockService_PurgeExpired(t *testing.T) {
	svc, store := newLockService()
	ctx := context.Background()

	// Insert an already-expired lock directly into the mock store.
	pageID := uuid.New()
	store.locks[pageID] = generated.PageLock{
		PageID: pageID,
		UserID: uuid.New(),
		ExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().Add(-time.Hour),
			Valid: true,
		},
	}

	err := svc.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}

	if _, ok := store.locks[pageID]; ok {
		t.Error("expired lock was not purged")
	}
}

func TestLockService_AcquireLock_StoreError(t *testing.T) {
	svc, store := newLockService()
	ctx := context.Background()

	store.err = errors.New("db error")

	_, err := svc.AcquireLock(ctx, uuid.New(), uuid.New(), "Eve")
	if err == nil {
		t.Error("expected error from store, got nil")
	}
}

func TestLockService_GetLock_StoreError(t *testing.T) {
	svc, store := newLockService()
	ctx := context.Background()

	// First insert a lock so GetPageLock doesn't return ErrNoRows.
	pageID := uuid.New()
	store.locks[pageID] = generated.PageLock{PageID: pageID}

	store.err = errors.New("db error")

	_, err := svc.GetLock(ctx, pageID)
	if err == nil {
		t.Error("expected error from store, got nil")
	}
}
