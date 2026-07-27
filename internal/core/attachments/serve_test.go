package attachments_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// ServeTypeFor is the whole serving policy, so it is tested directly on bytes
// as well as through the HTTP routes
// (internal/core/api/attachment_serve_integration_test.go). Nothing here
// touches persistence.

func TestServeTypeFor_InlineAllowList(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}, "image/jpeg"},
		{"gif87", []byte("GIF87a\x01\x00\x01\x00"), "image/gif"},
		{"gif89", []byte("GIF89a\x01\x00\x01\x00"), "image/gif"},
		{"webp", append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 8)...), "image/webp"},
		{"pdf", []byte("%PDF-1.7\n1 0 obj\n"), "application/pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attachments.ServeTypeFor(tc.head)
			require.True(t, got.Inline, "%s must render inline", tc.name)
			require.Equal(t, tc.want, got.ContentType, "the SNIFFED type is served")
		})
	}
}

// TestServeTypeFor_EverythingElseDownloads is the negative half. Each case is
// something a browser would execute, or something ambiguous enough that it
// might. If the allow-list check were deleted these would all still be
// reported inline with a live type, so every line here can fail.
func TestServeTypeFor_EverythingElseDownloads(t *testing.T) {
	cases := []struct {
		name string
		head []byte
	}{
		{"html document", []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")},
		{"html fragment", []byte("<HTML><BODY>hi</BODY></HTML>")},
		{"script tag first", []byte("<SCRIPT>alert(1)</SCRIPT>")},
		{"html comment", []byte("<!-- --><html></html>")},
		{"svg bare", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"svg xml-declared", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)},
		{"xml", []byte(`<?xml version="1.0"?><root/>`)},
		{"plain text", []byte("just some notes")},
		{"empty", []byte{}},
		{"nil", nil},
		{"pdf not at offset zero", []byte("\n%PDF-1.7")},
		{"zip/office", []byte("PK\x03\x04\x14\x00\x00\x00")},
		{"wasm", []byte("\x00asm\x01\x00\x00\x00")},
		{"bmp", []byte("BM\x00\x00\x00\x00")},
		{"ico", []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}},
		{"mp4 video", []byte("\x00\x00\x00\x18ftypmp42")},
		{"json-ish", []byte(`{"note":"<script>alert(1)</script>"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attachments.ServeTypeFor(tc.head)
			require.False(t, got.Inline, "%s must not render inline", tc.name)
			require.Equal(t, attachments.DownloadContentType, got.ContentType,
				"a non-allow-listed object is served opaquely, never as its sniffed type")
		})
	}
}

// TestServeTypeFor_SVGNeverEscapesAsAnImage pins the specific trap: an SVG is
// scriptable, and it is the one "image" a naive allow-list would let through.
func TestServeTypeFor_SVGNeverEscapesAsAnImage(t *testing.T) {
	for _, head := range [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<?xml version="1.0" encoding="UTF-8"?><svg onload="alert(1)"/>`),
		[]byte("\xEF\xBB\xBF<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), // BOM-prefixed
	} {
		got := attachments.ServeTypeFor(head)
		require.False(t, got.Inline)
		require.NotContains(t, got.ContentType, "svg")
		require.NotContains(t, got.ContentType, "xml")
	}
}

// TestOpenForServing_RestoresSniffedBytes: the sniff must not eat the front of
// the object. Sizes straddle the 512-byte sniff window on purpose — a peek
// that forgot to put its head back would truncate the large cases and would
// pass the small ones, so a single size could not catch it.
func TestOpenForServing_RestoresSniffedBytes(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

	cases := []struct {
		name       string
		content    []byte
		wantInline bool
		wantType   string
	}{
		{"empty", []byte{}, false, attachments.DownloadContentType},
		{"under the window", append(png, bytes.Repeat([]byte{0x01}, 100)...), true, "image/png"},
		{"exactly the window", append(png, bytes.Repeat([]byte{0x02}, 512-len(png))...), true, "image/png"},
		{"one past the window", append(png, bytes.Repeat([]byte{0x03}, 512-len(png)+1)...), true, "image/png"},
		{"far past the window", append(png, bytes.Repeat([]byte{0x04}, 100_000)...), true, "image/png"},
		{"large non-image", append([]byte("<html>"), bytes.Repeat([]byte{0x05}, 5000)...), false, attachments.DownloadContentType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			blobs := storage.NewMemoryStore()
			svc := attachments.NewService(&serveMemStore{}, blobs)

			att, err := svc.Upload(ctx, attachments.UploadInput{
				OrgID:      uuid.New(),
				EntityType: "page",
				EntityID:   uuid.New(),
				Filename:   "object.bin",
				// Declared honestly or not is irrelevant here; serving ignores it.
				ContentType: "text/html",
				CreatedBy:   uuid.New(),
				Content:     bytes.NewReader(tc.content),
				Size:        int64(len(tc.content)),
			})
			require.NoError(t, err)

			rc, serveType, err := svc.OpenForServing(ctx, att)
			require.NoError(t, err)
			defer func() { require.NoError(t, rc.Close()) }()

			require.Equal(t, tc.wantInline, serveType.Inline)
			require.Equal(t, tc.wantType, serveType.ContentType)

			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.Len(t, got, len(tc.content), "every byte is returned, sniffed head included")
			require.Equal(t, tc.content, got)
		})
	}
}

// serveMemStore is an in-memory Store for the serve tests. It records rows
// only; the bytes live in the real storage.MemoryStore alongside it. No
// database is involved because no database behaviour is under test here —
// ServeTypeFor never reads a row.
type serveMemStore struct {
	rows []attachments.Attachment
}

func (m *serveMemStore) CreateAttachment(_ context.Context, a attachments.Attachment) (attachments.Attachment, error) {
	m.rows = append(m.rows, a)
	return a, nil
}

func (m *serveMemStore) GetAttachment(_ context.Context, id uuid.UUID) (attachments.Attachment, error) {
	for _, a := range m.rows {
		if a.ID == id {
			return a, nil
		}
	}
	return attachments.Attachment{}, attachments.ErrNotFound
}

func (m *serveMemStore) ListAttachmentsByEntity(_ context.Context, entityType string, entityID uuid.UUID) ([]attachments.Attachment, error) {
	var out []attachments.Attachment
	for _, a := range m.rows {
		if a.EntityType == entityType && a.EntityID == entityID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *serveMemStore) SoftDeleteAttachment(_ context.Context, id uuid.UUID) error {
	for i, a := range m.rows {
		if a.ID == id {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return nil
		}
	}
	return attachments.ErrNotFound
}
