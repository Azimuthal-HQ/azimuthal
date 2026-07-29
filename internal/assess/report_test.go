package assess

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// update rewrites the golden files instead of comparing against them.
// Run: go test ./internal/assess/ -update
var update = flag.Bool("update", false, "rewrite the golden report files")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.WriteFile(path, got, 0o600))
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // G304 — a testdata path built in-test
	require.NoError(t, err, "golden file missing; run: go test ./internal/assess/ -update")

	// Normalise line endings so a checkout with autocrlf does not fail here.
	require.Equal(t, normalise(string(want)), normalise(string(got)),
		"report output changed; if the change is intended, run: go test ./internal/assess/ -update")
}

func normalise(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// TestReport_MarkdownGolden pins the deliverable.
//
// The markdown report is the product here, and its wording is the part a reader
// acts on — "preserved as unknown" and "unmappable" mean very different things
// to someone deciding whether to migrate. A golden file makes any change to
// that wording deliberate.
func TestReport_MarkdownGolden(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	var buf bytes.Buffer
	require.NoError(t, res.WriteMarkdown(&buf))
	checkGolden(t, "report.golden.md", buf.Bytes())
}

// TestReport_JSONGolden pins the machine-readable shape, which is a contract
// for anything built on --json.
func TestReport_JSONGolden(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	var buf bytes.Buffer
	require.NoError(t, res.WriteJSON(&buf))
	checkGolden(t, "report.golden.json", buf.Bytes())
}

// TestReport_HeadlineMatchesTheDetail is the arithmetic check a reader would
// otherwise have to do by hand: the summary table's numbers must be the same
// numbers the per-class sections add up to.
func TestReport_HeadlineMatchesTheDetail(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	h := res.Summarise()

	byVerdict := map[Verdict]int{}
	for _, c := range res.Ledger.Classes {
		for _, f := range c.Findings {
			byVerdict[f.Verdict] += f.Count
		}
	}
	require.Equal(t, h.Clean, byVerdict[VerdictClean])
	require.Equal(t, h.Approximated, byVerdict[VerdictApproximated])
	require.Equal(t, h.Preserved, byVerdict[VerdictPreserved])
	require.Equal(t, h.Unmappable, byVerdict[VerdictUnmappable])
}

// TestReport_IsDeterministic — the same input must render identically twice, or
// the golden files are noise.
func TestReport_IsDeterministic(t *testing.T) {
	t.Parallel()

	var a, b bytes.Buffer
	require.NoError(t, runFixtures(t).WriteMarkdown(&a))
	require.NoError(t, runFixtures(t).WriteMarkdown(&b))
	require.Equal(t, a.String(), b.String())
}

// TestReport_NamesEveryAssumption — the assumptions section is part of the
// deliverable, not a footnote. A reader deciding whether to trust the numbers
// needs to know which rest on an inference about an undocumented format.
func TestReport_NamesEveryAssumption(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	var buf bytes.Buffer
	require.NoError(t, res.WriteMarkdown(&buf))

	require.NotEmpty(t, res.Assumptions)
	for _, a := range res.Assumptions {
		require.Contains(t, buf.String(), a, "every assumption must reach the report")
	}
	require.Contains(t, buf.String(), "Neither export format is documented as a contract")
}

// TestStreaming_MemoryDoesNotTrackTheExportSize proves the streaming claim
// rather than asserting it.
//
// The export is generated on the fly by a reader that never holds it, so the
// input contributes nothing to the heap and whatever the measurement shows is
// the parsers' own footprint. If they accumulated rows, or retained page bodies
// instead of censusing and releasing them, growth would track the export size.
//
// Feeding a pre-built string would have made this vacuous: the string alone
// would sit on the heap and cover a full second copy inside any sane budget.
func TestStreaming_MemoryDoesNotTrackTheExportSize(t *testing.T) {
	// Not parallel: it measures process-wide heap.
	const (
		issues = 20000
		pages  = 20000
	)

	total := generatedSize(jiraGen(issues)) + generatedSize(confluenceGen(pages))
	require.Greater(t, total, int64(24<<20),
		"the generated export must be large enough for the bound to mean something")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	res, err := RunReaders(jiraGen(issues), confluenceGen(pages))
	require.NoError(t, err)
	require.NoError(t, res.Ledger.Reconcile())

	runtime.ReadMemStats(&after)
	growth := heapDelta(before.HeapAlloc, after.HeapAlloc)

	// Generous in absolute terms but a small fraction of the export: a parser
	// that buffered would land at or above `total`, an order of magnitude out.
	const budget = int64(16 << 20)
	require.Less(t, growth, budget,
		"heap grew by %d bytes while streaming a %d byte export; the readers are accumulating rather than streaming",
		growth, total)

	// And the assessment actually saw all of it.
	require.Equal(t, issues, findClass(t, res, "Jira issues → project items").Observed)
	require.Equal(t, pages, findClass(t, res, "Confluence pages → Codex pages").Observed)
}

// generatedSize drains a generator to measure what it would have produced,
// without keeping any of it.
func generatedSize(r io.Reader) int64 {
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return 0
	}
	return n
}

