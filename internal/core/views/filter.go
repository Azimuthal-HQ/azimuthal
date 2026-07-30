// Package views implements saved views (ADR-0009): named, reusable queries
// over Beacon tickets and Vector project items, stored as a query and never as
// results, resolved per viewer on every read.
//
// This file is the single place the filter vocabulary is defined. It is the
// contract three other things depend on, and they will drift the moment it is
// duplicated:
//
//   - the API validator, which refuses anything this file does not name;
//   - the two per-module SQL fan-outs, which translate these fields into
//     column predicates;
//   - the Jira importer anticipated by ADR-0011 and ADR-0012, which maps JQL
//     onto exactly these fields and must be able to report what it cannot
//     represent rather than approximating it silently.
//
// # Why this is not a query language
//
// The spec's §4 sketch modelled the document as a list of {field, op, value}
// predicates over an open operator set. That is a query language: it has a
// grammar, its field names come from the caller, and every consumer has to
// implement an evaluator for it. ADR-0011 refused arbitrary scripting in
// workflows for reasons that apply here with no change — a query you cannot
// reason about statically is one you cannot index, explain, migrate or bound.
//
// So the document is a record, not a tree. Fields are named here and nowhere
// else, each has one meaning, and the set is closed. Everything a filter can
// express is visible in the Filter struct below. There is no `op`, no
// `and`/`or` nesting, and no way for a caller to name a column. Fields
// combine with AND; the values within one field combine with OR. That is the
// whole semantics, and it is the whole semantics on purpose.
//
// # Field semantics, for the importer
//
// Two of the eight fields are NOT cross-module, because the underlying tables
// genuinely differ (ADR-0003 keeps them split, and this is one of the places
// that costs something). Verified against the database, not the migrations:
//
//	field        tickets              project_items
//	----------   ------------------   -------------------------
//	space_ids    space_id             space_id
//	statuses     status  (free text)  status  (free text)
//	priorities   priority (CHECKed)   priority (CHECKed, same 4)
//	assignees    assignee_id          assignee_id
//	text         title ILIKE          title ILIKE
//	kinds        -- no such column    kind
//	sprint_ids   -- no such column    sprint_id
//
// A ticket has no type and no sprint. Naming `kinds` or `sprint_ids` in a
// filter whose module set includes Beacon is therefore rejected at write time
// rather than silently matching nothing: a saved view that returns an empty
// Beacon half forever, because it asked tickets for a column they do not have,
// is a defect the user cannot see. The importer must report the same thing —
// a JQL `sprint = X` scoped across both modules does not translate.
//
// # Version 2: dates and negation
//
// v2 adds two capabilities and changes nothing else. The document is still a
// record: no operator grammar, no boolean tree, no cross-field OR. An OR across
// two different fields has no representation in v2 either, and that is
// deliberate — the importer reports such a query as unmappable rather than
// approximating it.
//
//	{"v":2,"filter":{
//	   "modules":["beacon"],
//	   "updated_at":{"after":"-7d"},
//	   "statuses":["closed"],"not":{"statuses":true}
//	 },"sort":{...}}
//
// DATES. Each of the four date fields takes a range of optional {after, before}
// bounds. A bound is EITHER an absolute RFC3339 instant OR a relative token from
// the closed grammar ParseDateBound defines. The interval is half-open — `after`
// is inclusive, `before` is exclusive — so adjacent ranges partition cleanly and
// no row is counted twice at a boundary.
//
// Relative tokens are stored as written and resolved server-side at evaluation,
// against ONE instant per request. That is the rule the `me` token has obeyed
// since v1, for the same reason: a bound resolved at write time freezes the view
// to the moment it was saved, and a view called "updated this week" would
// quietly come to mean "updated during the week I created this".
//
// NEGATION. `not` carries one flag per membership field, meaning "everything
// except these values". It is a per-field flag rather than a clause-level NOT
// precisely so that it cannot compose into a boolean tree.
//
// There is no date negation, and no key that could express one: Negate names
// only the six membership fields, so `{"not":{"created_at":true}}` is refused by
// the unknown-key rule rather than by a special case. Bounds already express
// exclusion — "not in the last 7 days" is `{"before":"-7d"}` — so a second
// spelling would give one meaning two representations.
//
// # Why v1 documents are never rewritten
//
// The version a document declares is the vocabulary it was WRITTEN against, and
// this build preserves it rather than upgrading it. A stored v1 document parses,
// validates, evaluates and re-encodes byte for byte as itself. There is no
// backfill and no upgrade-on-read.
//
// That is not conservatism; it is what keeps evaluation version-free. A v1
// document leaves the v2 fields unset, and an unset bound and an unset negation
// flag are already no-ops in the fan-out — a NULL bound and a false flag. So no
// query, no adapter and no surface below this file branches on the version at
// all. The only code that knows v2 exists is the validator.
//
// The rule is therefore monotone and stated once: V is 1 or 2, and a document
// using any v2 capability must declare 2. A v2 document need not use any v2
// capability — which lets a client raise the version only when its document
// actually requires it, and lets an older client go on reading every view that
// does not.
package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Version is the newest filter-document version this build writes. Documents
// declaring MinVersion..Version are read; anything else is refused rather than
// guessed at.
const Version = 2

