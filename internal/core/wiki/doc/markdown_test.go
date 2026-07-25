package doc_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// TestFromMarkdown_ConversionRules is the on-ramp's contract, one case per rule.
// Every existing Codex page is markdown, so each of these runs the first time
// somebody opens a page in the new editor — and each is a chance to lose their
// content.
func TestFromMarkdown_ConversionRules(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		markdown string
		// wantContains are fragments that must appear in the converted document.
		wantContains []string
		// wantAbsent are fragments that must NOT appear.
		wantAbsent []string
	}{
		"heading": {
			markdown:     "## Section",
			wantContains: []string{`"type":"heading"`, `"level":2`, `"text":"Section"`},
		},
		"paragraph with emphasis": {
			markdown: "plain **bold** and *italic* and `code`",
			wantContains: []string{
				`"type":"paragraph"`,
				`{"type":"bold"}`, `"text":"bold"`,
				`{"type":"italic"}`, `"text":"italic"`,
				`{"type":"code"}`, `"text":"code"`,
			},
		},
		"strikethrough is GFM": {
			markdown:     "~~gone~~",
			wantContains: []string{`{"type":"strike"}`, `"text":"gone"`},
		},
		"link keeps its destination and title": {
			markdown:     `[label](https://example.com "the title")`,
			wantContains: []string{`"type":"link"`, `"href":"https://example.com"`, `"title":"the title"`, `"text":"label"`},
		},
		"autolink becomes linked text": {
			markdown:     "<https://example.com/x>",
			wantContains: []string{`"type":"link"`, `"href":"https://example.com/x"`},
		},
		"bullet list": {
			markdown:     "- one\n- two",
			wantContains: []string{`"type":"bulletList"`, `"type":"listItem"`, `"text":"one"`, `"text":"two"`},
			wantAbsent:   []string{`"taskList"`},
		},
		"ordered list keeps its start": {
			markdown:     "3. three\n4. four",
			wantContains: []string{`"type":"orderedList"`, `"start":3`},
		},
		"task list keeps checked state": {
			markdown:     "- [x] done\n- [ ] todo",
			wantContains: []string{`"type":"taskList"`, `"type":"taskItem"`, `"checked":true`, `"checked":false`},
			// The checkbox must not also survive as literal text in the item.
			wantAbsent: []string{`"text":"[x] "`, `"text":"[ ] "`},
		},
		"blockquote": {
			markdown:     "> quoted",
			wantContains: []string{`"type":"blockquote"`, `"text":"quoted"`},
		},
		"fenced code keeps its language": {
			markdown:     "```go\nfmt.Println(1)\n```",
			wantContains: []string{`"type":"codeBlock"`, `"language":"go"`, `fmt.Println(1)`},
		},
		"indented code has no language": {
			markdown:     "    indented := 1\n",
			wantContains: []string{`"type":"codeBlock"`, `"language":""`, `indented := 1`},
		},
		"thematic break": {
			markdown:     "---",
			wantContains: []string{`"type":"horizontalRule"`},
		},
		"GFM table splits header from body": {
			markdown: "| a | b |\n| --- | --- |\n| 1 | 2 |",
			wantContains: []string{
				`"type":"table"`, `"type":"tableRow"`,
				`"type":"tableHeader"`, `"type":"tableCell"`,
				`"text":"a"`, `"text":"1"`,
			},
		},
		"hard break": {
			markdown:     "one  \ntwo",
			wantContains: []string{`"type":"hardBreak"`},
		},
		"http image becomes a first-class image": {
			markdown:     `![alt text](https://example.com/x.png)`,
			wantContains: []string{`"type":"image"`, `"src":"https://example.com/x.png"`, `"alt":"alt text"`},
			wantAbsent:   []string{doc.LegacyImage},
		},
		"site-relative image becomes a first-class image": {
			markdown:     `![a](/api/v1/orgs/x/spaces/y/attachments/z)`,
			wantContains: []string{`"type":"image"`, `"src":"/api/v1/orgs/x/spaces/y/attachments/z"`},
		},

		// ── the preservation rules ──────────────────────────────────────────
		"HTML block is preserved verbatim, not dropped": {
			markdown:     "<div class=\"legacy\">\n  <b>kept</b>\n</div>",
			wantContains: []string{doc.LegacyHTMLBlock, `class=\"legacy\"`, `<b>kept</b>`},
		},
		"inline HTML is preserved verbatim": {
			// This is not hypothetical: Codex's markdown editor writes text
			// colour and highlight as exactly this.
			markdown:     `before <span style="color:#e53e3e">red</span> after`,
			wantContains: []string{doc.LegacyHTMLInline, `style=\"color:#e53e3e\"`, `"text":"before "`, `"text":" after"`},
		},
		"data-URI image is preserved rather than rendered": {
			markdown:     `![x](data:image/png;base64,AAAA)`,
			wantContains: []string{doc.LegacyImage, `data:image/png;base64,AAAA`},
			wantAbsent:   []string{`"type":"image"`},
		},
		"scheme-relative image is preserved rather than rendered": {
			markdown:     `![x](//evil.example/x.png)`,
			wantContains: []string{doc.LegacyImage},
			wantAbsent:   []string{`"type":"image"`},
		},
		"javascript-scheme image is preserved rather than rendered": {
			markdown:     `![x](javascript:alert(1))`,
			wantContains: []string{doc.LegacyImage},
			wantAbsent:   []string{`"type":"image"`},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			converted, err := doc.FromMarkdown(tc.markdown)
			require.NoError(t, err)
			require.NoError(t, doc.Validate(converted))

			for _, want := range tc.wantContains {
				require.Contains(t, string(converted), want)
			}
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, string(converted), absent)
			}
		})
	}
}

