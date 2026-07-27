package people

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
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
	if !inlineSafeAvatarType(doc.SniffImageType(data)) {
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

// AvatarObject returns the stored avatar bytes and the content type they may
// be served as. The key is derived from the ids, never from the client.
//
// S7. The type is not merely sniffed — it is sniffed AND checked against the
// inline allow-list, and an object that is not on it is refused rather than
// served. Sniffing alone was the defect: the serve handler sets the returned
// type with Content-Disposition: inline and X-Content-Type-Options: nosniff,
// so an object sniffing as text/html would have been rendered as a document,
// on the app's own origin, by a browser explicitly told not to second-guess
// the type. The upload gate has always refused such an object, but the serve
// path trusted what it found in storage — and a serve path that is only safe
// because some other code path was careful is not safe, it is lucky. Objects
// written before the gate existed, or by any future writer to the same bucket
// prefix, go through this check too.
//
// The allow-list is doc.SupportedImageType, the same four raster types the
// page-image gate and this service's own upload gate use, rather than
// attachments.ServeTypeFor. ServeTypeFor is the right shared decision for an
// attachment and additionally admits application/pdf; an avatar is rendered
// in an <img>, so a PDF there is a broken image at best. The four-type list is
// the shared thing; the PDF exception belongs to attachments.
//
// It is not a fourth copy of the sniffer either: this used to carry its own
// http.DetectContentType wrapper and its own map of the same four types. Both
// are gone.
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
	sniffed := doc.SniffImageType(data)
	if !inlineSafeAvatarType(sniffed) {
		return nil, "", ErrAvatarType
	}
	return data, sniffed, nil
}

// inlineSafeAvatarType reports whether a sniffed content type may be served
// inline as an avatar. One predicate, used by both the upload gate and the
// serve path, so the two can never drift apart.
func inlineSafeAvatarType(sniffed string) bool {
	return doc.SupportedImageType(sniffed)
}