// MinVersion is the oldest document version this build still reads. v1
// documents are read, evaluated and re-encoded unchanged — see the note on
// versioning in the package comment. Lowering support for a version that has
// been stored is a data-loss change, not a cleanup.
const MinVersion = 1

// Bounds on a stored filter. They are not security boundaries — every value is
// a bound parameter — they stop one saved view from becoming an unbounded
// query, which is a denial of service against the person who saved it.
const (
	MaxSpaceIDs  = 50
	MaxStatuses  = 50
	MaxAssignees = 50
	MaxKinds     = 50
	MaxSprintIDs = 50
	MaxTextLen   = 200
	MaxNameLen   = 120
	MaxDescLen   = 500
	// MaxRelativeUnits bounds the magnitude of a relative date token, so
	// "-999999999w" cannot ask PostgreSQL to compute an instant outside the
	// range a timestamptz can hold. It is a bound on the TOKEN, not on how far
	// back a view may look: an absolute RFC3339 bound has no such limit.
	MaxRelativeUnits = 999
)

// Module names which table a fan-out reads. The values match spaces.type
// (migration 021), so a module and the spaces it covers are the same word
// everywhere in the product.
type Module string

// The two modules a saved view can read. Codex is deliberately absent: pages
// are found through P6 search, which owns the page read path and its cascade
// share semantics. See the note on Resolve in resolve.go.
const (
	ModuleBeacon Module = "beacon" // tickets
	ModuleVector Module = "vector" // project_items
)

// AssigneeMe is the viewer-relative assignee token. It is resolved to the
// calling user's id at query time, never at write time — that is what lets one
// shared view mean "assigned to me" for each of its viewers independently.
const AssigneeMe = "me"

// AssigneeUnassigned matches rows with a NULL assignee_id.
const AssigneeUnassigned = "unassigned"

// DateNow is the relative token for the evaluating instant itself. It is what
// makes "overdue" expressible — `due_at: {"before":"now"}` — without a client
// having to write a timestamp it would then have to keep refreshing.
const DateNow = "now"

// relativeToken is the whole relative grammar: a sign, a magnitude and one of
// three units. The sign is REQUIRED. "7d" is refused rather than assumed to
// mean the past, because a filter that guesses a direction is one whose author
// cannot tell from reading it which way it points.
//
//	-7d    seven days ago         +7d   seven days from now
//	-4w    four weeks ago         +2w   two weeks from now
//	-1mo   one calendar month ago
//
// Days and weeks are exact multiples of 24h. Months are CALENDAR months, with
// end-of-month clamping — see addMonths.
//
// THE MONTH UNIT IS "mo", NOT "m", AND THAT IS NOT A STYLE CHOICE. In Jira's
// JQL, relative date literals use w/d/h/m where `m` is MINUTES — JQL has no
// month unit at all. Spelling ours `m` would mean "-1m" was a valid token in
// both vocabularies meaning two things three orders of magnitude apart, and the
// importer would translate it silently and wrongly. An unshared spelling makes
// the mismatch a parse error instead. The JQL classifier records `m` as
// unmappable for exactly this reason.
var relativeToken = regexp.MustCompile(`^([+-])([0-9]{1,3})(d|w|mo)$`)

