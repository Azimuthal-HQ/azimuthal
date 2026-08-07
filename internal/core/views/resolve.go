package views

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// DefaultPageSize and MaxPageSize bound one results request.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Viewer is everything about the calling user that a resolution depends on.
// It is assembled per request and never cached across requests — which is what
// makes revocation and share expiry immediate (ADR-0008 rules 8 and 11).
type Viewer struct {
	UserID uuid.UUID
	// ReadableSpaceIDs is the caller's resolved readable set. It is the access
	// control, not a hint.
	ReadableSpaceIDs []uuid.UUID
	// SharedTicketIDs and SharedItemIDs are the caller's DIRECTLY shared
	// entities of each type, from access.SharedEntities.DirectIDs.
	//
	// There is no cascade equivalent and there does not need to be: migration
	// 026 constrains cascade to pages, so for these two tables a share is
	// always exactly one entity. See the header on ListViewTickets.
	SharedTicketIDs []uuid.UUID
	SharedItemIDs   []uuid.UUID

	// At is the instant this request resolves relative date bounds against.
	//
	// It lives on the Viewer for one reason: the Viewer is assembled ONCE per
	// request, so everything evaluated during that request shares this field
	// without anyone having to remember to pass it. Two gadgets reading "-7d"
	// therefore land on the same boundary, and a row created between them
	// cannot appear in one count and not the other.
	//
	// A time.Now() inside buildParams would read the clock once per
	// evaluation instead, and the disagreement it produced would be invisible:
	// two tiles differing by one, occasionally, for rows written in the
	// microseconds between two calls.
	//
	// The zero value is not defaulted to "now". buildParams refuses a query
	// carrying a relative bound when At is zero, because a caller that forgot
	// to set it would otherwise resolve "-7d" against year 1 and match
	// everything — a silently wider result set, which is the failure this
	// package refuses everywhere else.
	At time.Time
}

// Origin records how a row became visible, which decides what may be said
// about it. A row reached only through an entity share carries no container
// identity, so the surface has to render provenance instead of inventing one.
//
// The values mirror the search surface's `origin` field deliberately: the same
// distinction, the same wire vocabulary, so one row component can render either.
type Origin string

const (
	// OriginSpace means the viewer can read the space this row lives in.
	OriginSpace Origin = "space"
	// OriginShare means the row reached the viewer through a share on the
	// entity itself and they cannot enter its space.
	OriginShare Origin = "share"
)

// Result is one row of a saved view's results, from either module. The shape
// is deliberately the same for both so the merge compares like with like and
// the API renders one row component.
type Result struct {
	Module Module
	ID     uuid.UUID
	Key    string
	Number int32
	Title  string
	// Origin is set by redactSharedContainers, after the fan-outs and before
	// anything is rendered — never by a query.
	Origin   Origin
	SpaceID  uuid.UUID
	SpaceKey string
	// SpaceName is carried because a cross-container result list has to say
	// which container each row came from; a key prefix alone is not enough
	// once two spaces in different teams are in one list.
	SpaceName string
	Status    string
	Priority  string
	// AssigneeID and AssigneeName travel together. The name is joined in the
	// fan-out rather than looked up per row — a per-row lookup is the shape
	// spec §2.5 case 23 forbids. Nil name with a non-nil id means the join
	// found no user, which the UI shows as the id rather than as nothing.
	AssigneeID   *uuid.UUID
	AssigneeName *string
	Kind         *string
	SprintID     *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DueAt        *time.Time
	ResolvedAt   *time.Time

	// SortKey is the database's collapsed ordering value for this row. It is
	// not part of the wire format — it exists so the merge can order two
	// modules against each other and so the cursor can name a position.
	SortKey string
}

// FanoutParams is one module's half of a resolved query. Both store methods
// take the same struct: the two tables differ in three columns, and giving
// them two parameter types would mean two places to add the next filter.
type FanoutParams struct {
	OrgID            uuid.UUID
	ReadableSpaceIDs []uuid.UUID
	// SharedIDs are the directly shared ids of THIS module's entity type.
	SharedIDs         []uuid.UUID
	SpaceIDs          []uuid.UUID
	Statuses          []string
	Priorities        []string
	FilterAssignee    bool
	AssigneeIDs       []uuid.UUID
	IncludeUnassigned bool
	Kinds             []string
	SprintIDs         []uuid.UUID
	// TextPattern is a complete, already-escaped ILIKE pattern, or "".
	TextPattern string

	// The v2 negation flags. Each says "everything except the values in the
	// field beside it" and is meaningless without them — validate() refuses a
	// flag set over an empty field, so a true here always has values to negate.
	NotSpaceIDs   bool
	NotStatuses   bool
	NotPriorities bool
	NotAssignees  bool
	NotKinds      bool
	NotSprintIDs  bool

	// The v2 date bounds, ALREADY RESOLVED to instants. Relative tokens do not
	// reach this struct: buildParams resolves them against Viewer.At, so the
	// fan-out compares timestamps and never parses a grammar. Nil means the
	// bound is absent, which the SQL reads as no predicate at all.
	//
	// Half-open, matching the SQL: *After is inclusive, *Before is exclusive.
	CreatedAfter   *time.Time
	CreatedBefore  *time.Time
	UpdatedAfter   *time.Time
	UpdatedBefore  *time.Time
	DueAfter       *time.Time
	DueBefore      *time.Time
	ResolvedAfter  *time.Time
	ResolvedBefore *time.Time

	SortField  string
	Descending bool
	CursorKey  string
	CursorID   uuid.UUID
	Limit      int32
	// GroupBy names the breakdown field for the two grouped fan-outs, and is
	// empty for every other query. It lives on this struct rather than on a
	// third parameter type for the reason stated above: the two tables differ
	// in three columns, and a second struct would mean two places to add the
	// next filter.
	GroupBy string
}

