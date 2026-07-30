// Package queries holds no Go code — it is the sqlc source directory. This one
// test file lives here so that the thing it guards is the file next to it.
package queries

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSavedViewFanouts_CarryIdenticalFilterPredicates is a structural guard on
// saved_views.sql.
//
// SIX QUERIES CARRY THE SAME FILTER PREDICATE BLOCK, not two. The two list
// fan-outs were joined by a count pair and a breakdown pair when P5 added
// dashboard gadgets, and every one of them has to apply the same filter — a
// count gadget that reads a different predicate than the list it counts reports
// a number for a query nobody ran.
//
// Nothing mechanical links the six. They are six hand-maintained copies of one
// idea, and the file's own header says why that is dangerous: "four copies of
// one expression is four chances for one to drift". Adding filter v2's date
// ranges and negation flags, this drift happened IMMEDIATELY and silently — a
// replace-all matched five of the six blocks because ListViewTickets carries a
// comment the others do not, and the result compiled, generated and passed
// every existing test with the Beacon list fan-out ignoring every new filter.
//
// So the parity is asserted rather than hoped for. This test fails the moment a
// parameter is added to one block and not its siblings, which is the only
// moment at which the mistake is cheap to fix.
func TestSavedViewFanouts_CarryIdenticalFilterPredicates(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("saved_views.sql")
	if err != nil {
		t.Fatalf("reading saved_views.sql: %v", err)
	}

	// The ticket half and the item half differ by exactly the two Vector-only
	// fields, because tickets has neither column. Every other parameter must
	// appear in all six.
	ticketQueries := []string{"ListViewTickets", "CountViewTickets", "BreakdownViewTickets"}
	itemQueries := []string{"ListViewProjectItems", "CountViewProjectItems", "BreakdownViewProjectItems"}

	got := map[string][]string{}
	for _, name := range append(append([]string{}, ticketQueries...), itemQueries...) {
		body := queryBody(t, string(src), name)
		got[name] = filterParams(body)
	}

	assertAllEqual(t, got, ticketQueries, "the three Beacon fan-outs")
	assertAllEqual(t, got, itemQueries, "the three Vector fan-outs")

	// The two halves must differ by the Vector-only fields and nothing else. A
	// v2 field added to one module's SQL and forgotten in the other is exactly
	// the shape that makes a cross-module view return a filtered Vector half
	// and an unfiltered Beacon one.
	vectorOnly := map[string]struct{}{
		"kinds": {}, "not_kinds": {}, "sprint_ids": {}, "not_sprint_ids": {},
	}
	var unexpected []string
	for _, p := range got[itemQueries[0]] {
		if _, ok := vectorOnly[p]; ok {
			continue
		}
		if !contains(got[ticketQueries[0]], p) {
			unexpected = append(unexpected, p)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("the Vector fan-out filters on %v, which the Beacon fan-out does not — "+
			"a cross-module view would return a filtered Vector half and an unfiltered Beacon one. "+
			"If these are genuinely Vector-only columns, add them to vectorOnly above WITH THE REASON",
			unexpected)
	}
	for _, p := range got[ticketQueries[0]] {
		if !contains(got[itemQueries[0]], p) {
			t.Errorf("the Beacon fan-out filters on %q, which the Vector fan-out does not", p)
		}
	}

	// Canaries on the guard itself. If queryBody or either extractor stops
	// matching — a renamed query, a reshaped predicate — every set above goes
	// empty and every comparison passes vacuously, which is the one failure
	// mode a parity test must not have.
	//
	// The two extractors are checked SEPARATELY and each against a floor above
	// what the other alone could supply. A single combined threshold is the
	// weaker check it looks like: the eleven @-style parameters would satisfy a
	// count of eight on their own, so a total failure of the sqlc.narg
	// extractor — which is where every date bound comes from — would slip
	// through it unnoticed.
	ticketParams := got[ticketQueries[0]]
	if n := countMatching(ticketParams, dateParamNames); n != 8 {
		t.Fatalf("found %d of the 8 date-bound parameters in %s; the sqlc.narg extractor has "+
			"stopped matching and every date predicate is now unchecked", n, ticketQueries[0])
	}
	if len(ticketParams) < 15 {
		t.Fatalf("only %d filter parameters found in %s; an extractor has stopped matching "+
			"and this test is no longer checking anything", len(ticketParams), ticketQueries[0])
	}
}

// dateParamNames are the eight bounds, which reach the queries ONLY through
// sqlc.narg — so they are exactly the set that vanishes if that extractor
// breaks.
var dateParamNames = []string{
	"created_after", "created_before", "updated_after", "updated_before",
	"due_after", "due_before", "resolved_after", "resolved_before",
}

func countMatching(have, want []string) int {
	n := 0
	for _, w := range want {
		if contains(have, w) {
			n++
		}
	}
	return n
}

// TestSavedViewFanouts_NegateNullableColumnsWithCoalesce pins the three-valued
// logic fix.
//
// `col = ANY(arr)` is NULL — not false — when col is NULL, and `NULL <> true`
// is NULL, so a negated membership test SILENTLY DROPS every row whose column
// is null. For assignee_id that means "not assigned to Alice" excludes the
// unassigned work, which is precisely the set a person means to see.
//
// Only the nullable columns need the COALESCE, and only two of the six
// negatable columns are nullable: assignee_id and sprint_id. That was verified
// against the live database rather than against the migrations, per CLAUDE.md
// §5. This test pins the fix in place; the integration test that proves the
// BEHAVIOUR is TestViewResults_NegationKeepsNullRows.
func TestSavedViewFanouts_NegateNullableColumnsWithCoalesce(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("saved_views.sql")
	if err != nil {
		t.Fatalf("reading saved_views.sql: %v", err)
	}
	text := string(src)

	for _, q := range []struct{ name, col string }{
		{"ListViewTickets", "tk.assignee_id"},
		{"CountViewTickets", "tk.assignee_id"},
		{"BreakdownViewTickets", "tk.assignee_id"},
		{"ListViewProjectItems", "pi.assignee_id"},
		{"CountViewProjectItems", "pi.assignee_id"},
		{"BreakdownViewProjectItems", "pi.assignee_id"},
		{"ListViewProjectItems", "pi.sprint_id"},
		{"CountViewProjectItems", "pi.sprint_id"},
		{"BreakdownViewProjectItems", "pi.sprint_id"},
	} {
		body := queryBody(t, text, q.name)
		want := "COALESCE(" + q.col + " = ANY("
		if !strings.Contains(body, want) {
			t.Errorf("%s negates the NULLABLE column %s without COALESCE — "+
				"rows with a null %s will be dropped from the negated result rather than included in it",
				q.name, q.col, q.col)
		}
	}
}

// queryBody returns the SQL of one named sqlc query, with comment lines
// removed.
//
// It runs to the next `-- name:` marker rather than to the first semicolon:
// these queries carry long explanatory headers, and two of them contain a
// semicolon in prose, which cut the body to nothing and made every comparison
// below pass vacuously. Comments are stripped for the mirror-image reason — the
// headers quote predicates verbatim, so a parameter named only in prose would
// otherwise count as one the query filters on.
func queryBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "-- name: "+name+" ")
	if start < 0 {
		t.Fatalf("query %q not found in saved_views.sql", name)
	}
	rest := src[start+len("-- name: "):]
	if next := strings.Index(rest, "\n-- name:"); next >= 0 {
		rest = rest[:next]
	}
	var kept []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