// DateRange is an optional half-open interval on one date column: rows at or
// after After, and strictly before Before. Both bounds are optional, but an
// empty range is refused — `{}` is a filter that filters nothing, written by a
// caller who believed it did something.
//
// Each bound is a STRING because it holds either spelling: an RFC3339 instant
// or a relative token. Storing a resolved instant instead would be the write-time
// freeze the package comment describes, and giving the two spellings two fields
// would make "exactly one of these is set" a rule someone has to remember.
type DateRange struct {
	// After is the inclusive lower bound.
	After string `json:"after,omitempty"`
	// Before is the exclusive upper bound.
	Before string `json:"before,omitempty"`
}

// Negate carries one "everything except these values" flag per membership
// field. A flag with no values beside it is refused: negating an empty set means
// "everything", which is what the filter already does without it, and accepting
// it would let a document say the same thing two ways.
//
// The four date fields are absent by construction, which is how date negation
// is refused — there is no key to write. See the package comment.
type Negate struct {
	SpaceIDs   bool `json:"space_ids,omitempty"`
	Statuses   bool `json:"statuses,omitempty"`
	Priorities bool `json:"priorities,omitempty"`
	Assignees  bool `json:"assignees,omitempty"`
	Kinds      bool `json:"kinds,omitempty"`
	SprintIDs  bool `json:"sprint_ids,omitempty"`
}

// any reports whether any field is negated.
func (n Negate) any() bool {
	return n.SpaceIDs || n.Statuses || n.Priorities || n.Assignees || n.Kinds || n.SprintIDs
}

// ParseDateBound validates one bound and reports how it resolves.
//
// A bound is one of:
//
//	an RFC3339 instant   "2026-01-31T00:00:00Z"
//	the token "now"
//	a relative token     [+-]<1..999><d|w|m>
//
// It is the single definition of the grammar. The API validator, the two SQL
// fan-outs and the JQL classifier all describe what this function accepts, and
// they describe it correctly only for as long as none of them re-implements it.
func ParseDateBound(s string) (DateBound, error) {
	if s == "" {
		return DateBound{}, errors.New("a date bound may not be empty")
	}
	if s == DateNow {
		return DateBound{Relative: true}, nil
	}
	if m := relativeToken.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n == 0 {
			return DateBound{}, fmt.Errorf("relative date %q must name at least one unit", s)
		}
		if n > MaxRelativeUnits {
			return DateBound{}, fmt.Errorf("relative date %q may name at most %d units", s, MaxRelativeUnits)
		}
		if m[1] == "-" {
			n = -n
		}
		return DateBound{Relative: true, Units: n, Unit: m[3]}, nil
	}
	// Absolute. RFC3339 requires the offset, so an instant is unambiguous
	// without consulting anybody's time zone — which matters because the
	// columns are timestamptz and the comparison is between instants.
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return DateBound{}, fmt.Errorf(
			"date bound %q is neither an RFC3339 instant nor a relative token (%q, or a sign, 1-%d and d, w or mo — for example %q)",
			s, DateNow, MaxRelativeUnits, "-7d")
	}
	return DateBound{Absolute: t}, nil
}

// DateBound is a parsed bound: either an absolute instant or an offset waiting
// for the request's `now`.
type DateBound struct {
	Absolute time.Time
	Relative bool
	Units    int
	Unit     string
}

// Resolve turns a bound into the instant the fan-out compares against.
//
// `now` is passed in rather than read here, and that is the whole point: two
// gadgets on one dashboard resolve "-7d" against the SAME instant, so their
// counts agree at the boundary. A time.Now() in this function would give each
// caller its own boundary and the disagreement would appear only for rows
// created in the microseconds between two calls.
func (b DateBound) Resolve(now time.Time) time.Time {
	if !b.Relative {
		return b.Absolute
	}
	switch b.Unit {
	case "d":
		return now.AddDate(0, 0, b.Units)
	case "w":
		return now.AddDate(0, 0, 7*b.Units)
	case "mo":
		return addMonths(now, b.Units)
	default:
		// "now" itself: Relative with no unit.
		return now
	}
}

