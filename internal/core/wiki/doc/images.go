package doc

import (
	"encoding/json"
	"net/http"
	"strings"
)

// supportedImageTypes is the allow-list for an image node's attachment.
//
// The same four types the avatar surface allows (internal/core/people/avatar.go),
// and for the same reason: they are sniffed from the bytes, never taken from the
// client's Content-Type header.
//
// image/svg+xml is deliberately absent. An SVG is a document that can carry
// script, and attachments are streamed same-origin with `Content-Disposition:
// inline`, so serving one as an image would make any page's image upload a
// stored-XSS vector. http.DetectContentType does not report SVG at all, which is
// how that exclusion is enforced rather than merely intended.
var supportedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// SupportedImageType reports whether a sniffed content type may back an image
// node.
func SupportedImageType(contentType string) bool {
	return supportedImageTypes[normaliseContentType(contentType)]
}

// SupportedImageTypes returns the allow-list, for an error message that tells the
// user what would work.
func SupportedImageTypes() []string {
	return []string{"image/png", "image/jpeg", "image/webp", "image/gif"}
}

// SniffImageType detects a content type from the leading bytes and strips any
// parameters. The client's declared type is never consulted.
func SniffImageType(data []byte) string {
	return normaliseContentType(http.DetectContentType(data))
}

func normaliseContentType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// ImageAttachmentIDs returns the attachment ids every image node in the document
// refers to, in document order and without duplicates.
//
// An image that carries a plain `src` instead — a converted legacy markdown
// image pointing at an external URL — has no attachment to check and is not
// returned.
func ImageAttachmentIDs(document json.RawMessage) ([]string, error) {
	if err := Validate(document); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []string
	if err := collectImageIDs(document, seen, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func collectImageIDs(raw json.RawMessage, seen map[string]bool, out *[]string, depth int) error {
	if depth > maxDepth {
		return ErrTooDeep
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return err
	}
	nodeType, err := obj.typeOf()
	if err != nil {
		return err
	}
	if nodeType == "image" {
		if id, ok := obj.attrString("attachment_id"); ok && id != "" && !seen[id] {
			seen[id] = true
			*out = append(*out, id)
		}
	}
	// Preserved content is not descended into: its bytes are opaque by design,
	// and an attachment reference inside one is not something this document model
	// is claiming to render.
	if nodeType == NodeUnknownContent || nodeType == NodeUnknownInline {
		return nil
	}
	for _, member := range [...]string{"content", "marks"} {
		items, _, err := obj.array(member)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := collectImageIDs(item, seen, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// PreservedName returns the original node or mark type of a captured original, so
// a message about content that would be lost can name it.
func PreservedName(original json.RawMessage) string {
	obj, err := decodeObject(original)
	if err != nil {
		return ""
	}
	nodeType, err := obj.typeOf()
	if err != nil {
		return ""
	}
	return nodeType
}
