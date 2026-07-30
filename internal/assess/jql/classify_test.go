package jql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// TestVocabulary_MatchesTheRealFilterDocument is the drift guard.
//
// This package classifies JQL against a target vocabulary it names as string
// constants. If internal/core/views/filter.go gains, loses or renames a field,
// those constants become a description of a filter that no longer exists — and
// the failure is silent, because the classifier keeps producing confident
// verdicts about the wrong vocabulary. The report would tell a self-hoster
// their filters translate when they do not.
//
// So the constants are checked against the real struct's JSON tags, which are
// the wire contract. Fails in both directions: a field added to views.Filter
// without a mapping here, and a mapping here naming a field views.Filter does
// not have.
func TestVocabulary_MatchesTheRealFilterDocument(t *testing.T) {
	t.Parallel()

	defined := make(map[string]struct{})
	rt := reflect.TypeOf(views.Filter{})
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			defined[name] = struct{}{}
		}
	}

	// "modules" is chosen when the view is created, not by a JQL clause, so it
	// is the one filter field no mapping targets.
	require.Contains(t, defined, "modules")
	targetable := map[string]struct{}{}
	for name := range defined {
		if name != "modules" {
			targetable[name] = struct{}{}
		}
	}

	named := map[string]struct{}{
		fieldSpaceIDs: {}, fieldStatuses: {}, fieldPriorities: {},
		fieldAssignees: {}, fieldKinds: {}, fieldSprintIDs: {}, fieldText: {},
		// v2. The four date ranges are targets like any other field; `not` is a
		// modifier that no clause maps ONTO, but it still has to be named here
		// — the guard's whole point is that this package has an opinion about
		// every part of the document, and "negation flips these six fields and
		// no others" is an opinion that goes stale exactly as fast as the rest.
		fieldCreatedAt: {}, fieldUpdatedAt: {}, fieldDueAt: {}, fieldResolvedAt: {},
		fieldNot: {},
	}

	for f := range named {
		require.Contains(t, targetable, f,
			"this package maps onto filter field %q, which views.Filter no longer has", f)
	}
	for f := range targetable {
		require.Contains(t, named, f,
			"views.Filter gained field %q with no JQL mapping — classification is now incomplete", f)
	}

	// Every mapping's target must be one of the named fields.
	for jqlField, m := range jqlFieldMap {
		require.Contains(t, named, m.filterField,
			"JQL field %q maps to unknown filter field %q", jqlField, m.filterField)
	}
}

// TestVocabulary_NegatableFieldsMatchTheRealNegateRecord is the drift guard for
// v2's `not` record, and it exists because the guard above cannot see it.
//
// TestVocabulary_MatchesTheRealFilterDocument walks views.Filter's JSON tags,
// so it sees that a field called "not" exists and nothing more. WHICH fields
// that record can negate is a second vocabulary, duplicated here as
// negatableFields — and a classifier that believed the wrong six would report
// confident verdicts about a negation the server refuses, which is the exact
// failure the first guard was written to prevent, one level down.
//
// Fails in both directions: a field added to views.Negate with no entry here,
// and an entry here naming a field views.Negate does not have.
func TestVocabulary_NegatableFieldsMatchTheRealNegateRecord(t *testing.T) {
	t.Parallel()

	defined := map[string]struct{}{}
	rt := reflect.TypeOf(views.Negate{})
	for i := range rt.NumField() {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			defined[name] = struct{}{}
		}
	}

	for f := range defined {
		require.Contains(t, negatableFields, f,
			"views.Negate can negate %q and this package does not know it — a JQL negation on that "+
				"field is being reported as unmappable when it maps", f)
	}
	for f := range negatableFields {
		require.Contains(t, defined, f,
			"this package believes %q is negatable and views.Negate has no such field — a JQL "+
				"negation on it is being reported as mappable when the server would refuse it", f)
	}

	// The four date fields must NOT be negatable. v2 refuses date negation by
	// having no key for it, and the classifier's story about why depends on
	// that staying true.
	for _, d := range []string{fieldCreatedAt, fieldUpdatedAt, fieldDueAt, fieldResolvedAt} {
		require.NotContains(t, defined, d,
			"views.Negate gained the date field %q; v2's stated design is that a range's bounds "+
				"already express exclusion, and the classifier says so", d)
	}
}

