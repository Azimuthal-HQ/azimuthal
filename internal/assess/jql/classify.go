package jql

import (
	"regexp"
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

	// The four v2 date ranges. Each takes {after, before} bounds, half-open.
	fieldCreatedAt  = "created_at"
	fieldUpdatedAt  = "updated_at"
	fieldDueAt      = "due_at"
	fieldResolvedAt = "resolved_at"

	// fieldNot is v2's negation record. It is a MODIFIER rather than a target:
	// no JQL clause maps onto it, because it never appears alone — it flips a
	// membership field that some other clause named. It is listed here because
	// the drift guard walks every JSON tag on views.Filter and requires this
	// package to have an opinion about each one, and "this is a modifier, and
	// here is which operators reach it" is the opinion.
	fieldNot = "not"
)

// negatableFields are the six membership fields v2's `not` record can flip.
// The four date fields are absent because v2 defines no date negation, and
// `text` is absent because a substring match is not a set membership.
//
// This is a SECOND vocabulary, and the drift guard over views.Filter's JSON
// tags cannot see it — that one learns only that a field called "not" exists.
// TestVocabulary_NegatableFieldsMatchTheRealNegateRecord checks this list
// against views.Negate's own tags, in both directions.
var negatableFields = map[string]struct{}{
	fieldSpaceIDs: {}, fieldStatuses: {}, fieldPriorities: {},
	fieldAssignees: {}, fieldKinds: {}, fieldSprintIDs: {},
}

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

// dateFields maps each JQL date field onto the filter date field it becomes, or
// to "" where the product has no such column.
//
// Under v1 every one of these was lost: the filter document had no date
// predicate of any kind, which was the single largest bucket in the report.
// v2 gives four of them a home. `lastviewed` and `worklogdate` stay lost, and
// not because of the filter vocabulary — Azimuthal stores neither a per-user
// last-viewed timestamp nor a worklog, so there is no column for a predicate to
// name.
var dateFields = map[string]string{
	"created":        fieldCreatedAt,
	"createddate":    fieldCreatedAt,
	"updated":        fieldUpdatedAt,
	"updateddate":    fieldUpdatedAt,
	"duedate":        fieldDueAt,
	"due":            fieldDueAt,
	"resolved":       fieldResolvedAt,
	"resolutiondate": fieldResolvedAt,
	"lastviewed":     "",
	"worklogdate":    "",
}

// dateOperators maps a JQL comparison onto a v2 bound.
//
// The verdict turns on INCLUSIVITY. v2's range is half-open — `after` is
// inclusive, `before` is exclusive — so JQL's `>=` and `<` land exactly, while
// `>` and `<=` are off by one instant in a direction the filter cannot express.
// That is a real difference in the rows returned at a boundary, so it is
// reported as partial rather than waved through.
var dateOperators = map[string]struct {
	bound   string
	verdict Expressibility
	reason  string
}{
	">=": {"after", Expressible,
		"maps onto the range's \"after\" bound, which is inclusive exactly as >= is"},
	"<": {"before", Expressible,
		"maps onto the range's \"before\" bound, which is exclusive exactly as < is"},
	">": {"after", Partial,
		"the range's \"after\" bound is INCLUSIVE, so a strict > additionally matches rows at the boundary instant itself"},
	"<=": {"before", Partial,
		"the range's \"before\" bound is EXCLUSIVE, so an inclusive <= loses rows at the boundary instant itself"},
	"=": {"both", Partial,
		"JQL's = on a date means anywhere within that DAY; it becomes a two-bound range, and the day boundary is resolved in the server's zone rather than the author's"},
}

// dateRelative is JQL's own relative-date literal: a sign, a magnitude and a
// unit from w/d/h/m.
//
// THE UNITS DO NOT LINE UP WITH OURS, and the overlap is the dangerous part.
// JQL has no month unit and spells MINUTES `m`; the filter's grammar spells
// months `mo` and has no unit below a day. So `-4w` and `-7d` carry across
// exactly, while `-30m` and `-2h` have nothing to become — the filter's
// finest granularity is a day.
var dateRelative = regexp.MustCompile(`^([+-])([0-9]+)([wdhm])$`)

// historyOperators are the JQL operators that ask about an issue's past.
var historyOperators = map[string]struct{}{
	"was": {}, "changed": {}, "wasnot": {}, "wasin": {}, "wasnotin": {},
}

// negatingOperators are the operators that ask for the complement of a value
// set. Under v1 none of them could be expressed; under v2 most of them can, but
// only on the six fields the `not` record names — so membership here says
// "this is a negation", and classifyNegation decides whether it survives.
//
// "isnot" belongs here and is easy to miss: IS NOT EMPTY reads like a presence
// test rather than a negation. It now translates for assignees, where the empty
// case has its own token to negate, and for nothing else.
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
	var s splitter
	for _, t := range tokens {
		s.feed(t)
	}
	s.flush(false, false)
	return s.groups
}

