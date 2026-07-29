package jira

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const wellFormed = `<?xml version="1.0" encoding="UTF-8"?>
<entity-engine-xml>
  <Project id="10000" key="ABC" name="Alpha" lead="admin"/>
  <Project id="10001" key="DEF" name="Delta" lead="admin"/>
  <Issue id="10100" key="ABC-1" project="10000" type="1" status="1" summary="First"/>
  <Issue id="10101" key="ABC-2" project="10000" type="2" status="3" summary="Second"/>
  <Action id="10200" issue="10100" type="comment" body="a comment"/>
  <CustomFieldValue id="11077" issue="10100" customfield="10600" textvalue=""/>
</entity-engine-xml>`

func TestScan_CountsEveryRowByEntityType(t *testing.T) {
	t.Parallel()

	c, err := NewScanner().Scan(strings.NewReader(wellFormed))
	require.NoError(t, err)
	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)

	require.Equal(t, map[string]int{
		"Project": 2, "Issue": 2, "Action": 1, "CustomFieldValue": 1,
	}, c.Entities)
	require.Equal(t, 6, c.Rows)
	require.Equal(t, c.Rows, sumMap(c.Entities), "the per-type counts must sum to the row total")
}

func TestScan_CollectsOnlyRequestedRows(t *testing.T) {
	t.Parallel()

	var issues []Row
	s := NewScanner().On("Issue", func(r Row) error {
		issues = append(issues, r)
		return nil
	})

	c, err := s.Scan(strings.NewReader(wellFormed))
	require.NoError(t, err)

	require.Len(t, issues, 2)
	require.Equal(t, "ABC-1", issues[0].Get("key"))
	require.Equal(t, "First", issues[0].Get("summary"))
	require.Equal(t, "10000", issues[0].Get("project"))
	// Everything else was still counted without being materialised.
	require.Equal(t, 2, c.Entities["Project"])
}

// TestScan_ReadsFieldsFromChildElementsAsWellAsAttributes is the defect that
// would look like success: Jira's entity engine serialises short columns as
// attributes but long text can arrive as a child element, and an
// attributes-only reader returns empty descriptions and comment bodies across
// an entire export while reporting every row as parsed.
//
// Fails before the fix (readChildFields removed), passes after — verified in
// both directions.
func TestScan_ReadsFieldsFromChildElementsAsWellAsAttributes(t *testing.T) {
	t.Parallel()

	const mixed = `<entity-engine-xml>
  <Issue id="1" key="ABC-1" summary="Short">
    <description>A description
that spans lines.</description>
  </Issue>
  <Action id="2" issue="1" type="comment">
    <body>A comment body.</body>
  </Action>
</entity-engine-xml>`

	var rows []Row
	collect := func(r Row) error { rows = append(rows, r); return nil }
	_, err := NewScanner().On("Issue", collect).On("Action", collect).Scan(strings.NewReader(mixed))
	require.NoError(t, err)

	require.Len(t, rows, 2)
	require.Equal(t, "Short", rows[0].Get("summary"), "attribute fields still read")
	require.Equal(t, "A description\nthat spans lines.", rows[0].Get("description"),
		"a long text field arrives as a child element")
	require.Equal(t, "A comment body.", rows[1].Get("body"))
}

// TestScan_ChildElementCarryingMarkupContributesItsText — a description with
// inline markup must not read as empty.
func TestScan_ChildElementCarryingMarkupContributesItsText(t *testing.T) {
	t.Parallel()

	const withMarkup = `<entity-engine-xml>
  <Issue id="1"><description>before <b>bold</b> after</description></Issue>
</entity-engine-xml>`

	var got Row
	_, err := NewScanner().On("Issue", func(r Row) error { got = r; return nil }).
		Scan(strings.NewReader(withMarkup))
	require.NoError(t, err)
	require.Equal(t, "before bold after", got.Get("description"))
}

// TestScan_AttributeWinsOverChildOfTheSameName pins the precedence rather than
// leaving it to whichever the decoder happened to see last.
func TestScan_AttributeWinsOverChildOfTheSameName(t *testing.T) {
	t.Parallel()

	const dup = `<entity-engine-xml>
  <Issue id="1" summary="from-attribute"><summary>from-child</summary></Issue>
</entity-engine-xml>`

	var got Row
	_, err := NewScanner().On("Issue", func(r Row) error { got = r; return nil }).
		Scan(strings.NewReader(dup))
	require.NoError(t, err)
	require.Equal(t, "from-attribute", got.Get("summary"))
}