// TestFromMarkdown_PreservedHTMLRoundTripsThroughTheEditor is the on-ramp's half
// of the ADR-0012 guarantee. Converting is not enough: the HTML the converter
// could not represent has to survive being opened, edited and saved, which means
// it has to flow through the same capture/restore path as an imported macro.
func TestFromMarkdown_PreservedHTMLRoundTripsThroughTheEditor(t *testing.T) {
	t.Parallel()

	const legacy = "Intro paragraph.\n\n" +
		"<div class=\"gliffy\" data-id=\"42\">\n  <svg viewBox=\"0 0 1 1\"/>\n</div>\n\n" +
		"Closing paragraph."

	converted, err := doc.FromMarkdown(legacy)
	require.NoError(t, err)

	shielded, err := doc.Shield(converted)
	require.NoError(t, err)
	require.Len(t, shielded.Captured, 1, "the HTML block is the one thing needing preservation")
	requireEveryTypeIsInTheSchema(t, shielded.Document)

	edited := simulateEditorRoundTrip(t, shielded.Document, "Intro paragraph.", "Intro paragraph, edited.")

	restored, err := doc.Restore(edited, shielded)
	require.NoError(t, err)
	require.Empty(t, restored.Dropped)
	require.Empty(t, restored.Unresolved)

	// The legacy HTML is intact, character for character, inside the node that
	// preserved it.
	require.Contains(t, string(restored.Document), `class=\"gliffy\" data-id=\"42\"`)
	require.Contains(t, string(restored.Document), `viewBox=\"0 0 1 1\"`)
	require.Contains(t, string(restored.Document), "Intro paragraph, edited.")
	require.Contains(t, string(restored.Document), "Closing paragraph.")
}

// TestFromMarkdown_IsDeterministic is not a tidiness test. Publish re-derives the
// base document by converting the same markdown again, and matches the
// preservation ids the editor is holding against it. A converter that emitted
// different bytes the second time would make every legacy page's first save fail
// with unresolvable placeholders.
func TestFromMarkdown_IsDeterministic(t *testing.T) {
	t.Parallel()

	const markdown = "# Title\n\nBody with **bold**, a [link](https://example.com), and:\n\n" +
		"<div data-b=\"2\" data-a=\"1\">html</div>\n\n" +
		"| h1 | h2 |\n| --- | --- |\n| c1 | c2 |\n\n- [x] done\n- [ ] todo\n"

	first, err := doc.FromMarkdown(markdown)
	require.NoError(t, err)

	for range 20 {
		again, err := doc.FromMarkdown(markdown)
		require.NoError(t, err)
		require.Equal(t, string(first), string(again),
			"converting the same markdown twice must produce the same bytes")
	}

	// And the ids that come out of shielding it are stable too, which is the
	// property publish actually depends on.
	a, err := doc.Shield(first)
	require.NoError(t, err)
	b, err := doc.Shield(first)
	require.NoError(t, err)
	require.Equal(t, a.Order, b.Order)
}

