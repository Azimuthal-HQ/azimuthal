// Package storage defines the ObjectStore interface and its implementations.
// All file storage must go through this interface — never write to local disk.
package storage

import (
	"context"
	"io"
)

// ObjectStore is the interface for all object storage operations.
// Implementations include S3/MinIO (production) and in-memory (tests).
// All file I/O in Azimuthal must go through this interface.
type ObjectStore interface {
	// Put stores an object under the given key, reading content from r.
	Put(ctx context.Context, key string, r io.Reader) error

	// Get retrieves the object at the given key.
	// The caller is responsible for closing the returned ReadCloser.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at the given key.
	// It returns nil if the object does not exist.
	Delete(ctx context.Context, key string) error
}