// ResultStore is the read seam for the two fan-outs.
//
// Deliberately separate from the ticket and project-item repositories, for the
// same reason tickets.SuggestionStore is: these read across many spaces at
// once, which no other read of either table does, and widening those
// interfaces would push a cross-space method onto every implementation.
type ResultStore interface {
	ListTickets(ctx context.Context, p FanoutParams) ([]Result, error)
	ListProjectItems(ctx context.Context, p FanoutParams) ([]Result, error)
}

// Page is one page of results.
type Page struct {
	Results    []Result
	NextCursor string
	HasMore    bool
}

// Resolve runs a view's query for one viewer and returns one page.
//
// PER VIEWER, ALWAYS. A shared view shares the definition, never the results.
// Two people opening the same view legitimately see different rows, and a
// viewer with less access silently sees fewer — that is correct behaviour, not
// a bug to smooth over. Nothing here consults the view's owner: the owner's
// access is irrelevant to what a viewer may read.
//
// THE ADR-0008 EXCEPTION. Space-scoped listings never union shares. A saved
// view is cross-container by nature and is the sanctioned exception: results
// resolve against the viewer's readable spaces UNIONED with their shares. The
// exception is recorded in docs/design/shared-surfaces.md beside the rule it
// excepts.
//
//nolint:cyclop,funlen // the two module fan-outs and the merge belong in one place: the access union and the ordering contract are only checkable together
func Resolve(ctx context.Context, store ResultStore, q Query, v Viewer, cursor string, limit int) (Page, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}

	cur, err := decodeCursor(cursor)
	if err != nil {
		return Page{}, err
	}

	base, err := buildParams(q, v, cur, limit)
	if err != nil {
		return Page{}, err
	}

	// A viewer who can read nothing and holds no share is answered with
	// nothing, without a round trip. `= ANY('{}')` would return no rows
	// anyway, so this is not what makes the endpoint safe — it makes the
	// intent explicit at the layer where "no access at all" is a meaningful
	// state rather than an empty array literal in SQL.
	var merged []Result
	if q.Filter.HasModule(ModuleBeacon) &&
		(len(v.ReadableSpaceIDs) > 0 || len(v.SharedTicketIDs) > 0) {
		p := base
		p.SharedIDs = v.SharedTicketIDs
		// Vector-only fields never reach the ticket fan-out. Validation
		// already refuses them alongside Beacon, so this is belt and braces
		// rather than the enforcement.
		p.Kinds, p.SprintIDs = nil, nil
		p.NotKinds, p.NotSprintIDs = false, false
		rows, err := store.ListTickets(ctx, p)
		if err != nil {
			return Page{}, fmt.Errorf("resolving beacon results: %w", err)
		}
		for i := range rows {
			rows[i].Module = ModuleBeacon
		}
		merged = append(merged, rows...)
	}
	if q.Filter.HasModule(ModuleVector) &&
		(len(v.ReadableSpaceIDs) > 0 || len(v.SharedItemIDs) > 0) {
		p := base
		p.SharedIDs = v.SharedItemIDs
		rows, err := store.ListProjectItems(ctx, p)
		if err != nil {
			return Page{}, fmt.Errorf("resolving vector results: %w", err)
		}
		for i := range rows {
			rows[i].Module = ModuleVector
		}
		merged = append(merged, rows...)
	}

	redactSharedContainers(merged, v.ReadableSpaceIDs)

	sortResults(merged, q.Sort.Dir == "desc")

	page := Page{Results: merged}
	if len(merged) > limit {
		page.Results = merged[:limit]
		page.HasMore = true
	}
	if page.HasMore && len(page.Results) > 0 {
		last := page.Results[len(page.Results)-1]
		page.NextCursor = encodeCursor(cursorPos{Key: last.SortKey, ID: last.ID})
	}
	if page.Results == nil {
		page.Results = []Result{}
	}
	return page, nil
}

