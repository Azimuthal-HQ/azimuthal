package doc_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// storedWithUnknownMacro is a document as it would sit in pages.doc after an
// import: two paragraphs the editor understands, wrapping a macro it has never
// heard of.
//
// It is deliberately awkward. The attributes are in non-alphabetical order, one
// number is written as an exponent, the body carries angle brackets and an
// ampersand, and the macro has both nested content and a mark. Every one of
// those is something a JSON layer somewhere in the pipeline would like to
// normalise, and normalising any of them is the data loss ADR-0012 forbids.
const storedWithUnknownMacro = `{"type":"doc","content":[` +
	`{"type":"paragraph","content":[{"type":"text","text":"Before the diagram."}]},` +
	`{"type":"gliffyDiagram","attrs":{"zzz":"last","macroId":"a1b2","scale":1e2,"nested":{"b":2,"a":1}},` +
	`"content":[{"type":"gliffyLayer","attrs":{"raw":"<shape a=\"1\" b='2'>x &amp; y</shape>"}}],` +
	`"marks":[{"type":"confluenceAnchor","attrs":{"name":"fig-1"}}]},` +
	`{"type":"paragraph","content":[{"type":"text","text":"After the diagram."}]}` +
	`]}`

// unknownMacroSubtree is the exact byte sequence the macro occupies inside
// storedWithUnknownMacro. The round-trip assertion compares against this, not
// against a re-serialisation of it, because a re-serialisation is precisely the
// thing under test.
const unknownMacroSubtree = `{"type":"gliffyDiagram","attrs":{"zzz":"last","macroId":"a1b2","scale":1e2,"nested":{"b":2,"a":1}},` +
	`"content":[{"type":"gliffyLayer","attrs":{"raw":"<shape a=\"1\" b='2'>x &amp; y</shape>"}}],` +
	`"marks":[{"type":"confluenceAnchor","attrs":{"name":"fig-1"}}]}`

// TestShield_UnknownNodeSurvivesAnEditByteIdentically is the phase's binding
// test (ADR-0012 point 3): "Editing and saving a page containing unknown nodes
// leaves those nodes byte-identical."
//
// It walks the whole pipeline the way a save does — shield on the way out, an
// edit that touches the paragraphs either side of the unknown macro, restore on
// the way in — and then asserts on bytes, not on a decoded value. A comparison
// of decoded values would pass while the stored document had silently had its
// keys reordered, its 1e2 rewritten to 100 and its angle brackets escaped.
//
// Verified by deliberate breakage in both directions while writing it:
//   - making Shield return its input unchanged (no capture) makes the shielded
//     document still contain "gliffyDiagram", so the "editor never sees an
//     unknown type" assertion fails;
//   - making Restore return its input unchanged makes the restored document
//     still contain the placeholder, so the byte comparison fails with the
//     placeholder in place of the macro.
//
// Both were restored afterwards.
func TestShield_UnknownNodeSurvivesAnEditByteIdentically(t *testing.T) {
	t.Parallel()

	// ── Read: the server shields the stored document before the editor sees it.
	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	// One capture, not two: capturing a node captures its whole subtree, so the
	// unknown mark ON the unknown macro is preserved inside the macro's bytes
	// rather than separately. An unknown mark is only its own capture when it
	// sits on content the schema does understand — covered below.
	require.Len(t, shielded.Captured, 1)
	require.Equal(t, []string{"u1"}, shielded.Order)

	// The editor must never be handed a type its schema does not define — that
	// is the whole mechanism, because a type ProseMirror does not know is
	// dropped there, before this package can do anything about it. Asserted
	// structurally rather than by searching the text: the original type name is
	// deliberately still present, in the placeholder's az_name (ADR-0012 point 1
	// requires storing it) and inside its display copy of the original.
	requireEveryTypeIsInTheSchema(t, shielded.Document)
	require.Contains(t, string(shielded.Document), `"unknownContent"`)

	// ── Edit: the author changes the paragraphs either side, leaves the inert
	// block alone, and the editor re-serialises the whole document as ProseMirror
	// always does — placeholders included, with their attribute order and number
	// formatting entirely at the browser's discretion.
	edited := simulateEditorRoundTrip(t, shielded.Document,
		"Before the diagram.", "Before the diagram, now edited.")

	// ── Write: the server puts the originals back.
	restored, err := doc.Restore(edited, shielded)
	require.NoError(t, err)
	require.Empty(t, restored.Dropped, "nothing was deleted, so nothing may be reported dropped")
	require.Empty(t, restored.Unresolved, "every placeholder came from this base")

	// The assertion this whole phase exists for.
	require.Contains(t, string(restored.Document), unknownMacroSubtree,
		"the preserved macro is not byte-identical after an edit-and-save cycle")

	// And the edit that prompted the save actually landed, so the test is not
	// passing by having done nothing.
	require.Contains(t, string(restored.Document), "Before the diagram, now edited.")
	require.Contains(t, string(restored.Document), "After the diagram.")
}

