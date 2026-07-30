package assess

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess/jql"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304 — a testdata path built in-test
	require.NoError(t, err)
	return string(b)
}

func runFixtures(t *testing.T) *Result {
	t.Helper()
	res, err := RunReaders(
		strings.NewReader(readFixture(t, "jira_entities.xml")),
		strings.NewReader(readFixture(t, "confluence_entities.xml")),
	)
	require.NoError(t, err)
	return res
}

// TestRun_ReconcilesOverBothFixtures is the invariant the whole report rests
// on: every entity read lands in exactly one bucket, and the row-based classes
// sum to what the parsers actually counted.
//
// RunReaders already calls both reconciliations, so a classification bug fails
// this test by returning an error rather than by producing a plausible-looking
// report that quietly totals to less than its input.
func TestRun_ReconcilesOverBothFixtures(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)

	require.NoError(t, res.Ledger.Reconcile())
	require.NoError(t, res.Ledger.ReconcileRows(res.totalRows()))

	h := res.Summarise()
	require.Equal(t, h.Total, h.Clean+h.Approximated+h.Preserved+h.Unmappable,
		"the four buckets must partition the whole ledger")
	require.Positive(t, h.Total)

	// The headline percentages must sum to 100 (within float tolerance).
	require.InDelta(t, 100.0, h.CleanPct+h.ApproxPct+h.PreservedPct+h.LostPct, 0.01)
}

// TestRun_RowArithmeticCatchesADoubleCount is the negative half of the
// invariant above, and the reason ReconcileRows exists at all.
//
// A remainder class always sums to itself, so Reconcile alone would pass
// however wrong the classification was. Claiming the same rows twice must fail.
func TestRun_RowArithmeticCatchesADoubleCount(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	parsed := res.totalRows()
	require.NoError(t, res.Ledger.ReconcileRows(parsed))

	// Simulate a classifier claiming rows a second time.
	dup := res.Ledger.Class("Jira issues → project items")
	dup.Observed += 5
	dup.Add(VerdictClean, 5, "double-counted")

	require.NoError(t, dup.reconcile(), "the class itself still balances — that is the trap")
	err := res.Ledger.ReconcileRows(parsed)
	require.ErrorIs(t, err, ErrUnreconciled)
	require.Contains(t, err.Error(), "account for")
}

// TestRun_UnclassifiedEntitiesAreCountedNotDropped — the fixture deliberately
// contains entity types the assessor does not classify. They must appear in the
// totals, not vanish from them.
func TestRun_UnclassifiedEntitiesAreCountedNotDropped(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)

	other := findClass(t, res, "Other Jira entities")
	require.Positive(t, other.Observed, "the fixture contains unclassified entity types")
	require.NotEmpty(t, other.Findings[0].Detail, "and they are named, not merely counted")
	require.Contains(t, strings.Join(other.Findings[0].Detail, " "), "OSPropertyEntry")
}

// TestRun_DetectsCrossExportKeyCollision is the case that justifies accepting
// both exports at once: a Jira project and a Confluence space claiming the same
// space key is invisible in either export alone.
func TestRun_DetectsCrossExportKeyCollision(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)

	var cross *Collision
	for i := range res.Collisions {
		if res.Collisions[i].CrossExport {
			cross = &res.Collisions[i]
			break
		}
	}
	require.NotNil(t, cross, "the fixtures share a key across the two exports")
	require.Equal(t, "DOCS", cross.Key)
	require.Len(t, cross.Origins, 2)
	require.Contains(t, cross.Describe(), "neither could reveal alone")
}

// TestRun_ReportsKeyCoercions — a Confluence key that must change shape changes
// every item key derived from it.
func TestRun_ReportsKeyCoercions(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)

	var found bool
	for _, c := range res.Coercions {
		if c.Original == "my-team-notes" {
			found = true
			// "my-team-notes" → uppercased, hyphens stripped → "MYTEAMNOTES"
			// (11) → truncated to the 10-character limit.
			require.Equal(t, "MYTEAMNOTE", c.Coerced, "lowercase, hyphens and length all violate ^[A-Z0-9]{1,10}$")
			require.Regexp(t, SpaceKeyPattern, c.Coerced)
		}
	}
	require.True(t, found, "coercions: %+v", res.Coercions)
}

// TestRun_ClassifiesSavedFilters — the JQL half reaching the report.
func TestRun_ClassifiesSavedFilters(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	require.Len(t, res.Filters, 4)

	cl := findClass(t, res, "Jira saved filters (JQL)")
	require.Equal(t, 4, cl.Observed)
	require.Equal(t, 4, cl.Classified())

	// The fixture spans all three verdicts, and each is asserted BY NAME rather
	// than by a "positive count" that any one of them could satisfy.
	//
	// The date filter is the one that moved. Under filter document v1 it was
	// unmappable — v1 had no date predicate at all — and v2's ranges recovered
	// it. Asserting it is now CLEAN is what stops a later change from quietly
	// putting the largest bucket in the report back where it was.
	byQuery := map[string]jql.Expressibility{}
	for _, f := range res.Filters {
		byQuery[f.Raw] = f.Verdict
	}
	require.Equal(t, jql.Expressible,
		byQuery[`project = DOCS AND status = Open AND assignee = currentUser()`])
	require.Equal(t, jql.Partial, byQuery[`text ~ "pool"`])
	require.Equal(t, jql.Expressible,
		byQuery[`project IN (DOCS, PLAT) AND created >= -30d ORDER BY created DESC`],
		"the relative date clause is expressible under filter document v2")
	require.Equal(t, jql.NotExpressible,
		byQuery[`assignee = currentUser() OR status != Done`],
		"an OR across two fields has no representation in v2 either, by decision")
	require.Positive(t, cl.CountBy(VerdictUnmappable))
}