// TestScan_UnknownEntityTypesAreCountedAndNamed is the preservation philosophy
// applied to parsing. The format is undocumented and drifts with every Jira
// release, so an entity nobody anticipated must appear in the report rather
// than being skipped.
func TestScan_UnknownEntityTypesAreCountedAndNamed(t *testing.T) {
	t.Parallel()

	const withNovel = `<entity-engine-xml>
  <Issue id="1"/>
  <SomeEntityAddedIn2027 id="2" whatever="x"/>
  <SomeEntityAddedIn2027 id="3"/>
  <AnotherNovelThing id="4"><nested><deep>value</deep></nested></AnotherNovelThing>
</entity-engine-xml>`

	c, err := NewScanner().Scan(strings.NewReader(withNovel))
	require.NoError(t, err)
	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)

	require.Equal(t, 2, c.Entities["SomeEntityAddedIn2027"], "an unanticipated entity is counted")
	require.Equal(t, 1, c.Entities["AnotherNovelThing"])
	require.Contains(t, c.SortedEntityNames(), "SomeEntityAddedIn2027", "and named")
	require.Equal(t, 4, c.Rows)
	require.Equal(t, c.Rows, sumMap(c.Entities))
}

// TestScan_RefusesAStreamThatIsNotAnEntityExport — reporting "0 issues" for a
// file that was never a Jira export is a lie the reader cannot detect.
func TestScan_RefusesAStreamThatIsNotAnEntityExport(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"wrong root": `<?xml version="1.0"?><some-other-document><Issue id="1"/></some-other-document>`,
		"html page":  `<html><body>Not an export</body></html>`,
		"empty":      ``,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewScanner().Scan(strings.NewReader(input))
			require.Error(t, err)
			require.ErrorIs(t, err, ErrNotAnEntityExport)
		})
	}
}

// TestScan_TruncatedStreamKeepsWhatItCounted — the defensive path. A backup cut
// off partway must report the rows it did contain, flagged, not zero and not an
// error.
func TestScan_TruncatedStreamKeepsWhatItCounted(t *testing.T) {
	t.Parallel()

	truncated := wellFormed[:len(wellFormed)-120]
	c, err := NewScanner().Scan(strings.NewReader(truncated))

	require.NoError(t, err, "a truncated export is reported, not refused")
	require.True(t, c.Truncated, "truncation must be flagged")
	require.NotEmpty(t, c.TruncationReason)
	require.Positive(t, c.Rows, "rows before the fault are kept")
	require.Equal(t, 2, c.Entities["Project"])
}

// TestScan_MalformedRowDoesNotAbortTheScan — one bad row must not cost the
// whole report.
func TestScan_MalformedRowDoesNotAbortTheScan(t *testing.T) {
	t.Parallel()

	const malformed = `<entity-engine-xml>
  <Project id="10000" key="ABC"/>
  <Issue id="1" key="ABC-1" unclosed="yes"
</entity-engine-xml>`

	c, err := NewScanner().Scan(strings.NewReader(malformed))
	require.NoError(t, err)
	require.True(t, c.Truncated)
	require.Equal(t, 1, c.Entities["Project"], "rows before the malformed one survive")
}

// TestScan_ToleratesANonUTF8Encoding — exports from older instances declare
// windows-1252, and Go's decoder refuses any non-UTF-8 declaration without a
// CharsetReader. Refusing the whole file over its first line would be a worse
// answer than reading it.
func TestScan_ToleratesANonUTF8Encoding(t *testing.T) {
	t.Parallel()

	const legacy = `<?xml version="1.0" encoding="windows-1252"?>
<entity-engine-xml><Issue id="1" key="ABC-1"/></entity-engine-xml>`

	c, err := NewScanner().Scan(strings.NewReader(legacy))
	require.NoError(t, err)
	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)
	require.Equal(t, 1, c.Entities["Issue"])
}

// TestScan_HandlerErrorStopsTheScan lets a caller bail out deliberately.
func TestScan_HandlerErrorStopsTheScan(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("stop here")
	_, err := NewScanner().On("Issue", func(Row) error { return sentinel }).
		Scan(strings.NewReader(wellFormed))
	require.ErrorIs(t, err, sentinel)
}

// TestScan_EntityTypeMatchIsCaseInsensitive — the export's casing is not a
// documented contract.
func TestScan_EntityTypeMatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	var n int
	_, err := NewScanner().On("issue", func(Row) error { n++; return nil }).
		Scan(strings.NewReader(wellFormed))
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestScan_NilReaderIsAnError(t *testing.T) {
	t.Parallel()
	_, err := NewScanner().Scan(nil)
	require.Error(t, err)
}

// TestScan_DistinctEntityCapBoundsMemoryWithoutLosingCounts mirrors the
// Confluence census bound: the map is keyed by strings the archive controls.
func TestScan_DistinctEntityCapBoundsMemoryWithoutLosingCounts(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("<entity-engine-xml>")
	const distinct = maxDistinctEntities + 100
	for i := range distinct {
		b.WriteString("<Entity")
		b.WriteString(strings.Repeat("X", 1+i%3))
		b.WriteString(itoa(i))
		b.WriteString(" id=\"1\"/>")
	}
	b.WriteString("</entity-engine-xml>")

	c, err := NewScanner().Scan(strings.NewReader(b.String()))
	require.NoError(t, err)
	require.True(t, c.NameCapReached)
	require.LessOrEqual(t, len(c.Entities), maxDistinctEntities+1)
	require.Equal(t, distinct, c.Rows, "no row is lost when the cap is reached")
	require.Equal(t, c.Rows, sumMap(c.Entities))
}

func sumMap(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}
