package jql

import (
	"sort"
	"strings"
)

// Expressibility is how well one JQL clause survives translation into a saved
// view's filter document.
type Expressibility string

const (
	// Expressible means the clause maps onto a filter field with the same
	// meaning.
	Expressible Expressibility = "expressible"

	// Partial means something survives but the meaning narrows — a text match
	// that becomes title-only, a value that must be coerced. A reader must be
	// told, because a partially-translated filter silently returns a different
	// set of rows.
	Partial Expressibility = "partial"

	// NotExpressible means the filter vocabulary has nothing for it.
	NotExpressible Expressibility = "not_expressible"
)

// Clause is one classified JQL clause.
type Clause struct {
	// Raw is the clause as written, for the report.
	Raw string `json:"raw"`
	// Field is the JQL field name, lowercased. Empty when the clause could not
	// be read as field/operator/value.
	Field string `json:"field,omitempty"`
	// Operator is the JQL operator as written.
	Operator string `json:"operator,omitempty"`
	// Verdict is the classification.
	Verdict Expressibility `json:"verdict"`
	// FilterField names the saved-view field this maps onto, when it maps.
	FilterField string `json:"filter_field,omitempty"`
	// Reason explains the verdict in terms of the real vocabulary.
	Reason string `json:"reason"`
}

// Query is a whole classified JQL string.
type Query struct {
	// Raw is the input.
	Raw string `json:"raw"`
	// Clauses are the classified clauses, in source order.
	Clauses []Clause `json:"clauses"`
	// Structural are findings about the query's shape rather than its fields:
	// cross-field OR, negation, an unsupported sort. Each is a reason the query
	// as a whole cannot be expressed even when every individual clause could.
	Structural []string `json:"structural,omitempty"`
	// Verdict is the whole query's classification: the worst of its clauses,
	// downgraded further by any structural finding.
	Verdict Expressibility `json:"verdict"`
}

// The saved-view filter vocabulary, from internal/core/views/filter.go. Named
// here rather than imported as strings so the mapping table below reads as one
// piece; filter_vocabulary_test.go asserts these against the real package and
// fails if the vocabulary changes underneath.
const (
	fieldSpaceIDs   = "space_ids"
	fieldStatuses   = "statuses"
	fieldPriorities = "priorities"
	fieldAssignees  = "assignees"
	fieldKinds      = "kinds"
	fieldSprintIDs  = "sprint_ids"
	fieldText       = "text"
)

// mapping is one JQL field's disposition.
type mapping struct {
	filterField string
	verdict     Expressibility
	reason      string
}

// jqlFieldMap is the whole answer to "which JQL fields survive".
//
// It is short because the target vocabulary is short — eight fields, of which
// this can reach seven (modules is chosen by the view, not by a clause). Every
// other JQL field is unmappable, and the map's own smallness is the finding.
var jqlFieldMap = map[string]mapping{
	"project": {fieldSpaceIDs, Expressible,
		"project maps to a space; the space_ids filter takes the ids the keys resolve to"},
	"status": {fieldStatuses, Expressible,
		"statuses is free text on both tables, so a status name carries across as written"},
	"priority": {fieldPriorities, Partial,
		"priorities is a closed four-value set (urgent, high, medium, low); any other Jira priority must be coerced into one of them"},
	"assignee": {fieldAssignees, Expressible,
		"assignees takes user ids, plus \"me\" for the viewer and \"unassigned\" for a null assignee"},
	"issuetype": {fieldKinds, Partial,
		"kinds matches project_items.kind and is Vector-only — filter.go refuses it on a view that also names Beacon, because tickets have no type column"},
	"type": {fieldKinds, Partial,
		"kinds matches project_items.kind and is Vector-only — filter.go refuses it on a view that also names Beacon, because tickets have no type column"},
	"sprint": {fieldSprintIDs, Partial,
		"sprint_ids is Vector-only for the same reason: a ticket has no sprint, so a view naming both modules cannot use it"},
	"summary": {fieldText, Partial,
		"text is a literal substring match on the title, not a word match: Jira's ~ finds word stems and this does not"},
	"text": {fieldText, Partial,
		"text searches the title only — Jira's text search also covers description, comments and attachments, so the translated filter matches strictly fewer rows"},
	"description": {fieldText, Partial,
		"there is no description filter; text matches the title only, so a description search does not survive"},
	"comment": {fieldText, Partial,
		"there is no comment filter; text matches the title only, so a comment search does not survive"},
}

