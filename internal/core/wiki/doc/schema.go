package doc

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

// schemaJSON is the shared node/mark vocabulary. It is a data file rather than
// a Go slice so the editor's TypeScript schema can be checked against the same
// bytes; see the comment inside it, and web/src/lib/codex/schema.test.ts.
//
//go:embed schema.json
var schemaJSON []byte

// The preservation placeholder types. Named constants because the capture path,
// the restore path, the projection and the editor all have to agree on them,
// and a typo in any one of them would look like ordinary unknown content.
const (
	// NodeUnknownContent is the block-level placeholder (ADR-0012 section 1).
	NodeUnknownContent = "unknownContent"

	// NodeUnknownInline is the inline placeholder.
	NodeUnknownInline = "unknownInline"

	// MarkUnknownMark is the mark placeholder.
	MarkUnknownMark = "unknownMark"
)

// The document vocabulary this phase added, named as constants for the same
// reason the preservation types are: the input rule, the projection, the
// publish-time tag aggregation and the editor all have to agree on them.
const (
	// NodeInlineTag is the inline `#tag` token. Its label is the text the
	// author typed; the slug it resolves to is derived server-side, so a tag
	// rename never has to rewrite a stored document.
	NodeInlineTag = "inlineTag"

	// MarkLink is the link mark, which carries all three of a link's possible
	// destinations — see [AttrLinkTargetTitle].
	MarkLink = "link"
)

// Attribute names the projection and the publish path read by name.
const (
	// AttrTagLabel is the text of an inline tag, as the author typed it.
	AttrTagLabel = "label"

	// AttrLinkHref is an external link's destination.
	AttrLinkHref = "href"

	// AttrLinkPageID is a resolved internal link's target page.
	AttrLinkPageID = "page_id"

	// AttrLinkTargetTitle is an UNRESOLVED wikilink's target: the title the
	// author wrote inside [[...]] when no page of that name was chosen.
	//
	// It is a third state, not a variant of the other two, and the distinction
	// is load-bearing. A link with a page_id resolves to a page; a link with an
	// href leaves Azimuthal; a link with only a target_title names a page that
	// does not exist yet, and clicking it offers to create one. Storing an
	// unresolved link as an href (say "#design-docs") would make it an external
	// link that goes nowhere, and storing it as a page_id is impossible —
	// there is no page.
	AttrLinkTargetTitle = "target_title"
)

// schema is the parsed manifest: type name -> group, plus the two position
// lists capture needs to choose between the block and inline placeholders, plus
// the attribute names the markdown projection reads.
type schema struct {
	Nodes              map[string]string `json:"nodes"`
	Marks              map[string]string `json:"marks"`
	InlineNodes        []string          `json:"inlineNodes"`
	InlineContentNodes []string          `json:"inlineContentNodes"`
	ProjectedAttrs     projectedAttrs    `json:"projectedAttrs"`
}

// projectedAttrs names the attributes [ToMarkdown] reads, per type. See the
// manifest's own comment for why an attribute rename fails differently from a
// type rename — nothing breaks, the content just stops being findable.
type projectedAttrs struct {
	Nodes map[string][]string `json:"nodes"`
	Marks map[string][]string `json:"marks"`
}

var parsedSchema = mustParseSchema()

func mustParseSchema() schema {
	var s schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		panic(fmt.Sprintf("wiki/doc: schema.json is not valid JSON: %v", err))
	}
	// The manifest is the boundary that decides what gets preserved. If it were
	// ever shipped without the placeholder types, capture would produce nodes
	// the next Shield treated as unknown again, wrapping them in an endless
	// nest. Fail at init rather than at runtime.
	for _, required := range []string{NodeUnknownContent, NodeUnknownInline} {
		if _, ok := s.Nodes[required]; !ok {
			panic(fmt.Sprintf("wiki/doc: schema.json omits the %q node, so preserved content would be re-captured on every save", required))
		}
	}
	if _, ok := s.Marks[MarkUnknownMark]; !ok {
		panic(fmt.Sprintf("wiki/doc: schema.json omits the %q mark", MarkUnknownMark))
	}
	// A projected attribute on a type that is not in the vocabulary describes a
	// projection that can never run. That is a manifest mistake rather than a
	// runtime state, so it fails at init like the missing placeholders above.
	for nodeType := range s.ProjectedAttrs.Nodes {
		if _, ok := s.Nodes[nodeType]; !ok {
			panic(fmt.Sprintf("wiki/doc: schema.json projects attributes of %q, which is not a node in the vocabulary", nodeType))
		}
	}
	for markType := range s.ProjectedAttrs.Marks {
		if _, ok := s.Marks[markType]; !ok {
			panic(fmt.Sprintf("wiki/doc: schema.json projects attributes of %q, which is not a mark in the vocabulary", markType))
		}
	}
	return s
}

// KnownNode reports whether the editor's schema defines this node type. A false
// answer means the node must be preserved rather than parsed.
func KnownNode(nodeType string) bool {
	_, ok := parsedSchema.Nodes[nodeType]
	return ok
}

// KnownMark reports whether the editor's schema defines this mark type.
func KnownMark(markType string) bool {
	_, ok := parsedSchema.Marks[markType]
	return ok
}

// SchemaNodes returns every node type the editor's schema defines, sorted.
func SchemaNodes() []string { return sortedKeys(parsedSchema.Nodes) }

// SchemaMarks returns every mark type the editor's schema defines, sorted.
func SchemaMarks() []string { return sortedKeys(parsedSchema.Marks) }

// SchemaJSON returns the raw manifest bytes, for the endpoint that lets the
// frontend assert it is registering the same vocabulary the server is
// preserving against.
func SchemaJSON() []byte { return schemaJSON }

// InlineNodes returns the node types that are themselves inline, sorted.
func InlineNodes() []string { return sortedCopy(parsedSchema.InlineNodes) }

// InlineContentNodes returns the node types whose children are inline, sorted.
func InlineContentNodes() []string { return sortedCopy(parsedSchema.InlineContentNodes) }

// ProjectedNodeAttrs returns the attribute names the markdown projection reads
// from each node type, sorted within each type.
func ProjectedNodeAttrs() map[string][]string { return copyAttrMap(parsedSchema.ProjectedAttrs.Nodes) }

// ProjectedMarkAttrs returns the attribute names the markdown projection reads
// from each mark type, sorted within each type.
func ProjectedMarkAttrs() map[string][]string { return copyAttrMap(parsedSchema.ProjectedAttrs.Marks) }

func copyAttrMap(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = sortedCopy(values)
	}
	return out
}

// isInlineNode reports whether a node type is inline content.
func isInlineNode(nodeType string) bool {
	for _, n := range parsedSchema.InlineNodes {
		if n == nodeType {
			return true
		}
	}
	return false
}

// hasInlineContent reports whether a node type's children are inline content.
func hasInlineContent(nodeType string) bool {
	for _, n := range parsedSchema.InlineContentNodes {
		if n == nodeType {
			return true
		}
	}
	return false
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
