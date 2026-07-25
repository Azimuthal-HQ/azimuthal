package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// ErrUnsupportedImage is returned when an upload's bytes are not one of the
// image types a Codex document may render.
var ErrUnsupportedImage = errors.New("unsupported image type")

// sniffBytes is how much of a stream http.DetectContentType needs. Reading
// exactly this much means an image is classified without buffering the whole
// upload — the rest streams to object storage as before.
const sniffBytes = 512

// UploadPageImage stores an image on a page.
//
// It is the same table, the same object store and the same server-derived object
// key as any other attachment — not a parallel pipeline — with two things the
// generic upload path cannot do. The entity comes from the caller's URL rather
// than a form field, so an upload cannot be aimed at an entity the caller
// happens to know the id of. And the content type is SNIFFED from the leading
// bytes and checked against the document model's allow-list, so what gets stored
// is what the file is rather than what the client said it was.
//
// That second point is the avatar surface's discipline
// (internal/core/people/avatar.go), and it matters more here: the download path
// serves an attachment inline with its stored content type, so a stored type
// taken on trust is a stored type an uploader chooses.
func (s *Service) UploadPageImage(ctx context.Context, in wiki.UploadImageInput) (wiki.PageImage, error) {
	if in.Size > MaxSizeBytes {
		return wiki.PageImage{}, ErrTooLarge
	}

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(in.Content, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return wiki.PageImage{}, fmt.Errorf("reading image: %w", err)
	}
	head = head[:n]

	contentType := doc.SniffImageType(head)
	if !doc.SupportedImageType(contentType) {
		return wiki.PageImage{}, fmt.Errorf("%w: %s is not one of %v", ErrUnsupportedImage, contentType, doc.SupportedImageTypes())
	}

	att, err := s.Upload(ctx, UploadInput{
		OrgID:       in.OrgID,
		EntityType:  access.ShareEntityPage,
		EntityID:    in.PageID,
		Filename:    in.Filename,
		ContentType: contentType,
		CreatedBy:   in.UploadedBy,
		// The sniffed head is put back in front of the remainder, so storage
		// receives the whole object exactly once.
		Content: io.MultiReader(bytes.NewReader(head), in.Content),
		Size:    in.Size,
	})
	if err != nil {
		return wiki.PageImage{}, err
	}
	return wiki.PageImage{
		AttachmentID: att.ID,
		Filename:     att.Filename,
		ContentType:  att.ContentType,
		SizeBytes:    att.SizeBytes,
	}, nil
}

// PageImageContentType sniffs the stored bytes of an attachment on a page.
//
// It sniffs rather than reading the recorded content_type because the generic
// upload endpoint records what the client declared, and any space writer can put
// any file on a page through it. A document's image reference is only sound if
// the thing it points at is genuinely an image, so this asks the bytes.
//
// Only the leading [sniffBytes] are read, and the reader is closed immediately.
func (s *Service) PageImageContentType(ctx context.Context, pageID, attachmentID uuid.UUID) (string, error) {
	att, err := s.GetForEntity(ctx, access.ShareEntityPage, pageID, attachmentID)
	if err != nil {
		return "", err
	}
	rc, err := s.Open(ctx, att)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("reading attachment head: %w", err)
	}
	return doc.SniffImageType(head[:n]), nil
}
