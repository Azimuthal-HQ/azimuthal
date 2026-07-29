package assess

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReconcile_AcceptsAPartitionedClass is the baseline: findings that sum to
// the observed count are accepted.
func TestReconcile_AcceptsAPartitionedClass(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("issues")
	c.Observed = 10
	c.Add(VerdictClean, 6, "type and status both resolve to seeded values")
	c.Add(VerdictApproximated, 3, "issue type slug coerced to the item_types slug format")
	c.Add(VerdictUnmappable, 1, "issue references a deleted project")

	require.NoError(t, l.Reconcile())
	require.Equal(t, 10, c.Classified())
	require.Equal(t, 6, c.CountBy(VerdictClean))
	require.Equal(t, 0, c.CountBy(VerdictPreserved))
}

// TestReconcile_RefusesAnUndercountedClass is the defect this invariant exists
// to catch: a classifier that forgets a group produces a report whose totals
// silently understate the input.
//
// This test fails if Reconcile's sum check is deleted — that is the point of
// it. Verified in both directions: with the check removed from
// (*Class).reconcile, this case returns nil and the test fails.
func TestReconcile_RefusesAnUndercountedClass(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("issues")
	c.Observed = 10
	c.Add(VerdictClean, 6, "resolves cleanly")
	// The remaining 4 were never classified.

	err := l.Reconcile()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnreconciled)
	require.Contains(t, err.Error(), "observed 10 entities but classified 6")
}

// TestReconcile_RefusesAnOvercountedClass catches the opposite error: an entity
// counted in two buckets inflates the headline percentages.
func TestReconcile_RefusesAnOvercountedClass(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("pages")
	c.Observed = 5
	c.Add(VerdictClean, 4, "every macro maps to an implemented node")
	c.Add(VerdictPreserved, 3, "unrecognised macro preserved in an unknownContent carrier")

	err := l.Reconcile()
	require.ErrorIs(t, err, ErrUnreconciled)
	require.Contains(t, err.Error(), "observed 5 entities but classified 7")
}

// TestReconcile_RefusesAReasonlessFinding keeps the report's value intact. A
// count with no reason is a number the reader cannot act on.
func TestReconcile_RefusesAReasonlessFinding(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("custom_fields")
	c.Observed = 1
	c.Findings = append(c.Findings, Finding{Verdict: VerdictUnmappable, Count: 1, Reason: ""})

	err := l.Reconcile()
	require.ErrorIs(t, err, ErrUnreconciled)
	require.Contains(t, err.Error(), "no reason")
}

// TestReconcile_RefusesAnUnknownVerdict stops a typo'd bucket from vanishing
// from the totals while still summing correctly.
func TestReconcile_RefusesAnUnknownVerdict(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("comments")
	c.Observed = 2
	c.Findings = append(c.Findings, Finding{Verdict: Verdict("mostly"), Count: 2, Reason: "typo'd bucket"})

	err := l.Reconcile()
	require.ErrorIs(t, err, ErrUnreconciled)
	require.Contains(t, err.Error(), "unknown verdict")
}

// TestReconcile_EmptyClassIsConsistent — a class with nothing in the export is
// legal and reconciles at zero. It must not be an error, because an export that
// simply has no sprints is not a defect.
func TestReconcile_EmptyClassIsConsistent(t *testing.T) {
	t.Parallel()

	var l Ledger
	l.Class("sprints").Observed = 0

	require.NoError(t, l.Reconcile())
	require.Equal(t, 0, l.Total())
}

// TestAdd_IgnoresNonPositiveCounts lets classifiers add unconditionally from a
// counter map without emitting empty rows.
func TestAdd_IgnoresNonPositiveCounts(t *testing.T) {
	t.Parallel()

	var c Class
	c.Add(VerdictClean, 0, "nothing here")
	c.Add(VerdictClean, -3, "negative")
	require.Empty(t, c.Findings)
}

