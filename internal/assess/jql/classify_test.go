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

	real := make(map[string]struct{})
	rt := reflect.TypeOf(views.Filter{})
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			real[name] = struct{}{}
		}
	}

	// "modules" is chosen when the view is created, not by a JQL clause, so it
	// is the one filter field no mapping targets.
	require.Contains(t, real, "modules")
	targetable := map[string]struct{}{}
	for name := range real {
		if name != "modules" {
			targetable[name] = struct{}{}
		}
	}

	named := map[string]struct{}{
		fieldSpaceIDs: {}, fieldStatuses: {}, fieldPriorities: {},
		fieldAssignees: {}, fieldKinds: {}, fieldSprintIDs: {}, fieldText: {},
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

// TestClassify_DateClausesAreNotExpressible is the finding most likely to
// surprise a reader: the four date columns are sortable, which makes it natural
// to assume they are filterable. They are not — the filter document has no date
// predicate of any kind.
func TestClassify_DateClausesAreNotExpressible(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{
		`created >= -30d`,
		`updated < "2026-01-01"`,
		`duedate <= endOfWeek()`,
		`resolutiondate > -1w`,
	} {
		q := Classify(jql)
		require.Equal(t, NotExpressible, q.Verdict, "query: %s", jql)
		require.NotEmpty(t, q.Clauses[0].Reason)
	}

	// And with an operator the vocabulary would otherwise accept, the field is
	// still the reason.
	q := Classify(`created = "2026-01-01"`)
	require.Equal(t, NotExpressible, q.Clauses[0].Verdict)
	require.Contains(t, q.Clauses[0].Reason, "no date predicate")
}

func TestClassify_NegationIsNotExpressible(t *testing.T) {
	t.Parallel()

	for _, jql := range []string{
		`status != Done`,
		`project NOT IN (ABC, DEF)`,
		`summary !~ "spike"`,
	} {
		q := Classify(jql)
		require.Equal(t, NotExpressible, q.Verdict, "query: %s", jql)
		require.Contains(t, strings.ToLower(q.Clauses[0].Reason), "negation", "query: %s", jql)
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
func TestClassify_IsNotEmptyIsNegationNotPresence(t *testing.T) {
	t.Parallel()

	q := Classify(`assignee IS NOT EMPTY`)
	require.Equal(t, NotExpressible, q.Verdict)
	require.Contains(t, q.Clauses[0].Reason, "negation")
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
	require.Equal(t, NotExpressible, q.Clauses[3].Verdict, "the date clause is the one that is lost")
	require.Equal(t, NotExpressible, q.Verdict, "the whole query is only as good as its worst clause")
}
