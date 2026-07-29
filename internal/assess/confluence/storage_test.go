package confluence

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestScanBody_CountsMacrosByName is the baseline behaviour.
func TestScanBody_CountsMacrosByName(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`
<ac:structured-macro ac:name="info"><ac:rich-text-body><p>a</p></ac:rich-text-body></ac:structured-macro>
<ac:structured-macro ac:name="info"/>
<ac:structured-macro ac:name="drawio"/>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, map[string]int{"info": 2, "drawio": 1}, c.Macros)
	require.Equal(t, 3, c.MacroTotal())
}

// TestScanBody_AcLinkSurvivesTheHTMLAutoCloseCollision is a regression test for
// the defect that made this scanner report almost every real page as truncated.
//
// encoding/xml matches AutoClose entries on Name.Local and ignores the
// namespace, so xml.HTMLAutoClose's void <link> also matches Confluence's
// <ac:link>: the decoder self-closed it and then failed the document on the
// real </ac:link>. Fails before the fix (autoCloseVoidElements still containing
// "link"), passes after — verified in both directions.
func TestScanBody_AcLinkSurvivesTheHTMLAutoCloseCollision(t *testing.T) {
	t.Parallel()

	// "link" must not be in the list, and the stock list must still contain it —
	// if a future Go release drops it, this test should say so rather than
	// quietly protecting against nothing.
	require.Contains(t, xml.HTMLAutoClose, "link",
		"stock list no longer contains \"link\"; re-check whether the ac:link workaround is still needed")
	require.NotContains(t, autoCloseVoidElements, "link")

	c := ScanBodyString(`<p>before</p>
<ac:link><ri:page ri:content-title="Other" ri:space-key="DOCS"/></ac:link>
<ac:link ac:anchor="x"><ac:link-body>text</ac:link-body></ac:link>
<p>after</p>`)

	require.False(t, c.Truncated, "ac:link must not truncate the body; reason: %s", c.TruncationReason)
	require.Equal(t, 2, c.Elements["ac:link"])
	require.Equal(t, 1, c.Elements["ri:page"])
	require.Equal(t, 1, c.Elements["ac:link-body"])
	// Content after the links was still reached — the real symptom of the bug.
	require.Equal(t, 2, c.HTMLElements["p"])
}

// TestScanBody_ToleratesHTMLEntities — &nbsp; is not an XML entity, and a
// default decoder fails the whole document on the first one.
func TestScanBody_ToleratesHTMLEntities(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<p>Hello&nbsp;world &mdash; &amp; more&hellip;</p><p>second</p>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 2, c.HTMLElements["p"])
}

// TestScanBody_ToleratesUnclosedVoidTags — real bodies accumulate <br> and
// <img> without the XHTML slash.
func TestScanBody_ToleratesUnclosedVoidTags(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<p>a<br>b</p><img src="x.png"><hr><p>end</p>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 2, c.HTMLElements["p"])
	require.Equal(t, 1, c.HTMLElements["br"])
	require.Equal(t, 1, c.HTMLElements["img"])
	require.Equal(t, 1, c.HTMLElements["hr"])
}

// TestScanBody_ReadsAFragmentWithManyRoots — a storage-format body is a
// fragment, not a document. Without the synthetic root the decoder stops after
// the first top-level element closes, which silently under-counts every page.
func TestScanBody_ReadsAFragmentWithManyRoots(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<p>one</p><p>two</p><p>three</p><table><tr><td>x</td></tr></table>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 3, c.HTMLElements["p"], "all top-level siblings must be counted")
	require.Equal(t, 1, c.HTMLElements["table"])
	require.Zero(t, c.HTMLElements["azimuthal-assess-root"], "the synthetic root is not content")
}

// TestScanBody_ResolvesNamespacesWhetherDeclaredOrNot — the same body counted
// identically whether the prefixes are undeclared (as in entities.xml) or
// resolved to URIs (as re-serialising tooling emits). Matching only the literal
// prefix would reclassify every macro as plain HTML and report a page full of
// macros as mapping cleanly.
func TestScanBody_ResolvesNamespacesWhetherDeclaredOrNot(t *testing.T) {
	t.Parallel()

	body := `<ac:structured-macro ac:name="info"/><ac:link><ri:page ri:content-title="T"/></ac:link>`

	undeclared := ScanBodyString(body)
	declared := ScanBodyString(
		`<span xmlns:ac="http://atlassian.com/content" xmlns:ri="http://atlassian.com/resource/identifier">` +
			body + `</span>`)

	require.False(t, undeclared.Truncated)
	require.False(t, declared.Truncated)

	require.Equal(t, 1, undeclared.Macros["info"])
	require.Equal(t, 1, declared.Macros["info"], "a declared ac: namespace must resolve to the same macro")
	require.Equal(t, 1, undeclared.Elements["ac:link"])
	require.Equal(t, 1, declared.Elements["ac:link"])
	require.Equal(t, 1, declared.Elements["ri:page"])
}

// TestScanBody_CDATAWithMarkupCharacters — code macros carry raw source with
// < and & in a CDATA section.
func TestScanBody_CDATAWithMarkupCharacters(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[if (a < b && c > d) { x(); }]]></ac:plain-text-body></ac:structured-macro><p>after</p>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 1, c.Macros["code"])
	require.Equal(t, 1, c.HTMLElements["p"], "content after a CDATA body must still be reached")
}

// TestScanBody_TruncatedBodyKeepsWhatItCounted — the defensive path. A body cut
// off mid-element reports what it did contain, flags itself, and keeps the
// decoder's reason. It must not return an empty census and it must not error.
func TestScanBody_TruncatedBodyKeepsWhatItCounted(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<p>one</p><ac:structured-macro ac:name="info"><ac:rich-text-body><p>two`)

	require.True(t, c.Truncated, "an unterminated body must be flagged")
	require.NotEmpty(t, c.TruncationReason, "the decoder's reason must be kept for the report")
	require.Equal(t, 1, c.Macros["info"], "constructs before the fault are still counted")
	require.Equal(t, 2, c.HTMLElements["p"])
}

