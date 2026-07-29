package doc_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// The server's half of the shared markdown corpus.
//
// `markdown_corpus.json` holds one sample of each construct the Codex markdown
// dialect covers, with the document it must produce. This test checks
// [doc.FromMarkdown] — the legacy on-ramp, which converts a markdown page to a
// document the first time somebody opens it in the editor.
// `web/src/lib/codex/markdownPaste.test.ts` checks the editor's paste converter
// against the same bytes.
//
// The point is that the two are never checked against each other. There were
// already two markdown dialects in this system before this phase — the server's
// converter and TipTap's type-time input rules — and a paste path made a third
// place the same text can enter a document. Three implementations that agree by
// inspection agree only until somebody edits one of them.
//
// This file also pins FromMarkdown itself. `markdown_test.go` covers the
// converter's rules in its own terms; this covers the subset that is a
// cross-language CONTRACT, and a change to any of it is a change two languages
// have to make together.

type corpusCase struct {
	Name     string          `json:"name"`
	Why      string          `json:"why"`
	Markdown string          `json:"markdown"`
	Doc      json.RawMessage `json:"doc"`
}

func readCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile("markdown_corpus.json")
	require.NoError(t, err, "the corpus must be readable from this package; the TypeScript side reads the same file by relative path")

	var file struct {
		Cases []corpusCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Greater(t, len(file.Cases), 10, "a corpus this small would not cover the dialect")
	return file.Cases
}

func TestMarkdownCorpus_ServerConverterMatchesTheCorpus(t *testing.T) {
	t.Parallel()

	for _, kase := range readCorpus(t) {
		t.Run(kase.Name, func(t *testing.T) {
			t.Parallel()

			got, err := doc.FromMarkdown(kase.Markdown)
			require.NoError(t, err)

			// Compared as canonical JSON rather than as bytes, because the
			// corpus file is indented for a human to read and FromMarkdown
			// emits compact output. Everything that matters — key order within
			// a node, attribute values, the exact text including trailing
			// newlines in a code block — survives that normalisation.
			require.JSONEq(t, string(kase.Doc), string(got),
				"the corpus says %q converts differently. Either the dialect changed, in which "+
					"case both converters and the corpus move together, or this is a regression.", kase.Markdown)
		})
	}
}

// TestMarkdownCorpus_EveryCaseIsAnActualConversion is the negative-test
// question applied to the corpus itself.
//
// A case whose markdown contains no construct — whose document is one paragraph
// of the same words — would pass the comparison above against almost any
// converter, including one that did nothing at all. Those cases are still worth
// having (both sides must agree that prose is prose), but the corpus must not
// consist only of them, or it would read as coverage while proving nothing.
func TestMarkdownCorpus_EveryCaseIsAnActualConversion(t *testing.T) {
	t.Parallel()

	converting := 0
	for _, kase := range readCorpus(t) {
		var document struct {
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		require.NoError(t, json.Unmarshal(kase.Doc, &document))

		// A case is "converting" if it produces something other than a bare
		// paragraph, or if its paragraph carries a mark.
		if len(document.Content) != 1 || document.Content[0].Type != "paragraph" ||
			bytes.Contains(kase.Doc, []byte(`"marks"`)) {
			converting++
		}
	}
	require.GreaterOrEqual(t, converting, 12,
		"most of the corpus must exercise a real construct, or it is a list of paragraphs")
}

// TestMarkdownCorpus_IsDeterministic re-runs every case, because publish
// re-derives a legacy page's base document from the same markdown to recover
// the preservation ids handed out at read time. A converter that produced
// different bytes the second time would break every legacy page's first save
// with unresolvable placeholders.
//
// `markdown_test.go` already asserts this for one document; the corpus is where
// the breadth is, so it is worth asserting across all of it.
func TestMarkdownCorpus_IsDeterministic(t *testing.T) {
	t.Parallel()

	for _, kase := range readCorpus(t) {
		first, err := doc.FromMarkdown(kase.Markdown)
		require.NoError(t, err)
		for range 5 {
			again, err := doc.FromMarkdown(kase.Markdown)
			require.NoError(t, err)
			require.True(t, bytes.Equal(first, again),
				"%s converted to different bytes on a second run", kase.Name)
		}
	}
}
