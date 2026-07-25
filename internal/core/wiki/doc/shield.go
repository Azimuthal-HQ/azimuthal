package doc

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Shielded is a document made safe to hand to ProseMirror, together with the
// originals that were taken out of it.
type Shielded struct {
	// Document has every unknown node and mark replaced by a placeholder whose
	// type the editor's schema defines, so ProseMirror parses it without
	// dropping anything.
	Document json.RawMessage

	// Captured maps placeholder id to the verbatim bytes of the original it
	// stands for. This is the authoritative copy — [Restore] reads it, never
	// the client's echo of it.
	Captured map[string]json.RawMessage

	// Order lists the ids in document order, which is the order [Shield]
	// assigns them. Two shieldings of the same document produce the same ids,
	// which is what lets a read and a later write agree on them with no
	// server-side session state in between.
	Order []string
}

// Any reports whether anything needed preserving.
func (s Shielded) Any() bool { return len(s.Captured) > 0 }

// Shield rewrites every node type outside [SchemaNodes] and every mark type
// outside [SchemaMarks] into a preservation placeholder, and returns the
// verbatim originals keyed by placeholder id.
//
// A document that needs nothing preserved is returned byte-identically — the
// walker splices unchanged subtrees rather than re-encoding them.
func Shield(document json.RawMessage) (Shielded, error) {
	if err := Validate(document); err != nil {
		return Shielded{}, err
	}
	s := &shielder{captured: make(map[string]json.RawMessage)}
	out, err := s.node(document, false, 0)
	if err != nil {
		return Shielded{}, err
	}
	return Shielded{Document: out, Captured: s.captured, Order: s.order}, nil
}

type shielder struct {
	n        int
	captured map[string]json.RawMessage
	order    []string
}

// node shields one node. inline says whether this node sits in an inline
// content position, which is how capture picks between the block and inline
// placeholder for a type it has never seen.
func (s *shielder) node(raw json.RawMessage, inline bool, depth int) (json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	nodeType, err := obj.typeOf()
	if err != nil {
		return nil, err
	}

	if !KnownNode(nodeType) {
		return s.capture(raw, nodeType, inline), nil
	}

	marksChanged, err := walkMember(obj, "marks", s.mark)
	if err != nil {
		return nil, err
	}

	childrenInline, err := inlinePositionOf(obj, nodeType)
	if err != nil {
		return nil, err
	}
	contentChanged, err := walkMember(obj, "content", func(child json.RawMessage) (json.RawMessage, error) {
		return s.node(child, childrenInline, depth+1)
	})
	if err != nil {
		return nil, err
	}

	// Nothing below this node changed, so hand back the original bytes rather
	// than a re-encoding of them. That is what makes Shield the identity
	// function on a document with no unknown content.
	if !marksChanged && !contentChanged {
		return raw, nil
	}
	return obj.encode()
}

// inlinePositionOf reports whether this node's children sit in an inline content
// position.
func inlinePositionOf(obj object, nodeType string) (bool, error) {
	content, present, err := obj.array("content")
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	return childrenAreInline(nodeType, content)
}

// mark shields one mark.
func (s *shielder) mark(raw json.RawMessage) (json.RawMessage, error) {
	obj, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	markType, err := obj.typeOf()
	if err != nil {
		return nil, err
	}
	if KnownMark(markType) {
		return raw, nil
	}
	return s.capture(raw, markType, false, MarkUnknownMark), nil
}

// capture stores the original verbatim and returns the placeholder that stands
// in for it. The optional final argument overrides the placeholder type, which
// the mark path uses; nodes pick between the block and inline placeholder from
// their position.
func (s *shielder) capture(raw json.RawMessage, originalType string, inline bool, override ...string) json.RawMessage {
	s.n++
	id := fmt.Sprintf("u%d", s.n)

	// Copy rather than retain: the caller's slice can alias a decoder buffer,
	// and this copy is the authoritative original for the rest of the request.
	stored := make(json.RawMessage, len(raw))
	copy(stored, raw)
	s.captured[id] = stored
	s.order = append(s.order, id)

	placeholderType := NodeUnknownContent
	if inline {
		placeholderType = NodeUnknownInline
	}
	if len(override) > 0 {
		placeholderType = override[0]
	}

	return placeholder(placeholderType, id, originalType, stored)
}

// childrenAreInline decides whether a node's children occupy an inline content
// position. The declared content model answers it for every known parent; the
// sibling scan is a backstop for a parent whose model this list has wrong or
// has not caught up with, since an inline sibling proves the position.
func childrenAreInline(parentType string, children []json.RawMessage) (bool, error) {
	if hasInlineContent(parentType) {
		return true, nil
	}
	for _, child := range children {
		obj, err := decodeObject(child)
		if err != nil {
			return false, err
		}
		childType, err := obj.typeOf()
		if err != nil {
			return false, err
		}
		if isInlineNode(childType) {
			return true, nil
		}
	}
	return false, nil
}

