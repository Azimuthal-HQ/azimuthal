package confluence

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const spaceExport = `<?xml version="1.0" encoding="UTF-8"?>
<hibernate-generic datetime="2026-07-29 10:00:00">
  <object class="Space" package="com.atlassian.confluence.spaces">
    <id name="id">12345</id>
    <property name="key"><![CDATA[DOCS]]></property>
    <property name="name"><![CDATA[Documentation]]></property>
  </object>
  <object class="Page" package="com.atlassian.confluence.pages">
    <id name="id">98</id>
    <property name="title"><![CDATA[My page]]></property>
    <property name="version">3</property>
    <property name="contentStatus">current</property>
    <property name="space" class="Space" package="com.atlassian.confluence.spaces">
      <id name="id">12345</id>
    </property>
    <collection name="bodyContents" class="java.util.Collection">
      <element class="BodyContent" package="com.atlassian.confluence.core">
        <id name="id">54321</id>
      </element>
    </collection>
    <collection name="historicalVersions" class="java.util.Collection">
      <element class="Page" package="com.atlassian.confluence.pages"><id name="id">97</id></element>
      <element class="Page" package="com.atlassian.confluence.pages"><id name="id">96</id></element>
    </collection>
  </object>
  <object class="Page" package="com.atlassian.confluence.pages">
    <id name="id">97</id>
    <property name="title"><![CDATA[My page]]></property>
    <property name="version">2</property>
    <property name="contentStatus">current</property>
    <property name="originalVersion" class="Page" package="com.atlassian.confluence.pages">
      <id name="id">98</id>
    </property>
  </object>
  <object class="BodyContent" package="com.atlassian.confluence.core">
    <id name="id">54321</id>
    <property name="body"><![CDATA[<p>Hello</p><ac:structured-macro ac:name="info"/>]]></property>
    <property name="content" class="Page" package="com.atlassian.confluence.pages">
      <id name="id">98</id>
    </property>
  </object>
  <object class="Attachment" package="com.atlassian.confluence.pages">
    <id name="id">777</id>
    <property name="fileName"><![CDATA[diagram.png]]></property>
    <property name="containerContent" class="Page" package="com.atlassian.confluence.pages">
      <id name="id">98</id>
    </property>
  </object>
</hibernate-generic>`

func TestScan_CountsEveryObjectByClass(t *testing.T) {
	t.Parallel()

	c, err := NewScanner().Scan(strings.NewReader(spaceExport))
	require.NoError(t, err)
	require.False(t, c.Truncated, "reason: %s", c.TruncationReason)

	require.Equal(t, map[string]int{
		"Space": 1, "Page": 2, "BodyContent": 1, "Attachment": 1,
	}, c.Objects)
	require.Equal(t, 5, c.Total)
}

// TestScan_ScalarAndReferencePropertiesAreKeptApart is the discriminator the
// format turns on: a property is either character data or a nested <id>
// reference. Merging them into one map would let a caller asking for a page
// title receive an object id instead.
func TestScan_ScalarAndReferencePropertiesAreKeptApart(t *testing.T) {
	t.Parallel()

	var pages []Object
	_, err := NewScanner().On("Page", func(o Object) error {
		pages = append(pages, o)
		return nil
	}).Scan(strings.NewReader(spaceExport))
	require.NoError(t, err)

	require.Len(t, pages, 2)
	p := pages[0]

	require.Equal(t, "98", p.ID)
	require.Equal(t, "My page", p.Prop("title"), "a CDATA scalar")
	require.Equal(t, "3", p.Prop("version"))
	require.Equal(t, ContentStatusCurrent, p.Prop("contentStatus"))

	require.Equal(t, "12345", p.Refs["space"], "a reference property yields the referenced id")
	require.Empty(t, p.Props["space"], "and does not also appear as a scalar")
	require.Empty(t, p.Refs["title"], "a scalar does not appear as a reference")
}

// TestScan_CollectionsYieldMemberIDsInOrder — the bodyContents indirection is
// how a page reaches its body at all, so losing it means every page reads as
// empty.
func TestScan_CollectionsYieldMemberIDsInOrder(t *testing.T) {
	t.Parallel()

	var p Object
	_, err := NewScanner().On("Page", func(o Object) error {
		if o.ID == "98" {
			p = o
		}
		return nil
	}).Scan(strings.NewReader(spaceExport))
	require.NoError(t, err)

	require.Equal(t, []string{"54321"}, p.Collections["bodyContents"])
	require.Equal(t, []string{"97", "96"}, p.Collections["historicalVersions"],
		"members keep document order")
}