// dateFields are the JQL fields whose values are dates.
//
// They are called out separately because their failure is structural and
// surprising: the filter document has no date predicate of any kind. Dates are
// sortable — updated_at, created_at, due_at and resolved_at are all valid sort
// fields — which makes it easy to assume they are filterable too. They are not,
// so every date clause in every saved filter is lost.
var dateFields = map[string]struct{}{
	"created": {}, "updated": {}, "resolved": {}, "duedate": {}, "due": {},
	"resolutiondate": {}, "lastviewed": {}, "worklogdate": {},
}

// historyOperators are the JQL operators that ask about an issue's past.
var historyOperators = map[string]struct{}{
	"was": {}, "changed": {}, "wasnot": {}, "wasin": {}, "wasnotin": {},
}

// negatingOperators cannot be expressed because the vocabulary has no negation
// at all: a filter field lists values that match, and there is no way to say
// "not these".
//
// "isnot" belongs here and is easy to miss: IS NOT EMPTY reads like a presence
// test rather than a negation, and IS EMPTY genuinely is expressible for
// assignee (it is the "unassigned" token). Its negation is not.
var negatingOperators = map[string]struct{}{
	"!=": {}, "!~": {}, "notin": {}, "isnot": {},
}

// orderingOperators need a comparison the vocabulary does not have. Fields hold
// value lists compared for equality; there is no >, <, >= or <=.
var orderingOperators = map[string]struct{}{
	">": {}, "<": {}, ">=": {}, "<=": {},
}

// sortableFields mirrors validSortFields in internal/core/views/filter.go.
var sortableFields = map[string]string{
	"updated": "updated_at", "updateddate": "updated_at",
	"created": "created_at", "createddate": "created_at",
	"duedate": "due_at", "due": "due_at",
	"resolved": "resolved_at", "resolutiondate": "resolved_at",
	"priority": "priority",
	"summary":  "title",
}

// Classify reads a JQL string and reports what a saved view could express.
func Classify(input string) Query {
	q := Query{Raw: strings.TrimSpace(input), Verdict: Expressible}
	if q.Raw == "" {
		q.Verdict = NotExpressible
		q.Structural = []string{"the filter has no query text"}
		return q
	}

	body, orderBy := splitOrderBy(lex(q.Raw))
	q.Structural = append(q.Structural, classifyOrderBy(orderBy)...)

	groups := splitClauses(body)
	for _, g := range groups {
		q.Clauses = append(q.Clauses, classifyClause(g.tokens))
	}
	q.Structural = append(q.Structural, structuralFindings(groups, q.Clauses)...)

	q.Verdict = overallVerdict(q)
	return q
}

// group is one clause plus the connective that preceded it.
type group struct {
	tokens []token
	// joinedByOR records that this clause was reached with OR rather than AND.
	joinedByOR bool
	// negated records a NOT applying to the clause.
	negated bool
	// grouped records that the clause sat inside parentheses.
	grouped bool
}

// splitOrderBy separates the trailing ORDER BY, which is a sort and not a
// filter.
func splitOrderBy(tokens []token) (body, orderBy []token) {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].quoted || !strings.EqualFold(tokens[i].text, "ORDER") {
			continue
		}
		if strings.EqualFold(tokens[i+1].text, "BY") {
			return tokens[:i], tokens[i+2:]
		}
	}
	return tokens, nil
}

// splitClauses breaks the query body on connectives, tracking OR, NOT and
// parenthesisation.
func splitClauses(tokens []token) []group {
	var groups []group
	var cur group
	depth := 0

	flush := func(nextOR, nextNeg bool) {
		if len(cur.tokens) > 0 {
			groups = append(groups, cur)
		}
		cur = group{joinedByOR: nextOR, negated: nextNeg}
	}

	// valueList tracks parens that belong to an IN list rather than to
	// grouping. "project IN (ABC, DEF)" is a value list — exactly what a
	// space_ids filter holds — and reporting it as unrepresentable nesting
	// would put a structural failure on the single most common JQL shape there
	// is. Only parens that group sub-expressions are structural.
	valueList := false

	for _, t := range tokens {
		switch {
		case t.kind == tokLParen:
			depth++
			if valueList {
				break
			}
			cur.grouped = true
		case t.kind == tokRParen:
			depth--
			if depth == 0 {
				valueList = false
			}
		case depth == 0 && isConnective(t):
			flush(strings.EqualFold(t.text, "OR") || t.text == "||", false)
		case depth == 0 && isNot(t) && len(cur.tokens) == 0:
			cur.negated = true
		default:
			if depth == 0 && !t.quoted && strings.EqualFold(t.text, "IN") {
				valueList = true
			}
			cur.tokens = append(cur.tokens, t)
		}
	}
	flush(false, false)
	return groups
}

