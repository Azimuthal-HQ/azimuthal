// Package doc is the Codex document model: the ProseMirror-native document
// that migration 036 stores in pages.doc, and the machinery ADR-0012 requires
// around it.
//
// # The problem this package exists to solve
//
// ProseMirror — and therefore TipTap — silently drops content that does not
// match its schema. Not at import, where a defect would be caught, but later,
// one page at a time, when somebody opens an imported page to fix a typo and
// unknowingly destroys forty macros on save. ADR-0012 calls that out as the
// requirement most likely to be missed and the most damaging when it is.
//
// So the editor is never handed a document it might not survive. Every node
// and mark type outside [SchemaNodes] / [SchemaMarks] is rewritten, before the
// document leaves the server, into a placeholder whose type the editor DOES
// know — and the verbatim original is put back, server-side, on the way in.
//
// # Where the guarantee actually lives
//
// Not in the editor, and not in the wire format. In [Shield] and [Restore],
// and in one property of how they are used:
//
//	The bytes written back are the bytes that were read. They never pass
//	through the client.
//
// [Shield] returns the originals keyed by placeholder id. [Restore] splices
// those exact byte slices back in. A placeholder's `raw` attribute travels to
// the browser so the editor can label and size the block, but it is display
// only — [Restore] resolves from the caller's captured map and ignores whatever
// the client sent back. A client that mangles, truncates or re-encodes `raw`
// therefore cannot corrupt the stored document; the worst it can do is lose the
// placeholder entirely, and [Restored.Dropped] reports that so the caller can
// refuse the write.
//
// Because ids are assigned in document order over a document the caller
// re-reads at the base version, they are stable between the read and the write
// without any server-side session state.
//
// # The three preservation primitives
//
// ADR-0012 names one, `unknownContent`. ProseMirror content can occupy three
// positions, though, and a node type cannot be both block and inline, so the
// same guarantee needs three carriers:
//
//	unknownContent  a block-level node        (an unknown block, macro, table…)
//	unknownInline   an inline node            (raw inline HTML, an unknown atom)
//	unknownMark     a mark on inline content  (an unknown inline formatting)
//
// This is an interpretation of ADR-0012 rather than a quotation of it: the ADR
// names the node and is silent on marks and inline content. It is recorded in
// docs/design/spec-repo-reconciliation.md for a maintainer to ratify. The
// alternative reading — preserve unknown blocks, drop unknown marks and inline
// HTML — contradicts the ADR's own Decision heading ("Zero silent data loss"),
// and inline HTML is not hypothetical here: the markdown editor Codex shipped
// with serialises text colour and highlight as inline <span> HTML, so real
// pages in this repository already contain it.
package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// maxDepth bounds document recursion. A document nested deeper than this is
// rejected rather than walked: the walkers are recursive, and a hostile or
// corrupt document should not be able to exhaust the stack. Real documents are
// nowhere near it — a table cell inside a layout column inside an expand is
// about six levels.
const maxDepth = 200

// ErrTooDeep is returned when a document nests past [maxDepth].
var ErrTooDeep = errors.New("document nests too deeply")

// Errors returned by this package.
var (
	// ErrNotAnObject is returned when a document, node or mark is not a JSON
	// object where one is required.
	ErrNotAnObject = errors.New("document node must be a JSON object")

	// ErrNoType is returned when a node or mark object has no "type" string.
	ErrNoType = errors.New("document node must carry a type")

	// ErrNotADoc is returned when the top-level node is not type "doc".
	ErrNotADoc = errors.New(`document root must be type "doc"`)

	// ErrPlaceholderNoID is returned when a preservation placeholder reaches
	// [Restore] without the id that identifies which original it stands for.
	// A caller cannot resolve it and must not guess, because guessing is how
	// the wrong bytes get written into a document.
	ErrPlaceholderNoID = errors.New("preservation placeholder is missing its id")
)

// Attribute names on the preservation placeholders. They are prefixed so a
// placeholder's attributes can never collide with an attribute of the original
// node they describe.
const (
	// AttrID identifies which captured original a placeholder stands for.
	AttrID = "az_id"

	// AttrName is the original node or mark type, shown to the reader.
	AttrName = "az_name"

	// AttrSource records where the content came from — "document" for content
	// already in a stored document, or an importer's own label. ADR-0012
	// section 1 requires the source system be stored.
	AttrSource = "az_source"

	// AttrRaw carries the verbatim original as a JSON *string*, so that a
	// JSON.parse/JSON.stringify round trip in the browser cannot alter it the
	// way it would alter object key order or number literals. It is display
	// only; see the package comment.
	AttrRaw = "az_raw"

	// AttrTextFallback is the plain-text rendering of preserved content, used
	// for the search projection and as the label when a reader has no better
	// summary. ADR-0012: indexing an unknown body as plain text is acceptable,
	// pretending it renders is not.
	AttrTextFallback = "az_text"
)

// SourceDocument is the [AttrSource] value for content that was already inside
// a stored Codex document — as opposed to content an importer brought in from
// somewhere else. This package only ever produces this value; the constant is
// exported because the importer ADR-0012 anticipates will produce others, and
// they must be distinguishable.
const SourceDocument = "document"

// Empty is the canonical empty document: what a page with no content holds.
func Empty() json.RawMessage {
	return json.RawMessage(`{"type":"doc","content":[]}`)
}

// object is a JSON object with every member preserved verbatim. Walking a
// document through this type keeps members this package has no opinion about —
// including ones a later schema version adds — instead of narrowing the node to
// the fields Go happens to know.
type object map[string]json.RawMessage