// TestRun_RestrictedCommentsAreUnmappable — the comments table has no
// visibility column, so importing a restricted comment would widen who can
// read it. That is a data-exposure change, not an approximation.
func TestRun_RestrictedCommentsAreUnmappable(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	cl := findClass(t, res, "Jira comments")

	require.Equal(t, 3, cl.Observed)
	require.Equal(t, 1, cl.CountBy(VerdictUnmappable), "one comment carries a rolelevel restriction")
	require.Equal(t, 2, cl.CountBy(VerdictClean))

	var reason string
	for _, f := range cl.Findings {
		if f.Verdict == VerdictUnmappable {
			reason = f.Reason
		}
	}
	require.Contains(t, reason, "no visibility column")
}

// TestRun_ConfluenceMacrosSplitAcrossBuckets — the Confluence half's centre of
// gravity, and the place ADR-0012's carrier does its work.
func TestRun_ConfluenceMacrosSplitAcrossBuckets(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	cl := findClass(t, res, "Confluence macros → Codex nodes")

	require.True(t, cl.Derived, "macro occurrences are not exported rows")
	require.Positive(t, cl.CountBy(VerdictClean), "info/code/toc have first-class nodes")
	require.Positive(t, cl.CountBy(VerdictPreserved), "drawio has none and is preserved")
	require.Equal(t, cl.Observed, cl.Classified())

	var preserved string
	for _, f := range cl.Findings {
		if f.Verdict == VerdictPreserved {
			preserved = strings.Join(f.Detail, " ")
		}
	}
	require.Contains(t, preserved, "drawio", "an unmapped macro is named, not just counted")
}

// TestRun_LivePagesAreSeparatedFromRevisions — a space export carries one Page
// object per revision, so counting them all would overstate the page count.
func TestRun_LivePagesAreSeparatedFromRevisions(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	cl := findClass(t, res, "Confluence pages → Codex pages")

	require.Equal(t, 3, cl.Observed, "three Page objects in the fixture")
	require.Equal(t, 2, cl.CountBy(VerdictClean), "two are live")
	require.Equal(t, 1, cl.CountBy(VerdictApproximated), "one is a historical revision")
	require.Contains(t, strings.Join(cl.Notes, " "), "page_revisions")
}

func TestRun_RefusesWithNoInput(t *testing.T) {
	t.Parallel()

	_, err := Run(Input{})
	require.ErrorIs(t, err, ErrNoInput)
}

// TestRun_TruncatedExportIsReportedNotRefused — the defensive path end to end.
func TestRun_TruncatedExportIsReportedNotRefused(t *testing.T) {
	t.Parallel()

	full := readFixture(t, "jira_entities.xml")
	res, err := RunReaders(strings.NewReader(full[:len(full)*2/3]), nil)
	require.NoError(t, err, "a truncated export is assessed, not refused")

	require.True(t, res.Sources[0].Truncated)
	require.NotEmpty(t, res.Sources[0].TruncationReason)
	require.NoError(t, res.Ledger.Reconcile(), "and the partial assessment still reconciles")

	var md bytes.Buffer
	require.NoError(t, res.WriteMarkdown(&md))
	require.Contains(t, md.String(), "could not be read to the end",
		"the report must say the totals understate the export")
}

// TestRun_JiraOnlyAndConfluenceOnly — each export assessable alone.
func TestRun_EachExportAssessableAlone(t *testing.T) {
	t.Parallel()

	jiraOnly, err := RunReaders(strings.NewReader(readFixture(t, "jira_entities.xml")), nil)
	require.NoError(t, err)
	require.Len(t, jiraOnly.Sources, 1)
	require.Empty(t, jiraOnly.Collisions, "a collision needs two claimants")

	confOnly, err := RunReaders(nil, strings.NewReader(readFixture(t, "confluence_entities.xml")))
	require.NoError(t, err)
	require.Len(t, confOnly.Sources, 1)
}

func TestWriteJSON_IsValidAndCarriesTheHeadline(t *testing.T) {
	t.Parallel()

	res := runFixtures(t)
	var buf bytes.Buffer
	require.NoError(t, res.WriteJSON(&buf))

	require.Contains(t, buf.String(), `"headline"`)
	require.Contains(t, buf.String(), `"clean_pct"`)
	require.Contains(t, buf.String(), `"assumptions"`)
}

func findClass(t *testing.T, res *Result, name string) *Class {
	t.Helper()
	for _, c := range res.Ledger.Classes {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("class %q not found in ledger", name)
	return nil
}