// classifyClause is the per-clause decision.
func classifyClause(tokens []token) Clause {
	c := Clause{Raw: renderTokens(tokens), Verdict: NotExpressible}
	if len(tokens) == 0 {
		c.Reason = "empty clause"
		return c
	}

	field, op, ok := readTriple(tokens)
	if !ok {
		c.Reason = "clause is not a field/operator/value comparison, so there is nothing to map onto a filter field"
		return c
	}
	c.Field, c.Operator = field, op

	if v, done := classifyOperator(&c, op); done {
		return v
	}
	if _, isDate := dateFields[field]; isDate {
		c.Reason = "the filter document has no date predicate at all — created_at, updated_at, due_at and resolved_at are sortable but not filterable, so every date clause is lost"
		return c
	}
	return classifyField(c, field, tokens)
}

// classifyOperator rejects the operator classes the vocabulary cannot express.
// The bool reports that the verdict is settled.
func classifyOperator(c *Clause, op string) (Clause, bool) {
	norm := strings.ToLower(strings.ReplaceAll(op, " ", ""))
	if _, bad := historyOperators[norm]; bad {
		c.Reason = "history operators ask what an issue used to be; a saved view filters the current row and has no history model"
		return *c, true
	}
	if _, bad := negatingOperators[norm]; bad {
		c.Reason = "the vocabulary has no negation: a filter field lists the values that match and cannot say \"not these\""
		return *c, true
	}
	if _, bad := orderingOperators[norm]; bad {
		c.Reason = "the vocabulary has no comparison operators — fields hold value lists matched for equality, so >, <, >= and <= have nothing to translate to"
		return *c, true
	}
	return *c, false
}

// classifyField decides a clause whose operator is already acceptable.
func classifyField(c Clause, field string, tokens []token) Clause {
	if strings.HasPrefix(field, "cf[") || isCustomFieldName(tokens) {
		c.Reason = "custom fields are not in the filter vocabulary; a saved view can only filter the eight fields filter.go names"
		return c
	}
	m, ok := jqlFieldMap[field]
	if !ok {
		c.Reason = "no filter field covers it; the vocabulary is closed at eight fields and this is not one of them"
		return c
	}
	c.FilterField, c.Verdict, c.Reason = m.filterField, m.verdict, m.reason

	// A function value may narrow the verdict further.
	if fnReason, fnVerdict, hit := classifyFunctionValue(tokens, m); hit {
		c.Verdict, c.Reason = fnVerdict, fnReason
	}
	return c
}

// classifyFunctionValue handles JQL function operands.
//
// currentUser() is the one that translates exactly: the filter's "me" token is
// resolved per viewer at query time, which is the same semantics. The rest name
// sets a static filter cannot compute.
func classifyFunctionValue(tokens []token, m mapping) (string, Expressibility, bool) {
	for _, t := range tokens {
		if t.quoted || !strings.Contains(t.text, "(") {
			continue
		}
		fn := strings.ToLower(strings.SplitN(t.text, "(", 2)[0])
		switch fn {
		case "currentuser":
			if m.filterField == fieldAssignees {
				return "currentUser() maps exactly onto the \"me\" token, which the filter resolves per viewer at query time", Expressible, true
			}
			return "currentUser() has no meaning for this field in the filter vocabulary", NotExpressible, true
		case "":
			continue
		default:
			return "JQL function " + fn + "() computes a set at query time; the filter stores literal values and cannot call functions", NotExpressible, true
		}
	}
	return "", "", false
}

// readTriple pulls field and operator out of a clause.
//
// It also recognises the two-word operators (IS NOT, NOT IN, WAS IN, ...) that
// lex as separate words, joining them so classifyOperator sees one string.
func readTriple(tokens []token) (field, op string, ok bool) {
	if len(tokens) < 2 {
		return "", "", false
	}
	field = strings.ToLower(strings.Trim(tokens[0].text, `"'`))

	i := 1
	if tokens[i].kind == tokOperator {
		return field, tokens[i].text, true
	}
	// Word operators, possibly two words.
	first := strings.ToLower(tokens[i].text)
	if !isWordOperator(first) {
		return "", "", false
	}
	if i+1 < len(tokens) && tokens[i+1].kind == tokWord {
		second := strings.ToLower(tokens[i+1].text)
		if isWordOperator(second) || second == "in" || second == "not" {
			return field, first + " " + second, true
		}
	}
	return field, first, true
}