// TestVocabulary_SortFieldsMatchTheRealSortVocabulary guards the ORDER BY half
// the same way. views.Sort's allowed set lives in an unexported map, so it is
// probed through the exported validator instead.
func TestVocabulary_SortFieldsMatchTheRealSortVocabulary(t *testing.T) {
	t.Parallel()

	accepts := func(field string) bool {
		q := views.Query{
			V:      views.Version,
			Filter: views.Filter{Modules: []views.Module{views.ModuleVector}},
			Sort:   views.Sort{Field: field, Dir: "asc"},
		}
		return q.Validate() == nil
	}

	for jqlName, azName := range sortableFields {
		require.True(t, accepts(azName),
			"ORDER BY %q maps to sort field %q, which views no longer accepts", jqlName, azName)
	}
	require.False(t, accepts("status"), "status must remain unsortable")
	require.False(t, accepts("invented_field"))
}

func TestClassify_ProjectMapsToSpaceIDs(t *testing.T) {
	t.Parallel()

	q := Classify(`project = ABC`)
	require.Len(t, q.Clauses, 1)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Equal(t, "space_ids", q.Clauses[0].FilterField)
	require.Equal(t, Expressible, q.Verdict)
}

func TestClassify_StatusAndAssigneeAreExpressible(t *testing.T) {
	t.Parallel()

	q := Classify(`status = "In Progress" AND assignee = currentUser()`)
	require.Len(t, q.Clauses, 2)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Equal(t, "statuses", q.Clauses[0].FilterField)

	require.Equal(t, Expressible, q.Clauses[1].Verdict, "currentUser() is exactly the \"me\" token")
	require.Contains(t, q.Clauses[1].Reason, `"me"`)
	require.Equal(t, Expressible, q.Verdict)
}

// TestClassify_DateClausesAgainstV2Ranges replaces the v1 test that asserted
// every date clause was lost.
//
// v1's assertion was a single fact — "all of these are NotExpressible" — and
// it is no longer true. What replaces it is stricter, not weaker: every case
// names its EXACT verdict, so the test fails if a clause is over-promoted as
// readily as if one is under-promoted. Roughly half of these still do not
// translate, and those halves are the point.
func TestClassify_DateClausesAgainstV2Ranges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		jql    string
		want   Expressibility
		reason string
	}{
		// Exact operator, exact value: >= is the inclusive `after` bound and a
		// day-or-week relative literal is spelled the same in both grammars.
		{`created >= -30d`, Expressible, "inclusive after bound, relative literal"},
		{`updated >= -4w`, Expressible, "weeks carry across unchanged"},
		{`duedate < now()`, Expressible, "now() is the filter's own token — this is \"overdue\""},

		// Inclusivity mismatches. The range is half-open, so > and <= are each
		// off by one instant in a direction the document cannot express.
		{`resolutiondate > -1w`, Partial, "after is inclusive; a strict > differs at the boundary"},
		{`created <= -1d`, Partial, "before is exclusive; an inclusive <= differs at the boundary"},

		// An absolute JQL date carries no zone, so pinning it to an instant is
		// a choice the translation has to make.
		{`updated < "2026-01-01"`, Partial, "absolute date, no zone"},
		{`created = "2026-01-01"`, Partial, "= means a whole day, so a two-bound range plus a zone choice"},

		// Still lost, and for four different reasons.
		{`duedate <= endOfWeek()`, NotExpressible, "calendar truncation, not an offset"},
		{`created >= startOfMonth()`, NotExpressible, "same — startOf/endOf cannot be an offset"},
		{`created >= -30m`, NotExpressible, "JQL m is MINUTES; the filter bottoms out at days"},
		{`updated >= -2h`, NotExpressible, "hours are finer than the filter's smallest unit"},
		{`lastViewed >= -7d`, NotExpressible, "the product stores no last-viewed timestamp"},
		{`worklogDate >= -7d`, NotExpressible, "the product has no worklog"},
		{`created != -7d`, NotExpressible, "v2 defines no date negation — bounds already exclude"},
		{`duedate IS EMPTY`, NotExpressible, "a range has two bounds and no way to say \"unset\""},
	}
	for _, c := range cases {
		q := Classify(c.jql)
		require.Len(t, q.Clauses, 1, "query: %s", c.jql)
		require.Equal(t, c.want, q.Clauses[0].Verdict, "query: %s (%s)", c.jql, c.reason)
		require.NotEmpty(t, q.Clauses[0].Reason, "query: %s", c.jql)
	}
}

