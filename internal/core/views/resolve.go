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
}

// Result is one row of a saved view's results, from either module. The shape
// is deliberately the same for both so the merge compares like with like and
// the API renders one row component.
type Result struct {
	Module   Module
	ID       uuid.UUID
	Key      string
	Number   int32
	Title    string
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
	Labels       []string
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
	SortField   string
	Descending  bool
	CursorKey   string
	CursorID    uuid.UUID
	Limit       int32
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
	return p, nil
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
