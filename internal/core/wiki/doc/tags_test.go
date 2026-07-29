package doc_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// The tests in this file are pure document-model logic — InlineTagLabels reads
// JSON and returns strings, and touches no persistence — so there is no test
// database here. The database side of the same feature (the tag rows a publish
// actually mints from these labels) is covered against real PostgreSQL by the
// wiki package's own integration tests; this file is about what the walker
// finds, and deliberately nothing else.

// ── fixture helpers ────────────────────────────────────────────────────────
//
// The documents below are built as literal JSON rather than marshalled from Go
// structs, for the same reason the rest of this package's tests are: the walker
// reads bytes, and a struct round trip would quietly normalise exactly the
// shapes — a missing attrs object, an attribute the model does not know — that
// the walker has to survive.

func tagNode(label string) string {
	return `{"type":"inlineTag","attrs":{"label":` + strconv.Quote(label) + `}}`
}

func tagText(text string) string {
	return `{"type":"text","text":` + strconv.Quote(text) + `}`
}

func tagParagraph(children ...string) string {
	return `{"type":"paragraph","content":[` + strings.Join(children, ",") + `]}`
}

func tagDoc(blocks ...string) string {
	return `{"type":"doc","content":[` + strings.Join(blocks, ",") + `]}`
}

// TestInlineTagLabels_ReturnsLabelsInDocumentOrder pins the order, not the set.
//
// The labels are deliberately chosen so that alphabetical order, reverse
// document order and map-iteration order all differ from the expected answer:
// swapping the append for anything that sorts, or collecting into a map and
// ranging over it, fails here. An assertion on membership would not have
// noticed any of that, and order is what the author sees in the tag list the
// publish writes.
func TestInlineTagLabels_ReturnsLabelsInDocumentOrder(t *testing.T) {
	t.Parallel()

	document := tagDoc(
		tagParagraph(tagNode("zebra"), tagText(" and ")),
		tagParagraph(tagText("prose that mentions nothing")),
		tagParagraph(tagNode("alpha")),
		tagParagraph(tagNode("middle")),
	)

	labels, err := doc.InlineTagLabels(json.RawMessage(document))
	require.NoError(t, err)
	require.Equal(t, []string{"zebra", "alpha", "middle"}, labels,
		"labels must come back in document order; alphabetical would be alpha, middle, zebra")
}

// TestInlineTagLabels_CollapsesDuplicatesCaseInsensitively covers both halves of
// the rule in recordTagLabel: the duplicate check is case-folded, and the
// spelling that survives is the FIRST one in document order.
//
// Which half breaks matters. If the fold were dropped, "#Design" and "#design"
// would become two tag rows for one concept in an org-scoped table, and no
// author could tell them apart in a picker. If the fold stayed but the last
// spelling won, the surviving label would depend on where in the page somebody
// last typed the tag — so the expected slice asserts "Design" and "Ops", the
// leading spellings, and not their lowercase or uppercase forms.
func TestInlineTagLabels_CollapsesDuplicatesCaseInsensitively(t *testing.T) {
	t.Parallel()

	document := tagDoc(
		tagParagraph(tagNode("Design"), tagNode("design"), tagNode("DESIGN")),
		// The trim happens before the fold, so a padded repeat is still a repeat.
		tagParagraph(tagNode("  Ops  "), tagNode("ops")),
		tagParagraph(tagNode("dEsIgN")),
	)

	labels, err := doc.InlineTagLabels(json.RawMessage(document))
	require.NoError(t, err)
	require.Equal(t, []string{"Design", "Ops"}, labels,
		"case variants must collapse to one label, and the first spelling in document order is the one kept")
}

// TestInlineTagLabels_TrimsAndSkipsEmptyLabels covers the four ways an inline
// tag can carry no usable label.
//
// The empty cases must be skipped entirely rather than returned as "". A "" in
// this slice reaches the caller, which slugifies it and writes a tag row — an
// unnameable tag that no author can have typed and none can remove, produced by
// a node the editor can leave behind mid-input. The assertion is an exact slice
// precisely so an extra "" element fails rather than passing a length check.
func TestInlineTagLabels_TrimsAndSkipsEmptyLabels(t *testing.T) {
	t.Parallel()

	document := tagDoc(
		tagParagraph(tagNode("  Release Notes  ")),
		tagParagraph(tagNode("   ")),
		tagParagraph(tagNode("")),
		// An inline tag whose attrs exist but carry no label at all, and one with
		// no attrs object whatsoever — both are shapes the editor can produce
		// while a tag is being typed and before it is committed.
		tagParagraph(`{"type":"inlineTag","attrs":{}}`),
		tagParagraph(`{"type":"inlineTag"}`),
		tagParagraph(tagNode("kept")),
	)

	labels, err := doc.InlineTagLabels(json.RawMessage(document))
	require.NoError(t, err)
	require.Equal(t, []string{"Release Notes", "kept"}, labels,
		"labels are trimmed, and a label with nothing left after trimming contributes no entry at all")
}

