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
package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Version is the only filter-document version this build reads or writes. A
// document carrying any other value is refused rather than guessed at.
const Version = 1

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
	if q.V != Version {
		return fmt.Errorf("filter document version %d is not supported (this build reads version %d)", q.V, Version)
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