// decodeObject decodes a JSON object, keeping every member's raw bytes.
func decodeObject(raw json.RawMessage) (object, error) {
	var obj object
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotAnObject, err)
	}
	if obj == nil {
		return nil, ErrNotAnObject
	}
	return obj, nil
}

// typeOf reads the node or mark type.
func (o object) typeOf() (string, error) {
	raw, ok := o["type"]
	if !ok {
		return "", ErrNoType
	}
	var t string
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoType, err)
	}
	if t == "" {
		return "", ErrNoType
	}
	return t, nil
}

// array decodes a member that should be a JSON array of objects, keeping each
// element's raw bytes. A missing member yields (nil, false, nil); a member that
// is present but not an array is an error, because silently treating it as
// absent would drop whatever it held.
func (o object) array(member string) ([]json.RawMessage, bool, error) {
	raw, ok := o[member]
	if !ok {
		return nil, false, nil
	}
	// An explicit null is how some serialisers write "no content"; treat it as
	// absent rather than as a malformed array.
	if string(raw) == "null" {
		return nil, false, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false, fmt.Errorf("decoding %q of a document node: %w", member, err)
	}
	return items, true, nil
}

// attrString reads a string attribute from a node's attrs, if present.
func (o object) attrString(name string) (string, bool) {
	attrsRaw, ok := o["attrs"]
	if !ok {
		return "", false
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(attrsRaw, &attrs); err != nil {
		return "", false
	}
	raw, ok := attrs[name]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// memberOrder is the canonical member order for a re-encoded node. Any member
// not listed follows, sorted, so the output is deterministic whatever a future
// schema version adds.
var memberOrder = [...]string{"type", "attrs", "content", "marks", "text"}

// encode re-serialises the object, splicing every member's value bytes in
// verbatim.
//
// encoding/json cannot be used for this, and the reason is the whole point of
// the package: json.Marshal runs compact() over a json.RawMessage, which strips
// insignificant whitespace inside it and — with the default HTML escaping —
// rewrites `<`, `>` and `&` as <, > and &. Both are
// value-preserving and neither is byte-preserving, and ADR-0012 asks for
// byte-identical. A preserved Confluence macro body is full of angle brackets.
//
// Only the object's own framing and member order are this function's; every
// value inside it passes through untouched.
func (o object) encode() (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	write := func(key string) error {
		value, ok := o[key]
		if !ok {
			return nil
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		quoted, err := marshalPlain(key)
		if err != nil {
			return fmt.Errorf("encoding document node member %q: %w", key, err)
		}
		buf.Write(quoted)
		buf.WriteByte(':')
		buf.Write(value)
		return nil
	}

	known := make(map[string]bool, len(memberOrder))
	for _, key := range memberOrder {
		known[key] = true
		if err := write(key); err != nil {
			return nil, err
		}
	}
	rest := make([]string, 0, len(o))
	for key := range o {
		if !known[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		if err := write(key); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.Bytes()), nil
}

// marshalPlain is encoding/json without the HTML escaping.
//
// The default encoder rewrites `<`, `>` and `&` as their \u escapes, which is
// value-preserving and byte-mangling. It costs nothing correctness-wise — the
// string decodes back the same — but a preserved Confluence macro or a block of
// legacy HTML stored that way is unreadable and ungreppable in the database,
// which is exactly when somebody is reading it: while diagnosing whether a page
// lost content. Documents in this package are written as they read.
func marshalPlain(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encoding document value: %w", err)
	}
	// Encode appends a newline; a JSON value in a document must not carry one.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// walkMember applies transform to every element of the named array member,
// rewriting the member only when something actually changed. It reports whether
// it did.
//
// Both the capture walk and the restore walk are the same shape — visit marks,
// visit content, re-encode only if a child moved — and the "only if" is the part
// that matters: an untouched subtree must come back as its own bytes, not as a
// re-encoding of them.
func walkMember(obj object, member string, transform func(json.RawMessage) (json.RawMessage, error)) (bool, error) {
	items, present, err := obj.array(member)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}

	out := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, item := range items {
		transformed, err := transform(item)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(transformed, item) {
			changed = true
		}
		out = append(out, transformed)
	}
	if changed {
		obj[member] = encodeArray(out)
	}
	return changed, nil
}

// encodeArray writes a JSON array, splicing each element's bytes verbatim. Same
// reasoning as [object.encode].
func encodeArray(items []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return json.RawMessage(buf.Bytes())
}

// Validate checks that raw is a syntactically well-formed document: a JSON
// object of type "doc" whose nodes and marks all carry a type. It deliberately
// does NOT check that the types are known — an unknown type is the case this
// package exists to handle, not an error.
func Validate(raw json.RawMessage) error {
	root, err := decodeObject(raw)
	if err != nil {
		return err
	}
	t, err := root.typeOf()
	if err != nil {
		return err
	}
	if t != "doc" {
		return fmt.Errorf("%w: got %q", ErrNotADoc, t)
	}
	return walkTypes(raw)
}

// walkTypes recurses through a node, asserting every node and mark carries a
// type.
func walkTypes(raw json.RawMessage) error {
	obj, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if _, err := obj.typeOf(); err != nil {
		return err
	}
	for _, member := range [...]string{"content", "marks"} {
		items, _, err := obj.array(member)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := walkTypes(item); err != nil {
				return err
			}
		}
	}
	return nil
}