// TestScan_BodyContentCarriesTheStorageFormat — and it round-trips into the
// storage-format census, which is the whole Confluence assessment path.
func TestScan_BodyContentCarriesTheStorageFormat(t *testing.T) {
	t.Parallel()

	var body string
	_, err := NewScanner().On("BodyContent", func(o Object) error {
		body = o.Prop("body")
		return nil
	}).Scan(strings.NewReader(spaceExport))
	require.NoError(t, err)

	require.Contains(t, body, "<ac:structured-macro")
	census := ScanBodyString(body)
	require.Equal(t, 1, census.Macros["info"])
	require.Equal(t, 1, census.HTMLElements["p"])
}

// TestScan_HistoricalRevisionsAreSeparateObjects is the counting trap: a space
// export holds one object per revision, so a parser that counts every Page
// object reports several times the real page count.
func TestScan_HistoricalRevisionsAreSeparateObjects(t *testing.T) {
	t.Parallel()

	c, err := NewScanner().Scan(strings.NewReader(spaceExport))
	require.NoError(t, err)

	require.Equal(t, 2, c.Objects["Page"],
		"both the live page and its revision are Page objects; the assessor must not report 2 pages")
}

func TestScan_UnknownObjectClassesAreCountedAndNamed(t *testing.T) {
	t.Parallel()

	const novel = `<hibernate-generic>
  <object class="Page"><id name="id">1</id></object>
  <object class="SomeClassAddedIn2027"><id name="id">2</id><property name="x">y</property></object>
  <object class="SomeClassAddedIn2027"><id name="id">3</id></object>
  <object><id name="id">4</id></object>
</hibernate-generic>`

	c, err := NewScanner().Scan(strings.NewReader(novel))
	require.NoError(t, err)
	require.Equal(t, 2, c.Objects["SomeClassAddedIn2027"])
	require.Equal(t, 1, c.Objects["(object with no class)"], "an unclassed object is named, not dropped")
	require.Equal(t, 4, c.Total)
}

func TestScan_RefusesAStreamThatIsNotASpaceExport(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"jira export": `<entity-engine-xml><Issue id="1"/></entity-engine-xml>`,
		"html":        `<html><body>nope</body></html>`,
		"empty":       ``,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewScanner().Scan(strings.NewReader(input))
			require.ErrorIs(t, err, ErrNotASpaceExport)
		})
	}
}

func TestScan_TruncatedExportKeepsWhatItCounted(t *testing.T) {
	t.Parallel()

	c, err := NewScanner().Scan(strings.NewReader(spaceExport[:len(spaceExport)-400]))
	require.NoError(t, err)
	require.True(t, c.Truncated)
	require.NotEmpty(t, c.TruncationReason)
	require.Positive(t, c.Total)
	require.Equal(t, 1, c.Objects["Space"])
}

// TestCountCDATATerminatorEscapes — Confluence rewrites a body's own "]]>" to
// "]] >" because nested CDATA is illegal. The rewrite is ambiguous, so the
// assessor counts and reports rather than un-escaping and corrupting the bodies
// that legitimately contained "]] >".
func TestCountCDATATerminatorEscapes(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, CountCDATATerminatorEscapes(`<p>ordinary</p>`))
	require.Equal(t, 2, CountCDATATerminatorEscapes(`a ]] > b ]] > c`))
}

func TestScan_NilReaderIsAnError(t *testing.T) {
	t.Parallel()
	_, err := NewScanner().Scan(nil)
	require.Error(t, err)
}

func TestScan_HandlerErrorStopsTheScanAndIsRecorded(t *testing.T) {
	t.Parallel()

	c, err := NewScanner().On("Page", func(Object) error {
		return errRefused
	}).Scan(strings.NewReader(spaceExport))

	require.NoError(t, err)
	require.True(t, c.Truncated)
	require.Contains(t, c.TruncationReason, "refused")
}

var errRefused = errStr("refused")

type errStr string

func (e errStr) Error() string { return string(e) }