// TestScanBody_MacroWithoutNameIsNamedNotDropped — the preservation philosophy
// applied to parsing: an unidentifiable construct is still counted.
func TestScanBody_MacroWithoutNameIsNamedNotDropped(t *testing.T) {
	t.Parallel()

	c := ScanBodyString(`<ac:structured-macro/><ac:structured-macro ac:name="  "/>`)

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 2, c.Macros[UnnamedMacro])
	require.Equal(t, 2, c.MacroTotal(), "unnamed macros still count toward the total")
}

// TestScanBody_DistinctNameCapBoundsMemoryWithoutLosingCounts is the bounded
// memory guarantee for the census map. Past the cap, names collapse into
// OverflowName but the arithmetic still closes — which is what lets the report
// stay reconcilable on hostile input.
func TestScanBody_DistinctNameCapBoundsMemoryWithoutLosingCounts(t *testing.T) {
	t.Parallel()

	const distinct = maxDistinctNames + 250
	var b strings.Builder
	for i := range distinct {
		b.WriteString(`<ac:structured-macro ac:name="macro-`)
		b.WriteString(itoa(i))
		b.WriteString(`"/>`)
	}

	c := ScanBodyString(b.String())

	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.True(t, c.NameCapReached, "the cap must announce itself")
	require.LessOrEqual(t, len(c.Macros), maxDistinctNames+1, "distinct names are bounded")
	require.Equal(t, distinct, c.MacroTotal(), "no macro is lost when the cap is reached")
	require.Positive(t, c.Macros[OverflowName])
}

// TestMerge_AccumulatesAcrossPages backs the whole-space census.
func TestMerge_AccumulatesAcrossPages(t *testing.T) {
	t.Parallel()

	total := NewBodyCensus()
	total.Merge(ScanBodyString(`<ac:structured-macro ac:name="info"/><p>a</p>`))
	total.Merge(ScanBodyString(`<ac:structured-macro ac:name="info"/><ac:structured-macro ac:name="jira"/>`))
	total.Merge(nil)

	require.Equal(t, 2, total.Macros["info"])
	require.Equal(t, 1, total.Macros["jira"])
	require.Equal(t, 1, total.HTMLElements["p"])
	require.Equal(t, 4, total.Total())
}

// TestMerge_PropagatesTruncation — one bad page must not be hidden by nine good
// ones in the whole-space census.
func TestMerge_PropagatesTruncation(t *testing.T) {
	t.Parallel()

	total := NewBodyCensus()
	total.Merge(ScanBodyString(`<p>fine</p>`))
	total.Merge(ScanBodyString(`<p>broken<ac:structured-macro ac:name="x">`))

	require.True(t, total.Truncated)
	require.NotEmpty(t, total.TruncationReason)
}

func TestScanBody_NilReaderIsAnError(t *testing.T) {
	t.Parallel()

	_, err := ScanBody(nil)
	require.Error(t, err)
}

func TestScanBody_EmptyBodyIsEmptyNotTruncated(t *testing.T) {
	t.Parallel()

	c := ScanBodyString("")
	require.False(t, c.Truncated)
	require.Zero(t, c.Total())
}