// TestFromMarkdown_LosesNothingFromAWholeLegacyPage is the blunt version: take a
// page using every construct at once, convert it, and assert every piece of the
// author's text is somewhere in the result. The per-rule table above can pass
// while a rule silently swallows its neighbour.
func TestFromMarkdown_LosesNothingFromAWholeLegacyPage(t *testing.T) {
	t.Parallel()

	const page = `# Runbook

Intro with **bold**, *italic*, ~~struck~~ and ` + "`inline`" + `.

## Steps

1. First step
2. Second step

- [x] Checked task
- [ ] Unchecked task

> A quoted warning.

` + "```bash\necho hello\n```" + `

| Env | Host |
| --- | --- |
| prod | prod.example |

<div class="callout">Legacy HTML callout</div>

A [link](https://example.com/docs) and an ![image](https://example.com/i.png).

---

Final line.
`

	converted, err := doc.FromMarkdown(page)
	require.NoError(t, err)

	// The projection back to text is what search sees, so asserting on it proves
	// the content survived into something findable rather than merely into JSON.
	projected, err := doc.ToMarkdown(converted)
	require.NoError(t, err)

	for _, fragment := range []string{
		"Runbook", "bold", "italic", "struck", "inline",
		"Steps", "First step", "Second step",
		"Checked task", "Unchecked task",
		"A quoted warning.",
		"echo hello",
		"prod.example",
		"Legacy HTML callout",
		"https://example.com/docs",
		"Final line.",
	} {
		require.Contains(t, projected, fragment,
			"converting a legacy page lost %q", fragment)
	}

	// Nothing in a legacy page should be dropped for lack of a branch.
	require.NotContains(t, string(converted), doc.LegacyBlock)
	require.NotContains(t, string(converted), doc.LegacyInline)
}

// TestFromMarkdown_EmptyIsTheEmptyDocument — a page created with no body is the
// overwhelmingly common case, and it must not become a document the editor
// rejects.
func TestFromMarkdown_EmptyIsTheEmptyDocument(t *testing.T) {
	t.Parallel()

	for _, markdown := range []string{"", "   ", "\n\n"} {
		converted, err := doc.FromMarkdown(markdown)
		require.NoError(t, err)
		require.NoError(t, doc.Validate(converted))
		require.JSONEq(t, string(doc.Empty()), string(converted))
	}
}

// TestFromMarkdown_HandlesADeeplyNestedList bounds the recursive converter the
// same way the walkers are bounded.
func TestFromMarkdown_HandlesADeeplyNestedList(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := range 300 {
		b.WriteString(strings.Repeat("  ", i))
		b.WriteString("- item\n")
	}
	_, err := doc.FromMarkdown(b.String())
	// Either it converts or it refuses, but it must not exhaust the stack.
	if err != nil {
		require.ErrorIs(t, err, doc.ErrTooDeep)
	}
}

// TestToMarkdown_ProjectsEveryMacroAndPreservedNode covers the projection's
// contract: nothing reaches the search index as silence. A node type with no
// branch of its own still contributes its text.
func TestToMarkdown_ProjectsEveryMacroAndPreservedNode(t *testing.T) {
	t.Parallel()

	document := json.RawMessage(`{"type":"doc","content":[
		{"type":"panel","attrs":{"kind":"warning"},"content":[
			{"type":"paragraph","content":[{"type":"text","text":"Mind the gap"}]}]},
		{"type":"expand","attrs":{"title":"More detail"},"content":[
			{"type":"paragraph","content":[{"type":"text","text":"Hidden body"}]}]},
		{"type":"paragraph","content":[
			{"type":"statusLozenge","attrs":{"text":"IN PROGRESS","colour":"yellow"}}]},
		{"type":"tableOfContents"},
		{"type":"childrenDisplay"},
		{"type":"pageInclude","attrs":{"page_id":"11111111-1111-1111-1111-111111111111"}},
		{"type":"layout","content":[{"type":"layoutColumn","content":[
			{"type":"paragraph","content":[{"type":"text","text":"Column text"}]}]}]},
		{"type":"someFutureMacro","attrs":{"body":"Findable prose inside an unknown node"}}
	]}`)

	projected, err := doc.ToMarkdown(document)
	require.NoError(t, err)

	require.Contains(t, projected, "**WARNING**")
	require.Contains(t, projected, "Mind the gap")
	require.Contains(t, projected, "**More detail**")
	require.Contains(t, projected, "Hidden body")
	require.Contains(t, projected, "`IN PROGRESS`")
	require.Contains(t, projected, "[Table of contents]")
	require.Contains(t, projected, "[Child pages]")
	require.Contains(t, projected, "11111111-1111-1111-1111-111111111111")
	require.Contains(t, projected, "Column text")
	require.Contains(t, projected, "Findable prose inside an unknown node",
		"an unknown node must still reach the search index as plain text (ADR-0012)")
}