// splitter walks a token stream, cutting it into clauses at top-level
// connectives.
//
// valueList tracks parens that belong to an IN list rather than to grouping.
// "project IN (ABC, DEF)" is a value list — exactly what a space_ids filter
// holds — and reporting it as unrepresentable nesting would put a structural
// failure on the single most common JQL shape there is. Only parens that group
// sub-expressions are structural.
type splitter struct {
	groups    []group
	cur       group
	depth     int
	valueList bool
}

func (s *splitter) flush(nextOR, nextNeg bool) {
	if len(s.cur.tokens) > 0 {
		s.groups = append(s.groups, s.cur)
	}
	s.cur = group{joinedByOR: nextOR, negated: nextNeg}
}

func (s *splitter) feed(t token) {
	switch {
	case t.kind == tokLParen:
		s.openParen()
	case t.kind == tokRParen:
		s.closeParen()
	case s.top() && isConnective(t):
		s.flush(isOR(t), false)
	case s.top() && isNot(t) && len(s.cur.tokens) == 0:
		s.cur.negated = true
	default:
		s.appendToken(t)
	}
}

func (s *splitter) openParen() {
	s.depth++
	s.cur.grouped = s.cur.grouped || !s.valueList
}

func (s *splitter) closeParen() {
	s.depth--
	s.valueList = s.valueList && s.depth != 0
}

func (s *splitter) appendToken(t token) {
	s.valueList = s.valueList || (s.top() && isIN(t))
	s.cur.tokens = append(s.cur.tokens, t)
}

func (s *splitter) top() bool { return s.depth == 0 }

func isOR(t token) bool {
	return strings.EqualFold(t.text, "OR") || t.text == "||"
}

func isIN(t token) bool {
	return !t.quoted && strings.EqualFold(t.text, "IN")
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

	// DATE FIELDS ARE DECIDED BEFORE THE OPERATOR CLASSES, and the order is the
	// whole point. On a date field, `>` and `<` are exactly what v2's `after`
	// and `before` bounds express, so rejecting them as "the vocabulary has no
	// comparison operators" — which is still true of every OTHER field — would
	// leave the entire date bucket in the lost pile that v2 was built to empty.
	//
	// Under v1 this ordering was invisible: date clauses were lost either way,
	// so whichever branch answered first gave the same verdict.
	if _, isDate := dateFields[field]; isDate {
		return classifyDateClause(c, field, tokens)
	}
	if v, done := classifyOperator(&c, op); done {
		return v
	}
	return classifyField(c, field, tokens)
}

// classifyDateClause decides a clause on a date field, against v2's ranges.
//
// Three things must all survive: the field must have a column, the operator
// must be a bound the range can express, and the value must be a form the
// bound can hold. The worst of the three is the verdict.
func classifyDateClause(c Clause, field string, tokens []token) Clause {
	target := dateFields[field]
	if target == "" {
		c.Reason = "there is no such column in the product — Azimuthal stores neither a per-user last-viewed timestamp nor a worklog, so this has nothing to filter on rather than nothing to express it with"
		return c
	}
	c.FilterField = target

	norm := strings.ToLower(strings.ReplaceAll(c.Operator, " ", ""))
	if _, bad := historyOperators[norm]; bad {
		c.Reason = "history operators ask what an issue used to be; a saved view filters the current row and has no history model"
		return c
	}
	if norm == "is" || norm == "isnot" {
		c.Reason = "IS EMPTY and IS NOT EMPTY ask whether the date is set at all; a range has two bounds and no way to say \"unset\", and negation is not defined on date fields"
		return c
	}
	if _, neg := negatingOperators[norm]; neg {
		c.Reason = "v2 negates membership fields only. A date range already expresses exclusion through its bounds, so there is deliberately no date negation to translate != into"
		return c
	}
	op, ok := dateOperators[norm]
	if !ok {
		c.Reason = "the range expresses only ordered bounds (after and before); this operator is not one of them"
		return c
	}

	valVerdict, valReason := classifyDateValue(tokens)
	if rank(valVerdict) > rank(op.verdict) {
		c.Verdict, c.Reason = valVerdict, valReason
		return c
	}
	c.Verdict = op.verdict
	c.Reason = "the " + target + " range " + op.reason
	if valVerdict == Partial && op.verdict == Expressible {
		c.Verdict, c.Reason = Partial, valReason
	}
	return c
}