// TestReconcile_SortsFindingsDeterministically underwrites the golden-file
// tests: the same ledger must render identically on every run, whatever order
// the classifiers happened to append in.
func TestReconcile_SortsFindingsDeterministically(t *testing.T) {
	t.Parallel()

	build := func() *Ledger {
		var l Ledger
		c := l.Class("issues")
		c.Observed = 30
		c.Add(VerdictUnmappable, 5, "z-reason")
		c.Add(VerdictClean, 10, "b-reason")
		c.Add(VerdictClean, 10, "a-reason")
		c.Add(VerdictPreserved, 5, "y-reason")
		return &l
	}

	l := build()
	require.NoError(t, l.Reconcile())

	got := make([]string, 0, len(l.Classes[0].Findings))
	for _, f := range l.Classes[0].Findings {
		got = append(got, string(f.Verdict)+":"+f.Reason)
	}
	// Fidelity order first; equal counts within a bucket break on reason.
	require.Equal(t, []string{"clean:a-reason", "clean:b-reason", "preserved:y-reason", "unmappable:z-reason"}, got)
}

// TestClass_ReturnsTheSameInstance — the ledger is accumulated by several
// classifiers naming the same class, so Class must not create duplicates.
func TestClass_ReturnsTheSameInstance(t *testing.T) {
	t.Parallel()

	var l Ledger
	a := l.Class("issues")
	a.Observed = 3
	b := l.Class("issues")
	b.Add(VerdictClean, 3, "fine")

	require.Len(t, l.Classes, 1)
	require.Same(t, a, b)
	require.NoError(t, l.Reconcile())
}

// TestTotals_SumAcrossClasses backs the headline percentages.
func TestTotals_SumAcrossClasses(t *testing.T) {
	t.Parallel()

	var l Ledger
	issues := l.Class("issues")
	issues.Observed = 8
	issues.Add(VerdictClean, 5, "ok")
	issues.Add(VerdictApproximated, 3, "coerced")

	pages := l.Class("pages")
	pages.Observed = 4
	pages.Add(VerdictPreserved, 4, "unrecognised macros")

	require.NoError(t, l.Reconcile())
	require.Equal(t, 12, l.Total())
	require.Equal(t, 5, l.TotalBy(VerdictClean))
	require.Equal(t, 3, l.TotalBy(VerdictApproximated))
	require.Equal(t, 4, l.TotalBy(VerdictPreserved))
	require.Equal(t, 0, l.TotalBy(VerdictUnmappable))

	// The four buckets partition the whole ledger — this is the arithmetic the
	// report headline claims, checked rather than asserted.
	sum := 0
	for _, v := range AllVerdicts {
		sum += l.TotalBy(v)
	}
	require.Equal(t, l.Total(), sum)
}

// TestVerdict_RankCoversEveryDefinedBucket stops a newly added verdict from
// silently sorting last and being omitted from AllVerdicts (and so from every
// total in the report).
func TestVerdict_RankCoversEveryDefinedBucket(t *testing.T) {
	t.Parallel()

	// Labels are asserted exactly: they are the column headings a reader uses to
	// judge the migration, so a silent change to one is a change to the report's
	// meaning. An undefined verdict falls through Label's default and returns
	// its own wire value, which is how a missing case would show up here.
	want := map[Verdict]string{
		VerdictClean:        "maps cleanly",
		VerdictApproximated: "maps with approximation",
		VerdictPreserved:    "preserved as unknown",
		VerdictUnmappable:   "unmappable",
	}
	for _, v := range AllVerdicts {
		require.True(t, v.Valid(), "verdict %q must be in AllVerdicts", v)
		require.Equal(t, want[v], v.Label(), "verdict %q label", v)
	}
	require.Len(t, want, len(AllVerdicts), "every verdict in AllVerdicts needs an asserted label")

	require.False(t, Verdict("invented").Valid())
	require.Equal(t, len(AllVerdicts), Verdict("invented").Rank(), "an unknown verdict sorts last")
	require.Len(t, AllVerdicts, 4)
}

func TestErrUnreconciled_IsMatchable(t *testing.T) {
	t.Parallel()

	var l Ledger
	c := l.Class("x")
	c.Observed = 1
	require.True(t, errors.Is(l.Reconcile(), ErrUnreconciled))
}
