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

// schema is the parsed manifest: type name -> group, plus the two position
// lists capture needs to choose between the block and inline placeholders.
type schema struct {
	Nodes              map[string]string `json:"nodes"`
	Marks              map[string]string `json:"marks"`
	InlineNodes        []string          `json:"inlineNodes"`
	InlineContentNodes []string          `json:"inlineContentNodes"`
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