// TestToMarkdown_ProjectsTheCoreSetAsValidMarkdown keeps the projection honest
// about the column it feeds: pages.content is read by markdown renderers.
func TestToMarkdown_ProjectsTheCoreSetAsValidMarkdown(t *testing.T) {
	t.Parallel()

	document := json.RawMessage(`{"type":"doc","content":[
		{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Title"}]},
		{"type":"paragraph","content":[
			{"type":"text","marks":[{"type":"bold"}],"text":"b"},
			{"type":"text","text":" "},
			{"type":"text","marks":[{"type":"italic"}],"text":"i"},
			{"type":"text","text":" "},
			{"type":"text","marks":[{"type":"link","attrs":{"href":"https://e.example"}}],"text":"l"}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[
			{"type":"paragraph","content":[{"type":"text","text":"one"}]}]}]},
		{"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":true},"content":[
			{"type":"paragraph","content":[{"type":"text","text":"done"}]}]}]},
		{"type":"codeBlock","attrs":{"language":"sql"},"content":[{"type":"text","text":"SELECT 1"}]},
		{"type":"table","content":[
			{"type":"tableRow","content":[
				{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"h"}]}]}]},
			{"type":"tableRow","content":[
				{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"c"}]}]}]}]},
		{"type":"horizontalRule"}
	]}`)

	projected, err := doc.ToMarkdown(document)
	require.NoError(t, err)

	require.Contains(t, projected, "### Title")
	require.Contains(t, projected, "**b**")
	require.Contains(t, projected, "*i*")
	require.Contains(t, projected, "[l](https://e.example)")
	require.Contains(t, projected, "- one")
	require.Contains(t, projected, "- [x] done")
	require.Contains(t, projected, "```sql\nSELECT 1\n```")
	require.Contains(t, projected, "| h |")
	require.Contains(t, projected, "| --- |")
	require.Contains(t, projected, "| c |")
	require.Contains(t, projected, "---")
}

// TestPlainText_FallsBackToStringLeavesForUnknownContent: an imported macro keeps
// its body in an attribute, not in text nodes, and it still has to be findable.
func TestPlainText_FallsBackToStringLeavesForUnknownContent(t *testing.T) {
	t.Parallel()

	require.Equal(t, "the body", doc.PlainText(json.RawMessage(
		`{"type":"confluenceMacro","attrs":{"body":"the body"}}`)))

	// A document node's own text wins over the attribute scan.
	require.Equal(t, "real text", doc.PlainText(json.RawMessage(
		`{"type":"paragraph","attrs":{"id":"ignored"},"content":[{"type":"text","text":"real text"}]}`)))

	// The placeholder's own bookkeeping is not prose.
	require.NotContains(t, doc.PlainText(json.RawMessage(
		`{"type":"unknownContent","attrs":{"az_id":"u1","az_name":"x","az_text":"y"}}`)), "u1")
}

// TestPlainText_IsBoundedSoOneMacroCannotDominateTheIndex.
func TestPlainText_IsBoundedSoOneMacroCannotDominateTheIndex(t *testing.T) {
	t.Parallel()

	huge, err := json.Marshal(map[string]any{
		"type":  "hugeMacro",
		"attrs": map[string]any{"body": strings.Repeat("x", 50_000)},
	})
	require.NoError(t, err)

	text := doc.PlainText(huge)
	require.NotEmpty(t, text)
	require.LessOrEqual(t, len(text), 2000)
}
