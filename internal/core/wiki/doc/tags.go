package doc

import (
	"encoding/json"
	"strings"
)

// maxInlineTagsPerDocument bounds how many distinct inline tags one publish can
// mint. A tag row is created by use, so a document is an untrusted path into an
// org-scoped table; without a ceiling, one pasted page could create thousands of
// them. Real pages are nowhere near it — a page with fifty distinct tags is not
// tagging, it is a word list.
//
// It deliberately equals `tags.MaxTagsPerPage`, and is written as a literal
// rather than as that constant because this package knows the document model and
// nothing about the tag table. The agreement is what closes a silent drop: were
// this ceiling the higher of the two, a body carrying more labels than a page
// may hold would have the excess discarded downstream with nothing reporting it.
const maxInlineTagsPerDocument = 50

// InlineTagLabels returns the labels of every inline tag token in the document,
// in document order and without duplicates, capped at
// [maxInlineTagsPerDocument].
//
// The label is the text the author typed. It is deliberately NOT slugified
// here: this package knows the document model and nothing about the tag table,
// and the slug convention belongs with the table that has the CHECK constraint
// on it. The caller slugifies.
//
// Like [ImageAttachmentIDs], this does not descend into preserved content. A
// `#tag` inside a captured Confluence macro is bytes this document model has
// explicitly declined to interpret, and minting an org-scoped tag from an
// opaque body would be interpreting it.
func InlineTagLabels(document json.RawMessage) ([]string, error) {
	if err := Validate(document); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []string
	if err := collectTagLabels(document, seen, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func collectTagLabels(raw json.RawMessage, seen map[string]bool, out *[]string, depth int) error {
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

	if nodeType == NodeInlineTag {
		recordTagLabel(obj, seen, out)
	}
	if nodeType == NodeUnknownContent || nodeType == NodeUnknownInline {
		return nil
	}
	return descendForTagLabels(obj, seen, out, depth)
}

func recordTagLabel(obj object, seen map[string]bool, out *[]string) {
	if len(*out) >= maxInlineTagsPerDocument {
		return
	}
	label, ok := obj.attrString(AttrTagLabel)
	if !ok {
		return
	}
	label = strings.TrimSpace(label)
	// Case-folded for the duplicate check only. Which spelling survives is the
	// first one in document order, and the tag table then decides for the org —
	// two pages disagreeing about capitalisation must not create two tags.
	key := strings.ToLower(label)
	if label == "" || seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, label)
}

func descendForTagLabels(obj object, seen map[string]bool, out *[]string, depth int) error {
	for _, member := range [...]string{"content", "marks"} {
		items, _, err := obj.array(member)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := collectTagLabels(item, seen, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
