package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// DownloadContentType is the type every attachment that may not render inline
// is served as. It is deliberately opaque: a browser hands it to the download
// machinery instead of choosing a renderer.
const DownloadContentType = "application/octet-stream"

// inlinePDFType is the one non-image type on the inline allow-list. A PDF
// opens in the browser's own viewer, which is not a document in the app's
// origin, and it is the type users most expect to preview rather than save.
const inlinePDFType = "application/pdf"

// ServeType is the decision about how an attachment's bytes may be handed to
// a browser.
type ServeType struct {
	// ContentType is the type to send. For an inline-safe object it is the
	// SNIFFED type; for everything else it is [DownloadContentType]. It is
	// never the stored content_type.
	ContentType string
	// Inline reports whether the object may be rendered in place rather than
	// downloaded.
	Inline bool
}

// ServeTypeFor decides how an object may be served, from its leading bytes.
//
// The stored content_type is not an input, by design. It is whatever the
// client declared at upload (the generic upload route records the multipart
// part's header verbatim), and it survives on every row written before this
// check existed. Trusting it for an `inline`, same-origin response would let
// any space writer upload HTML declaring `text/html` and have it served as a
// document — reachable, per ADR-0008, by share recipients who sit outside the
// space entirely. Deciding from the bytes at serve time is also what makes
// rows already in the table safe without a migration.
//
// The inline allow-list is deliberately short:
//
//   - The four raster image types ([doc.SupportedImageTypes]) are inert
//     bitmaps — a malformed one is a broken image, never script. They are the
//     same four the page-image upload gate and the avatar surface already
//     allow, and they must stay inline: page images are streamed through this
//     very path, so dropping them would break every document image.
//   - application/pdf, per above.
//
// Everything else downloads. That includes SVG, which is a scriptable XML
// document rather than a bitmap, along with HTML, XML, and anything ambiguous
// or unrecognised. http.DetectContentType never reports image/svg+xml at all
// — an SVG sniffs as text/plain or text/xml — so SVG is excluded by not being
// on the list, and would still be excluded if that ever changed.
func ServeTypeFor(head []byte) ServeType {
	sniffed := doc.SniffImageType(head)
	if doc.SupportedImageType(sniffed) || sniffed == inlinePDFType {
		return ServeType{ContentType: sniffed, Inline: true}
	}
	return ServeType{ContentType: DownloadContentType, Inline: false}
}

// OpenForServing opens an attachment's object and decides how it may be
// served. The object key comes from the row, never from the caller.
//
// The returned reader still yields the WHOLE object: the leading bytes the
// sniff consumed are put back in front of it, the same way UploadPageImage
// restores its head before streaming to storage. The caller closes it.
func (s *Service) OpenForServing(ctx context.Context, att Attachment) (io.ReadCloser, ServeType, error) {
	rc, err := s.Open(ctx, att)
	if err != nil {
		return nil, ServeType{}, err
	}

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = rc.Close()
		return nil, ServeType{}, fmt.Errorf("reading attachment head: %w", err)
	}
	head = head[:n]

	return &restoredReadCloser{
		Reader: io.MultiReader(bytes.NewReader(head), rc),
		closer: rc,
	}, ServeTypeFor(head), nil
}

// restoredReadCloser reads the sniffed head followed by the rest of the
// object, and closes the underlying object stream.
type restoredReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *restoredReadCloser) Close() error {
	if err := r.closer.Close(); err != nil {
		return fmt.Errorf("closing attachment object: %w", err)
	}
	return nil
}