// redactSharedContainers strips container identity from every row the viewer
// reached only through an entity share.
//
// The §13 exception lets a saved view union shares into a cross-space listing —
// that is the one place a space-scoped listing may do so. It widens which ROWS
// are visible; it does not widen what may be said ABOUT them. Matrix case 16
// forbids a share-only read from disclosing the space it lives in, and a saved
// view row is still a read. Search enforced exactly this at
// search.redactSharedContainers and the views fan-out had no equivalent, so the
// same union that made the row visible also emitted its space id, space key and
// space name.
//
// What is stripped follows the canonical share projection in
// internal/core/api/shares/reader.go, which is the sanctioned shape for "an
// entity seen through a share": id, title, body, status, priority, timestamps —
// no container, no human key, no assignee. Key and Number go with the space
// because both encode it: Key is composed as <SPACE_KEY>-<number>, so leaving it
// would hand back the space key the SpaceKey field just removed.
//
// The decision is by SPACE, not by "was it in the shared-id list": an entity can
// be both directly shared and in a space the viewer can read, and then the
// container is already theirs to see. Enforced here, once, at the merge, rather
// than in the handler — so a second response shape cannot forget it.
func redactSharedContainers(rows []Result, readable []uuid.UUID) {
	readableSet := make(map[uuid.UUID]struct{}, len(readable))
	for _, id := range readable {
		readableSet[id] = struct{}{}
	}
	for i := range rows {
		if _, ok := readableSet[rows[i].SpaceID]; ok {
			rows[i].Origin = OriginSpace
			continue
		}
		rows[i].Origin = OriginShare
		rows[i].SpaceID = uuid.Nil
		rows[i].SpaceKey = ""
		rows[i].SpaceName = ""
		rows[i].Key = ""
		rows[i].Number = 0
		rows[i].AssigneeID = nil
		rows[i].AssigneeName = nil
	}
}

// buildParams turns a stored query plus a viewer into the parameters both
// fan-outs take. This is the ONE place the `me` token is resolved.
func buildParams(q Query, v Viewer, cur cursorPos, limit int) (FanoutParams, error) {
	p := FanoutParams{
		OrgID:            uuid.Nil, // set by the caller-facing service
		ReadableSpaceIDs: v.ReadableSpaceIDs,
		SpaceIDs:         q.Filter.SpaceIDs,
		Statuses:         q.Filter.Statuses,
		Priorities:       q.Filter.Priorities,
		Kinds:            q.Filter.Kinds,
		SprintIDs:        q.Filter.SprintIDs,
		SortField:        q.Sort.Field,
		Descending:       q.Sort.Dir == "desc",
		CursorKey:        cur.Key,
		CursorID:         cur.ID,
		// One more than the page so HasMore is answerable without a second
		// query. Each module fetches limit+1; the merge then has at least
		// limit+1 rows whenever any remain.
		Limit: int32(limit) + 1, //nolint:gosec // limit is clamped to MaxPageSize above
	}

	// THE `me` TOKEN, RESOLVED HERE AND NOWHERE ELSE.
	//
	// Resolution happens per request against the CALLING user, which is what
	// lets one shared view mean "assigned to me" for each of its viewers
	// independently. Resolving at write time would freeze the view to its
	// author, and resolving in two places would let the two drift.
	for _, a := range q.Filter.Assignees {
		switch a {
		case AssigneeMe:
			p.FilterAssignee = true
			p.AssigneeIDs = append(p.AssigneeIDs, v.UserID)
		case AssigneeUnassigned:
			p.FilterAssignee = true
			p.IncludeUnassigned = true
		default:
			id, err := uuid.Parse(a)
			if err != nil {
				// Unreachable through the API — Validate rejects this on the
				// way in — but a stored document is still data, and data that
				// cannot be parsed must not silently widen the filter to
				// "everyone".
				return FanoutParams{}, fmt.Errorf("stored filter names an unparseable assignee %q: %w", a, err)
			}
			p.FilterAssignee = true
			p.AssigneeIDs = append(p.AssigneeIDs, id)
		}
	}

	// The text term becomes a complete ILIKE pattern here, escaped with
	// access.EscapeLike — the same helper the shared-subtree matching uses.
	// It is deliberately NOT a fourth hand-rolled escape and deliberately not
	// a copy of the replace(replace(replace(...))) idiom that users.sql and
	// tickets.sql spell out in SQL: one algorithm, one implementation.
	if t := strings.TrimSpace(q.Filter.Text); t != "" {
		p.TextPattern = "%" + access.EscapeLike(t) + "%"
	}

	// Negation is carried through unchanged. It is a flag on the field it
	// negates, so there is nothing to resolve — the SQL flips the membership
	// test rather than the filter naming a different set of values.
	p.NotSpaceIDs = q.Filter.Not.SpaceIDs
	p.NotStatuses = q.Filter.Not.Statuses
	p.NotPriorities = q.Filter.Not.Priorities
	p.NotAssignees = q.Filter.Not.Assignees
	p.NotKinds = q.Filter.Not.Kinds
	p.NotSprintIDs = q.Filter.Not.SprintIDs

	// THE RELATIVE DATE TOKENS, RESOLVED HERE AND NOWHERE ELSE.
	//
	// The same rule as the `me` token above, for the same reason, against the
	// same per-request value: one instant serves every bound in this query and
	// every other query built from this Viewer.
	if err := resolveDates(&p, q.Filter, v.At); err != nil {
		return FanoutParams{}, err
	}
	return p, nil
}

