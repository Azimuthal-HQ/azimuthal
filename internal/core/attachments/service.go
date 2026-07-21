// Package attachments implements entity attachments (P3, ADR-0008 rule 3:
// "attachments follow the entity"). It is the first production consumer of
// the storage.ObjectStore (known-issues #16).
//
// The security property that matters most: a read never accepts an object
// key from the client. The handler looks an attachment up by id — always
// constrained to the entity in the URL — decides access from that entity
// (space access, or an active share), and derives the object key from the
// stored ROW. Guessing a key, or pointing a valid attachment id at the
// wrong entity, cannot read another object (leak failure mode 4).
package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// ErrNotFound is returned when an attachment does not exist live.
var ErrNotFound = errors.New("attachment not found")

// ErrEntityMismatch is returned when an attachment does not belong to the
// entity it was requested under — the guard that stops an attachment id
// being pointed at an entity the caller can reach.
var ErrEntityMismatch = errors.New("attachment does not belong to this entity")

// ErrTooLarge is returned when an upload exceeds the size ceiling.
var ErrTooLarge = errors.New("attachment exceeds the maximum size")

// MaxSizeBytes caps a single upload (25 MiB). A boot-time knob is overkill
// for P3; the ceiling exists so an unbounded stream cannot exhaust storage.
const MaxSizeBytes = 25 * 1024 * 1024

// Attachment is an attachment row in domain form. It deliberately carries
// no space id: authorisation derives from the owning entity, and the object
// key is internal.
type Attachment struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	EntityType  string
	EntityID    uuid.UUID
	Filename    string
	ContentType string
	SizeBytes   int64
	ObjectKey   string
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
}

// Store is the persistence contract for attachment rows.
type Store interface {
	CreateAttachment(ctx context.Context, a Attachment) (Attachment, error)
	GetAttachment(ctx context.Context, id uuid.UUID) (Attachment, error)
	ListAttachmentsByEntity(ctx context.Context, entityType string, entityID uuid.UUID) ([]Attachment, error)
	SoftDeleteAttachment(ctx context.Context, id uuid.UUID) error
}

// Service owns attachment lifecycle: object storage plus row bookkeeping.
type Service struct {
	store  Store
	blobs  storage.ObjectStore
	newKey func(orgID, id uuid.UUID) string
}

// NewService creates an attachment Service over a metadata store and an
// object store.
func NewService(store Store, blobs storage.ObjectStore) *Service {
	return &Service{
		store: store,
		blobs: blobs,
		// Key layout: orgs/{org}/attachments/{id}. Org-prefixed so a bucket
		// policy can scope by tenant; id-suffixed so it is unguessable and
		// unique. Never derived from client input.
		newKey: func(orgID, id uuid.UUID) string {
			return fmt.Sprintf("orgs/%s/attachments/%s", orgID, id)
		},
	}
}

// UploadInput carries a new attachment's bytes and metadata.
type UploadInput struct {
	OrgID       uuid.UUID
	EntityType  string
	EntityID    uuid.UUID
	Filename    string
	ContentType string
	CreatedBy   uuid.UUID
	Content     io.Reader
	// Size is the declared content length; enforced against MaxSizeBytes
	// and re-checked against the bytes actually read.
	Size int64
}

// Upload stores the object then records the row. The object goes first: a
// row without its object would render broken, whereas an orphaned object
// (row insert fails) is invisible and swept later. The reader is capped at
// MaxSizeBytes+1 so an over-large or mislabelled stream is rejected, not
// stored.
func (s *Service) Upload(ctx context.Context, in UploadInput) (Attachment, error) {
	if !access.ValidShareEntityType(in.EntityType) {
		return Attachment{}, access.ErrInvalidShareEntityType
	}
	if in.Size > MaxSizeBytes {
		return Attachment{}, ErrTooLarge
	}
	id := uuid.New()
	key := s.newKey(in.OrgID, id)

	// Count bytes as they stream to storage; a stream that exceeds the
	// ceiling (or lied about Size) is rejected and its partial object
	// removed.
	counted := &countingReader{r: io.LimitReader(in.Content, MaxSizeBytes+1)}
	if err := s.blobs.Put(ctx, key, counted); err != nil {
		return Attachment{}, fmt.Errorf("storing attachment object: %w", err)
	}
	if counted.n > MaxSizeBytes {
		_ = s.blobs.Delete(ctx, key)
		return Attachment{}, ErrTooLarge
	}

	row, err := s.store.CreateAttachment(ctx, Attachment{
		ID:          id,
		OrgID:       in.OrgID,
		EntityType:  in.EntityType,
		EntityID:    in.EntityID,
		Filename:    in.Filename,
		ContentType: in.ContentType,
		SizeBytes:   counted.n,
		ObjectKey:   key,
		CreatedBy:   in.CreatedBy,
	})
	if err != nil {
		_ = s.blobs.Delete(ctx, key)
		return Attachment{}, fmt.Errorf("recording attachment: %w", err)
	}
	return row, nil
}

// GetForEntity loads an attachment and asserts it belongs to the named
// entity. Callers pass the entity from the URL, so a valid attachment id
// under the wrong entity is ErrEntityMismatch, not a successful read.
func (s *Service) GetForEntity(ctx context.Context, entityType string, entityID, attachmentID uuid.UUID) (Attachment, error) {
	att, err := s.store.GetAttachment(ctx, attachmentID)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("getting attachment: %w", err)
	}
	if att.EntityType != entityType || att.EntityID != entityID {
		return Attachment{}, ErrEntityMismatch
	}
	return att, nil
}

// Get loads an attachment by id (no entity constraint). The space-scoped
// read path uses it, then verifies the attachment's entity belongs to the
// URL space itself.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Attachment, error) {
	att, err := s.store.GetAttachment(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("getting attachment: %w", err)
	}
	return att, nil
}

// Open returns the attachment's content stream. The key comes from the row,
// never from the caller. The caller closes the reader.
func (s *Service) Open(ctx context.Context, att Attachment) (io.ReadCloser, error) {
	rc, err := s.blobs.Get(ctx, att.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("reading attachment object: %w", err)
	}
	return rc, nil
}

// ListByEntity returns the entity's live attachments (metadata only).
func (s *Service) ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID) ([]Attachment, error) {
	list, err := s.store.ListAttachmentsByEntity(ctx, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	return list, nil
}

// Delete soft-deletes the row. The object is retained (a nightly sweep can
// reclaim it), so an in-flight read never 500s on a vanished object.
func (s *Service) Delete(ctx context.Context, entityType string, entityID, attachmentID uuid.UUID) error {
	att, err := s.GetForEntity(ctx, entityType, entityID, attachmentID)
	if err != nil {
		return err
	}
	if err := s.store.SoftDeleteAttachment(ctx, att.ID); err != nil {
		return fmt.Errorf("deleting attachment: %w", err)
	}
	return nil
}

// countingReader counts bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
