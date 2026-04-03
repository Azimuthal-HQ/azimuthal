package storage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// TestMemoryStore_PutGetDelete exercises the full lifecycle of an object.
func TestMemoryStore_PutGetDelete(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	content := "hello, azimuthal"
	if err := store.Put(ctx, "test/key", strings.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if store.Len() != 1 {
		t.Errorf("expected Len()=1, got %d", store.Len())
	}

	rc, err := store.Get(ctx, "test/key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			t.Errorf("rc.Close: %v", cerr)
		}
	}()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Errorf("expected %q, got %q", content, string(got))
	}

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
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			t.Errorf("rc.Close: %v", cerr)
		}
	}()
	data, _ := io.ReadAll(rc)
	if string(data) != "second" {
		t.Errorf("expected %q after overwrite, got %q", "second", string(data))
	}
}

// TestMemoryStore_IsolatesReads verifies that mutations to returned bytes don't affect the store.
func TestMemoryStore_IsolatesReads(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	if err := store.Put(ctx, "key", bytes.NewReader([]byte("original"))); err != nil {
		t.Fatal(err)
	}

	rc, _ := store.Get(ctx, "key")
	data, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Fatalf("rc.Close: %v", err)
	}
	for i := range data {
		data[i] = 'X'
	}

	rc2, _ := store.Get(ctx, "key")
	data2, _ := io.ReadAll(rc2)
	if err := rc2.Close(); err != nil {
		t.Fatalf("rc2.Close: %v", err)
	}
	if string(data2) != "original" {
		t.Errorf("stored data was mutated; got %q", string(data2))
	}
}

// TestMemoryStore_Concurrent exercises the store under concurrent access.
func TestMemoryStore_Concurrent(_ *testing.T) {
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
func TestObjectStoreInterface(_ *testing.T) {
	var _ storage.ObjectStore = storage.NewMemoryStore()
}

// ---- S3Store tests ----

// TestNewS3Store_Valid verifies that a valid endpoint creates a client without error.
// minio.New only builds a client struct and does not make network calls.
func TestNewS3Store_Valid(t *testing.T) {
	store, err := storage.NewS3Store("localhost:9000", "access", "secret", "bucket", false)
	if err != nil {
		t.Fatalf("expected no error creating S3Store, got: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil S3Store")
	}
}

// TestNewS3Store_EmptyEndpoint verifies that an empty endpoint is rejected immediately.
func TestNewS3Store_EmptyEndpoint(t *testing.T) {
	_, err := storage.NewS3Store("", "access", "secret", "bucket", false)
	if err == nil {
		t.Fatal("expected error for empty endpoint, got nil")
	}
}

// TestS3Store_InterfaceCompliance verifies *S3Store satisfies ObjectStore at compile time.
func TestS3Store_InterfaceCompliance(_ *testing.T) {
	store, err := storage.NewS3Store("localhost:9000", "access", "secret", "bucket", false)
	if err != nil {
		return
	}
	var _ storage.ObjectStore = store
}

// TestS3Store_ErrorsOnUnreachableHost verifies that all operations return errors when
// the configured MinIO endpoint is not reachable. This exercises the error-return paths
// in EnsureBucket, Put, Get, and Delete without requiring a running MinIO instance.
func TestS3Store_ErrorsOnUnreachableHost(t *testing.T) {
	// Port 19998 is chosen to be almost certainly not listening.
	store, err := storage.NewS3Store("127.0.0.1:19998", "key", "secret", "bucket", false)
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}

	// Use a short timeout so the test doesn't hang on connection attempts.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := store.EnsureBucket(ctx); err == nil {
		t.Error("EnsureBucket: expected error on unreachable host, got nil")
	}
	if err := store.Put(ctx, "k", strings.NewReader("v")); err == nil {
		t.Error("Put: expected error on unreachable host, got nil")
	}
	// MinIO GetObject is lazy: the call itself may succeed, but the error surfaces
	// when the caller reads from the returned object.
	rc, getErr := store.Get(ctx, "k")
	if getErr == nil {
		_, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr == nil {
			t.Error("Get/Read: expected error on unreachable host, got nil")
		}
	}
	if err := store.Delete(ctx, "k"); err == nil {
		t.Error("Delete: expected error on unreachable host, got nil")
	}
}

// TestS3Store_Integration tests real MinIO operations; skipped when STORAGE_ENDPOINT is unset.
func TestS3Store_Integration(t *testing.T) {
	endpoint := os.Getenv("STORAGE_ENDPOINT")
	if endpoint == "" {
		t.Skip("STORAGE_ENDPOINT not set — skipping S3 integration tests")
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")

	store, err := storage.NewS3Store(
		endpoint,
		os.Getenv("STORAGE_ACCESS_KEY"),
		os.Getenv("STORAGE_SECRET_KEY"),
		"azimuthal-test",
		false,
	)
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}

	ctx := context.Background()
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	const key = "test/integration"
	if err := store.Put(ctx, key, strings.NewReader("integration")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if string(data) != "integration" {
		t.Errorf("expected %q, got %q", "integration", string(data))
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
