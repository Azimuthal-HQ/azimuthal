package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Store is an ObjectStore backed by S3-compatible object storage (AWS S3 or MinIO).
type S3Store struct {
	client *minio.Client
	bucket string
}

// NewS3Store creates a new S3Store connected to the given endpoint.
// Set useSSL=false for local MinIO development.
func NewS3Store(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3Store, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating minio client: %w", err)
	}
	return &S3Store{client: client, bucket: bucket}, nil
}

// EnsureBucket creates the bucket if it does not already exist.
// Call this on startup before any Put/Get/Delete operations.
func (s *S3Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", s.bucket, err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("creating bucket %q: %w", s.bucket, err)
		}
	}
	return nil
}

// Put stores an object under the given key, reading content from r.
func (s *S3Store) Put(ctx context.Context, key string, r io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, -1, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("putting object %q: %w", key, err)
	}
	return nil
}

// Get retrieves the object at the given key.
// The caller is responsible for closing the returned ReadCloser.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting object %q: %w", key, err)
	}
	return obj, nil
}

// Delete removes the object at the given key.
// It returns nil if the object does not exist.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("deleting object %q: %w", key, err)
	}
	return nil
}