// TestClassify_DateClauseOrderingBug is the regression test for the ordering
// defect v2 exposed.
//
// classifyClause used to reject the comparison operators BEFORE asking whether
// the field was a date. Under v1 that was invisible, because a date clause was
// lost either way and both branches gave the same verdict. Under v2 it would
// have kept the entire date bucket in the lost pile — the exact bucket v2 was
// built to empty — while every other test still passed.
//
// Fails before the reorder in classifyClause, passes after. The assertion is
// on the REASON as well as the verdict, because the verdict alone would also
// be satisfied by a date clause that mapped for some unrelated cause.
func TestClassify_DateClauseOrderingBug(t *testing.T) {
	t.Parallel()

	q := Classify(`created >= -30d`)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Equal(t, fieldCreatedAt, q.Clauses[0].FilterField)
	require.NotContains(t, q.Clauses[0].Reason, "comparison operators",
		"the operator branch answered first, so every date clause is still lost")

	// The same operators on a NON-date field must still be refused — the
	// reorder must not have widened the operator rule for everybody.
	for _, jql := range []string{`priority > Medium`, `status < Done`, `summary >= "a"`} {
		got := Classify(jql)
		require.Equal(t, NotExpressible, got.Clauses[0].Verdict, "query: %s", jql)
		require.Contains(t, got.Clauses[0].Reason, "comparison operators", "query: %s", jql)
	}
}

// TestClassify_NegationAgainstV2NotRecord replaces the v1 test that asserted
// all negation was lost.
//
// v2 negates the SIX MEMBERSHIP FIELDS and nothing else, so the interesting
// content of this test is the boundary rather than the capability: `text` and
// the four date fields must still be refused, and refused for a reason that
// names why rather than for the blanket reason v1 gave.
func TestClassify_NegationAgainstV2NotRecord(t *testing.T) {
	t.Parallel()

	// Negation maps exactly on the fields whose VALUES also map exactly.
	for _, jql := range []string{
		`status != Done`,
		`project NOT IN (ABC, DEF)`,
		`assignee != currentUser()`,
	} {
		q := Classify(jql)
		require.Equal(t, Expressible, q.Clauses[0].Verdict, "query: %s", jql)
		require.Contains(t, strings.ToLower(q.Clauses[0].Reason), "negat", "query: %s", jql)
	}

	// Negation does NOT rescue a field that was already partial for its own
	// reasons — priority's four-value set, and the two Vector-only fields. The
	// clause still needs the same value coercion it needed before it was
	// negated, so the verdict stays partial and the report keeps saying so.
	for _, jql := range []string{
		`priority != Low`,
		`issuetype NOT IN (Bug, Task)`,
		`sprint != 4`,
	} {
		q := Classify(jql)
		require.Equal(t, Partial, q.Clauses[0].Verdict, "query: %s", jql)
		// The REASON must be the field's, not the negation's. A "partial"
		// verdict beside a reason saying the clause carries across unchanged is
		// the worst line a migration report can print: the reader is told there
		// is an approximation and not told what it is.
		require.Contains(t, q.Clauses[0].Reason, "the field's own limitation stands",
			"query: %s — the negation reason overwrote the field's", jql)
	}

	// The fields v2 deliberately left out of the `not` record.
	notExpressible := []string{
		`summary !~ "spike"`,      // text is a substring match, not a membership
		`description !~ "spike"`,  // same, via the same field
		`created != -7d`,          // dates express exclusion through bounds
		`duedate != "2026-01-01"`, // same
	}
	for _, jql := range notExpressible {
		q := Classify(jql)
		require.Equal(t, NotExpressible, q.Clauses[0].Verdict, "query: %s", jql)
	}
}

func TestClassify_ComparisonOperatorsAreNotExpressible(t *testing.T) {
	t.Parallel()

	q := Classify(`priority > Medium`)
	require.Equal(t, NotExpressible, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "no comparison operators")
}

func TestClassify_HistoryOperatorsAreNotExpressible(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{
		`status WAS "In Progress"`,
		`assignee CHANGED`,
		`status WAS IN (Open, Reopened)`,
	} {
		q := Classify(jql)
		require.Equal(t, NotExpressible, q.Verdict, "query: %s", jql)
		require.Contains(t, q.Clauses[0].Reason, "history", "query: %s", jql)
	}
}