// chunkReader emits a prefix, then n per-index chunks, then a suffix — as a
// stream, never materialising the whole document.
type chunkReader struct {
	prefix, suffix string
	n              int
	chunk          func(i int) string

	i    int
	buf  string
	pos  int
	sent bool
	done bool
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for c.pos >= len(c.buf) {
		switch {
		case !c.sent:
			c.buf, c.pos, c.sent = c.prefix, 0, true
		case c.i < c.n:
			c.buf, c.pos = c.chunk(c.i), 0
			c.i++
		case !c.done:
			c.buf, c.pos, c.done = c.suffix, 0, true
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, c.buf[c.pos:])
	c.pos += n
	return n, nil
}

func jiraGen(issues int) io.Reader {
	desc := strings.Repeat("body text ", 20)
	comment := strings.Repeat("comment ", 20)
	return &chunkReader{
		prefix: "<entity-engine-xml>\n" + `<Project id="1" key="BIG" name="Big"/>` + "\n",
		suffix: "</entity-engine-xml>\n",
		n:      issues,
		chunk: func(i int) string {
			return fmt.Sprintf(
				`<Issue id="%d" key="BIG-%d" project="1" type="1" status="1" priority="3" summary="Issue %d"><description>%s</description></Issue>`+"\n"+
					`<Action id="%d" issue="%d" type="comment" body="%s"/>`+"\n",
				i+100, i+1, i+1, desc, i+900000, i+100, comment)
		},
	}
}

func confluenceGen(pages int) io.Reader {
	body := `<p>` + strings.Repeat("page text ", 30) + `</p>` +
		`<ac:structured-macro ac:name="info"><ac:rich-text-body><p>note</p></ac:rich-text-body></ac:structured-macro>` +
		`<ac:structured-macro ac:name="drawio"/>` +
		`<ac:link><ri:page ri:content-title="Other"/></ac:link>`
	return &chunkReader{
		prefix: "<hibernate-generic>\n" +
			`<object class="Space"><id name="id">1</id><property name="key"><![CDATA[BIG]]></property></object>` + "\n",
		suffix: "</hibernate-generic>\n",
		n:      pages,
		chunk: func(i int) string {
			return fmt.Sprintf(
				`<object class="Page"><id name="id">%d</id><property name="title"><![CDATA[Page %d]]></property>`+
					`<property name="contentStatus">current</property>`+
					`<collection name="bodyContents"><element class="BodyContent"><id name="id">%d</id></element></collection></object>`+"\n"+
					`<object class="BodyContent"><id name="id">%d</id><property name="body"><![CDATA[%s]]></property></object>`+"\n",
				i+1000, i, i+50000, i+50000, body)
		},
	}
}

// heapDelta computes after-before without an unchecked uint64 to int64 cast.
//
// HeapAlloc can legitimately fall between samples, so the signed result is
// derived from the unsigned pair rather than by converting each side. The
// magnitude is clamped to MaxInt64 so the conversion is provably in range: a
// heap that really moved by more than eight exabytes is not a case this test
// needs to distinguish, and clamping keeps it a failure rather than a wrapped
// negative that would read as a pass.
func heapDelta(before, after uint64) int64 {
	if after >= before {
		return clampToInt64(after - before)
	}
	return -clampToInt64(before - after)
}

func clampToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}