// TestInlineTagLabels_DescendsIntoNestedStructures puts a tag where a real one
// ends up: in a paragraph, in a table cell, in a row, in a table, in a panel.
//
// A walker that only looked at the document root's own content would return
// just "outer" here, which is why the buried tag comes FIRST in the expected
// slice — the assertion fails both if the nesting is not descended and if the
// order across nesting levels is wrong.
func TestInlineTagLabels_DescendsIntoNestedStructures(t *testing.T) {
	t.Parallel()

	buried := `{"type":"panel","attrs":{"variant":"info"},"content":[` +
		`{"type":"table","content":[` +
		`{"type":"tableRow","content":[` +
		`{"type":"tableCell","content":[` +
		tagParagraph(tagText("owned by "), tagNode("platform")) +
		`]}]}]}]}`

	document := tagDoc(buried, tagParagraph(tagNode("outer")))

	labels, err := doc.InlineTagLabels(json.RawMessage(document))
	require.NoError(t, err)
	require.Equal(t, []string{"platform", "outer"}, labels,
		"a tag five levels down in a panelled table must be found, and found before a later top-level one")
}

// TestInlineTagLabels_IgnoresTagsInsidePreservedContent is the important one.
//
// A preservation placeholder's az_raw is the verbatim body of content this
// document model has explicitly declined to interpret — a captured Confluence
// macro, a block of legacy inline HTML. Minting an org-scoped tag row out of it
// would be interpreting those bytes after all, and doing so from the one part
// of the document whose contents nobody has validated: the label would come
// from a foreign system's serialisation, it would appear in every org member's
// tag picker, and no author could point at the page that supposedly created it,
// because the editor renders that block as an inert placeholder.
//
// The fixture puts a tag in both places a placeholder can hide one — inside the
// az_raw string and inside a content array hanging off the placeholder — so the
// test fails if the early return for the placeholder types is removed in either
// direction.
func TestInlineTagLabels_IgnoresTagsInsidePreservedContent(t *testing.T) {
	t.Parallel()

	preservedRaw := `{"type":"confluenceMacro","content":[` + tagNode("hiddenInRaw") + `]}`

	for _, placeholderType := range []string{doc.NodeUnknownContent, doc.NodeUnknownInline} {
		t.Run(placeholderType, func(t *testing.T) {
			t.Parallel()

			placeholder := `{"type":"` + placeholderType + `","attrs":{` +
				`"az_id":"u1","az_name":"confluenceMacro","az_source":"document",` +
				`"az_raw":` + strconv.Quote(preservedRaw) + `,` +
				`"az_text":"#hiddenInText"},` +
				`"content":[` + tagNode("hiddenInChild") + `]}`

			document := tagDoc(tagParagraph(tagNode("visible")), placeholder)

			// Without this the test could pass because the fixture lost its
			// buried tag, rather than because the walker refused to read it.
			require.Contains(t, document, "hiddenInRaw",
				"the fixture no longer carries a tag inside the preserved bytes, so this test asserts nothing")
			require.Contains(t, document, "hiddenInChild",
				"the fixture no longer carries a tag under the placeholder, so this test asserts nothing")

			labels, err := doc.InlineTagLabels(json.RawMessage(document))
			require.NoError(t, err)
			require.Equal(t, []string{"visible"}, labels,
				"preserved content is opaque: nothing inside a %s may become a tag", placeholderType)
		})
	}
}

// TestInlineTagLabels_CapsTheNumberOfDistinctTags covers the ceiling that keeps
// one pasted page from minting thousands of rows in an org-scoped table.
//
// 100 is the value of the unexported constant maxInlineTagsPerDocument in
// tags.go; the number is written out here because a test in doc_test cannot
// reference it, and the comment is what ties the two together if the constant
// moves.
//
// The assertion is the exact first hundred labels, not just the length: a cap
// that kept the LAST hundred, or that stopped walking the document early enough
// to also lose the ordering, would satisfy a length check and fail this one.
func TestInlineTagLabels_CapsTheNumberOfDistinctTags(t *testing.T) {
	t.Parallel()

	const distinct = 150
	blocks := make([]string, 0, distinct)
	want := make([]string, 0, 100)
	for i := range distinct {
		label := fmt.Sprintf("tag-%03d", i)
		blocks = append(blocks, tagParagraph(tagNode(label)))
		if i < 100 {
			want = append(want, label)
		}
	}

	labels, err := doc.InlineTagLabels(json.RawMessage(tagDoc(blocks...)))
	require.NoError(t, err)
	require.Len(t, labels, 100,
		"a document with 150 distinct tags must yield exactly maxInlineTagsPerDocument of them")
	require.Equal(t, want, labels,
		"the cap keeps the first hundred in document order and drops the rest")
	require.NotContains(t, labels, "tag-100",
		"the first label past the cap must not appear")
}