// placeholderAttrs is a struct rather than a map so the attribute order is
// fixed, which keeps a shielded document stable across runs.
type placeholderAttrs struct {
	ID     string `json:"az_id"`
	Name   string `json:"az_name"`
	Source string `json:"az_source"`
	Raw    string `json:"az_raw"`
	Text   string `json:"az_text"`
}

type placeholderNode struct {
	Type  string           `json:"type"`
	Attrs placeholderAttrs `json:"attrs"`
}

// placeholder builds a preservation placeholder standing in for original.
//
// The original is carried as a JSON string in az_raw: a string survives the
// browser's JSON.parse and JSON.stringify unchanged, where object key order and
// number literals do not. It is still only a display copy — [Restore] resolves
// from the captured map, so a client that alters az_raw changes nothing about
// what gets stored.
func placeholder(placeholderType, id, name string, original json.RawMessage) json.RawMessage {
	out, err := marshalPlain(placeholderNode{
		Type: placeholderType,
		Attrs: placeholderAttrs{
			ID:     id,
			Name:   name,
			Source: SourceDocument,
			Raw:    string(original),
			Text:   PlainText(original),
		},
	})
	if err != nil {
		// Unreachable: every field is a string.
		panic(fmt.Sprintf("wiki/doc: encoding a preservation placeholder: %v", err))
	}
	return out
}

// Restored is the result of putting preserved originals back.
type Restored struct {
	// Document is the document as it will be stored: every placeholder the
	// caller could resolve replaced by the exact bytes that were captured.
	Document json.RawMessage

	// Dropped lists captured ids the incoming document no longer carries, in
	// the order they appeared in the base document. Every one is content that
	// was preserved and is now gone. A user may legitimately delete an inert
	// block, so this is not an error by itself — but it must be a decision the
	// caller can see and require to be acknowledged, never a silent outcome.
	Dropped []string

	// Unresolved lists placeholder ids present in the incoming document with no
	// captured original behind them. That means the document was shielded
	// against a different base than the one being restored against, or a client
	// invented a placeholder. Either way the correct answer is to refuse, not
	// to write a placeholder into storage as though it were content.
	Unresolved []string
}

// Restore is [Shield]'s inverse: it puts the captured originals back where
// their placeholders are, splicing the exact bytes that were captured.
//
// The originals come from base, never from the placeholders in document — a
// client cannot influence what gets written by editing an az_raw attribute. A
// placeholder duplicated by the author (copy-pasting an inert block is a
// reasonable thing to do) restores the same original at both positions.
func Restore(document json.RawMessage, base Shielded) (Restored, error) {
	if err := Validate(document); err != nil {
		return Restored{}, err
	}
	r := &restorer{captured: base.Captured, seen: make(map[string]bool)}
	out, err := r.node(document, 0)
	if err != nil {
		return Restored{}, err
	}

	dropped := make([]string, 0)
	for _, id := range base.Order {
		if !r.seen[id] {
			dropped = append(dropped, id)
		}
	}
	return Restored{Document: out, Dropped: dropped, Unresolved: r.unresolved}, nil
}

type restorer struct {
	captured   map[string]json.RawMessage
	seen       map[string]bool
	unresolved []string
}

func (r *restorer) node(raw json.RawMessage, depth int) (json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	nodeType, err := obj.typeOf()
	if err != nil {
		return nil, err
	}

	if nodeType == NodeUnknownContent || nodeType == NodeUnknownInline {
		return r.resolve(raw, obj)
	}

	marksChanged, err := walkMember(obj, "marks", r.mark)
	if err != nil {
		return nil, err
	}
	contentChanged, err := walkMember(obj, "content", func(child json.RawMessage) (json.RawMessage, error) {
		return r.node(child, depth+1)
	})
	if err != nil {
		return nil, err
	}

	if !marksChanged && !contentChanged {
		return raw, nil
	}
	return obj.encode()
}

func (r *restorer) mark(raw json.RawMessage) (json.RawMessage, error) {
	obj, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	markType, err := obj.typeOf()
	if err != nil {
		return nil, err
	}
	if markType != MarkUnknownMark {
		return raw, nil
	}
	return r.resolve(raw, obj)
}

// resolve swaps one placeholder for its captured original. An id it cannot
// resolve is recorded and the placeholder is left in place: dropping it here
// would destroy the only remaining description of the missing content, and the
// caller is going to refuse the write anyway.
func (r *restorer) resolve(raw json.RawMessage, obj object) (json.RawMessage, error) {
	id, ok := obj.attrString(AttrID)
	if !ok || id == "" {
		return nil, ErrPlaceholderNoID
	}
	original, ok := r.captured[id]
	if !ok {
		r.unresolved = append(r.unresolved, id)
		return raw, nil
	}
	r.seen[id] = true
	return original, nil
}

func bytesEqual(a, b json.RawMessage) bool { return bytes.Equal(a, b) }
