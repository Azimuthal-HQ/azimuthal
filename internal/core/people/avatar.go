package people

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// MaxAvatarBytes caps an avatar upload (5 MiB). Avatars are small; the ceiling
// exists so an unbounded or mislabelled stream cannot exhaust storage.
const MaxAvatarBytes = 5 * 1024 * 1024

var (
	// ErrAvatarTooLarge is returned when an upload exceeds MaxAvatarBytes.
	ErrAvatarTooLarge = errors.New("avatar exceeds the maximum size")
	// ErrAvatarType is returned when the uploaded bytes are not a supported
	// image type (sniffed server-side, never trusting the client header).
	ErrAvatarType = errors.New("avatar must be a PNG, JPEG, WebP, or GIF image")
	// ErrAvatarNotFound is returned when no avatar object exists for a user.
	ErrAvatarNotFound = errors.New("no avatar set")
)

var allowedAvatarTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// AvatarStore is the persistence the avatar service needs: it records the
// serve reference on the user row, scoped to the org (member check).
type AvatarStore interface {
	// SetAvatarURL sets users.avatar_url for a member of orgID. ErrNotMember
	// when the target is not a member of the org.
	SetAvatarURL(ctx context.Context, orgID, userID uuid.UUID, url string) error
}

// AvatarService stores avatar images in object storage and records a serve
// reference on the user row. It reuses the shared storage.ObjectStore rather
// than the entity-scoped attachments table (whose type CHECK and share model
// do not apply to users).
type AvatarService struct {
	store AvatarStore
	blobs storage.ObjectStore
}

// NewAvatarService creates an AvatarService.
func NewAvatarService(store AvatarStore, blobs storage.ObjectStore) *AvatarService {
	return &AvatarService{store: store, blobs: blobs}
}

// avatarKey is derived server-side from the ids, never from client input, so
// the serve path cannot be turned into an arbitrary-object read primitive.
func avatarKey(orgID, userID uuid.UUID) string {
	return fmt.Sprintf("orgs/%s/avatars/%s", orgID, userID)
}

// SetAvatar validates the uploaded image (size ceiling + sniffed content type)
// and stores it, then records the serve URL on the user row. Returns the serve
// URL. The object is written before the row so a failed row write leaves an
// orphan object (invisible) rather than a broken reference.
func (s *AvatarService) SetAvatar(ctx context.Context, orgID, userID uuid.UUID, r io.Reader) (string, error) {
	// Read up to the ceiling+1 so we can both sniff the type and reject an
	// over-large stream before storing anything.
	data, err := io.ReadAll(io.LimitReader(r, MaxAvatarBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading avatar: %w", err)
	}
	if int64(len(data)) > MaxAvatarBytes {
		return "", ErrAvatarTooLarge
	}
	if !allowedAvatarTypes[sniffImageType(data)] {
		return "", ErrAvatarType
	}

	key := avatarKey(orgID, userID)
	if err := s.blobs.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("storing avatar object: %w", err)
	}
	serveURL := fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", orgID, userID)
	if err := s.store.SetAvatarURL(ctx, orgID, userID, serveURL); err != nil {
		_ = s.blobs.Delete(ctx, key)
		return "", fmt.Errorf("recording avatar reference: %w", err)
	}
	return serveURL, nil
}

// AvatarObject returns the stored avatar bytes and its sniffed content type for
// serving. The key is derived from the ids, never from the client.
func (s *AvatarService) AvatarObject(ctx context.Context, orgID, userID uuid.UUID) ([]byte, string, error) {
	rc, err := s.blobs.Get(ctx, avatarKey(orgID, userID))
	if err != nil {
		return nil, "", ErrAvatarNotFound
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, MaxAvatarBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading avatar object: %w", err)
	}
	return data, sniffImageType(data), nil
}

// sniffImageType detects the content type from the leading bytes and strips
// any parameters (http.DetectContentType may append "; charset=...").
func sniffImageType(data []byte) string {
	ct := http.DetectContentType(data)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}