// TestClassify_TextSearchIsPartialNotClean — the filter's text field is a
// literal substring on the title. Reporting a description or comment search as
// clean would promise rows the translated view will never return.
func TestClassify_TextSearchIsPartial(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ jql, want string }{
		{`summary ~ "login"`, "title"},
		{`text ~ "login"`, "title only"},
		{`description ~ "login"`, "description"},
		{`comment ~ "login"`, "comment"},
	} {
		q := Classify(tc.jql)
		require.Equal(t, Partial, q.Clauses[0].Verdict, "query: %s", tc.jql)
		require.Equal(t, "text", q.Clauses[0].FilterField)
		require.Contains(t, q.Clauses[0].Reason, tc.want)
	}
}

// TestClassify_KindAndSprintCarryTheVectorOnlyRestriction — filter.go refuses
// both on a view that also names Beacon, because a ticket has no type column
// and no sprint. That restriction is the whole reason these are Partial.
func TestClassify_KindAndSprintCarryTheVectorOnlyRestriction(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{`issuetype = Bug`, `type = Story`, `sprint = 42`} {
		q := Classify(jql)
		require.Equal(t, Partial, q.Clauses[0].Verdict, "query: %s", jql)
		require.Contains(t, q.Clauses[0].Reason, "Vector-only", "query: %s", jql)
	}
}

func TestClassify_PriorityIsPartialBecauseTheSetIsClosed(t *testing.T) {
	t.Parallel()

	q := Classify(`priority = Blocker`)
	require.Equal(t, Partial, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "urgent, high, medium, low")
}

// TestClassify_SameFieldORIsExpressibleButCrossFieldORIsNot is the subtlety
// worth getting right: values within one field OR by definition, so
// "project = A OR project = B" is exactly a two-element space_ids list. An OR
// across two fields has no representation at all.
func TestClassify_SameFieldORIsExpressibleButCrossFieldORIsNot(t *testing.T) {
	t.Parallel()

	same := Classify(`project = ABC OR project = DEF`)
	require.Empty(t, same.Structural, "an OR within one field is exactly a value list")
	require.Equal(t, Expressible, same.Verdict)

	cross := Classify(`project = ABC OR status = Done`)
	require.Equal(t, NotExpressible, cross.Verdict)
	require.NotEmpty(t, cross.Structural)
	require.Contains(t, strings.Join(cross.Structural, " "), "cross-field OR")
}

func TestClassify_ParenthesesAreStructurallyUnrepresentable(t *testing.T) {
	t.Parallel()

	q := Classify(`project = ABC AND (status = Open OR status = Reopened)`)
	require.Equal(t, NotExpressible, q.Verdict)
	require.Contains(t, strings.Join(q.Structural, " "), "flat record")
}

func TestClassify_CustomFieldsAreNotExpressible(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{`cf[10001] = "x"`, `"Story Points" > 3`} {
		q := Classify(jql)
		require.Equal(t, NotExpressible, q.Verdict, "query: %s", jql)
	}
	require.Contains(t, Classify(`cf[10001] = "x"`).Clauses[0].Reason, "custom fields")
}

func TestClassify_UnknownFieldsAreNotExpressible(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"labels", "component", "fixVersion", "reporter", "resolution", "watcher", "parent"} {
		q := Classify(f + ` = x`)
		require.Equal(t, NotExpressible, q.Verdict, "field: %s", f)
		require.Contains(t, q.Clauses[0].Reason, "closed at eight fields", "field: %s", f)
	}
}

// TestClassify_FunctionsOtherThanCurrentUserAreNotExpressible — the filter
// stores literal values and cannot call anything.
func TestClassify_FunctionsOtherThanCurrentUserAreNotExpressible(t *testing.T) {
	t.Parallel()

	q := Classify(`assignee IN membersOf("platform")`)
	require.Equal(t, NotExpressible, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "membersof()")
}

func TestClassify_OrderBy(t *testing.T) {
	t.Parallel()

	ok := Classify(`project = ABC ORDER BY updated DESC`)
	require.Empty(t, ok.Structural, "one sortable key is fine")
	require.Equal(t, Expressible, ok.Verdict)

	multi := Classify(`project = ABC ORDER BY priority DESC, updated ASC`)
	require.Contains(t, strings.Join(multi.Structural, " "), "2 sort keys")

	bad := Classify(`project = ABC ORDER BY status`)
	require.Contains(t, strings.Join(bad.Structural, " "), "not sortable")
}

// TestClassify_QuotedConnectivesAreValuesNotJoins — a status literally named
// "Ready AND Waiting" must not split the clause in half.
func TestClassify_QuotedConnectivesAreValuesNotJoins(t *testing.T) {
	t.Parallel()

	q := Classify(`status = "Ready AND Waiting"`)
	require.Len(t, q.Clauses, 1, "a quoted AND is part of the value")
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
}

