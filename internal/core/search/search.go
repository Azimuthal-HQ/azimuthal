package search

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MaxPageSize bounds one page of results. The fan-out asks each module for
// limit+1 rows, so the merge can tell "there are more" from "that was all"
// without a second query.
const (
	DefaultPageSize = 20
	MaxPageSize     = 50
)

// Origin says HOW a result became visible to this viewer. It is not decoration:
// it decides what may be said about the result.
type Origin string

const (
	// OriginSpace — the viewer can read the entity's space. The container is
	// theirs to see, so the result carries it.
	OriginSpace Origin = "space"
	// OriginShare — the entity reached the viewer ONLY through a share. The
	// viewer cannot enter its space and must not learn the space exists.
	OriginShare Origin = "share"
)

// ResultState distinguishes the three ways a search can come back with nothing,
// which the surface must render differently. "No results" and "there was
// nothing to search for" are not the same answer, and neither is an error.
type ResultState string

const (
	// StateOK — the query ran. Results may still be empty; that means nothing matched.
	StateOK ResultState = "ok"
	// StateNoSearchableTerms — the query reduced to an empty tsquery, so nothing
	// COULD match. A query of stopwords ("the of a"), pure punctuation, or a
	// single 3000-character token all land here.
	StateNoSearchableTerms ResultState = "no_searchable_terms"
	// StateNoReadableScope — the viewer can read no space and holds no share, so
	// there is nothing to search. Distinct from "nothing matched": the fan-out
	// never ran.
	StateNoReadableScope ResultState = "no_readable_scope"
)

// Result is one hit, already stripped of anything this viewer may not see.
//
// SpaceID/SpaceKey/SpaceName are populated ONLY when Origin is OriginSpace.
// Matrix case 16 forbids a share-only read from disclosing its container, and
// that rule does not weaken because the entity arrived through search rather
// than through /shared. Spec §7 asks for results "tagged with module and owning
// team"; where the two disagree the enforced matrix case wins, and a share-only
// hit is rendered the way /shared already renders one — module, title, and a
// shared-provenance chip, with no container identity.
type Result struct {
	Module  Module    `json:"module"`
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	Origin  Origin    `json:"origin"`
	SortKey string    `json:"-"`

	SpaceID   *uuid.UUID `json:"space_id,omitempty"`
	SpaceKey  string     `json:"space_key,omitempty"`
	SpaceName string     `json:"space_name,omitempty"`

	// Per-module identity. Number/ItemKey are the human reference; Path is
	// Codex's tree position.
	Number   int32  `json:"number,omitempty"`
	ItemKey  string `json:"item_key,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	Path     string `json:"path,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`

	// Snippet is the ts_headline excerpt, filled in for the visible page only.
	Snippet string `json:"snippet,omitempty"`
}

// FanoutParams is one module's half of a search. Every array is access control,
// never a hint: none may be widened, and the service refuses to run the fan-out
// when they are all empty.
type FanoutParams struct {
	OrgID            uuid.UUID
	Query            string
	ReadableSpaceIDs []uuid.UUID
	SharedPageIDs    []uuid.UUID
	SharedTicketIDs  []uuid.UUID
	SharedItemIDs    []uuid.UUID
	SubtreeSpaceIDs  []uuid.UUID
	SubtreePatterns  []string
	FilterTag        bool
	TagID            uuid.UUID
	CursorKey        string
	CursorID         uuid.UUID
	Limit            int32
}

// ErrTagNotFound reports a tag slug that names no tag in the org.
var ErrTagNotFound = errors.New("search: no such tag")

// Store is the persistence contract. Each method maps to one query in
// internal/db/queries/search.sql and returns rows already shaped as Results,
// so the merge below never touches generated types.
type Store interface {
	// ParsedQuery returns PostgreSQL's own parse of the query text. It is the
	// only trustworthy emptiness check: a query can look non-empty in Go and
	// reduce to nothing in the text search parser.
	ParsedQuery(ctx context.Context, text string) (string, error)
	// ResolveTagSlug maps an org-scoped tag slug to its id, or ErrTagNotFound.
	ResolveTagSlug(ctx context.Context, orgID uuid.UUID, slug string) (uuid.UUID, error)

	SearchPages(ctx context.Context, p FanoutParams) ([]Result, error)
	SearchTickets(ctx context.Context, p FanoutParams) ([]Result, error)
	SearchProjectItems(ctx context.Context, p FanoutParams) ([]Result, error)
}