var (
	paramRe = regexp.MustCompile(`@([a-z_]+)`)
	nargRe  = regexp.MustCompile(`sqlc\.narg\(([a-z_]+)\)`)
)

// filterParams is the set of parameters a query filters on, excluding the ones
// that are about the page or the access union rather than about the filter.
func filterParams(body string) []string {
	skip := map[string]struct{}{
		// Access control and scope — not filter vocabulary.
		"org_id": {}, "readable_space_ids": {}, "shared_ticket_ids": {}, "shared_item_ids": {},
		// Paging and ordering, which the count and breakdown queries correctly
		// do not have.
		"sort_field": {}, "cursor_key": {}, "cursor_id": {}, "descending": {}, "row_limit": {},
		// The breakdown queries' grouping choice.
		"group_by": {},
	}
	seen := map[string]struct{}{}
	for _, m := range paramRe.FindAllStringSubmatch(body, -1) {
		if _, ok := skip[m[1]]; !ok {
			seen[m[1]] = struct{}{}
		}
	}
	for _, m := range nargRe.FindAllStringSubmatch(body, -1) {
		if _, ok := skip[m[1]]; !ok {
			seen[m[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func assertAllEqual(t *testing.T, got map[string][]string, names []string, what string) {
	t.Helper()
	first := got[names[0]]
	for _, name := range names[1:] {
		if strings.Join(got[name], ",") == strings.Join(first, ",") {
			continue
		}
		t.Errorf("%s do not filter identically.\n  %s: %v\n  %s: %v\n"+
			"missing from %s: %v\n  extra in %s: %v",
			what, names[0], first, name, got[name],
			name, missing(first, got[name]), name, missing(got[name], first))
	}
}

func missing(want, have []string) []string {
	var out []string
	for _, w := range want {
		if !contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