// TestShield_WithoutCapturingTheEditorWouldDestroyTheMacro is the negative half.
// It runs the same document through a filter that does what ProseMirror does to
// content outside its schema — drops it — and asserts the loss really happens.
//
// Without this, the test above proves only that a pipeline preserves something;
// it does not prove there was anything to lose. This is the "would this test
// still pass if the check were deleted?" question from CLAUDE.md section 2,
// answered in the test file rather than in a commit message.
func TestShield_WithoutCapturingTheEditorWouldDestroyTheMacro(t *testing.T) {
	t.Parallel()

	// Straight to the schema filter, with no shielding first.
	filtered := dropUnknownTypes(t, json.RawMessage(storedWithUnknownMacro))

	require.NotContains(t, string(filtered), "gliffyDiagram",
		"the schema filter did not drop the unknown node, so this test asserts nothing")
	require.NotContains(t, string(filtered), "shape a=",
		"the macro body survived a schema filter — then there is no data loss to prevent")
	require.Contains(t, string(filtered), "Before the diagram.",
		"the filter should drop only what the schema does not define")

	// The same document, shielded first, keeps the macro through the identical
	// filter — because the filter now recognises every type it sees.
	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)
	survived := dropUnknownTypes(t, shielded.Document)
	restored, err := doc.Restore(survived, shielded)
	require.NoError(t, err)
	require.Contains(t, string(restored.Document), unknownMacroSubtree)
}

// TestShield_IsIdentityWhenThereIsNothingToPreserve pins the property that makes
// the walker safe to run on every read: a document already inside the schema
// comes back as the same bytes, so shielding cannot itself be a source of drift.
func TestShield_IsIdentityWhenThereIsNothingToPreserve(t *testing.T) {
	t.Parallel()

	const clean = `{"type":"doc","content":[` +
		`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},` +
		`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"bold"}],"text":"bold"}]}` +
		`]}`

	shielded, err := doc.Shield(json.RawMessage(clean))
	require.NoError(t, err)
	require.Empty(t, shielded.Captured)
	require.Equal(t, clean, string(shielded.Document),
		"shielding a document that needs nothing preserved must not rewrite it")
}

// TestShield_AssignsTheSameIDsEveryTime is what lets a read and a later write
// agree on placeholder ids with no session state between them: publish
// re-shields the base document and expects the ids the editor is holding.
func TestShield_AssignsTheSameIDsEveryTime(t *testing.T) {
	t.Parallel()

	first, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)
	second, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	require.Equal(t, first.Order, second.Order)
	require.Equal(t, string(first.Document), string(second.Document))
	for id, raw := range first.Captured {
		require.Equal(t, string(raw), string(second.Captured[id]))
	}
}