// Request is one search as the handler received it.
type Request struct {
	OrgID  uuid.UUID
	Raw    string
	Cursor string
	Limit  int

	// The viewer's resolved access, exactly as the middleware computed it.
	ReadableSpaceIDs []uuid.UUID
	SharedPageIDs    []uuid.UUID
	SharedTicketIDs  []uuid.UUID
	SharedItemIDs    []uuid.UUID
	SubtreeSpaceIDs  []uuid.UUID
	SubtreePatterns  []string
}

// Page is one page of merged results.
type Page struct {
	Results    []Result    `json:"results"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Modules    []Module    `json:"modules"`
	TagSlug    string      `json:"tag,omitempty"`
	State      ResultState `json:"state"`
}

// Service runs the fan-out and owns the merge.
type Service struct{ store Store }

// NewService builds the search service.
func NewService(store Store) *Service { return &Service{store: store} }

// Search parses, fans out, merges and pages.
//
// The order of the guards matters. Access is checked before the query is even
// parsed, because a viewer with no readable scope has nothing to search
// whatever they typed; and the tsquery emptiness check happens before the
// fan-out, because an empty tsquery matches nothing and would otherwise return
// a plausible empty page — which is precisely the shape that makes every
// "the unreadable row does not appear" assertion pass vacuously.
func (s *Service) Search(ctx context.Context, req Request) (Page, error) {
	q := Parse(req.Raw)
	page := Page{Results: []Result{}, Modules: q.Modules, TagSlug: q.TagSlug, State: StateOK}

	if !hasAnyAccess(req) {
		page.State = StateNoReadableScope
		return page, nil
	}

	parsed, err := s.store.ParsedQuery(ctx, q.Text)
	if err != nil {
		return Page{}, fmt.Errorf("parsing the search query: %w", err)
	}
	if strings.TrimSpace(parsed) == "" {
		page.State = StateNoSearchableTerms
		return page, nil
	}

	cursor, err := decodeCursor(req.Cursor)
	if err != nil {
		return Page{}, err
	}

	limit := clampLimit(req.Limit)

	p := FanoutParams{
		OrgID:            req.OrgID,
		Query:            q.Text,
		ReadableSpaceIDs: req.ReadableSpaceIDs,
		SharedPageIDs:    req.SharedPageIDs,
		SharedTicketIDs:  req.SharedTicketIDs,
		SharedItemIDs:    req.SharedItemIDs,
		SubtreeSpaceIDs:  req.SubtreeSpaceIDs,
		SubtreePatterns:  req.SubtreePatterns,
		CursorKey:        cursor.Key,
		CursorID:         cursor.ID,
		// One more than the page, so "there are more" is knowable without a
		// second query. Asked of EACH module, because any one of them could
		// supply the whole page.
		Limit: int32(limit) + 1, //nolint:gosec // clampLimit bounds it to MaxPageSize
	}

	resolved, unusedTag, err := s.applyTag(ctx, req.OrgID, q, p)
	if err != nil {
		return Page{}, err
	}
	if unusedTag {
		// A tag nobody has used is not an error — it is a search with no
		// possible hits. StateOK with no results says exactly that.
		return page, nil
	}
	p = resolved

	merged, err := s.fanout(ctx, q.Modules, p)
	if err != nil {
		return Page{}, err
	}

	sortResults(merged)
	page.Results, page.NextCursor = cut(merged, limit)
	redactSharedContainers(page.Results, req.ReadableSpaceIDs)
	return page, nil
}

// clampLimit bounds one page. A zero or negative limit is the default rather
// than "unbounded" — a zero-value limit reaching the query would return no rows,
// HTTP 200 and an empty list, which is the silent failure every absence
// assertion in the suite would pass against.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultPageSize
	case n > MaxPageSize:
		return MaxPageSize
	default:
		return n
	}
}

// applyTag resolves a tag slug to its id and pins it on the fan-out params.
// The bool reports a slug that names no tag, which is a search with no possible
// hits rather than an error.
func (s *Service) applyTag(ctx context.Context, orgID uuid.UUID, q Query, p FanoutParams) (FanoutParams, bool, error) {
	if !q.TagFiltered() {
		return p, false, nil
	}
	tagID, err := s.store.ResolveTagSlug(ctx, orgID, q.TagSlug)
	switch {
	case errors.Is(err, ErrTagNotFound):
		return p, true, nil
	case err != nil:
		return p, false, fmt.Errorf("resolving tag %q: %w", q.TagSlug, err)
	}
	p.FilterTag, p.TagID = true, tagID
	return p, false, nil
}

// fanout runs only the modules the query asked for. A narrowed `type:` skips a
// branch entirely rather than filtering its rows away afterwards — the query
// that is never issued is the one that cannot be slow and cannot leak.
func (s *Service) fanout(ctx context.Context, modules []Module, p FanoutParams) ([]Result, error) {
	var out []Result
	for _, m := range modules {
		var (
			rows []Result
			err  error
		)
		switch m {
		case ModuleCodex:
			rows, err = s.store.SearchPages(ctx, p)
		case ModuleBeacon:
			rows, err = s.store.SearchTickets(ctx, p)
		case ModuleVector:
			rows, err = s.store.SearchProjectItems(ctx, p)
		default:
			// Unreachable: Parse only ever produces the three above. An
			// unknown module must not silently widen the fan-out.
			return nil, fmt.Errorf("search: unknown module %q", m)
		}
		if err != nil {
			return nil, fmt.Errorf("searching %s: %w", m, err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// hasAnyAccess reports whether the viewer can reach anything at all.
//
// Without this the fan-out would run with every array empty, and `= ANY('{}')`
// is false for every row — so the answer would be an ordinary empty page, and
// "you may see nothing" would be indistinguishable from "nothing matched".
// tickets.SuggestionService short-circuits for the same reason.
func hasAnyAccess(req Request) bool {
	return len(req.ReadableSpaceIDs) > 0 ||
		len(req.SharedPageIDs) > 0 ||
		len(req.SharedTicketIDs) > 0 ||
		len(req.SharedItemIDs) > 0 ||
		len(req.SubtreeSpaceIDs) > 0
}

// redactSharedContainers strips container identity from every result the viewer
// reached only through a share.
//
// This is enforced HERE, once, rather than in the handler, so a second response
// shape cannot forget it. Matrix case 16 forbids a share-only read from
// disclosing the space it lives in; a search hit is still a read.
//
// The decision is by SPACE, not by "was it in the shared-id list": a page can be
// both directly shared and in a space the viewer can read, and in that case the
// container is already theirs to see.
func redactSharedContainers(rows []Result, readable []uuid.UUID) {
	readableSet := make(map[uuid.UUID]struct{}, len(readable))
	for _, id := range readable {
		readableSet[id] = struct{}{}
	}
	for i := range rows {
		if rows[i].SpaceID == nil {
			continue
		}
		if _, ok := readableSet[*rows[i].SpaceID]; ok {
			rows[i].Origin = OriginSpace
			continue
		}
		rows[i].Origin = OriginShare
		rows[i].SpaceID = nil
		rows[i].SpaceKey = ""
		rows[i].SpaceName = ""
	}
}

// sortResults orders the merged rows exactly as each module's query ordered its
// own half: sort key descending, id descending.
//
// The comparison must agree with the SQL byte for byte, or the merge interleaves
// correctly-sorted halves incorrectly. That is why the fan-outs apply COLLATE "C"
// to the sort key — PostgreSQL then orders it bytewise, which is what Go's string
// comparison does here — and why the id tiebreaker is compared as its string
// form: that is what the SQL compares.
func sortResults(rows []Result) {
	less := func(a, b Result) bool {
		if a.SortKey != b.SortKey {
			return a.SortKey > b.SortKey
		}
		return a.ID.String() > b.ID.String()
	}
	// Insertion sort over already-sorted runs: n is bounded by
	// 3*(limit+1) and the input is nearly ordered.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// cut trims the merged run to one page and issues the cursor for the next.
//
// The cursor is minted from the LAST ROW RETURNED, never from the extra row: the
// next page resumes strictly after what the caller has actually seen.
func cut(rows []Result, limit int) ([]Result, string) {
	if len(rows) <= limit {
		return rows, ""
	}
	page := rows[:limit]
	last := page[len(page)-1]
	return page, encodeCursor(cursorPos{Key: last.SortKey, ID: last.ID})
}

// cursorPos is a position in the merged ordering.
//
// One cursor serves all three module queries. The merged output is their union
// ordered by (sort_key, id), so once the page is cut at a position, every row at
// or before it has been emitted from whichever module it came from — and
// resuming each half strictly after that position is exactly right. A
// per-module cursor triple would encode the same information and add two ways
// for the halves to disagree.
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
	key, id, found := strings.Cut(string(raw), "\x00")
	if !found {
		return cursorPos{}, fmt.Errorf("%w: missing separator", ErrBadCursor)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return cursorPos{}, fmt.Errorf("%w: bad id", ErrBadCursor)
	}
	return cursorPos{Key: key, ID: parsed}, nil
}