// TestInlineTagLabels_RejectsMalformedDocuments: a document that is not a
// document must fail loudly rather than return whatever the walker managed to
// collect before it noticed.
//
// The nil check on the returned slice is the part that matters. A caller that
// got back (partialLabels, err) and logged the error while writing the labels
// anyway would tag a page from half a document, and the "with a tag before the
// bad node" case is there so the difference between "no labels because it
// failed" and "no labels because there were none" is actually exercised —
// InlineTagLabels validates the whole document before collecting anything.
func TestInlineTagLabels_RejectsMalformedDocuments(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		document string
		wantErr  error
	}{
		"not an object": {
			document: `[]`,
			wantErr:  doc.ErrNotAnObject,
		},
		"root is not a doc": {
			document: `{"type":"paragraph","content":[` + tagNode("nope") + `]}`,
			wantErr:  doc.ErrNotADoc,
		},
		"node without a type": {
			document: `{"type":"doc","content":[{"attrs":{"label":"nope"}}]}`,
			wantErr:  doc.ErrNoType,
		},
		"node without a type, after a perfectly good tag": {
			document: tagDoc(tagParagraph(tagNode("first")), `{"attrs":{}}`),
			wantErr:  doc.ErrNoType,
		},
		"mark without a type": {
			document: tagDoc(`{"type":"paragraph","marks":[{"attrs":{}}]}`),
			wantErr:  doc.ErrNoType,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			labels, err := doc.InlineTagLabels(json.RawMessage(tc.document))
			require.ErrorIs(t, err, tc.wantErr)
			require.Nil(t, labels,
				"a rejected document must yield no labels at all, not the ones collected before the failure")
		})
	}
}

// TestInlineTagLabels_ReturnsNothingForADocumentWithNoTags uses a document that
// is full of ordinary content rather than an empty one, so "nothing" means "no
// false positives from headings, images, links and preserved blocks" rather
// than "the walker was handed nothing to look at".
func TestInlineTagLabels_ReturnsNothingForADocumentWithNoTags(t *testing.T) {
	t.Parallel()

	document := tagDoc(
		`{"type":"heading","attrs":{"level":2},"content":[`+tagText("Release process")+`]}`,
		tagParagraph(tagText("Nothing here is a tag.")),
		`{"type":"image","attrs":{"attachment_id":"11111111-1111-1111-1111-111111111111"}}`,
		`{"type":"codeBlock","attrs":{"language":"sh"},"content":[`+tagText("make test-live")+`]}`,
	)

	labels, err := doc.InlineTagLabels(json.RawMessage(document))
	require.NoError(t, err)
	require.Empty(t, labels,
		"ordinary content must produce no tags")

	// And the empty document, which is what a page with no content holds.
	labels, err = doc.InlineTagLabels(doc.Empty())
	require.NoError(t, err)
	require.Empty(t, labels)
}

// TestInlineTagLabels_OnlyTheNodeTypeCounts is the guard against the most
// tempting wrong implementation: scanning prose for a leading hash.
//
// A tag exists because the author chose one from the input rule and the editor
// wrote an inlineTag node. Text that merely reads like a tag — a shell comment,
// a heading anchor in a URL, a paragraph that happens to carry a "label"
// attribute — is not a tag, and treating it as one would mint org-scoped rows
// from every code block anyone pastes. This test fails the moment somebody adds
// a helpful text scan.
//
// The second half is what stops it being vacuous: the same document with one
// real inlineTag added returns exactly that one, proving the walker did reach
// the content and simply declined everything in the first half.
func TestInlineTagLabels_OnlyTheNodeTypeCounts(t *testing.T) {
	t.Parallel()

	lookalikes := []string{
		tagParagraph(tagText("#design is how we write it in chat")),
		// A node that is not an inlineTag but carries the same attribute name.
		`{"type":"paragraph","attrs":{"label":"handbook"},"content":[` + tagText("labelled block") + `]}`,
		// A link whose href ends in a fragment that reads like a tag.
		tagParagraph(`{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com/x#ops"}}],"text":"the runbook"}`),
		`{"type":"codeBlock","attrs":{"language":"sh"},"content":[` + tagText("# platform: rebuild the index") + `]}`,
	}

	labels, err := doc.InlineTagLabels(json.RawMessage(tagDoc(lookalikes...)))
	require.NoError(t, err)
	require.Empty(t, labels,
		"only the inlineTag node type is a tag; text that reads like one is not")

	withReal, err := doc.InlineTagLabels(json.RawMessage(tagDoc(append(lookalikes, tagParagraph(tagNode("design")))...)))
	require.NoError(t, err)
	require.Equal(t, []string{"design"}, withReal,
		"the same document plus one real inlineTag yields exactly that tag, so the walk above really did visit this content")
}
