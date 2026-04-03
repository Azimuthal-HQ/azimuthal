package storage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// TestMemoryStore_PutGetDelete exercises the full lifecycle of an object.
func TestMemoryStore_PutGetDelete(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	// Put
	content := "hello, azimuthal"
	if err := store.Put(ctx, "test/key", strings.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if store.Len() != 1 {
		t.Errorf("expected Len()=1, got %d", store.Len())
	}

	// Get
	rc, err := store.Get(ctx, "test/key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Errorf("expected %q, got %q", content, string(got))
	}

	// Delete
	if err := store.Delete(ctx, "test/key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.Len() != 0 {
		t.Errorf("expected Len()=0 after delete, got %d", store.Len())
	}
}

// TestMemoryStore_GetMissing verifies that getting a nonexistent key returns an error.
func TestMemoryStore_GetMissing(t *testing.T) {
	store := storage.NewMemoryStore()
	_, err := store.Get(context.Background(), "does/not/exist")
	if err == nil {
		t.Fatal("expected error getting nonexistent key, got nil")
	}
}

// TestMemoryStore_DeleteMissing verifies that deleting a nonexistent key is a no-op.
func TestMemoryStore_DeleteMissing(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.Delete(context.Background(), "does/not/exist"); err != nil {
		t.Fatalf("expected no error deleting nonexistent key, got: %v", err)
	}
}

// TestMemoryStore_Overwrite verifies that putting the same key twice replaces the value.
func TestMemoryStore_Overwrite(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	if err := store.Put(ctx, "key", strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "key", strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	if store.Len() != 1 {
		t.Errorf("expected Len()=1 after overwrite, got %d", store.Len())
	}

	rc, err := store.Get(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "second" {
		t.Errorf("expected %q after overwrite, got %q", "second", string(data))
	}
}

// TestMemoryStore_IsolatesReads verifies that mutations to the returned bytes don't affect the store.
func TestMemoryStore_IsolatesReads(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	if err := store.Put(ctx, "key", bytes.NewReader([]byte("original"))); err != nil {
		t.Fatal(err)
	}

	rc, _ := store.Get(ctx, "key")
	data, _ := io.ReadAll(rc)
	rc.Close()
	// Mutate the returned data.
	for i := range data {
		data[i] = 'X'
	}

	// The stored copy should be unchanged.
	rc2, _ := store.Get(ctx, "key")
	data2, _ := io.ReadAll(rc2)
	rc2.Close()
	if string(data2) != "original" {
		t.Errorf("stored data was mutated; got %q", string(data2))
	}
}

// TestMemoryStore_Concurrent exercises the store under concurrent access.
func TestMemoryStore_Concurrent(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key/%d", n)
			_ = store.Put(ctx, key, strings.NewReader("data"))
			_, _ = store.Get(ctx, key)
			_ = store.Delete(ctx, key)
		}(i)
	}
	wg.Wait()
}

// TestObjectStoreInterface verifies that *MemoryStore satisfies ObjectStore at compile time.
func TestObjectStoreInterface(t *testing.T) {
	var _ storage.ObjectStore = storage.NewMemoryStore()
}
