package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// MemoryStore is an in-memory ObjectStore implementation intended for use in tests.
// It is safe for concurrent use.
type MemoryStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewMemoryStore creates a new empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string][]byte)}
}

// Put stores an object under the given key, reading content from r.
func (m *MemoryStore) Put(_ context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading object data for key %q: %w", key, err)
	}
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return nil
}

// Get retrieves the object at the given key.
// Returns an error if the key does not exist.
// The caller is responsible for closing the returned ReadCloser.
func (m *MemoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	data, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("object %q not found", key)
	}
	// Return a copy so callers cannot mutate the stored data.
	cp := make([]byte, len(data))
	copy(cp, data)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

// Delete removes the object at the given key.
// It returns nil if the object does not exist.
func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

// Len returns the number of objects currently stored. Useful in tests.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	n := len(m.objects)
	m.mu.RUnlock()
	return n
}