// TestRestore_IgnoresAClientSuppliedRaw is the property that keeps the guarantee
// independent of the browser. The placeholder carries a copy of the original so
// the editor can label the block; if that copy were what got written back, every
// fidelity claim in this package would rest on the client's JSON serialiser.
func TestRestore_IgnoresAClientSuppliedRaw(t *testing.T) {
	t.Parallel()

	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	// Rewrite the placeholder's az_raw the way a buggy or hostile client would —
	// through the JSON, not through the string, so the document stays valid and
	// the tamper is indistinguishable from a legitimate save.
	tampered := rewriteFirstPlaceholderRaw(t, shielded.Document, `{"type":"tamperedByTheClient"}`)
	require.Contains(t, string(tampered), "tamperedByTheClient",
		"the tamper did not take, so this test would pass vacuously")

	restored, err := doc.Restore(tampered, shielded)
	require.NoError(t, err)

	require.Contains(t, string(restored.Document), unknownMacroSubtree,
		"the stored document must come from the captured original, not from the client's copy")
	require.NotContains(t, string(restored.Document), "tamperedByTheClient")
}

// rewriteFirstPlaceholderRaw replaces the az_raw attribute of the first
// preservation placeholder in the document.
func rewriteFirstPlaceholderRaw(t *testing.T, document json.RawMessage, raw string) json.RawMessage {
	t.Helper()

	var value any
	require.NoError(t, json.Unmarshal(document, &value))
	require.True(t, setPlaceholderRaw(value, raw), "no placeholder found to tamper with")
	out, err := json.Marshal(value)
	require.NoError(t, err)
	return out
}

func setPlaceholderRaw(value any, raw string) bool {
	switch v := value.(type) {
	case map[string]any:
		if attrs, ok := v["attrs"].(map[string]any); ok {
			if _, has := attrs["az_raw"]; has {
				attrs["az_raw"] = raw
				return true
			}
		}
		for _, item := range v {
			if setPlaceholderRaw(item, raw) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if setPlaceholderRaw(item, raw) {
				return true
			}
		}
	}
	return false
}

// TestRestore_ReportsPreservedContentTheDocumentNoLongerCarries covers the case
// that turns a fidelity bug into a loud failure instead of a silent one: the
// editor came back without a placeholder it was given.
func TestRestore_ReportsPreservedContentTheDocumentNoLongerCarries(t *testing.T) {
	t.Parallel()

	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	// Everything gone but the two paragraphs — which is exactly what a save
	// would send if the editor's schema had silently dropped the block.
	stripped := json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Before the diagram."}]},` +
		`{"type":"paragraph","content":[{"type":"text","text":"After the diagram."}]}` +
		`]}`)

	restored, err := doc.Restore(stripped, shielded)
	require.NoError(t, err)
	require.Equal(t, shielded.Order, restored.Dropped,
		"every captured id is missing, and all of them must be reported, in document order")
	require.Empty(t, restored.Unresolved)
}