// resolveDates turns the four stored ranges into instants.
func resolveDates(p *FanoutParams, f Filter, at time.Time) error {
	for _, d := range []struct {
		name   string
		value  *DateRange
		after  **time.Time
		before **time.Time
	}{
		{"created_at", f.CreatedAt, &p.CreatedAfter, &p.CreatedBefore},
		{"updated_at", f.UpdatedAt, &p.UpdatedAfter, &p.UpdatedBefore},
		{"due_at", f.DueAt, &p.DueAfter, &p.DueBefore},
		{"resolved_at", f.ResolvedAt, &p.ResolvedAfter, &p.ResolvedBefore},
	} {
		if d.value == nil {
			continue
		}
		for _, side := range []struct {
			raw  string
			into **time.Time
		}{
			{d.value.After, d.after},
			{d.value.Before, d.before},
		} {
			if side.raw == "" {
				continue
			}
			b, err := ParseDateBound(side.raw)
			if err != nil {
				// Unreachable through the API — Validate refuses this on the
				// way in — but a stored document is still data, and a bound
				// that cannot be parsed must not silently become "no bound",
				// which would widen the view rather than narrow it.
				return fmt.Errorf("stored filter names an unparseable %s bound %q: %w", d.name, side.raw, err)
			}
			if b.Relative && at.IsZero() {
				return fmt.Errorf(
					"filter uses the relative date %q but the request carries no evaluation instant (Viewer.At is unset)",
					side.raw)
			}
			t := b.Resolve(at)
			*side.into = &t
		}
	}
	return nil
}

// sortResults orders the merged rows exactly as the database ordered each half.
//
// The comparison must agree with the SQL byte for byte or the merge interleaves
// two correctly-sorted halves incorrectly. That is why the fan-outs apply
// COLLATE "C" to the sort key: PostgreSQL then orders it bytewise, which is
// what Go's string comparison does here. The id tiebreaker is compared as its
// string form for the same reason — it is what the SQL compares.
func sortResults(rows []Result, desc bool) {
	less := func(a, b Result) bool {
		if a.SortKey != b.SortKey {
			if desc {
				return a.SortKey > b.SortKey
			}
			return a.SortKey < b.SortKey
		}
		ai, bi := a.ID.String(), b.ID.String()
		if desc {
			return ai > bi
		}
		return ai < bi
	}
	// Insertion sort over two already-sorted runs: n is bounded by
	// 2*(limit+1) and the input is nearly ordered, so this is linear in
	// practice and needs no allocation.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// cursorPos is a position in the merged ordering.
//
// One cursor serves BOTH module queries. The merged output is the union of the
// two halves ordered by (sort_key, id), so once the page is cut at a position,
// every row at or before it has been emitted from whichever module it came
// from — and resuming each half strictly after that position is exactly right.
// A per-module cursor pair would encode the same information and add a way for
// the two halves to disagree.
type cursorPos struct {
	Key string
	ID  uuid.UUID
}

// ErrBadCursor reports a cursor that is not one this build issued.
var ErrBadCursor = errors.New("malformed cursor")

func encodeCursor(c cursorPos) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.Key + "\x00" + c.ID.String()))
}

func decodeCursor(s string) (cursorPos, error) {
	if s == "" {
		return cursorPos{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPos{}, fmt.Errorf("%w: not base64url", ErrBadCursor)
	}
	// The sort key can itself be empty (a NULL due_at collapses to ""), so
	// split on the LAST separator rather than the first.
	i := strings.LastIndex(string(raw), "\x00")
	if i < 0 {
		return cursorPos{}, fmt.Errorf("%w: no separator", ErrBadCursor)
	}
	id, err := uuid.Parse(string(raw[i+1:]))
	if err != nil {
		return cursorPos{}, fmt.Errorf("%w: %w", ErrBadCursor, err)
	}
	return cursorPos{Key: string(raw[:i]), ID: id}, nil
}