// classifyDateValue decides the operand: a relative literal, a function call or
// an absolute date.
func classifyDateValue(tokens []token) (Expressibility, string) {
	if len(tokens) == 0 {
		return NotExpressible, "the clause names no value"
	}
	raw := tokens[len(tokens)-1]
	v := strings.Trim(raw.text, `"'`)

	// A function operand.
	if !raw.quoted && strings.Contains(raw.text, "(") {
		fn := strings.ToLower(strings.SplitN(raw.text, "(", 2)[0])
		if fn == "now" {
			return Expressible, "now() maps onto the \"now\" token, which the filter resolves per request at evaluation"
		}
		return NotExpressible, "JQL function " + fn + "() TRUNCATES to a calendar boundary rather than offsetting from an instant; the filter's relative tokens are offsets only, so start-of-day and end-of-week have no spelling"
	}

	// A relative literal.
	if m := dateRelative.FindStringSubmatch(v); m != nil {
		switch m[3] {
		case "d", "w":
			return Expressible, "the relative literal carries across unchanged — the filter spells days and weeks the same way and resolves them server-side at evaluation"
		default:
			return NotExpressible, "JQL's " + m[3] + " unit is finer than a day (h is hours, m is MINUTES — JQL has no month unit); the filter's relative grammar bottoms out at d, so a sub-day offset cannot be expressed"
		}
	}

	// An absolute date. JQL literals carry no zone, so the instant they mean
	// depends on the reader's Jira profile; the filter's absolute bound is
	// RFC3339 and names an unambiguous instant. The translation has to choose a
	// zone, and choosing is a narrowing.
	return Partial, "an absolute JQL date carries no time zone, so it must be pinned to one to become the RFC3339 instant the filter stores; rows within a day of the boundary can land on either side"
}

// classifyOperator rejects the operator classes the vocabulary cannot express.
// The bool reports that the verdict is settled.
func classifyOperator(c *Clause, op string) (Clause, bool) {
	norm := strings.ToLower(strings.ReplaceAll(op, " ", ""))
	if _, bad := historyOperators[norm]; bad {
		c.Reason = "history operators ask what an issue used to be; a saved view filters the current row and has no history model"
		return *c, true
	}
	// NEGATION IS NO LONGER DECIDED HERE. v2 negates per field, so whether a
	// negating operator survives depends on WHICH field it applies to — and
	// that is not known until classifyField resolves the mapping. Deciding it
	// on the operator alone was correct only while the answer was "never".
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
	if neg, hit := classifyNegation(c.Operator, m.filterField); hit {
		// Negation can only narrow. A clause that was already partial for its
		// FIELD stays partial even when its negation maps exactly — and it must
		// keep the field's reason, because that is the reason it is partial.
		//
		// Overwriting unconditionally would have produced the worst kind of
		// report line: the verdict "partial" beside a reason that says the
		// clause carries across unchanged. The reader would have no way to know
		// what the approximation was.
		if rank(neg.Verdict) > rank(c.Verdict) {
			c.Verdict, c.Reason = neg.Verdict, neg.Reason
			return c
		}
		if rank(neg.Verdict) < rank(c.Verdict) {
			c.Reason += "; the negation itself maps exactly, but the field's own limitation stands"
			return c
		}
		c.Reason = neg.Reason
	}
	return c
}

// classifyNegation decides a negating operator once the target field is known.
//
// v2's negation is a per-field flag over a set of values, so it translates
// exactly for the six membership fields and not at all for the others. The
// bool reports that the operator was a negating one.
func classifyNegation(op, filterField string) (Clause, bool) {
	norm := strings.ToLower(strings.ReplaceAll(op, " ", ""))
	if _, isNeg := negatingOperators[norm]; !isNeg {
		return Clause{}, false
	}

	// `!~` is a negated CONTAINS, and the only field it can reach is text —
	// which v2 deliberately left out of the negation record, because a
	// substring match is not a set membership and "not these values" has no
	// reading for it.
	if norm == "!~" {
		return Clause{Verdict: NotExpressible,
			Reason: "the text filter is a literal substring match and v2 does not negate it — `not` names the six membership fields only, so \"title does not contain X\" has no spelling"}, true
	}

	// IS NOT EMPTY asks for a present value rather than a different one. It
	// translates for assignees alone, where the empty case has its own token:
	// "unassigned", negated, is exactly "somebody holds this".
	if norm == "isnot" {
		if filterField == fieldAssignees {
			return Clause{Verdict: Expressible,
				Reason: "IS NOT EMPTY becomes the \"unassigned\" token with the assignees field negated, which is exactly \"has an assignee\""}, true
		}
		return Clause{Verdict: NotExpressible,
			Reason: "IS NOT EMPTY asks whether a value is set at all; only assignees has an empty-value token (\"unassigned\") for the negation to flip"}, true
	}

	if _, ok := negatableFields[filterField]; ok {
		return Clause{Verdict: Expressible,
			Reason: "v2 negates this field: the values carry across unchanged and `not` is set for it, giving \"everything except these\""}, true
	}
	return Clause{Verdict: NotExpressible,
		Reason: "v2's `not` record names the six membership fields, and this is not one of them"}, true
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
	// A leading NOT on a single clause is the same request as that clause's
	// negating operator, so under v2 it survives whenever the clause's field is
	// one the `not` record names. It is still a structural failure on any other
	// field: there is no clause-level NOT in the document, only a per-field
	// flag, and nothing to hang one on.
	for i, g := range groups {
		if !g.negated {
			continue
		}
		if i < len(clauses) {
			if _, ok := negatableFields[clauses[i].FilterField]; ok {
				continue
			}
		}
		out = append(out, "a NOT applies to a clause on a field the filter cannot negate; v2's `not` names the six membership fields, and there is no clause-level NOT to fall back on")
		break
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