// TestRestore_ReportsPlaceholdersItCannotResolve covers the other direction: a
// placeholder id with nothing behind it, which means the document was shielded
// against a different base than the one being restored against.
func TestRestore_ReportsPlaceholdersItCannotResolve(t *testing.T) {
	t.Parallel()

	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	invented := json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"unknownContent","attrs":{"az_id":"u99","az_name":"invented","az_source":"document","az_raw":"{}","az_text":""}}` +
		`]}`)

	restored, err := doc.Restore(invented, shielded)
	require.NoError(t, err)
	require.Equal(t, []string{"u99"}, restored.Unresolved)
	require.Contains(t, string(restored.Document), `"unknownContent"`,
		"an unresolvable placeholder is kept, not dropped — dropping it would destroy the last description of the missing content")
}

// TestRestore_RejectsAPlaceholderWithNoID: the one case where guessing would be
// worse than failing. An id-less placeholder cannot be matched to an original,
// and picking one would write the wrong bytes into somebody's page.
func TestRestore_RejectsAPlaceholderWithNoID(t *testing.T) {
	t.Parallel()

	shielded, err := doc.Shield(json.RawMessage(storedWithUnknownMacro))
	require.NoError(t, err)

	idless := json.RawMessage(`{"type":"doc","content":[` +
		`{"type":"unknownContent","attrs":{"az_name":"invented"}}` +
		`]}`)

	_, err = doc.Restore(idless, shielded)
	require.ErrorIs(t, err, doc.ErrPlaceholderNoID)
}

// TestShield_PreservesUnknownMarksAndInlineContent covers the two positions
// ADR-0012 does not name but ProseMirror drops just as silently. The inline case
// is the one real pages already contain: Codex's markdown editor writes text
// colour as an inline <span>.
func TestShield_PreservesUnknownMarksAndInlineContent(t *testing.T) {
	t.Parallel()

	const stored = `{"type":"doc","content":[{"type":"paragraph","content":[` +
		`{"type":"text","marks":[{"type":"bold"},{"type":"textColor","attrs":{"hex":"#e53e3e"}}],"text":"red and bold"},` +
		`{"type":"confluenceEmoticon","attrs":{"name":"smile"}}` +
		`]}]}`

	shielded, err := doc.Shield(json.RawMessage(stored))
	require.NoError(t, err)
	require.Len(t, shielded.Captured, 2)

	// The mark became a mark placeholder and the inline node an inline one —
	// getting these the wrong way round would put a block node inside a
	// paragraph, which ProseMirror rejects.
	require.Contains(t, string(shielded.Document), `"`+"unknownMark"+`"`)
	require.Contains(t, string(shielded.Document), `"`+"unknownInline"+`"`)
	require.NotContains(t, string(shielded.Document), "unknownContent",
		"inline content must not be preserved with the block placeholder")
	require.Contains(t, string(shielded.Document), `{"type":"bold"}`,
		"a known mark alongside an unknown one must be left alone")

	restored, err := doc.Restore(shielded.Document, shielded)
	require.NoError(t, err)
	require.Equal(t, stored, string(restored.Document),
		"a document shielded and immediately restored must come back unchanged")
}

// TestRestore_DuplicatedPlaceholderRestoresTwice: copy-pasting an inert block is
// a reasonable thing for an author to do, and both copies must carry the
// content. Reporting the second as unresolvable would refuse a legitimate edit.
func TestRestore_DuplicatedPlaceholderRestoresTwice(t *testing.T) {
	t.Parallel()

	const stored = `{"type":"doc","content":[{"type":"gliffyDiagram","attrs":{"id":"x"}}]}`

	shielded, err := doc.Shield(json.RawMessage(stored))
	require.NoError(t, err)

	var root map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(shielded.Document, &root))
	var content []json.RawMessage
	require.NoError(t, json.Unmarshal(root["content"], &content))
	require.Len(t, content, 1)
	doubled, err := json.Marshal([]json.RawMessage{content[0], content[0]})
	require.NoError(t, err)
	root["content"] = doubled
	duplicated, err := json.Marshal(root)
	require.NoError(t, err)

	restored, err := doc.Restore(duplicated, shielded)
	require.NoError(t, err)
	require.Empty(t, restored.Dropped)
	require.Empty(t, restored.Unresolved)
	require.Equal(t, 2, strings.Count(string(restored.Document), `"gliffyDiagram"`))
}

// TestShield_RejectsMalformedDocuments: the walkers recurse, so the bounds and
// the shape checks are load-bearing rather than tidiness.
func TestShield_RejectsMalformedDocuments(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		document string
		wantErr  error
	}{
		"not an object":        {`[]`, doc.ErrNotAnObject},
		"root is not a doc":    {`{"type":"paragraph"}`, doc.ErrNotADoc},
		"node without a type":  {`{"type":"doc","content":[{"attrs":{}}]}`, doc.ErrNoType},
		"mark without a type":  {`{"type":"doc","content":[{"type":"paragraph","marks":[{"attrs":{}}]}]}`, doc.ErrNoType},
		"content is an object": {`{"type":"doc","content":{"type":"paragraph"}}`, nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := doc.Shield(json.RawMessage(tc.document))
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

// TestShield_RejectsDocumentsNestedTooDeeply keeps a hostile document from
// exhausting the stack in a recursive walker.
func TestShield_RejectsDocumentsNestedTooDeeply(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	const depth = 400
	for range depth {
		b.WriteString(`{"type":"blockquote","content":[`)
	}
	b.WriteString(`{"type":"paragraph"}`)
	for range depth {
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)

	_, err := doc.Shield(json.RawMessage(b.String()))
	require.ErrorIs(t, err, doc.ErrTooDeep)
}

// ── helpers ────────────────────────────────────────────────────────────────

// simulateEditorRoundTrip stands in for the browser: it decodes the document,
// changes a piece of text, and re-encodes with encoding/json — which reorders
// object keys and rewrites number literals exactly as JSON.stringify does.
//
// Using a full decode/encode rather than a string replacement is the point: it
// is what proves the guarantee does not depend on the client leaving the
// document's bytes alone, because no real editor does.
func simulateEditorRoundTrip(t *testing.T, document json.RawMessage, from, to string) json.RawMessage {
	t.Helper()

	var value any
	require.NoError(t, json.Unmarshal(document, &value))
	replaceStrings(value, from, to)
	out, err := json.Marshal(value)
	require.NoError(t, err)
	return out
}

func replaceStrings(value any, from, to string) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if s, ok := item.(string); ok && s == from {
				v[key] = to
				continue
			}
			replaceStrings(item, from, to)
		}
	case []any:
		for _, item := range v {
			replaceStrings(item, from, to)
		}
	}
}

// requireEveryTypeIsInTheSchema walks the document and fails on any node or mark
// type the editor's schema does not define — the property that makes a document
// safe to hand to ProseMirror.
func requireEveryTypeIsInTheSchema(t *testing.T, document json.RawMessage) {
	t.Helper()

	var node map[string]any
	require.NoError(t, json.Unmarshal(document, &node))
	walkSchemaTypes(t, node, false)
}

func walkSchemaTypes(t *testing.T, node map[string]any, isMark bool) {
	t.Helper()

	nodeType, _ := node["type"].(string)
	if isMark {
		require.True(t, isKnownMark(nodeType),
			"mark type %q is outside the editor's schema, so ProseMirror would drop it", nodeType)
	} else {
		require.True(t, isKnownNode(nodeType),
			"node type %q is outside the editor's schema, so ProseMirror would drop it", nodeType)
	}
	for member, asMark := range map[string]bool{"content": false, "marks": true} {
		children, ok := node[member].([]any)
		if !ok {
			continue
		}
		for _, child := range children {
			childObj, ok := child.(map[string]any)
			require.True(t, ok, "%s must hold objects", member)
			walkSchemaTypes(t, childObj, asMark)
		}
	}
}

// dropUnknownTypes does to a document what ProseMirror does to content outside
// its schema: removes it, without complaint. It is the failure mode this package
// exists to prevent, kept here so the tests can demonstrate it rather than
// assert against a description of it.
func dropUnknownTypes(t *testing.T, document json.RawMessage) json.RawMessage {
	t.Helper()

	var node map[string]any
	require.NoError(t, json.Unmarshal(document, &node))
	out := filterNode(node)
	require.NotNil(t, out, "the document root itself was filtered away")
	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	return encoded
}

func filterNode(node map[string]any) map[string]any {
	nodeType, _ := node["type"].(string)
	if !isKnownNode(nodeType) {
		return nil
	}
	if marks, ok := node["marks"].([]any); ok {
		kept := make([]any, 0, len(marks))
		for _, mark := range marks {
			markObj, ok := mark.(map[string]any)
			if !ok {
				continue
			}
			markType, _ := markObj["type"].(string)
			if isKnownMark(markType) {
				kept = append(kept, markObj)
			}
		}
		node["marks"] = kept
	}
	if content, ok := node["content"].([]any); ok {
		kept := make([]any, 0, len(content))
		for _, child := range content {
			childObj, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if filtered := filterNode(childObj); filtered != nil {
				kept = append(kept, filtered)
			}
		}
		node["content"] = kept
	}
	return node
}

func isKnownNode(nodeType string) bool {
	for _, known := range doc.SchemaNodes() {
		if known == nodeType {
			return true
		}
	}
	return false
}

func isKnownMark(markType string) bool {
	for _, known := range doc.SchemaMarks() {
		if known == markType {
			return true
		}
	}
	return false
}