// addMonths shifts by calendar months, CLAMPING to the last day of the target
// month rather than overflowing into the next one.
//
// time.Time.AddDate does not clamp: it normalises, so 31 March minus one month
// is 31 February, which it reports as 3 March. A view called "changed in the
// last month" would then skip the last few days of February every March. Nobody
// would see that; they would see a count that was slightly wrong once a year.
//
// PostgreSQL's `interval '1 month'` clamps, so this is also the arithmetic the
// database would do if the resolution happened there.
func addMonths(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	target := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	if last := daysIn(target.Year(), target.Month()); d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func daysIn(y int, m time.Month) int {
	// Day 0 of the next month is the last day of this one.
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// orderingDays is a bound's position on a deterministic number line, used ONLY
// to catch an inverted range at write time. Never used for evaluation.
//
// A month is counted as 30 days here, which the calendar disagrees with. That is
// tolerable for an ordering check and intolerable for a comparison, so the two
// are kept apart: evaluation always goes through Resolve, which does real
// calendar arithmetic against the request's instant.
//
// The reason the check cannot simply resolve both bounds against time.Now() is
// that validation would then depend on the wall clock: "-1m" and "-30d" swap
// order between February and March, so a document accepted in one month would
// be rejected in another, and a stored view would become invalid without anyone
// touching it.
func (b DateBound) orderingDays() (float64, bool) {
	if !b.Relative {
		return 0, false
	}
	switch b.Unit {
	case "d":
		return float64(b.Units), true
	case "w":
		return float64(7 * b.Units), true
	case "mo":
		return float64(30 * b.Units), true
	default:
		return 0, true // "now"
	}
}

// validate checks one range's grammar and, where it can be known statically,
// that the range is not inverted.
func (r *DateRange) validate(field string) error {
	if r.After == "" && r.Before == "" {
		return fmt.Errorf("the %q filter names neither a start nor an end; remove it or give it a bound", field)
	}
	after, err := parseSide(field, "after", r.After)
	if err != nil {
		return err
	}
	before, err := parseSide(field, "before", r.Before)
	if err != nil {
		return err
	}
	if r.After == "" || r.Before == "" {
		return nil
	}
	if inverted(after, before) {
		return fmt.Errorf("the %q filter starts at or after it ends (%s is not before %s)", field, r.After, r.Before)
	}
	return nil
}

// parseSide parses one bound, naming the field and the side in the error so the
// message says which of eight bounds is wrong.
func parseSide(field, side, raw string) (DateBound, error) {
	if raw == "" {
		return DateBound{}, nil
	}
	b, err := ParseDateBound(raw)
	if err != nil {
		return DateBound{}, fmt.Errorf("%s %s: %w", field, side, err)
	}
	return b, nil
}

// inverted reports a range that is knowably backwards WITHOUT consulting a
// clock.
//
// Two absolute bounds compare directly. Two relative bounds compare on the
// deterministic number line orderingDays defines. A MIXED pair is never
// reported: which of them comes first depends on when the query runs, so any
// answer here would be one that could stop being true tomorrow — and a stored
// view that became invalid without anyone touching it is worse than an
// inverted range, which simply returns no rows.
func inverted(after, before DateBound) bool {
	switch {
	case !after.Relative && !before.Relative:
		return !after.Absolute.Before(before.Absolute)
	case after.Relative && before.Relative:
		ad, _ := after.orderingDays()
		bd, _ := before.orderingDays()
		return ad >= bd
	default:
		return false
	}
}

// Priorities are CHECK-constrained to these four on both tickets and
// project_items. Validating against the same list here turns a typo into a
// rejected write instead of a view that silently matches nothing.
var validPriorities = map[string]struct{}{
	"urgent": {}, "high": {}, "medium": {}, "low": {},
}

// sortOrdinal maps each sortable field to the column both tables share. Status
// is absent deliberately: it is free text with no total order that means
// anything, so sorting by it would order alphabetically and read as a bug.
var validSortFields = map[string]struct{}{
	"updated_at": {}, "created_at": {}, "due_at": {},
	"resolved_at": {}, "priority": {}, "title": {},
}

// Filter is the closed set of things a saved view can ask for. A zero Filter
// with one module set is legal and means "everything in that module the viewer
// can read".
//
// Every field is AND-ed with the others; values within a field are OR-ed. An
// empty or absent field is not a filter at all — it never means "match none".
type Filter struct {
	// Modules selects which tables to fan out over. At least one is required;
	// naming both is how a view crosses modules.
	Modules []Module `json:"modules"`
	// SpaceIDs narrows to specific spaces. Empty means every space the viewer
	// can read, which is the cross-container default and the reason this
	// endpoint is the sanctioned ADR-0008 exception.
	SpaceIDs []uuid.UUID `json:"space_ids,omitempty"`
	// Statuses matches the denormalised status text on both tables. Free text
	// because workflow states are user-defined per space (migration 016).
	Statuses []string `json:"statuses,omitempty"`
	// Priorities matches the shared four-value enum.
	Priorities []string `json:"priorities,omitempty"`
	// Assignees holds any mix of AssigneeMe, AssigneeUnassigned and user ids.
	Assignees []string `json:"assignees,omitempty"`
	// Kinds matches project_items.kind (the item-type slug). Vector only.
	Kinds []string `json:"kinds,omitempty"`
	// SprintIDs matches project_items.sprint_id. Vector only.
	SprintIDs []uuid.UUID `json:"sprint_ids,omitempty"`
	// Text is a literal substring matched against the title. It is not a
	// pattern: LIKE metacharacters in it match themselves.
	Text string `json:"text,omitempty"`

	// The four date ranges (v2). Nil means unfiltered. Each is half-open:
	// after is inclusive, before is exclusive.
	//
	// CreatedAt and UpdatedAt read NOT NULL columns on both tables. DueAt and
	// ResolvedAt read NULLABLE ones, so a row with no due date is not matched
	// by ANY due_at range — it is not "due before X" and not "due after X"
	// either. Verified against the database, not the migrations.
	CreatedAt  *DateRange `json:"created_at,omitempty"`
	UpdatedAt  *DateRange `json:"updated_at,omitempty"`
	DueAt      *DateRange `json:"due_at,omitempty"`
	ResolvedAt *DateRange `json:"resolved_at,omitempty"`

	// Not negates individual membership fields (v2). omitzero rather than
	// omitempty: encoding/json never treats a struct as empty, so omitempty on
	// a struct field emits `"not":{}` into every document — which would change
	// the bytes of every stored v1 document the moment a v2 build re-encoded
	// one. omitzero (Go 1.24) omits the zero struct, which is what keeps the v1
	// round trip byte-identical.
	Not Negate `json:"not,omitzero"`
}

// usesV2 reports whether this filter needs the v2 vocabulary.
//
// It is the whole definition of "what v2 added", and it is deliberately one
// function rather than a condition repeated at each call site: a capability
// added to Filter but forgotten here would be silently accepted inside a
// document claiming v1, which is the exact drift the version field exists to
// prevent.
func (f *Filter) usesV2() bool {
	return f.CreatedAt != nil || f.UpdatedAt != nil ||
		f.DueAt != nil || f.ResolvedAt != nil || f.Not.any()
}

// RequiredVersion is the lowest document version that can express this filter.
func (q *Query) RequiredVersion() int {
	if q.Filter.usesV2() {
		return 2
	}
	return MinVersion
}

// Sort is one ordering. Deliberately singular rather than the sketch's list.
//
// Results are a merge of two independently-sorted per-module queries, and the
// cursor that pages through that merge has to encode the sort position of the
// last row returned. One field plus the id tiebreaker encodes as a pair; an
// n-field sort encodes as an n+1-tuple whose comparison has to be reimplemented
// identically in Go and in both SQL fan-outs. The second field buys very little
// and costs a class of paging bug that only appears on page two.
type Sort struct {
	Field string `json:"field"`
	Dir   string `json:"dir"`
}

// Query is the stored document: {"v":1,"filter":{...},"sort":{...}}.
type Query struct {
	V      int    `json:"v"`
	Filter Filter `json:"filter"`
	Sort   Sort   `json:"sort"`
}

// DefaultSort is what a view gets when it does not say. Most recently touched
// first is what every list surface in the product already does.
func DefaultSort() Sort { return Sort{Field: "updated_at", Dir: "desc"} }

// ErrUnknownField reports a field the vocabulary does not define. It is
// separate from the value errors because it is the one that must never be
// tolerated: storing a field this build does not understand means a later
// build silently changes what the view means.
var ErrUnknownField = errors.New("unknown field in filter document")

// ParseQuery decodes and validates a stored or submitted filter document.
//
// Unknown fields are refused at every level rather than dropped, and the
// returned Query is re-marshalled by Encode before storage, so the bytes in the
// database are always this build's own serialisation of a document it fully
// understands. There is no path by which an unrecognised key reaches the table.
func ParseQuery(raw []byte) (Query, error) {
	var q Query
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		// DisallowUnknownFields reports unknown keys as a decode error; give
		// the caller the stable sentinel so the API layer can map it to one
		// message without matching on encoding/json's wording.
		if strings.Contains(err.Error(), "unknown field") {
			return Query{}, fmt.Errorf("%w: %w", ErrUnknownField, err)
		}
		return Query{}, fmt.Errorf("malformed filter document: %w", err)
	}
	// A single JSON value, not a stream: trailing content is a malformed
	// document, not a second document.
	if dec.More() {
		return Query{}, errors.New("trailing content after filter document")
	}
	if err := q.Validate(); err != nil {
		return Query{}, err
	}
	return q, nil
}

// Validate enforces every rule the vocabulary states. It is called by
// ParseQuery and separately by any caller that builds a Query in Go.
func (q *Query) Validate() error {
	if q.V < MinVersion || q.V > Version {
		return fmt.Errorf("filter document version %d is not supported (this build reads versions %d to %d)", q.V, MinVersion, Version)
	}
	// A v2 capability inside a document that declares v1 is refused, not
	// tolerated and not silently upgraded. The version is the reader's promise
	// about what it will find; a v1 reader that met a date range would drop it
	// and return a wider set of rows than the document asks for, which is the
	// failure mode nobody sees.
	if req := q.RequiredVersion(); q.V < req {
		return fmt.Errorf(
			"this filter uses date ranges or negation, which need document version %d (this document declares version %d)",
			req, q.V)
	}
	if q.Sort.Field == "" && q.Sort.Dir == "" {
		q.Sort = DefaultSort()
	}
	if err := q.Sort.validate(); err != nil {
		return err
	}
	return q.Filter.validate()
}

func (s Sort) validate() error {
	if _, ok := validSortFields[s.Field]; !ok {
		return fmt.Errorf("sort field %q is not sortable (allowed: %s)", s.Field, joinKeys(validSortFields))
	}
	if s.Dir != "asc" && s.Dir != "desc" {
		return fmt.Errorf("sort direction %q must be \"asc\" or \"desc\"", s.Dir)
	}
	return nil
}

//nolint:gocognit,cyclop,funlen // one linear rule per field; splitting it hides the vocabulary
func (f *Filter) validate() error {
	if len(f.Modules) == 0 {
		return errors.New("at least one module is required")
	}
	seen := make(map[Module]struct{}, len(f.Modules))
	for _, m := range f.Modules {
		if m != ModuleBeacon && m != ModuleVector {
			return fmt.Errorf("module %q is not queryable by a saved view (allowed: %q, %q)", m, ModuleBeacon, ModuleVector)
		}
		if _, dup := seen[m]; dup {
			return fmt.Errorf("module %q is listed twice", m)
		}
		seen[m] = struct{}{}
	}

	if len(f.SpaceIDs) > MaxSpaceIDs {
		return fmt.Errorf("at most %d space ids may be named (got %d)", MaxSpaceIDs, len(f.SpaceIDs))
	}
	if len(f.Statuses) > MaxStatuses {
		return fmt.Errorf("at most %d statuses may be named (got %d)", MaxStatuses, len(f.Statuses))
	}
	for _, s := range f.Statuses {
		if strings.TrimSpace(s) == "" {
			return errors.New("a status may not be blank")
		}
	}
	for _, p := range f.Priorities {
		if _, ok := validPriorities[p]; !ok {
			return fmt.Errorf("priority %q is not one of urgent, high, medium, low", p)
		}
	}
	if len(f.Assignees) > MaxAssignees {
		return fmt.Errorf("at most %d assignees may be named (got %d)", MaxAssignees, len(f.Assignees))
	}
	for _, a := range f.Assignees {
		if a == AssigneeMe || a == AssigneeUnassigned {
			continue
		}
		if _, err := uuid.Parse(a); err != nil {
			return fmt.Errorf("assignee %q must be %q, %q or a user id", a, AssigneeMe, AssigneeUnassigned)
		}
	}
	if len([]rune(f.Text)) > MaxTextLen {
		return fmt.Errorf("text term may be at most %d characters (got %d)", MaxTextLen, len([]rune(f.Text)))
	}

	// The two module-bound fields. Rejected rather than silently ignored:
	// tickets have no kind column and no sprint_id column, so a filter naming
	// either across both modules can never match a ticket, and a view that
	// returns an empty Beacon half forever is a defect its author cannot see.
	_, hasBeacon := seen[ModuleBeacon]
	if len(f.Kinds) > 0 {
		if len(f.Kinds) > MaxKinds {
			return fmt.Errorf("at most %d kinds may be named (got %d)", MaxKinds, len(f.Kinds))
		}
		if hasBeacon {
			return fmt.Errorf("the %q filter applies to %s items only — Beacon tickets have no type, so a view naming both modules cannot use it", "kinds", ModuleVector)
		}
		for _, k := range f.Kinds {
			if strings.TrimSpace(k) == "" {
				return errors.New("a kind may not be blank")
			}
		}
	}
	if len(f.SprintIDs) > 0 {
		if len(f.SprintIDs) > MaxSprintIDs {
			return fmt.Errorf("at most %d sprint ids may be named (got %d)", MaxSprintIDs, len(f.SprintIDs))
		}
		if hasBeacon {
			return fmt.Errorf("the %q filter applies to %s items only — Beacon tickets have no sprint, so a view naming both modules cannot use it", "sprint_ids", ModuleVector)
		}
	}

	if err := f.validateDates(); err != nil {
		return err
	}
	return f.validateNegation()
}

// validateDates checks the four ranges. Named per field so the message says
// which one is wrong rather than that "a date" is.
func (f *Filter) validateDates() error {
	for _, d := range []struct {
		name  string
		value *DateRange
	}{
		{"created_at", f.CreatedAt},
		{"updated_at", f.UpdatedAt},
		{"due_at", f.DueAt},
		{"resolved_at", f.ResolvedAt},
	} {
		if d.value == nil {
			continue
		}
		if err := d.value.validate(d.name); err != nil {
			return err
		}
	}
	return nil
}

// validateNegation refuses a negation flag with nothing to negate.
//
// "Everything except nothing" is everything, which the filter already says by
// leaving the field out. Accepting it would give one meaning two spellings, and
// the one that looks like a filter would be the one that is not.
func (f *Filter) validateNegation() error {
	for _, n := range []struct {
		name  string
		on    bool
		empty bool
	}{
		{"space_ids", f.Not.SpaceIDs, len(f.SpaceIDs) == 0},
		{"statuses", f.Not.Statuses, len(f.Statuses) == 0},
		{"priorities", f.Not.Priorities, len(f.Priorities) == 0},
		{"assignees", f.Not.Assignees, len(f.Assignees) == 0},
		{"kinds", f.Not.Kinds, len(f.Kinds) == 0},
		{"sprint_ids", f.Not.SprintIDs, len(f.SprintIDs) == 0},
	} {
		if n.on && n.empty {
			return fmt.Errorf("the %q filter is negated but names no values; \"everything except nothing\" is everything, so remove the negation or name what to exclude", n.name)
		}
	}
	return nil
}

// HasModule reports whether the filter reads the given module.
func (f *Filter) HasModule(m Module) bool {
	for _, got := range f.Modules {
		if got == m {
			return true
		}
	}
	return false
}

// Encode serialises the validated query for storage. Storage always goes
// through this rather than through the caller's original bytes, so a document
// in the table is by construction one this build produced and understands.
func (q *Query) Encode() ([]byte, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	out, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("encoding filter document: %w", err)
	}
	return out, nil
}

func joinKeys(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Stable output so an error message does not change between runs.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return strings.Join(keys, ", ")
}