func isWordOperator(w string) bool {
	switch w {
	case "in", "is", "not", "was", "changed":
		return true
	default:
		return false
	}
}

// isCustomFieldName reports a quoted first token, which in JQL is how a custom
// field with spaces in its name is referenced.
func isCustomFieldName(tokens []token) bool {
	return len(tokens) > 0 && tokens[0].quoted && strings.Contains(tokens[0].text, " ")
}

// structuralFindings reports the query-shape reasons a translation fails even
// when every clause is individually fine.
func structuralFindings(groups []group, clauses []Clause) []string {
	var out []string

	if crossFieldOR(groups, clauses) {
		out = append(out, "an OR spans two different fields; the filter ANDs across fields and ORs only within one, so a cross-field OR cannot be expressed")
	}
	for _, g := range groups {
		if g.negated {
			out = append(out, "a NOT applies to a clause; the vocabulary has no negation")
			break
		}
	}
	if nested := countGrouped(groups); nested > 0 {
		out = append(out, "the query uses parentheses; the filter document is a flat record with no nesting, so grouped sub-expressions cannot be represented")
	}
	return out
}

// crossFieldOR reports an OR between clauses on different filter fields.
//
// An OR within one field is fine — values within a field OR by definition, so
// "project = A OR project = B" is exactly a two-element space_ids list. An OR
// across fields is not, and the difference is the kind of thing a migration
// discovers after trusting the report.
func crossFieldOR(groups []group, clauses []Clause) bool {
	for i, g := range groups {
		if !g.joinedByOR || i == 0 || i >= len(clauses) {
			continue
		}
		prev, cur := clauses[i-1], clauses[i]
		if prev.Field != cur.Field {
			return true
		}
	}
	return false
}

func countGrouped(groups []group) int {
	n := 0
	for _, g := range groups {
		if g.grouped {
			n++
		}
	}
	return n
}

// classifyOrderBy checks the trailing sort against the six sortable fields and
// the one-key limit.
func classifyOrderBy(tokens []token) []string {
	if len(tokens) == 0 {
		return nil
	}
	var out []string
	keys := sortKeys(tokens)

	if len(keys) > 1 {
		out = append(out, "ORDER BY names "+itoa(len(keys))+" sort keys; a saved view stores exactly one, because its cursor encodes a single sort position")
	}
	for _, k := range keys {
		if _, ok := sortableFields[k]; !ok {
			out = append(out, "ORDER BY "+k+" is not sortable: the sort vocabulary is updated_at, created_at, due_at, resolved_at, priority and title")
		}
	}
	return out
}

// sortKeys extracts the field names from an ORDER BY tail, dropping ASC/DESC.
func sortKeys(tokens []token) []string {
	var keys []string
	for _, t := range tokens {
		if t.kind == tokComma {
			continue
		}
		w := strings.ToLower(strings.Trim(t.text, `"'`))
		if w == "asc" || w == "desc" || w == "" {
			continue
		}
		keys = append(keys, w)
	}
	return keys
}

// overallVerdict is the worst clause verdict, floored by any structural
// finding. A query every clause of which is expressible is still not
// expressible if its shape is not.
func overallVerdict(q Query) Expressibility {
	worst := Expressible
	for _, c := range q.Clauses {
		worst = worse(worst, c.Verdict)
	}
	if len(q.Structural) > 0 {
		worst = worse(worst, NotExpressible)
	}
	if len(q.Clauses) == 0 {
		return NotExpressible
	}
	return worst
}

func rank(e Expressibility) int {
	switch e {
	case Expressible:
		return 0
	case Partial:
		return 1
	case NotExpressible:
		return 2
	default:
		return 3
	}
}

func worse(a, b Expressibility) Expressibility {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// renderTokens reassembles a clause for display.
func renderTokens(tokens []token) string {
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t.quoted {
			parts = append(parts, `"`+t.text+`"`)
			continue
		}
		parts = append(parts, t.text)
	}
	return strings.Join(parts, " ")
}

// SortedFields lists every JQL field the vocabulary can reach, for the report.
func SortedFields() []string {
	out := make([]string, 0, len(jqlFieldMap))
	for k := range jqlFieldMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