func TestClassify_EmptyQuery(t *testing.T) {
	t.Parallel()

	q := Classify("   ")
	require.Equal(t, NotExpressible, q.Verdict)
	require.NotEmpty(t, q.Structural)
}

// TestClassify_UnparseableClauseIsReportedNotDropped — the defensive path. JQL
// this cannot read must still produce a verdict, because saying nothing about a
// filter is the least useful possible answer.
func TestClassify_UnparseableClauseIsReportedNotDropped(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{`project`, `=== nonsense ===`, `"unterminated`} {
		q := Classify(jql)
		require.NotEmpty(t, q.Clauses, "query: %s must still yield a clause", jql)
		require.Equal(t, NotExpressible, q.Verdict, "query: %s", jql)
		require.NotEmpty(t, q.Clauses[0].Reason, "query: %s", jql)
	}
}

func TestClassify_IsEmptyMapsToUnassignedForAssignee(t *testing.T) {
	t.Parallel()

	q := Classify(`assignee IS EMPTY`)
	require.Len(t, q.Clauses, 1)
	require.Equal(t, "assignees", q.Clauses[0].FilterField)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "unassigned")
}

// TestClassify_IsNotEmptyIsNegationNotPresence — the near-miss beside the case
// above. IS EMPTY genuinely maps (it is the "unassigned" token); its negation
// does not, and reads like a presence test rather than a negation. Classifying
// it as expressible would promise a translation that cannot exist.
// TestClassify_IsNotEmptyIsNegationNotPresence keeps v1's insight and updates
// its conclusion.
//
// IS NOT EMPTY still reads like a presence test and still is not one. What
// changed is that for ASSIGNEES the negation now has something to flip: the
// "unassigned" token, negated, is exactly "somebody holds this". No other field
// has an empty-value token, so no other field's IS NOT EMPTY translates — which
// is the half of this test that must not be lost.
func TestClassify_IsNotEmptyIsNegationNotPresence(t *testing.T) {
	t.Parallel()

	q := Classify(`assignee IS NOT EMPTY`)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "unassigned")

	for _, jql := range []string{`status IS NOT EMPTY`, `duedate IS NOT EMPTY`, `sprint IS NOT EMPTY`} {
		got := Classify(jql)
		require.Equal(t, NotExpressible, got.Clauses[0].Verdict, "query: %s", jql)
	}
}

// TestClassify_InValueListIsNotStructuralNesting — "project IN (ABC, DEF)" is
// the commonest JQL shape there is, and its parentheses are a value list, which
// is exactly what a space_ids filter holds. Treating them as grouping would put
// a structural failure on almost every real filter.
func TestClassify_InValueListIsNotStructuralNesting(t *testing.T) {
	t.Parallel()

	q := Classify(`project IN (ABC, DEF, GHI)`)
	require.Empty(t, q.Structural, "an IN list is a value list, not nesting: %v", q.Structural)
	require.Equal(t, Expressible, q.Verdict)
	require.Equal(t, "space_ids", q.Clauses[0].FilterField)

	// Genuine grouping is still structural.
	grouped := Classify(`project IN (ABC, DEF) AND (status = Open OR status = Reopened)`)
	require.NotEmpty(t, grouped.Structural)
	require.Contains(t, strings.Join(grouped.Structural, " "), "flat record")
}

func TestClassify_RealisticQueryMixesVerdicts(t *testing.T) {
	t.Parallel()

	q := Classify(`project = ABC AND status = "In Progress" AND assignee = currentUser() AND created >= -30d ORDER BY updated DESC`)

	require.Len(t, q.Clauses, 4)
	require.Equal(t, Expressible, q.Clauses[0].Verdict)
	require.Equal(t, Expressible, q.Clauses[1].Verdict)
	require.Equal(t, Expressible, q.Clauses[2].Verdict)
	// This clause is why the test exists. Under v1 it was the one that lost the
	// whole query; under v2 it is the one that no longer does.
	require.Equal(t, Expressible, q.Clauses[3].Verdict, "the date clause is what v2 recovered")
	require.Equal(t, Expressible, q.Verdict, "a query every clause of which maps, maps")

	// The same query with one unmappable clause still loses, so the
	// worst-clause rule has not been softened along the way.
	worse := Classify(`project = ABC AND created >= startOfMonth()`)
	require.Equal(t, NotExpressible, worse.Verdict, "the whole query is only as good as its worst clause")
}
